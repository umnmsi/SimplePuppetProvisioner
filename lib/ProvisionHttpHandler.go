package lib

import (
	"fmt"
	"net/http"
)

type ProvisionHttpHandler struct {
	appConfig *AppConfig
	notifier  *Notifications
}

func NewProvisionHttpHandler(appConfig *AppConfig, notifier *Notifications) *ProvisionHttpHandler {
	handler := ProvisionHttpHandler{appConfig: appConfig, notifier: notifier}
	return &handler
}

func (ctx ProvisionHttpHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	request.ParseForm()
	hostname := request.Form.Get("hostname")
	environment := request.Form.Get("environment")
	ctx.notifier.Notify(fmt.Sprintf("Node \"%s\" has requested to be provisioned in the %s environment.", hostname, environment))
}
