package certsign

import (
	"bytes"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/mbaynton/SimplePuppetProvisioner/interfaces"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
)

type signChanMessage struct {
	certSubject       string
	cleanExistingCert bool
	resultChan        chan<- SigningResult
}

type CertSigner struct {
	puppetConfig           *puppetconfig.PuppetConfig
	log                    *log.Logger
	stopped                bool
	stoppedChan            chan struct{}
	signQueue              chan signChanMessage
	cmdFactory             func(name string, arg ...string) *exec.Cmd
	lastCmdStdout          *bytes.Buffer
	lastCmdStderr          *bytes.Buffer
	authorizedCertSubjects *map[string]chan<- SigningResult // Not synchronized as it is currently only touched in
	csrWatcher             *interfaces.FsnotifyWatcher
	openFileFunc           func(name string, flag int, perm os.FileMode) (*os.File, error)
	notifyCallback         func(message string)
}

type SigningResult struct {
	Success bool
	Message string
}

func NewCertSigner(puppetConfig puppetconfig.PuppetConfig, log *log.Logger, watcher *interfaces.FsnotifyWatcher, notifyCallback func(message string)) (*CertSigner, error) {
	certSigner := CertSigner{puppetConfig: &puppetConfig, stopped: false, log: log}

	certSigner.signQueue = make(chan signChanMessage, 50)
	certSigner.stoppedChan = make(chan struct{}, 1)
	certSigner.cmdFactory = certSigner.puppetCmdFactory
	temp := make(map[string]chan<- SigningResult, 15)
	certSigner.authorizedCertSubjects = &temp
	certSigner.notifyCallback = notifyCallback
	certSigner.openFileFunc = os.OpenFile

	// Set up csr watcher.
	certSigner.csrWatcher = watcher
	go certSigner.csrWatchWorker()
	err := certSigner.csrWatcher.Add(puppetConfig.CsrDir)
	if err == nil {
		certSigner.log.Printf("Watching for CSRs in %s\n", puppetConfig.CsrDir)

		// Start one signing worker; these need to be one at a time for now.
		go certSigner.signQueueWorker()
	} else {
		certSigner.log.Printf("Failed to set up watch for CSRs in %s: %s\n", puppetConfig.CsrDir, err.Error())
	}

	return &certSigner, err
}

func (ctx *CertSigner) Sign(hostname string, cleanExistingCert bool) <-chan SigningResult {
	resultChan := make(chan SigningResult, 1)
	if ctx.stopped {
		resultChan <- SigningResult{Success: false, Message: "The certificate signing manager has been stopped. Shutting down?"}
		close(resultChan)
		return resultChan
	}

	ctx.signQueue <- signChanMessage{
		certSubject:       hostname,
		cleanExistingCert: cleanExistingCert,
		resultChan:        resultChan,
	}

	return resultChan
}

func (ctx *CertSigner) ProcessingBacklogLength() int {
	return len(ctx.signQueue)
}

func (ctx *CertSigner) Shutdown() {
	ctx.stopped = true
	ctx.csrWatcher.Close()
	close(ctx.signQueue)
	<-ctx.stoppedChan
}

func (ctx *CertSigner) signQueueWorker() {
	for message, opened := <-ctx.signQueue; opened; message, opened = <-ctx.signQueue {
		temp := *ctx.authorizedCertSubjects
		if message.resultChan != nil {
			// This request came from an external caller.
			// Authorize this certificate subject for signing if it pops up as a CSR later.
			temp[message.certSubject] = message.resultChan
		} else {
			// This request came from the CSR watcher.
			// We'll only process this Message if it is for a preauthorized subject with an available result channel.
			_, present := temp[message.certSubject]
			if !present {
				continue
			}
		}

		certExists := false
		existingCertPath := fmt.Sprintf("%s/%s.pem", ctx.puppetConfig.SignedCertDir, message.certSubject)
		fh, _ := ctx.openFileFunc(existingCertPath, os.O_RDONLY, 0660)
		if fh != nil {
			fh.Close()
			certExists = true
		}
		// Revoke existing certificate if present and requested.
		if message.cleanExistingCert {
			if certExists { // Looks like there's a cert to revoke.
				ctx.log.Printf("Revoking existing certificate for %s...\n", message.certSubject)
				// puppet cert clean appears to exit 0 on successfully signed, nonzero otherwise.
				cleanCmd := ctx.cmdFactory("puppet", "cert", "clean", message.certSubject)
				err := cleanCmd.Run()
				if err != nil {
					// So this is likely to cause the subsequent attempt to sign another certificate to fail,
					// but instead of giving up now let's let the actual puppet CA be the authority on what
					// it can sign.
					ctx.log.Printf("Revocation of %s failed. *** Stdout:\n%s\n*** Stderr:\n%s\n", message.certSubject, ctx.lastCmdStdout.String(), ctx.lastCmdStderr.String())
				} else {
					ctx.notify(fmt.Sprintf("An existing certificate for %s was revoked to make way for the new certificate.", message.certSubject))
					ctx.log.Printf("Revoked %s.\n", message.certSubject)
				}
				certExists = false
			}
		}

		// Try to sign the certificate.
		// puppet cert sign appears to exit 0 on successfully signed, nonzero otherwise.
		signCmd := ctx.cmdFactory("puppet", "cert", "sign", message.certSubject)
		err := signCmd.Run()
		if err != nil {
			// If it was because the cert is not present, the CSR watcher will get it later.
			stderr := ctx.lastCmdStderr.String()
			if strings.Contains(stderr, fmt.Sprintf("Could not find CSR for: \"%s\"", message.certSubject)) {
				info := fmt.Sprintf("Certificate for \"%s\" will be signed when a matching CSR arrives.", message.certSubject)
				ctx.notify(info)
				ctx.log.Printf("%s\n", info)
			} else {
				ctx.log.Printf("Certificate signing for %s failed. *** Stdout:\n%s\n*** Stderr:\n%s\n", message.certSubject, ctx.lastCmdStdout.String(), stderr)
				var info string
				if certExists {
					info = "Certificate signing for %s failed -- looks like there's already a signed cert for that host, and revocation was not requested."
				} else {
					info = "Certificate signing for \"%s\" failed! More info in log."
				}
				ctx.notify(fmt.Sprintf(info, message.certSubject))
				ctx.signingDone(message.certSubject, false, fmt.Sprintf(info, message.certSubject))
			}
		} else {
			info := fmt.Sprintf("Certificate for \"%s\" has been signed.", message.certSubject)
			ctx.notify(info)
			ctx.log.Println(info)
			ctx.signingDone(message.certSubject, true, info)
		}
	}

	// Signal stopped.
	ctx.stoppedChan <- struct{}{}
}

func (ctx *CertSigner) csrWatchWorker() {
	for {
		select {
		case event := <-ctx.csrWatcher.Events:
			if event.Op&(fsnotify.Create|fsnotify.Write) != 0 {
				csrName := path.Base(event.Name)
				extensionIx := strings.LastIndex(csrName, ".")
				if extensionIx > 0 {
					subject := csrName[:extensionIx]
					ctx.signQueue <- signChanMessage{
						certSubject:       subject,
						cleanExistingCert: false, // Would have been done already.
						resultChan:        nil,   // Will cause signQueueWorker to only proceed if subject has been authorized.
					}
				}
			}
		case err := <-ctx.csrWatcher.Errors:
			ctx.log.Println("CSR watcher reported error: ", err)
		}
	}
}

func (ctx *CertSigner) puppetCmdFactory(name string, arg ...string) *exec.Cmd {
	if name == "puppet" {
		name = ctx.puppetConfig.PuppetExecutable
	}
	cmd := exec.Command(name, arg...)
	ctx.lastCmdStdout = &bytes.Buffer{}
	ctx.lastCmdStderr = &bytes.Buffer{}
	cmd.Stdout = ctx.lastCmdStdout
	cmd.Stderr = ctx.lastCmdStderr
	return cmd
}

func (ctx *CertSigner) notify(message string) {
	// Just a passthrough for now. This func here in case we want to do something fancy later.
	ctx.notifyCallback(message)
}

func (ctx *CertSigner) signingDone(subject string, success bool, message string) {
	temp := *ctx.authorizedCertSubjects
	resultChan, present := temp[subject]
	if present {
		resultChan <- SigningResult{
			Success: success,
			Message: message,
		}
		close(resultChan)
		delete(temp, subject)
	}
}
