package lib

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/genericexec"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/githubwebhook"
	"github.com/oliveagle/jsonpath"
	"io"
	"io/ioutil"
	"log"
	"net/http"
)

type GithubWebhookHttpHandler struct {
	webhookConfig *WebhooksConfig
	execManager   genericexec.GenericExecManagerInterface
	log           *log.Logger
	ResultChan    chan githubwebhook.GitHubWebhookResult
}

type WebhooksConfig struct {
	Secret                     string
	EnableStandardR10kListener bool
	R10kExecutable             string
	Listeners                  []ExecListener
}

type ExecListener struct {
	Event      string
	Secret     string
	ExecConfig genericexec.GenericExecConfig
}

type jsonTemplateGetter struct {
	jsonData *interface{}
}

func NewGithubWebhookHttpHandler(config *WebhooksConfig, execManager genericexec.GenericExecManagerInterface, log *log.Logger) *GithubWebhookHttpHandler {
	webhookHandler := GithubWebhookHttpHandler{
		webhookConfig: config,
		execManager:   execManager,
		log:           log,
	}
	webhookHandler.ResultChan = make(chan githubwebhook.GitHubWebhookResult, 5)

	if webhookHandler.webhookConfig.R10kExecutable == "" {
		webhookHandler.webhookConfig.R10kExecutable = "/opt/puppetlabs/puppet/bin/r10k"
	}

	return &webhookHandler
}

func SetWebhookExecTaskConfigMap(config *WebhooksConfig, configMap map[string]genericexec.GenericExecConfig) {
	for _, listener := range config.Listeners {
		configMap[listener.ExecConfig.Name] = listener.ExecConfig
	}
}

func (ctx *GithubWebhookHttpHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.WriteHeader(http.StatusMethodNotAllowed)
		response.Write([]byte("This listener accepts only HTTP POST method requests."))
		return
	}

	eventType := request.Header.Get("X-GitHub-Event")
	if eventType == "" {
		response.WriteHeader(http.StatusBadRequest)
		response.Write([]byte("This listener accepts only requests compliant with the GitHub webhook API, including the X-GitHub-Event header. See https://developer.github.com/webhooks/."))
		return
	}

	maxRequestBodyBytes := int64(1024 * 1024 * 2)
	body, err := ioutil.ReadAll(io.LimitReader(request.Body, maxRequestBodyBytes))
	if err != nil {
		response.WriteHeader(http.StatusInternalServerError)
		response.Write([]byte(fmt.Sprintf("Error reading request body: %v", err.Error())))
		ctx.log.Printf("%p Error reading webhook request body for %s event: %v", request, eventType, err)
		return
	}
	if int64(len(body)) == maxRequestBodyBytes {
		response.WriteHeader(http.StatusRequestEntityTooLarge)
		response.Write([]byte(fmt.Sprintf("Request body must be less than %d bytes.", maxRequestBodyBytes)))
		ctx.log.Printf("%p Not processing webhook request json data of more than %d bytes for %s event.", request, maxRequestBodyBytes, eventType)
		return
	}

	secret := ctx.webhookConfig.Secret
	if secret != "" {
		// Verify HMAC.
		actualSignature := request.Header.Get("X-Hub-Signature")
		if actualSignature == "" {
			response.WriteHeader(http.StatusUnauthorized)
			response.Write([]byte("This listener has a signing secret configured, but the request lacked a signature. Be sure the secret is also set in your webhook configuration on GitHub."))
			ctx.log.Printf("%p Not processing webhook request for %s event: Missing X-Hub-Signature header.", request, eventType)
			return
		}

		expectedSignature := ctx.computeExpectedSignature(body)

		if !hmac.Equal([]byte(expectedSignature), []byte(actualSignature)) {
			response.WriteHeader(http.StatusForbidden)
			response.Write([]byte("HMAC signature verification failed. Ensure the secret configured on this listener and the secret configured for the webhook on GitHub match."))
			ctx.log.Printf("Not processing webhook request for %s event: HMAC signature verification failure.", eventType)
			return
		}
	}

	var bodyJson interface{}
	err = json.Unmarshal(body, &bodyJson)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		response.Write([]byte("The request body was not valid JSON."))
		ctx.log.Printf("%p Not processing webhook request for %s event: Request body was invalid JSON: %v", request, eventType, err)
		return
	}

	templateGetter := jsonTemplateGetter{jsonData: &bodyJson}

	matchedListeners := 0
	for _, listener := range ctx.webhookConfig.Listeners {
		// Does the listener match the event?
		if listener.Event != "" && listener.Event == eventType {
			eventUuid := uuid.New()
			res, _ := jsonpath.JsonPathLookup(bodyJson, "$.commits[:].id")
			commits, ok := res.([]interface{})
			if !ok {
				ctx.log.Printf("%p Failed to determine commits from webhook push, not sending event", request)
			} else {
				commitStrings := make([]string, len(commits))
				for i := range commits {
					commitStrings[i] = commits[i].(string)
				}
				ctx.ResultChan <- githubwebhook.GitHubWebhookResult{
					Event:   listener.Event,
					Commits: commitStrings,
					UUID:    eventUuid.String(),
				}
			}
			ctx.execManager.RunTask(listener.ExecConfig.Name, templateGetter, eventUuid.String())
			matchedListeners++
		}
	}

	ctx.log.Printf("%p %d listener(s) matched incoming GitHub Webhook %s event.", request, matchedListeners, eventType)
	response.WriteHeader(http.StatusOK)
	response.Write([]byte(fmt.Sprintf("%d listeners matched.", matchedListeners)))
}

// Precondition: ctx.webhookConfig.Secret is set.
func (ctx *GithubWebhookHttpHandler) computeExpectedSignature(body []byte) string {
	secret := ctx.webhookConfig.Secret
	mac := hmac.New(sha1.New, []byte(secret))
	mac.Write(body)
	expectedSignature := fmt.Sprintf("sha1=%s", hex.EncodeToString(mac.Sum(nil)))
	return expectedSignature
}

func (ctx jsonTemplateGetter) Get(path string) string {
	res, err := jsonpath.JsonPathLookup(*ctx.jsonData, path)
	if err != nil {
		return ""
	}

	return fmt.Sprintf("%v", res)
}

func StandardR10kListenerConfig(config *WebhooksConfig) ExecListener {
	return ExecListener{
		Event: "push",
		ExecConfig: genericexec.GenericExecConfig{
			Name:    "R10k sync",
			Command: config.R10kExecutable,
			Args:    []string{"deploy", "environment", "--puppetfile"},
			// TODO: add a nice success and error message, possibly with templatized if's for single / multiple commits in the push
		},
	}
}
