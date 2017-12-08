package genericexec

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func newTestLogger() (*log.Logger, *bytes.Buffer) {
	var logBuf bytes.Buffer
	testLog := log.New(&logBuf, "", 0)
	return testLog, &logBuf
}

func sutFactory(taskConfigs map[string]GenericExecConfig, execMocks []string) (*GenericExecManager, *bytes.Buffer, **[]string) {
	testLog, testLogBuf := newTestLogger()
	notifications := []string{}
	var notificationsPtr *[]string
	var mockNotification = func(message string) {
		notifications = append(notifications, message)
		notificationsPtr = &notifications
	}
	sut := NewGenericExecManager(taskConfigs, nil, testLog, mockNotification)
	if execMocks == nil {
		execMocks = []string{"TestHelperExit0"}
	}
	sut.cmdFactory = func(name string, argValues TemplateGetter, arg ...string) (*exec.Cmd, error) {
		// Actually make the executable that is run ourselves, with the first subroutine in execMocks acting as main()
		run := execMocks[0]
		if len(execMocks) > 1 {
			execMocks = execMocks[1:]
		}

		// Perform argument substitution.
		err := renderArgTemplates(arg, argValues)
		if err != nil {
			return nil, err
		}

		currFactory := func(name string, arg ...string) (*exec.Cmd, error) {
			// Trickery per https://npf.io/2015/06/testing-exec-command/
			cs := []string{fmt.Sprintf("-test.run=%s", run), "--", name}
			cs = append(cs, arg...)
			cmd := exec.Command(os.Args[0], cs...)
			cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
			return cmd, nil
		}

		return currFactory(name, arg...)
	}

	return sut, testLogBuf, &notificationsPtr
}

func TestNewGenericExecManager_Successful_Reentrant(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test": {
			Name:           "test",
			Command:        "test",
			Args:           []string{"{{request \"value1\"}}", "b"},
			SuccessMessage: "Test command is happy with {{stdout}}",
			Reentrant:      true,
		},
	}

	sut, testLogBuf, notifications := sutFactory(taskConfigs, nil)
	resultChan := sut.RunTask("test", url.Values{"value1": []string{"a"}})
	result := <-resultChan

	expect := "a b"
	if result.stdout != expect {
		t.Errorf("Expected stdout \"%s\", got \"%s\"", expect, result.stdout)
	}
	if result.exitCode != 0 {
		t.Errorf("Expected exit code %d, got %d", 0, result.exitCode)
	}

	expect = "Test command is happy with a b"
	if result.message != expect {
		t.Errorf("Expected message \"%s\", got \"%s\"", expect, result.message)
	}

	logStuff := testLogBuf.String()
	if !strings.Contains(logStuff, expect) {
		t.Errorf("Log did not contain expected success message; expected \"%s\", got \"%s\".", expect, logStuff)
	}

	if notifications != nil && *notifications != nil {
		n := **notifications
		if len(n) == 0 || n[0] != expect {
			t.Errorf("Expected notification \"%s\" not sent. Notifications: %v", expect, n)
		}
	} else {
		t.Errorf("Expected notification %s, but no notifications found.", expect)
	}

}

// Mock process exec bodies
func TestHelperExit0(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	// Echo the received arguments on stdout and exit 0
	fmt.Print(strings.Join(os.Args[4:], " "))
	os.Exit(0)
}
