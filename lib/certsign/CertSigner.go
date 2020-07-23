package certsign

import (
	"bytes"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/interfaces"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/lib/puppetconfig"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
)

type signChanMessage struct {
	certSubject       string
	signCSR           bool
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
	stoppedCsrWatcher      chan struct{}
	openFileFunc           func(name string, flag int, perm os.FileMode) (*os.File, error)
	notifyCallback         func(message string)
	ResultChan             chan SigningResult
}

type SigningResult struct {
	Action  string
	Success bool
	Message string
}

type CertSignerInterface interface {
	Sign(hostname string, cleanExistingCert bool) <-chan SigningResult
}

func NewCertSigner(puppetConfig puppetconfig.PuppetConfig, log *log.Logger, watcher *interfaces.FsnotifyWatcher, notifyCallback func(message string)) (*CertSigner, error) {
	certSigner := CertSigner{puppetConfig: &puppetConfig, stopped: false, log: log}

	certSigner.signQueue = make(chan signChanMessage, 50)
	certSigner.stoppedChan = make(chan struct{}, 1)
	certSigner.stoppedCsrWatcher = make(chan struct{}, 1)
	certSigner.cmdFactory = certSigner.puppetCmdFactory
	temp := make(map[string]chan<- SigningResult, 15)
	certSigner.authorizedCertSubjects = &temp
	certSigner.notifyCallback = notifyCallback
	certSigner.openFileFunc = os.OpenFile
	certSigner.ResultChan = make(chan SigningResult, 50)

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

func (ctx *CertSigner) Clean(hostname string) <-chan SigningResult {
	resultChan := make(chan SigningResult, 1)
	if ctx.stopped {
		ctx.sendResult(resultChan, SigningResult{Action: "revoke", Success: false, Message: "The certificate signing manager has been stopped. Shutting down?"})
		close(resultChan)
		return resultChan
	}

	ctx.signQueue <- signChanMessage{
		certSubject:       hostname,
		signCSR:           false,
		cleanExistingCert: true,
		resultChan:        resultChan,
	}

	return resultChan
}

// SigningResult channel will receive two messages if cleanExistingCert is true -
// one when cert is cleaned and one when cert is signed
func (ctx *CertSigner) Sign(hostname string, cleanExistingCert bool) <-chan SigningResult {
	resultChan := make(chan SigningResult, 3)
	if ctx.stopped {
		if cleanExistingCert {
			ctx.sendResult(resultChan, SigningResult{Action: "revoke", Success: false, Message: "The certificate signing manager has been stopped. Shutting down?"})
		}
		ctx.sendResult(resultChan, SigningResult{Action: "sign", Success: false, Message: "The certificate signing manager has been stopped. Shutting down?"})
		close(resultChan)
		return resultChan
	}

	ctx.signQueue <- signChanMessage{
		certSubject:       hostname,
		signCSR:           true,
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
	ctx.stoppedCsrWatcher <- struct{}{}
	<-ctx.stoppedChan
}

func (ctx *CertSigner) signQueueWorker() {
	for message, opened := <-ctx.signQueue; opened; message, opened = <-ctx.signQueue {
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
					ctx.actionDone("revoke", message, false, fmt.Sprintf("Revocation of %s failed.", message.certSubject))
				} else {
					var info string
					if message.signCSR {
						info = fmt.Sprintf("An existing certificate for %s was revoked to make way for the new certificate.", message.certSubject)
					} else {
						info = fmt.Sprintf("Existing certificate for %s was revoked.", message.certSubject)
					}
					ctx.notify(info)
					ctx.log.Printf("Revoked %s.\n", message.certSubject)
					ctx.actionDone("revoke", message, true, info)
					certExists = false
				}
			} else {
				ctx.log.Printf("No existing certificate found for %s\n", message.certSubject)
				ctx.actionDone("revoke", message, true, fmt.Sprintf("No existing certificate for %s to revoke.", message.certSubject))
			}
		}

		if message.signCSR {
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
						info = "Certificate signing for \"%s\" failed -- looks like there's already a signed cert for that host."
					} else {
						info = "Certificate signing for \"%s\" failed! More info in log."
					}
					ctx.notify(fmt.Sprintf(info, message.certSubject))
					ctx.actionDone("sign", message, false, fmt.Sprintf(info, message.certSubject))
				}
			} else {
				info := fmt.Sprintf("Certificate for \"%s\" has been signed.", message.certSubject)
				ctx.notify(info)
				ctx.log.Println(info)
				ctx.actionDone("sign", message, true, info)
			}
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
						signCSR:           true,
						cleanExistingCert: false, // Would have been done already.
						resultChan:        nil,   // Will cause signQueueWorker to only proceed if subject has been authorized.
					}
				}
			}
		case err, ok := <-ctx.csrWatcher.Errors:
			if !ok {
				continue
			}
			ctx.log.Println("CSR watcher reported error: ", err)
		case <-ctx.stoppedCsrWatcher:
			close(ctx.signQueue)
			return
		}
	}
}

func (ctx *CertSigner) puppetCmdFactory(name string, arg ...string) *exec.Cmd {
	if name == "puppet" {
		name = ctx.puppetConfig.PuppetExecutable
		// Need to pass these for non-root puppet cli to act on puppet master file locations :|
		origArg := arg
		arg = []string{origArg[0], "--config", "/etc/puppetlabs/puppet/puppet.conf", "--confdir", "/etc/puppetlabs/puppet"}
		arg = append(arg, origArg[1:]...)
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

func (ctx *CertSigner) actionDone(action string, entry signChanMessage, success bool, message string) {
	resultChan := entry.resultChan
	if action == "sign" {
		temp := *ctx.authorizedCertSubjects
		tempChan, present := temp[entry.certSubject]
		if present {
			resultChan = tempChan
			delete(temp, entry.certSubject)
		}
	}
	if resultChan != nil {
		ctx.sendResult(resultChan, SigningResult{
			Action:  action,
			Success: success,
			Message: message,
		})
		if action == "sign" || !entry.signCSR {
			close(resultChan)
		}
	}
}

func (ctx *CertSigner) sendResult(resultChan chan<- SigningResult, result SigningResult) {
	resultChan <- result
	ctx.ResultChan <- result
}
