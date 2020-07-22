package lib

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/certsign"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/nodeconfig"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	. "github.com/mbaynton/SimplePuppetProvisioner/lib/testlib"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

type NodeClassifierMock struct {
	nodeClassifier         *nodeconfig.NodeClassifier
	actionTimeout          time.Duration
	shouldWait, shouldFail bool
}

func (ctx *NodeClassifierMock) Classify(hostname, environment, primary_role string, missingOK bool, requestorName, requestorEmail string) <-chan nodeconfig.NodeConfigResult {
	resultChan := make(chan nodeconfig.NodeConfigResult, 1)
	result := nodeconfig.NodeConfigResult{
		Action:      "classify",
		Success:     true,
		Node:        hostname,
		Message:     fmt.Sprintf("Updated classification for %s", hostname),
		Environment: environment,
		PrimaryRole: primary_role,
	}
	if ctx.shouldFail {
		result.Success = false
		result.Message = fmt.Sprintf("Failed to classify %s", hostname)
	}
	go func() {
		if ctx.shouldWait {
			time.Sleep(ctx.actionTimeout)
		}
		resultChan <- result
	}()
	return resultChan
}

func (ctx *NodeClassifierMock) GetClassification(node string, missingOK bool) (*nodeconfig.NodeConfig, error) {
	config, err := ctx.nodeClassifier.GetClassification(node, missingOK)
	return config, err
}

func (ctx *NodeClassifierMock) GetEnvironments() nodeconfig.EnvironmentsMsg {
	return ctx.nodeClassifier.GetEnvironments()
}

func (ctx *NodeClassifierMock) GetRoles(environment string) nodeconfig.RolesMsg {
	return ctx.nodeClassifier.GetRoles(environment)
}

type CertSignerMock struct {
	actionTimeout          time.Duration
	shouldWait, shouldFail bool
}

func (ctx *CertSignerMock) Sign(hostname string, cleanExistingCert bool) <-chan certsign.SigningResult {
	resultChan := make(chan certsign.SigningResult, 1)
	result := certsign.SigningResult{
		Action:  "sign",
		Success: true,
		Message: "Cert was signed",
	}
	if ctx.shouldFail {
		result.Success = false
		result.Message = "Failed to sign cert"
	}
	go func() {
		if ctx.shouldWait {
			time.Sleep(ctx.actionTimeout)
		}
		resultChan <- result
	}()
	return resultChan
}

type JSONMock struct {
	shouldFail bool
}

func (ctx *JSONMock) Marshal(v interface{}) ([]byte, error) {
	if ctx.shouldFail {
		return []byte{}, fmt.Errorf("JSON Marshal() failure")
	}
	json, err := json.Marshal(v)
	return json, err
}

func sutNodeConfigFactory() (*NodeConfigHandler, *bytes.Buffer, *JSONMock, *CertSignerMock, *NodeClassifierMock, error) {
	appConfig := AppConfig{
		NodeConfigTimeout: 1,
		PuppetConfig: &puppetconfig.PuppetConfig{
			EnvironmentPath: []string{"../TestFixtures/environments"},
		},
	}
	var logBuf bytes.Buffer
	appConfig.Log = log.New(&logBuf, "", 0)
	certSignerMock := CertSignerMock{actionTimeout: 2 * time.Second}
	nodesDir, err := CreateTestNodesRepo("../TestFixtures/nodes")
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	nodeClassifier, err := nodeconfig.NewNodeClassifier(nodesDir, "../TestFixtures/id_rsa_test", "git", appConfig.PuppetConfig, appConfig.Log, func(message string) {}, 1, 1)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	nodeClassifierMock := NodeClassifierMock{nodeClassifier: nodeClassifier, actionTimeout: 2 * time.Second}
	handler := NewNodeConfigHandler(&appConfig, &certSignerMock, &nodeClassifierMock)
	jsonMock := JSONMock{}
	handler.jsonMarshaller = jsonMock.Marshal
	return handler, &logBuf, &jsonMock, &certSignerMock, &nodeClassifierMock, nil
}

func TestNodeConfigHandler_InvalidPathReturnsNotFound(t *testing.T) {
	req, err := http.NewRequest("GET", "http://0.0.0.0/invalid", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	sut, _, _, _, _, err := sutNodeConfigFactory()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	sut.ServeHTTP(response, req)

	if response.Code != http.StatusNotFound {
		t.Errorf("Expected StatusNotFound for invalid path, but got %s (%d)", http.StatusText(response.Code), response.Code)
	}
}

type actionExpect struct {
	name          string
	description   string
	method        string
	action        string
	params        url.Values
	status        int
	headers       map[string]string
	bodyMatch     string
	headerMatches map[string]string
	before, after func()
}

func TestNodeConfigHandler_Action(t *testing.T) {
	sut, logBuf, jsonMock, certSignerMock, nodeClassifierMock, err := sutNodeConfigFactory()
	if err != nil {
		t.Fatal(err)
	}
	origEnvironmentPath := sut.appConfig.PuppetConfig.EnvironmentPath
	origCsrDir := sut.appConfig.PuppetConfig.CsrDir
	origNodesDir := sut.appConfig.NodesDir
	expects := []actionExpect{
		{
			name:        "MissingGecos",
			description: "when Gecos header missing to display error",
			method:      "GET",
			status:      http.StatusOK,
			headers:     map[string]string{"Gecos": ""},
			bodyMatch:   `^Missing Uid or Gecos header - authentication appears to have been skipped. Bailing.$`,
		},
		{
			name:        "MissingUid",
			description: "when Uid header missing to display error",
			method:      "GET",
			status:      http.StatusOK,
			headers:     map[string]string{"Uid": ""},
			bodyMatch:   `^Missing Uid or Gecos header - authentication appears to have been skipped. Bailing.$`,
		},
		{
			name:        "MissingGecosAndUid",
			description: "when Gecos and Uid header missing to display error",
			method:      "GET",
			status:      http.StatusOK,
			headers:     map[string]string{"Gecos": "", "Uid": ""},
			bodyMatch:   `^Missing Uid or Gecos header - authentication appears to have been skipped. Bailing.$`,
		},
		{
			name:        "MessageEscaped",
			description: "when message present to display escaped message",
			method:      "GET",
			params:      url.Values{"message": {"Test<html>message"}},
			status:      http.StatusOK,
			bodyMatch:   `<div id="message">Test&lt;html&gt;message</div>`,
		},
		{
			name:        "CsrDirNotExistPrintsError",
			description: "when CsrDir doesn't exist to print error",
			method:      "GET",
			status:      http.StatusOK,
			bodyMatch:   `Failed to read CsrDir`,
		},
		{
			name:        "CsrDirExistsPrintsRequest",
			description: "when CsrDir exists to print requests",
			method:      "GET",
			status:      http.StatusOK,
			bodyMatch:   `data-cert="testnode.msi.umn.edu"`,
			before:      func() { sut.appConfig.PuppetConfig.CsrDir = "../TestFixtures/requests" },
			after:       func() { sut.appConfig.PuppetConfig.CsrDir = origCsrDir },
		},
		{
			name:        "NodesDirNotExistPrintsError",
			description: "when NodesDir doesn't exist to print error",
			method:      "GET",
			status:      http.StatusOK,
			bodyMatch:   `Failed to read nodes dir`,
		},
		{
			name:        "NodesDirExistsPrintsNodes",
			description: "when NodesDir exists to print nodes",
			method:      "GET",
			status:      http.StatusOK,
			bodyMatch:   `<option value="complex">complex</option>`,
			before:      func() { sut.appConfig.NodesDir = "../TestFixtures/nodes" },
			after:       func() { sut.appConfig.NodesDir = origNodesDir },
		},
		{
			name:        "MissingNodeParamErrors",
			description: "when missing node parameter to fail",
			method:      "GET",
			action:      "lookupNode",
			status:      http.StatusOK,
			bodyMatch:   `^{"Status":"ERROR","Message":"Missing 'node' argument for action 'lookupNode'"}$`,
		},
		{
			name:        "NodeNotExistErrors",
			description: "when node does not exist to fail",
			method:      "GET",
			action:      "lookupNode",
			params:      url.Values{"node": {"nonexistent"}},
			status:      http.StatusOK,
			bodyMatch:   `^{"Status":"ERROR","Message":"Failed to get config for node nonexistent: Failed to read YAML file for nonexistent: open .*nonexistent.yaml: no such file or directory"}$`,
		},
		{
			name:        "NodeExistsReturnsConfig",
			description: "when node exists to return config",
			method:      "GET",
			action:      "lookupNode",
			params:      url.Values{"node": {"complex"}},
			status:      http.StatusOK,
			bodyMatch:   `{"Action":"classify","Success":true,"Message":"","Node":"complex","Environment":"persistent_systems_production","PrimaryRole":"api_msi_server__jobrunner"}$`,
		},
		{
			name:        "JSONMarshalFailureErrors",
			description: "when JSON Marshal fails to return error",
			method:      "GET",
			action:      "lookupNode",
			params:      url.Values{"node": {"complex"}},
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"ERROR","Message":"Failed to encode JSON: JSON Marshal() failure"}\E$`,
			before:      func() { jsonMock.shouldFail = true },
			after:       func() { jsonMock.shouldFail = false },
		},
		{
			name:        "EnvironmentPathInvalidErrors",
			description: "when environment path invalid to fail",
			method:      "GET",
			action:      "getEnvironments",
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"ERROR","Message":"Failed to read environment path /nonexistent: open /nonexistent: no such file or directory","Environments":null}\E$`,
			before:      func() { sut.appConfig.PuppetConfig.EnvironmentPath = []string{"/nonexistent"} },
			after:       func() { sut.appConfig.PuppetConfig.EnvironmentPath = origEnvironmentPath },
		},
		{
			name:        "EnvironmentPathValidReturnsEnvironments",
			description: "when environment path valid to return environments",
			method:      "GET",
			action:      "getEnvironments",
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"OK","Message":"","Environments":["env1","env2"]}\E$`,
		},
		{
			name:        "JSONMarshalFailureErrors",
			description: "when JSON Marshal fails to return error",
			method:      "GET",
			action:      "getEnvironments",
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"ERROR","Message":"Failed to encode JSON: JSON Marshal() failure"}\E$`,
			before:      func() { jsonMock.shouldFail = true },
			after:       func() { jsonMock.shouldFail = false },
		},
		{
			name:        "EnvironmentExistsReturnsRoles",
			description: "when environment exists to return roles",
			method:      "GET",
			action:      "getRoles",
			params:      url.Values{"environment": {"env1"}},
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"OK","Message":"","Roles":["role1","role2"]}\E$`,
		},
		{
			name:        "EnvironmentHasNoRolesDirError",
			description: "when environment has no roles directory to fail",
			method:      "GET",
			action:      "getRoles",
			params:      url.Values{"environment": {"env2"}},
			status:      http.StatusOK,
			bodyMatch:   `^{"Status":"ERROR","Message":"Failed to read role path .*: no such file or directory","Roles":\[\]}$`,
		},
		{
			name:        "EnvironmentNotSpecifiedErrors",
			description: "when environment not specified to fail",
			method:      "GET",
			action:      "getRoles",
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"ERROR","Message":"Missing 'environment' argument for action 'getRoles'"}\E$`,
		},
		{
			name:        "JSONMarshalFailureErrors",
			description: "when JSON Marshal fails to return error",
			method:      "GET",
			action:      "getRoles",
			params:      url.Values{"environment": {"env1"}},
			status:      http.StatusOK,
			bodyMatch:   `^\Q{"Status":"ERROR","Message":"Failed to encode JSON: JSON Marshal() failure"}\E$`,
			before:      func() { jsonMock.shouldFail = true },
			after:       func() { jsonMock.shouldFail = false },
		},
		{
			name:        "UnknownActionErrors",
			description: "to fail",
			method:      "GET",
			action:      "unknown&Action",
			status:      http.StatusOK,
			bodyMatch:   `<div id="message">Invalid action 'unknown&amp;Action'</div>`,
		},
		{
			name:        "UnknownActionErrors",
			description: "to fail",
			method:      "POST",
			action:      "unknown&Action",
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Invalid action 'unknown&amp;Action'`,
			},
		},
		{
			name:        "MissingCertParameterErrors",
			description: "when missing cert parameter to fail",
			method:      "POST",
			action:      "sign",
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Missing 'cert' argument for action 'sign'`,
			},
		},
		{
			name:        "CertSigned",
			description: "to succeed",
			method:      "POST",
			action:      "sign",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Cert was signed`,
			},
		},
		{
			name:        "CertSigningFailsErrors",
			description: "when cert signing fails to return error",
			method:      "POST",
			action:      "sign",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Signing failed for node1: Failed to sign cert`,
			},
			before: func() { certSignerMock.shouldFail = true },
			after:  func() { certSignerMock.shouldFail = false },
		},
		{
			name:        "CertSigningTimeoutErrors",
			description: "when cert signing times out to return error",
			method:      "POST",
			action:      "sign",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Signing failed for node1: Timed out`,
			},
			before: func() { certSignerMock.shouldWait = true },
			after:  func() { certSignerMock.shouldWait = false },
		},
		{
			name:        "MissingCertParameterErrors",
			description: "when missing cert parameter to fail",
			method:      "POST",
			action:      "classify",
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Missing 'cert' argument for action 'classify'`,
			},
		},
		{
			name:        "Classify",
			description: "when missing cert parameter to fail",
			method:      "POST",
			action:      "classify",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Updated classification for node1`,
			},
		},
		{
			name:        "ClassifyFailsErrors",
			description: "when cert signing fails to return error",
			method:      "POST",
			action:      "classify",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Failed to classify node1`,
			},
			before: func() { nodeClassifierMock.shouldFail = true },
			after:  func() { nodeClassifierMock.shouldFail = false },
		},
		{
			name:        "ClassifyTimeoutErrors",
			description: "when classification times out to return error",
			method:      "POST",
			action:      "classify",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Classification failed for node1: Timed out`,
			},
			before: func() { nodeClassifierMock.shouldWait = true },
			after:  func() { nodeClassifierMock.shouldWait = false },
		},
		{
			name:        "MissingCertParameterErrors",
			description: "when missing cert parameter to fail",
			method:      "POST",
			action:      "classifycsr",
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Missing 'cert' argument for action 'classifycsr'`,
			},
		},
		{
			name:        "Classify",
			description: "when missing cert parameter to fail",
			method:      "POST",
			action:      "classifycsr",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Updated classification for node1`,
			},
		},
		{
			name:        "ClassifyFailsErrors",
			description: "when cert signing fails to return error",
			method:      "POST",
			action:      "classifycsr",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Failed to classify node1`,
			},
			before: func() { nodeClassifierMock.shouldFail = true },
			after:  func() { nodeClassifierMock.shouldFail = false },
		},
		{
			name:        "ClassifyTimeoutErrors",
			description: "when classification times out to return error",
			method:      "POST",
			action:      "classifycsr",
			params:      url.Values{"cert": {"node1"}},
			status:      http.StatusSeeOther,
			headerMatches: map[string]string{
				"Location": `Classification failed for node1: Timed out`,
			},
			before: func() { nodeClassifierMock.shouldWait = true },
			after:  func() { nodeClassifierMock.shouldWait = false },
		},
		{
			name:        "UnknownMethodErrors",
			description: "to fail",
			method:      "HEAD",
			status:      http.StatusOK,
			bodyMatch:   `<div id="message">Invalid method 'HEAD'</div>`,
		},
	}
	for _, expect := range expects {
		t.Run(expect.method+`/`+expect.action+`/`+expect.name, func(t *testing.T) {
			if expect.after != nil {
				defer expect.after()
			}
			if expect.before != nil {
				expect.before()
			}
			data := url.Values{"action": {expect.action}}
			for k, v := range expect.params {
				data[k] = v
			}
			headers := map[string]string{
				"Uid":   "user1234",
				"Gecos": "Test User",
			}
			for k, v := range expect.headers {
				headers[k] = v
			}
			var req *http.Request
			if expect.method == "POST" {
				req, err = http.NewRequest(expect.method, "http://0.0.0.0/nodeconfig", strings.NewReader(data.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			} else {
				req, err = http.NewRequest(expect.method, "http://0.0.0.0/nodeconfig?"+data.Encode(), nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			response := httptest.NewRecorder()
			sut.ServeHTTP(response, req)

			if response.Code != expect.status {
				t.Errorf("Expected status code %d %s, but got %d %s", expect.status, http.StatusText(expect.status), response.Code, http.StatusText(response.Code))
			}
			if expect.bodyMatch != "" && !regexp.MustCompile(expect.bodyMatch).MatchString(response.Body.String()) {
				snippet := response.Body.String()
				//if len(snippet) > 256 {
				//	snippet = snippet[0:256]
				//}
				t.Errorf("Expected action '%s' %s with body matching %s, but got '%s'", expect.action, expect.description, expect.bodyMatch, snippet)
			}
			if len(expect.headerMatches) > 0 {
				for header, match := range expect.headerMatches {
					if !regexp.MustCompile(match).MatchString(response.Header().Get(header)) {
						t.Errorf("Expected action '%s' %s with header %s matching '%s', but got '%s'", expect.action, expect.description, header, match, response.Header().Get(header))
					}
				}
			}
			fmt.Printf(logBuf.String())
			logBuf.Reset()
		})
	}
}
