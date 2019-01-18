package lib

import (
	"bytes"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/genericexec"
	"html/template"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type execManagerMock struct {
	execTaskConfigsByName map[string]genericexec.GenericExecConfig
	lastTaskName          string
	resultChans           []chan genericexec.GenericExecResult
}

func (ctx *execManagerMock) RunTask(taskName string, argValues genericexec.TemplateGetter) <-chan genericexec.GenericExecResult {
	ctx.lastTaskName = taskName

	returnChan := make(chan genericexec.GenericExecResult, 1)
	argsRendered, _ := renderArgTemplates(ctx.execTaskConfigsByName[taskName].Args, argValues)
	argsString := strings.Join(argsRendered, " ")
	returnChan <- genericexec.GenericExecResult{
		Name:     taskName,
		ExitCode: 0,
		StdOut:   argsString,
		StdErr:   "",
		Message:  "",
	}
	close(returnChan)
	ctx.resultChans = append(ctx.resultChans, returnChan)
	return returnChan
}

func renderArgTemplates(args []string, argValues genericexec.TemplateGetter) ([]string, error) {
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

func newTestLogger() (*log.Logger, *bytes.Buffer) {
	var logBuf bytes.Buffer
	testLog := log.New(&logBuf, "", 0)
	return testLog, &logBuf
}

func sutFactory(config *WebhooksConfig) (*GithubWebhookHttpHandler, *bytes.Buffer, *execManagerMock) {
	if config == nil {
		config = &WebhooksConfig{
			EnableStandardR10kListener: true,
			R10kExecutable:             "r10k",
		}
	}

	if config.EnableStandardR10kListener {
		config.Listeners = append(config.Listeners, StandardR10kListenerConfig(config))
	}

	testLog, testLogBuf := newTestLogger()

	execConfigMap := make(map[string]genericexec.GenericExecConfig, 5)
	SetWebhookExecTaskConfigMap(config, execConfigMap)
	execManager := &execManagerMock{
		execTaskConfigsByName: execConfigMap,
	}

	handler := NewGithubWebhookHttpHandler(config, execManager, testLog)

	return handler, testLogBuf, execManager
}

func TestWebhookHandlerWrongMethod(t *testing.T) {
	req, err := http.NewRequest("GET", "http://0.0.0.0/webhook", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}

	sut, _, _ := sutFactory(nil)
	response := httptest.NewRecorder()
	sut.ServeHTTP(response, req)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET method request did not result in method not allowed.")
	}
}

func TestWebhookHandlerMissingEvent(t *testing.T) {
	req, err := http.NewRequest("POST", "http://0.0.0.0/webhook", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}

	sut, _, _ := sutFactory(nil)
	response := httptest.NewRecorder()
	sut.ServeHTTP(response, req)

	if response.Code != http.StatusBadRequest {
		t.Errorf("POST with missing X-GitHub-Event did not result in Bad Request.")
	}

	expect := "This listener accepts only requests compliant with the GitHub webhook API, including the X-GitHub-Event header. See https://developer.github.com/webhooks/."
	if response.Body.String() != expect {
		t.Errorf("Expected body \"%s\", got \"%s\"", expect, response.Body.String())
	}
}

func TestWebhookHandlerPushEvent(t *testing.T) {
	sut, testLogBuf, execManagerMock := sutFactory(&WebhooksConfig{
		Secret:                     "asdf",
		EnableStandardR10kListener: true,
		R10kExecutable:             "r10k",
		Listeners: []ExecListener{
			{Event: "push", ExecConfig: genericexec.GenericExecConfig{
				Name:           "BodyParserTest",
				Command:        "test",
				Args:           []string{"property: {{ request \"$.property\" }}", "nested: {{ request \"$.object.v1\" }}"},
				SuccessMessage: "{{ stdout }}",
			}},
		},
	})

	req := simulatedWebhookRequest(t, "push", sut)
	response := httptest.NewRecorder()
	sut.ServeHTTP(response, req)

	if response.Code != http.StatusOK {
		t.Error("Expected HTTP 200 OK, got ", response.Code, response.Body.String())
	}

	expect := "property: property value nested: 1"
	execResult := <-execManagerMock.resultChans[0]
	if execResult.StdOut != expect {
		t.Errorf("Expected simulated stdout to be \"%s\" but got \"%s\".", expect, execResult.StdOut)
	}

	expect = "deploy environment --puppetfile"
	execResult = <-execManagerMock.resultChans[1]
	if execResult.StdOut != expect {
		t.Errorf("Expected simulated stdout to be \"%s\" but got \"%s\".", expect, execResult.StdOut)
	}

	logStuff := testLogBuf.String()
	expect = "2 listener(s) matched incoming GitHub Webhook push event."
	if !strings.Contains(logStuff, expect) {
		t.Errorf("Expected log to contain \"%s\". Log: %s", expect, logStuff)
	}
}

func simulatedWebhookRequest(t *testing.T, event string, sut *GithubWebhookHttpHandler) *http.Request {
	bodyString := webhookBodyForEvent(t, event)
	req, err := http.NewRequest("POST", "http://0.0.0.0/webhook", strings.NewReader(bodyString))
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Add("X-GitHub-Event", event)
	if sut.webhookConfig.Secret != "" {
		req.Header.Add("X-Hub-Signature", sut.computeExpectedSignature([]byte(bodyString)))
	}
	return req
}

func webhookBodyForEvent(t *testing.T, event string) string {
	bodies := map[string]string{
		"push": "{\"property\": \"property value\", \"object\": {\"v1\": 1, \"v2\": 2}}",
	}

	body, found := bodies[event]
	if !found {
		t.Fatal("Unknown webhook event type to test: ", event)
	}
	return body
}
