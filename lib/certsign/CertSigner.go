package certsign

import (
	"bytes"
	"fmt"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	"log"
	"os"
	"os/exec"
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
	authorizedCertSubjects *map[string]chan<- SigningResult
	notifyCallback         func(message string)
}

type SigningResult struct {
	success bool
	message string
}

func NewCertSigner(puppetConfig puppetconfig.PuppetConfig, log *log.Logger, notifyCallback func(message string)) *CertSigner {
	certSigner := CertSigner{puppetConfig: &puppetConfig, stopped: false, log: log}

	certSigner.signQueue = make(chan signChanMessage, 50)
	certSigner.stoppedChan = make(chan struct{}, 1)
	certSigner.cmdFactory = certSigner.puppetCmdFactory
	temp := make(map[string]chan<- SigningResult, 15)
	certSigner.authorizedCertSubjects = &temp
	certSigner.notifyCallback = notifyCallback

	// Start one signing worker; these need to be one at a time for now.
	go certSigner.signQueueWorker()

	// TODO: add inotify watcher on the CSR directory

	return &certSigner
}

func (ctx CertSigner) Sign(hostname string, cleanExistingCert bool) <-chan SigningResult {
	resultChan := make(chan SigningResult, 1)
	if ctx.stopped {
		resultChan <- SigningResult{success: false, message: "The certificate signing manager has been stopped. Shutting down?"}
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

func (ctx CertSigner) ProcessingBacklogLength() int {
	return len(ctx.signQueue)
}

func (ctx CertSigner) Shutdown() {
	ctx.stopped = true
	close(ctx.signQueue)
	<-ctx.stoppedChan
}

func (ctx CertSigner) signQueueWorker() {
	for message, opened := <-ctx.signQueue; opened; {

		// Revoke existing certificate if present and requested.
		if message.cleanExistingCert {
			existingCertPath := fmt.Sprintf("%s/%s.pem", ctx.puppetConfig.SignedCertDir, message.certSubject)
			fh, err := os.OpenFile(existingCertPath, os.O_RDONLY, 0660)
			if fh != nil {
				fh.Close()
			}
			if err == nil { // Looks like there's a cert to revoke.
				ctx.log.Println("Revoking existing certificate for %s...", message.certSubject)
				// puppet cert clean appears to exit 0 on successfully signed, nonzero otherwise.
				cleanCmd := ctx.cmdFactory("puppet", "cert", "clean", message.certSubject)
				err := cleanCmd.Run()
				if err != nil {
					// So this is likely to cause the subsequent attempt to sign another certificate to fail,
					// but instead of giving up now let's let the actual puppet CA be the authority on what
					// it can sign.
					ctx.log.Println("Revocation of %s failed. *** Stdout:\n%s\n*** Stderr:\n%s", message.certSubject, ctx.lastCmdStdout.String(), ctx.lastCmdStderr.String())
				} else {
					ctx.log.Println("Revoked %s.", message.certSubject)
				}
			}
		}

		// Authorize this certificate subject for signing if it pops up as a CSR later.
		temp := *ctx.authorizedCertSubjects
		temp[message.certSubject] = message.resultChan

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
				ctx.log.Println("%s", info)
			} else {
				ctx.log.Println("Certificate signing for %s failed. *** Stdout:\n%s\n*** Stderr:\n%s", message.certSubject, ctx.lastCmdStdout.String(), stderr)
				ctx.notify(fmt.Sprintf("Certificate signing for \"%s\" failed! More info in log.", message.certSubject))
				ctx.signingDone(message.certSubject, false, fmt.Sprintf("Certificate signing for \"%s\" failed!", message.certSubject))
			}
		} else {
			info := fmt.Sprintf("Certificate for \"%s\" has been signed.", message.certSubject)
			ctx.notify(info)
			ctx.log.Println("%s", info)
			ctx.signingDone(message.certSubject, true, info)
		}
	}

	// Signal stopped.
	ctx.stoppedChan <- struct{}{}
}

func (ctx CertSigner) puppetCmdFactory(name string, arg ...string) *exec.Cmd {
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

func (ctx CertSigner) notify(message string) {
	// Just a passthrough for now. This func here in case we want to do something fancy later.
	ctx.notifyCallback(message)
}

func (ctx CertSigner) signingDone(subject string, success bool, message string) {
	temp := *ctx.authorizedCertSubjects
	resultChan, present := temp[subject]
	if present {
		resultChan <- SigningResult{
			success: success,
			message: message,
		}
		close(resultChan)
		delete(temp, subject)
	}
}
