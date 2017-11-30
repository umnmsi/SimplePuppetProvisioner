package certsign

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/mbaynton/SimplePuppetProvisioner/interfaces"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func sutFactory(watcher *interfaces.FsnotifyWatcher, notifyCallback func(message string), execMocks []string) (*CertSigner, error, *bytes.Buffer) {
	puppetConfig := puppetconfig.PuppetConfig{
		PuppetExecutable: "puppet",
		SslDir:           "/testssl",
		CsrDir:           "/testssl/csr",
		SignedCertDir:    "/testssl/cert",
	}

	var logBuf bytes.Buffer
	testlog := log.New(&logBuf, "", 0)

	if watcher == nil {
		watcher = &interfaces.FsnotifyWatcher{}
	}

	if watcher.Add == nil {
		watcher.Add = func(name string) error {
			return nil
		}
	}
	if watcher.Remove == nil {
		watcher.Remove = func(name string) error {
			return nil
		}
	}
	if watcher.Close == nil {
		watcher.Close = func() error {
			return nil
		}
	}
	if watcher.Events == nil {
		watcher.Events = make(chan fsnotify.Event)
	}
	if watcher.Errors == nil {
		watcher.Errors = make(chan error)
	}

	if notifyCallback == nil {
		notifyCallback = func(message string) {}
	}

	sut, err := NewCertSigner(puppetConfig, testlog, watcher, notifyCallback)

	// Install test cmdFactory
	if execMocks != nil {
		sut.cmdFactory = func(name string, arg ...string) *exec.Cmd {
			run := execMocks[0]
			if len(execMocks) > 1 {
				execMocks = execMocks[1:]
			}
			currFactory := func(name string, arg ...string) *exec.Cmd {
				// Trickery per https://npf.io/2015/06/testing-exec-command/
				cs := []string{fmt.Sprintf("-test.run=%s", run), "--", name}
				cs = append(cs, arg...)
				cmd := exec.Command(os.Args[0], cs...)
				cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
				sut.lastCmdStdout = &bytes.Buffer{}
				sut.lastCmdStderr = &bytes.Buffer{}
				cmd.Stdout = sut.lastCmdStdout
				cmd.Stderr = sut.lastCmdStderr
				return cmd
			}

			return currFactory(name, arg...)
		}
	}

	return sut, err, &logBuf
}

func TestCertSigner_Sign_FailsIfStopped(t *testing.T) {
	sut, err, _ := sutFactory(nil, nil, nil)
	if err != nil {
		t.FailNow()
	}
	sut.Shutdown()
	resultChan := sut.Sign("foo.bar.com", false)
	result := <-resultChan

	if result.success {
		t.Error("Signing did not fail when signing manager had been shutdown")
	}
}

func TestNewCertSigner(t *testing.T) {
	sut, err, logBuf := sutFactory(nil, nil, nil)
	if sut == nil || err != nil {
		t.FailNow()
	}
	if !strings.Contains(logBuf.String(), "Watching for CSRs in /testssl/csr") {
		t.Error("Log entry indicating CSR watch initialization was not generated.")
	}
}

func TestNewCertSigner_watchSetupFailure(t *testing.T) {
	watcher := &interfaces.FsnotifyWatcher{
		Add: func(name string) error {
			return errors.New("simulated error")
		},
	}
	_, err, logBuf := sutFactory(watcher, nil, nil)
	if err == nil {
		t.Error("Expected error due to watch setup failure was not returned.")
	}
	if !strings.Contains(logBuf.String(), "Failed to set up watch for CSRs in /testssl/csr: simulated error") {
		t.Error("Expected error log entry was not generated.")
	}
}

func TestCertSigner_Sign(t *testing.T) {
	var lastNotification string
	var mockNotification = func(message string) {
		lastNotification = message
	}
	sut, err, logBuf := sutFactory(nil, mockNotification, []string{"TestHelperPuppetSignOk"})
	if err != nil {
		t.FailNow()
	}

	// Use a dummy for OpenFile that reports no existing cert.
	sut.openFileFunc = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		expect := "/testssl/cert/foo.bar.com.pem"
		if name != expect {
			t.Errorf("Expected to test for existing certificate at %s, got %s", expect, name)
		}
		return nil, errors.New("simulated error")
	}

	resultChan := sut.Sign("foo.bar.com", true)
	result := <-resultChan

	if !result.success {
		t.Error("Signing result was not success.")
	}
	expect := "Certificate for \"foo.bar.com\" has been signed."
	if result.message != expect {
		t.Errorf("Expected signing result message %s, got %s", expect, result.message)
	}
	if lastNotification != expect {
		t.Errorf("Expected notification message %s, got %s", expect, result.message)
	}

	logStuff := logBuf.String()
	if strings.Contains(logStuff, "Revok") {
		t.Error("Log suggested revocation attempted, but existing certificate should not have been found.")
	}
	if !strings.Contains(logStuff, expect) {
		t.Error("Expected signing result success message not found in log.")
	}
}

// Mock process exec bodies
func TestHelperPuppetSignOk(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	expect := "puppet cert sign"
	actual := strings.Join(os.Args[3:6], " ")
	if actual != expect {
		os.Stderr.WriteString(fmt.Sprintf("Expected arguments %s, got %s\n", expect, actual))
		os.Exit(1)
	}

	os.Exit(0)
}
