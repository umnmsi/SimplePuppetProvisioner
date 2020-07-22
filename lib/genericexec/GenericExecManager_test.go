package genericexec

import (
	"bytes"
	"errors"
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
	notificationsPtr := &notifications
	var mockNotification = func(message string) {
		notifications = append(*notificationsPtr, message)
		notificationsPtr = &notifications
	}
	sut := NewGenericExecManager(taskConfigs, nil, testLog, mockNotification)
	if execMocks == nil {
		execMocks = []string{"TestHelperExecHandler"}
	}
	sut.cmdFactory = func(name string, argValues TemplateGetter, arg ...string) (*exec.Cmd, error) {
		// Actually make the executable that is run ourselves, with the first subroutine in execMocks acting as main()
		run := execMocks[0]
		if len(execMocks) > 1 {
			execMocks = execMocks[1:]
		}

		// Perform argument substitution.
		renderedArgs, err := renderArgTemplates(arg, argValues)
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

		return currFactory(name, renderedArgs...)
	}

	go func(resultChan chan GenericExecResult) {
		for {
			result := <-resultChan
			fmt.Printf("Received event result %#v\n", result)
		}
	}(sut.ResultChan)

	return sut, testLogBuf, &notificationsPtr
}

type expectedResult struct {
	result              *GenericExecResult
	logExpects          []string
	notificationExpects []string
}

func TestGenericExecManager_Successful_Reentrant(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test": {
			Name:           "test",
			Command:        "test",
			Args:           []string{"{{request \"value1\"}}", "b"},
			SuccessMessage: "Test command is happy with {{StdOut}}",
			Reentrant:      true,
		},
	}
	expect := expectedResult{
		result: &GenericExecResult{
			StdOut:  "a b",
			Message: "Test command is happy with a b",
		},
		notificationExpects: []string{"Test command is happy with a b"},
		logExpects:          []string{"Test command is happy with a b"},
	}
	genericExecManagerTestCore(t, taskConfigs, []string{"test"}, []TemplateGetter{url.Values{"value1": []string{"a"}}}, []expectedResult{expect})
}

func TestGenericExecManager_Successful_Nonreentrant(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test": {
			Name:           "test",
			Command:        "test",
			Args:           []string{"{{request \"value1\"}}", "b"},
			SuccessMessage: "Test command is happy with {{StdOut}}",
			Reentrant:      false,
		},
	}
	expect := expectedResult{
		result: &GenericExecResult{
			StdOut:  "a b",
			Message: "Test command is happy with a b",
		},
		notificationExpects: []string{"Test command is happy with a b"},
		logExpects:          []string{"Test command is happy with a b"},
	}
	genericExecManagerTestCore(t, taskConfigs, []string{"test"}, []TemplateGetter{url.Values{"value1": []string{"a"}}}, []expectedResult{expect})
}

func TestGenericExecManager_Fail_Reentrant(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test": {
			Name:           "test",
			Command:        "fail",
			Args:           []string{"{{request \"value1\"}}", "b"},
			SuccessMessage: "Test command is happy with {{StdOut}}",
			ErrorMessage:   "Test command failed. Stderr: {{StdErr}}",
			Reentrant:      true,
		},
	}
	expect := expectedResult{
		result: &GenericExecResult{
			StdErr:   "a b",
			ExitCode: 2,
			Message:  "Test command failed. Stderr: a b",
		},
		notificationExpects: []string{"Test command failed. Stderr: a b"},
		logExpects:          []string{"Command \"fail a b\" exited 2!\nSending notification: \"Test command failed. Stderr: a b\"\nOn StdErr: a b"},
	}
	genericExecManagerTestCore(t, taskConfigs, []string{"test"}, []TemplateGetter{url.Values{"value1": []string{"a"}}}, []expectedResult{expect})
}

func TestGenericExecManager_Fail_Nonreentrant(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test": {
			Name:           "test",
			Command:        "fail",
			Args:           []string{"{{request \"value1\"}}", "b"},
			SuccessMessage: "Test command is happy with {{StdOut}}",
			ErrorMessage:   "Test command failed. Stderr: {{StdErr}}",
			Reentrant:      false,
		},
	}
	expect := expectedResult{
		result: &GenericExecResult{
			StdErr:   "a b",
			ExitCode: 2,
			Message:  "Test command failed. Stderr: a b",
		},
		notificationExpects: []string{"Test command failed. Stderr: a b"},
		logExpects:          []string{"Command \"fail a b\" exited 2!\nSending notification: \"Test command failed. Stderr: a b\"\nOn StdErr: a b"},
	}
	genericExecManagerTestCore(t, taskConfigs, []string{"test"}, []TemplateGetter{url.Values{"value1": []string{"a"}}}, []expectedResult{expect})
}

func TestGenericExecManager_reuse(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test-reentrant": {
			Name:           "test-reentrant",
			Command:        "test",
			Args:           []string{"{{request \"value1\"}}"},
			SuccessMessage: "{{StdOut}}",
			ErrorMessage:   "Test command failed. Stderr: {{StdErr}}",
			Reentrant:      true,
		},
		"test": {
			Name:           "test",
			Command:        "test",
			Args:           []string{"{{request \"value1\"}}"},
			SuccessMessage: "{{StdOut}}",
			ErrorMessage:   "Test command failed. Stderr: {{StdErr}}",
			Reentrant:      false,
		},
	}

	taskNames := []string{"test-reentrant", "test", "test-reentrant", "test-reentrant", "test"}
	taskArgs := make([]TemplateGetter, len(taskNames))
	expects := make([]expectedResult, len(taskNames))

	for i, _ := range taskNames {
		uniqueString := fmt.Sprintf("Invocation %d", i+1)
		taskArgs[i] = url.Values{"value1": []string{uniqueString}}
		expects[i] = expectedResult{
			result: &GenericExecResult{
				StdOut:   uniqueString,
				ExitCode: 0,
				Message:  uniqueString,
			},
			notificationExpects: []string{uniqueString},
			logExpects:          []string{uniqueString},
		}
	}

	genericExecManagerTestCore(t, taskConfigs, taskNames, taskArgs, expects)
}

func genericExecManagerTestCore(t *testing.T, taskConfigs map[string]GenericExecConfig, taskNamesSlice []string, taskArgsSlice []TemplateGetter, expectsSlice []expectedResult) {
	sut, testLogBuf, notifications := sutFactory(taskConfigs, nil)
	for i, taskName := range taskNamesSlice {
		taskArgs := taskArgsSlice[i]
		resultChan := sut.RunTask(taskName, taskArgs, "")
		result := <-resultChan

		expect := expectsSlice[i]
		// Verify result properties
		if expect.result.StdOut != "-" && expect.result.StdOut != result.StdOut {
			t.Errorf("Expected StdOut \"%s\", got \"%s\"", expect.result.StdOut, result.StdOut)
		}
		if expect.result.StdErr != "-" && expect.result.StdErr != result.StdErr {
			t.Errorf("Expected StdErr \"%s\", got \"%s\"", expect.result.StdErr, result.StdErr)
		}
		if expect.result.Message != "-" && expect.result.Message != result.Message {
			t.Errorf("Expected result Message \"%s\", got \"%s\"", expect.result.Message, result.Message)
		}
		if expect.result.ExitCode != result.ExitCode {
			t.Errorf("Expected exit code %d, got %d", 0, result.ExitCode)
		}

		// Verify notifications. This requires a pointer to a pointer so that the value returned from the sutFactory can
		// be updated by the notification test double callback afterwards.
		var dereferencedNotifications []string
		if notifications != nil && *notifications != nil {
			dereferencedNotifications = **notifications
		}
		if len(dereferencedNotifications) != len(expect.notificationExpects) {
			t.Errorf("Expected %d notifications, got %d", len(expect.notificationExpects), len(dereferencedNotifications))
		}
		for i, expectedNotification := range expect.notificationExpects {
			if dereferencedNotifications[i] != expectedNotification {
				t.Errorf("Expected notification %d to be \"%s\", got \"%s\"", i+1, expectedNotification, dereferencedNotifications[i])
			}
		}

		// Verify log.
		logStuff := testLogBuf.String()
		for _, expectedLog := range expect.logExpects {
			if !strings.Contains(logStuff, expectedLog) {
				t.Errorf("Log did not contain expected Message; expected \"%s\", got \"%s\".", expectedLog, logStuff)
			}
		}

		testLogBuf.Reset()
		dereferencedNotifications = make([]string, 0)
		*notifications = &dereferencedNotifications
	}
}

func TestNewGenericExecManager_FactoryError(t *testing.T) {
	taskConfigs := map[string]GenericExecConfig{
		"test": {
			Name:           "test",
			Command:        "test",
			Args:           []string{"{{request \"value1\"}}", "b"},
			SuccessMessage: "Test command is happy with {{StdOut}}",
			Reentrant:      true,
		},
	}

	sut, testLogBuf, _ := sutFactory(taskConfigs, nil)
	sut.cmdFactory = func(name string, argValues TemplateGetter, arg ...string) (*exec.Cmd, error) {
		return nil, errors.New("simulated error")
	}

	resultChan := sut.RunTask("test", url.Values{"value1": []string{"a"}}, "")
	result := <-resultChan

	if result.ExitCode != 1 {
		t.Error("Expected exit code 1")
	}

	if result.StdErr != "simulated error" {
		t.Errorf("Expected result StdErr to report %s, got \"%s\"", "simulated error", result.StdErr)
	}

	logStuff := testLogBuf.String()
	expect := "Could not prepare an executable command from the configuration for task test"
	if !strings.Contains(logStuff, expect) {
		t.Errorf("Log did not contain expected error Message; expected \"%s\", got \"%s\".", expect, logStuff)
	}
}

// Mock process exec bodies
func TestHelperExecHandler(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	if os.Args[3] == "fail" {
		// Echo the received arguments on StdErr and exit 2
		fmt.Fprintf(os.Stderr, "%s", strings.Join(os.Args[4:], " "))
		os.Exit(2)
	}

	// Echo the received arguments on StdOut and exit 0
	fmt.Print(strings.Join(os.Args[4:], " "))
	os.Exit(0)
}
