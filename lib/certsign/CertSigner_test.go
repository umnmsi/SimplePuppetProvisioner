package certsign

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/interfaces"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/lib/puppetconfig"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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

	go func(resultChan chan SigningResult) {
		for {
			result := <-resultChan
			fmt.Printf("Received event result %#v\n", result)
		}
	}(sut.ResultChan)

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

	if result.Success {
		t.Error("Signing did not fail when signing manager had been shutdown")
	}
}

func TestCertSigner_SignRevoke_FailsIfStopped(t *testing.T) {
	sut, err, _ := sutFactory(nil, nil, nil)
	if err != nil {
		t.FailNow()
	}
	sut.Shutdown()
	resultChan := sut.Sign("foo.bar.com", true)
	result := <-resultChan

	if result.Success {
		t.Error("Revoking did not fail when signing manager had been shutdown")
	}

	result = <-resultChan

	if result.Success {
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

	defer func() { sut.Shutdown() }()

	// Use a dummy for OpenFile that reports no existing cert.
	sut.openFileFunc = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		expect := "/testssl/cert/foo.bar.com.pem"
		if name != expect {
			t.Errorf("Expected to test for existing certificate at %s, got %s", expect, name)
		}
		return nil, errors.New("simulated error")
	}

	resultChan := sut.Sign("foo.bar.com", true)
	// 1st message is cert-clean
	result := <-resultChan

	if !result.Success {
		t.Error("Revocation result was not Success.")
	}
	expect := "No existing certificate for foo.bar.com to revoke."
	if result.Message != expect {
		t.Errorf("Expected signing result Message \"%s\", got \"%s\"", expect, result.Message)
	}

	// 2nd message is cert-sign
	result = <-resultChan

	if !result.Success {
		t.Error("Signing result was not Success.")
	}
	expect = "Certificate for \"foo.bar.com\" has been signed."
	if result.Message != expect {
		t.Errorf("Expected signing result Message %s, got %s", expect, result.Message)
	}
	if lastNotification != expect {
		t.Errorf("Expected notification Message %s, got %s", expect, result.Message)
	}

	logStuff := logBuf.String()
	if strings.Contains(logStuff, "Revok") {
		t.Error("Log suggested revocation attempted, but existing certificate should not have been found.")
	}
	if !strings.Contains(logStuff, expect) {
		t.Error("Expected signing result Success Message not found in log.")
	}
}

func TestCertSigner_Sign_RevokesWhenAppropriate(t *testing.T) {
	var notifications []string
	var mockNotification = func(message string) {
		notifications = append(notifications, message)
	}
	sut, err, logBuf := sutFactory(nil, mockNotification, []string{"TestHelperPuppetRevokeOk", "TestHelperPuppetSignOk"})
	if err != nil {
		t.FailNow()
	}

	defer func() { sut.Shutdown() }()

	// Use a dummy for OpenFile that reports an existing cert.
	sut.openFileFunc = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		expect := "/testssl/cert/foo.bar.com.pem"
		if name != expect {
			t.Errorf("Expected to test for existing certificate at %s, got %s", expect, name)
		}
		self, _ := os.Executable()
		return os.OpenFile(self, flag, perm)
	}

	resultChan := sut.Sign("foo.bar.com", true)
	// 1st message is cert-clean
	result := <-resultChan

	if !result.Success {
		t.Error("Revocation result was not Success.")
	}
	expect := "An existing certificate for foo.bar.com was revoked to make way for the new certificate."
	if result.Message != expect {
		t.Errorf("Expected signing result Message %s, got %s", expect, result.Message)
	}

	// 2nd message is cert-sign
	result = <-resultChan

	if !result.Success {
		t.Error("Signing result was not Success")
	}
	expect = "Certificate for \"foo.bar.com\" has been signed."
	if result.Message != expect {
		t.Errorf("Expected signing result Message %s, got %s", expect, result.Message)
	}

	if len(notifications) < 2 {
		t.Errorf("Expected 2 notifications, got %d (%v)", len(notifications), notifications)
	} else if notifications[1] != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, notifications[1])
	}
	expect = "An existing certificate for foo.bar.com was revoked to make way for the new certificate."
	if notifications[0] != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, notifications[0])
	}

	logStuff := logBuf.String()
	if !strings.Contains(logStuff, "Revoking existing") {
		t.Error("Log did not contain entry for beginning revocation.")
	}
	if !strings.Contains(logStuff, "Revoked foo.bar.com") {
		t.Error("Log did not contain entry for successful revocation.")
	}
	if !strings.Contains(logStuff, "has been signed.") {
		t.Error("Log did not contain entry for successful signing.")
	}
}

func TestCertSigner_Sign_HandlesSigningError(t *testing.T) {
	var lastNotification string
	var mockNotification = func(message string) {
		lastNotification = message
	}
	sut, err, logBuf := sutFactory(nil, mockNotification, []string{"TestHelperPuppetSignFail"})
	if err != nil {
		t.FailNow()
	}

	defer func() { sut.Shutdown() }()

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
	result = <-resultChan

	if result.Success {
		t.Error("Expected signing failure reported Success.")
	}
	expect := "Certificate signing for \"foo.bar.com\" failed! More info in log."
	if result.Message != expect {
		t.Errorf("Expected result Message \"%s\", got \"%s\"", expect, result.Message)
	}
	if lastNotification != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, lastNotification)
	}

	logStuff := logBuf.String()
	if !strings.Contains(logStuff, "Certificate signing for foo.bar.com failed") {
		t.Error("Log did not contain failure entry")
	}
	if !strings.Contains(logStuff, "Simulated failure in certificate signing") {
		t.Error("Log did not contain output from failed signing process")
	}
}

func TestCertSigner_Sign_HandlesExistingCertFailure(t *testing.T) {
	var lastNotification string
	var mockNotification = func(message string) {
		lastNotification = message
	}
	sut, err, logBuf := sutFactory(nil, mockNotification, []string{"TestHelperPuppetSignFail"})
	if err != nil {
		t.FailNow()
	}

	defer func() { sut.Shutdown() }()

	// Use a dummy for OpenFile that reports an existing cert.
	sut.openFileFunc = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		expect := "/testssl/cert/foo.bar.com.pem"
		if name != expect {
			t.Errorf("Expected to test for existing certificate at %s, got %s", expect, name)
		}
		self, _ := os.Executable()
		return os.OpenFile(self, flag, perm)
	}

	resultChan := sut.Sign("foo.bar.com", false)
	result := <-resultChan

	if result.Success {
		t.Error("Expected signing failure reported Success.")
	}
	expect := "Certificate signing for \"foo.bar.com\" failed -- looks like there's already a signed cert for that host."
	if result.Message != expect {
		t.Errorf("Expected result Message \"%s\", got \"%s\"", expect, result.Message)
	}
	if lastNotification != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, lastNotification)
	}

	logStuff := logBuf.String()
	if !strings.Contains(logStuff, "Certificate signing for foo.bar.com failed") {
		t.Error("Log did not contain failure entry")
	}
	if !strings.Contains(logStuff, "Simulated failure in certificate signing") {
		t.Error("Log did not contain output from failed signing process")
	}
}

func TestCertSigner_Sign_HandlesDeferredCsr(t *testing.T) {
	var notifications []string
	var watcher = &interfaces.FsnotifyWatcher{
		Events: make(chan fsnotify.Event),
	}
	var mockNotification = func(message string) {
		notifications = append(notifications, message)

		if message == "Certificate for \"foo.bar.com\" will be signed when a matching CSR arrives." {
			// When the signing goroutine gets this far, post an event on the watcher Events channel,
			// as fsnotify would do based on an inotify event.
			watcher.Events <- fsnotify.Event{
				Name: "/testssl/csr/foo.bar.com.pem",
				Op:   fsnotify.Create,
			}
		}
	}
	sut, err, logBuf := sutFactory(watcher, mockNotification, []string{"TestHelperPuppetSignNoCsr", "TestHelperPuppetSignOk"})
	if err != nil {
		t.FailNow()
	}

	defer func() { sut.Shutdown() }()

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
	result = <-resultChan

	logStuff := logBuf.String()
	if !result.Success {
		t.Error("Signing result was not Success")
	}
	expect := "Certificate for \"foo.bar.com\" has been signed."
	if result.Message != expect {
		t.Errorf("Expected signing result Message %s, got %s", expect, result.Message)
	}
	if len(notifications) < 2 {
		t.Errorf("Expected 2 notifications, got %d (%v)", len(notifications), notifications)
	} else if notifications[1] != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, notifications[1])
	}
	if !strings.Contains(logStuff, expect) {
		t.Errorf("Log did not contain entry for successful signing: %s", logStuff)
	}

	expect = "Certificate for \"foo.bar.com\" will be signed when a matching CSR arrives."
	if len(notifications) > 0 && notifications[0] != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, notifications[0])
	}
	if !strings.Contains(logStuff, expect) {
		t.Error("Log did not contain entry for missing CSR")
	}
}

func TestCertSigner_Sign_IgnoresUnauthorizedCsrs(t *testing.T) {
	var notifications []string
	var mockNotification = func(message string) {
		notifications = append(notifications, message)
	}

	sut, err, _ := sutFactory(nil, mockNotification, []string{"TestHelperPuppetSignOk"})
	if err != nil {
		t.FailNow()
	}

	sut.signQueue <- signChanMessage{
		certSubject:       "unauthorized.bar.com",
		signCSR:           true,
		cleanExistingCert: false, // Would have been done already.
		resultChan:        nil,   // Will cause signQueueWorker to only proceed if subject has been authorized.
	}

	// Ensure signing goroutine has handled the Message.
	sut.Shutdown()

	// Expect no signing activity took place as evidenced by lack of notifications.
	if len(notifications) > 0 {
		t.Error("Queued signing Message for a CSR that lacked authorization resulted in signing activity.")
	}
}

func TestCertSigner_Clean(t *testing.T) {
	var notifications []string
	var mockNotification = func(message string) {
		notifications = append(notifications, message)
	}
	sut, err, logBuf := sutFactory(nil, mockNotification, []string{"TestHelperPuppetRevokeOk"})
	if err != nil {
		t.FailNow()
	}

	defer func() { sut.Shutdown() }()

	// Use a dummy for OpenFile that reports an existing cert.
	sut.openFileFunc = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		expect := "/testssl/cert/foo.bar.com.pem"
		if name != expect {
			t.Errorf("Expected to test for existing certificate at %s, got %s", expect, name)
		}
		self, _ := os.Executable()
		return os.OpenFile(self, flag, perm)
	}

	resultChan := sut.Clean("foo.bar.com")
	result := <-resultChan

	if !result.Success {
		t.Error("Revocation result was not Success.")
	}
	expect := "Existing certificate for foo.bar.com was revoked."
	if result.Message != expect {
		t.Errorf("Expected signing result Message %s, got %s", expect, result.Message)
	}

	if notifications[0] != expect {
		t.Errorf("Expected notification \"%s\", got \"%s\"", expect, notifications[1])
	}

	logStuff := logBuf.String()
	if !strings.Contains(logStuff, "Revoking existing") {
		t.Error("Log did not contain entry for beginning revocation.")
	}
	if !strings.Contains(logStuff, "Revoked foo.bar.com") {
		t.Error("Log did not contain entry for successful revocation.")
	}
}

func TestCertSigner_Clean_FailsIfStopped(t *testing.T) {
	sut, err, _ := sutFactory(nil, nil, nil)
	if err != nil {
		t.FailNow()
	}
	sut.Shutdown()
	resultChan := sut.Clean("foo.bar.com")
	result := <-resultChan

	if result.Success {
		t.Error("Signing did not fail when signing manager had been shutdown")
	}
}

func TestCertSigner_ProcessingBacklogLength(t *testing.T) {
	var lastNotification string
	var mockNotification = func(message string) {
		lastNotification = message
	}
	sut, err, logBuf := sutFactory(nil, mockNotification, nil)
	if err != nil {
		t.FailNow()
	}

	// Use a channel to pause signQueueWorker
	stallChan := make(chan struct{}, 1)

	defer func() { sut.Shutdown() }()

	sut.openFileFunc = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		expect := "/testssl/cert/foo.bar.com.pem"
		if name != expect {
			t.Errorf("Expected to test for existing certificate at %s, got %s", expect, name)
		}
		<-stallChan
		return nil, errors.New("simulated error")
	}

	len := sut.ProcessingBacklogLength()

	if len != 0 {
		t.Error("Processing length for empty signQueue was not 0")
	}

	// 1st signQueue entry is removed from signQueue, but then signQueueWorker will pause in openFileFunc
	sut.signQueue <- signChanMessage{
		certSubject:       "foo.bar.com",
		signCSR:           false,
		cleanExistingCert: true,
		resultChan:        nil,
	}
	sut.signQueue <- signChanMessage{
		certSubject:       "foo.bar.com",
		signCSR:           false,
		cleanExistingCert: true,
		resultChan:        nil,
	}

	// Let signQueueWorker catch up
	time.Sleep(time.Second)

	len = sut.ProcessingBacklogLength()

	if len != 1 {
		t.Errorf("Expected signQueue length 1, got %d", len)
	}

	if lastNotification != "" {
		t.Errorf("Expected no notifications, but got \"%s\"", lastNotification)
	}

	logStuff := logBuf.String()
	if strings.Contains(logStuff, "certificate") {
		t.Errorf("No certificate activity expected in logs, but found \"%s\"", logStuff)
	}

	// Resume signQueue
	stallChan <- struct{}{}
	stallChan <- struct{}{}
}

// Mock process exec bodies
func TestHelperPuppetSignOk(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	expect := "puppet cert sign foo.bar.com"
	actual := strings.Join(os.Args[3:7], " ")
	if actual != expect {
		os.Stderr.WriteString(fmt.Sprintf("Expected arguments %s, got %s\n", expect, actual))
		os.Exit(2)
	}

	os.Exit(0)
}
func TestHelperPuppetSignFail(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	expect := "puppet cert sign"
	actual := strings.Join(os.Args[3:6], " ")
	if actual != expect {
		os.Stderr.WriteString(fmt.Sprintf("Expected arguments %s, got %s\n", expect, actual))
		os.Exit(2)
	}
	os.Stderr.WriteString("Simulated failure in certificate signing")
	os.Exit(1)
}
func TestHelperPuppetSignNoCsr(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	expect := "puppet cert sign"
	actual := strings.Join(os.Args[3:6], " ")
	if actual != expect {
		os.Stderr.WriteString(fmt.Sprintf("Expected arguments %s, got %s\n", expect, actual))
		os.Exit(2)
	}
	os.Stderr.WriteString(fmt.Sprintf("Error: Could not find CSR for: \"%s\".", os.Args[6]))
	os.Exit(24)
}
func TestHelperPuppetRevokeOk(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	expect := "puppet cert clean"
	actual := strings.Join(os.Args[3:6], " ")
	if actual != expect {
		os.Stderr.WriteString(fmt.Sprintf("Expected arguments %s, got %s\n", expect, actual))
		os.Exit(1)
	}

	os.Exit(0)
}
