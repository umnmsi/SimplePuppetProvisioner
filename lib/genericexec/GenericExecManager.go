package genericexec

import (
	"bytes"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"syscall"
	"text/template"

	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
)

type GenericExecManager struct {
	log                   *log.Logger
	execTaskConfigsByName map[string]GenericExecConfig
	puppetConfig          *puppetconfig.PuppetConfig
	mutexQueues           map[string]chan mutexQueueMessage
	notifyCallback        func(message string)

	cmdFactory func(name string, argValues TemplateGetter, arg ...string) (*exec.Cmd, error)
}

type GenericExecManagerInterface interface {
	RunTask(taskName string, getter TemplateGetter) <-chan GenericExecResult
}

type GenericExecConfig struct {
	Name           string
	Command        string
	Args           []string
	SuccessMessage string
	ErrorMessage   string
	Reentrant      bool
}

type GenericExecResult struct {
	Name     string
	ExitCode int
	StdOut   string
	StdErr   string
	Message  string
}

type mutexQueueMessage struct {
	cmd            *exec.Cmd
	execTaskConfig *GenericExecConfig
	requestValues  TemplateGetter
	resultChan     chan GenericExecResult
}

type TemplateGetter interface {
	Get(string) string
}

func NewGenericExecManager(execTaskConfigsByName map[string]GenericExecConfig, puppetConfig *puppetconfig.PuppetConfig, log *log.Logger, notifyCallback func(message string)) *GenericExecManager {
	execManager := GenericExecManager{
		log:                   log,
		execTaskConfigsByName: execTaskConfigsByName,
		puppetConfig:          puppetConfig,
		notifyCallback:        notifyCallback,
	}
	execManager.cmdFactory = execManager.productionCmdFactory

	// Find non-reentrant commands and add queues for them.
	// Queues are per command, not task name, so if two tasks were configured that run the same
	// command and both are marked not reentrant, only one will run at a time.
	execManager.mutexQueues = make(map[string]chan mutexQueueMessage, len(execTaskConfigsByName))
	for _, execConfig := range execTaskConfigsByName {
		if _, queueCreated := execManager.mutexQueues[execConfig.Command]; !queueCreated && !execConfig.Reentrant {
			execManager.mutexQueues[execConfig.Command] = make(chan mutexQueueMessage, 50)
			go execManager.mutexQueueConsumer(execManager.mutexQueues[execConfig.Command])
		}
	}

	return &execManager
}

func (ctx *GenericExecManager) IsTaskConfigured(taskName string) bool {
	_, found := ctx.execTaskConfigsByName[taskName]
	return found
}

func (ctx *GenericExecManager) RunTask(taskName string, argValues TemplateGetter) <-chan GenericExecResult {
	resultChan := make(chan GenericExecResult, 1)

	// Translate task to Cmd.
	execConfig, found := ctx.execTaskConfigsByName[taskName]
	if !found {
		panic(fmt.Sprintf("No task configuration for task \"%s\"", taskName))
	}
	cmd, err := ctx.cmdFactory(execConfig.Command, argValues, execConfig.Args...)
	if err != nil {
		resultChan <- GenericExecResult{
			Name:     taskName,
			ExitCode: 1,
			StdOut:   "",
			StdErr:   err.Error(),
		}
		close(resultChan)

		ctx.log.Printf("Could not prepare an executable command from the configuration for task %s: %v", taskName, err)
		return resultChan
	}

	if execConfig.Reentrant {
		go ctx.doRunRunRunDaDooRunRun(cmd, &execConfig, argValues, resultChan)
	} else {
		ctx.mutexQueues[execConfig.Command] <- mutexQueueMessage{
			cmd:            cmd,
			execTaskConfig: &execConfig,
			requestValues:  argValues,
			resultChan:     resultChan,
		}
	}

	return resultChan
}

// https://en.wikipedia.org/wiki/Da_Doo_Ron_Ron
func (ctx *GenericExecManager) doRunRunRunDaDooRunRun(cmd *exec.Cmd, execConfig *GenericExecConfig, templateValues TemplateGetter, resultChan chan<- GenericExecResult) {
	outBuffer := &bytes.Buffer{}
	errBuffer := &bytes.Buffer{}
	cmd.Stdout = outBuffer
	cmd.Stderr = errBuffer

	result := GenericExecResult{Name: execConfig.Name}
	err := cmd.Run()
	result.StdErr = errBuffer.String()
	errBuffer.Truncate(0)
	result.StdOut = outBuffer.String()
	outBuffer.Truncate(0)
	if err != nil {
		result.ExitCode = 1
		// It takes two(!) type assertions to get at the exit code.
		if exitErr, isExitErr := err.(*exec.ExitError); isExitErr {
			if waitStatus, isWaitStatus := exitErr.Sys().(syscall.WaitStatus); isWaitStatus {
				result.ExitCode = waitStatus.ExitStatus()
			}
		}
	} else {
		result.ExitCode = 0
	}

	// Send notifications if configured, and log.
	var logMsg, notificationMsg string
	if result.ExitCode == 0 {
		if execConfig.SuccessMessage != "" {
			logMsg, err = renderMessageTemplate(execConfig.SuccessMessage, templateValues, &result.StdOut, &result.StdErr)
			if err != nil {
				logMsg = fmt.Sprintf("Command \"%s\" exited 0. However, an error occurred processing the success Message template: %v", cmdStringApproximation(cmd), err)
			} else {
				notificationMsg = logMsg
			}
		} else {
			// Log, but no notification if no explicit SuccessMessage was configured.
			logMsg = fmt.Sprintf("Command \"%s\" exited 0.", cmdStringApproximation(cmd))
		}
	} else {
		if len(result.StdOut) == 0 && len(result.StdErr) == 0 {
			logMsg = fmt.Sprintf("Command \"%s\" exited %d!", cmdStringApproximation(cmd), result.ExitCode)
		} else if len(result.StdOut) == 0 {
			logMsg = fmt.Sprintf("Command \"%s\" exited %d! On StdErr: %s", cmdStringApproximation(cmd), result.ExitCode, result.StdErr)
		} else if len(result.StdErr) == 0 {
			logMsg = fmt.Sprintf("Command \"%s\" exited %d! On StdOut: %s", cmdStringApproximation(cmd), result.ExitCode, result.StdOut)
		} else {
			logMsg = fmt.Sprintf("Command \"%s\" exited %d! On StdOut: %s\nOn StdErr: %s", cmdStringApproximation(cmd), result.ExitCode, result.StdOut, result.StdErr)
		}

		if execConfig.ErrorMessage != "" {
			notificationMsg, err = renderMessageTemplate(execConfig.ErrorMessage, templateValues, &result.StdOut, &result.StdErr)
			if err != nil {
				notificationMsg = ""
				logMsg = logMsg + fmt.Sprintf("Additionally, an error occurred processing the error Message template: %v", err)
			}
		}
	}

	if logMsg != "" {
		ctx.log.Println(logMsg)
	}

	if notificationMsg != "" {
		ctx.notifyCallback(notificationMsg)
		result.Message = notificationMsg
	}

	resultChan <- result
	close(resultChan)
}

func (ctx *GenericExecManager) mutexQueueConsumer(queue <-chan mutexQueueMessage) {
	for message, isOpen := <-queue; isOpen; message, isOpen = <-queue {
		ctx.doRunRunRunDaDooRunRun(message.cmd, message.execTaskConfig, message.requestValues, message.resultChan)
	}
}

func (ctx *GenericExecManager) productionCmdFactory(name string, argValues TemplateGetter, arg ...string) (*exec.Cmd, error) {
	// Pass arguments through the template engine.
	renderedArgs, err := renderArgTemplates(arg, argValues)
	if err != nil {
		return nil, err
	}

	// Do magic if the command is the keyword "puppet".
	if name == "puppet" {
		name = ctx.puppetConfig.PuppetExecutable
		// Need to pass these for non-root puppet cli to act on puppet master file locations :|
		origArg := renderedArgs
		renderedArgs = []string{origArg[0], "--config", ctx.puppetConfig.ConfFile, "--confdir", ctx.puppetConfig.ConfDir}
		renderedArgs = append(renderedArgs, origArg[1:]...)
	}

	cmd := exec.Command(name, renderedArgs...)
	return cmd, nil
}

func renderArgTemplates(args []string, argValues TemplateGetter) ([]string, error) {
	funcMap := template.FuncMap{
		"request": argValues.Get,
	}
	renderedArgs := make([]string, len(args))
	for ix, templateString := range args {
		templateEngine := template.New("args processor").Funcs(funcMap)
		tmpl, err := templateEngine.Parse(templateString)
		if err != nil {
			return nil, err
		}
		var outBuf bytes.Buffer
		tmpl.Execute(&outBuf, nil)
		renderedArgs[ix] = outBuf.String()
	}
	return renderedArgs, nil
}

func renderMessageTemplate(messageTemplate string, values TemplateGetter, stdout *string, stderr *string) (string, error) {
	funcMap := template.FuncMap{
		"request": values.Get,
		"StdOut": func() string {
			return strings.Trim(*stdout, " \n")
		},
		"StdErr": func() string {
			return strings.Trim(*stderr, " \n")
		},
	}
	templateEngine := template.New("Message processor").Funcs(funcMap)
	tmpl, err := templateEngine.Parse(messageTemplate)
	if err != nil {
		return "", err
	}
	var outBuf bytes.Buffer
	tmpl.Execute(&outBuf, nil)
	return outBuf.String(), nil
}

func cmdStringApproximation(cmd *exec.Cmd) string {
	// Result will likely be shorter than 4k, so one malloc will occur. If we're wrong, the slice will just malloc more.
	temp := make([]byte, 4096)
	buffer := bytes.NewBuffer(temp)
	buffer.Reset()

	if len(cmd.Env) > 0 && cmd.Env[0] == "GO_WANT_HELPER_PROCESS=1" {
		// For tests.
		buffer.WriteString(strings.Join(cmd.Args[3:], " "))
	} else {
		// For production.
		buffer.WriteString(cmd.Path)
		if len(cmd.Args) > 0 {
			buffer.WriteString(" ")
			buffer.WriteString(strings.Join(cmd.Args, " "))
		}
	}

	return buffer.String()
}
