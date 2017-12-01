package lib

import (
	"encoding/json"
	"fmt"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/certsign"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

type ProvisionHttpHandler struct {
	appConfig  *AppConfig
	notifier   *Notifications
	certSigner *certsign.CertSigner
}

func NewProvisionHttpHandler(appConfig *AppConfig, notifier *Notifications, certSigner *certsign.CertSigner) *ProvisionHttpHandler {
	handler := ProvisionHttpHandler{appConfig: appConfig, notifier: notifier, certSigner: certSigner}
	return &handler
}

func (ctx ProvisionHttpHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		response.WriteHeader(http.StatusMethodNotAllowed)
		response.Write([]byte("This API accepts only HTTP POST method requests."))
		return
	}

	request.ParseForm()
	hostname := request.Form.Get("hostname")
	if hostname == "" {
		response.WriteHeader(http.StatusBadRequest)
		response.Write([]byte("No hostname provided."))
		return
	}

	var tasks sort.StringSlice
	tasks = strings.Split(request.Form.Get("tasks"), ",")
	tasks.Sort()
	if tasks.Len() == 0 {
		response.WriteHeader(http.StatusBadRequest)
		response.Write([]byte("No tasks provided."))
		return
	}

	var waits sort.StringSlice
	waits = strings.Split(request.Form.Get("waits"), ",")
	waits.Sort()

	var environment = ""

	// How you say "contains" in Go...
	if i := tasks.Search("environment"); i < len(tasks) && tasks[i] == "environment" {
		environment = request.Form.Get("environment")

		if environment == "" {
			response.WriteHeader(http.StatusBadRequest)
			response.Write([]byte("Environment provisioning was listed in tasks, but the target environment was not given."))
			return
		}
	}
	info := fmt.Sprintf("Provisioning %s", hostname)
	if environment == "" {
		info = info + fmt.Sprintf(" in the %s environment", environment)
	}
	ctx.notifier.Notify(fmt.Sprintf("%s...", info))

	// Set up slice for response channels we've been asked to wait on.
	waitResultChans := make([]reflect.SelectCase, len(waits))

	var signingResult *certsign.SigningResult
	if i := tasks.Search("cert"); i < len(tasks) && tasks[i] == "cert" {
		var certRevoke = false
		if request.Form.Get("cert-revoke") != "" && request.Form.Get("cert-revoke") != "false" {
			certRevoke = true
		}

		signingResultChan := ctx.certSigner.Sign(hostname, certRevoke)
		if i := waits.Search("cert"); i < len(waits) && waits[i] == "cert" {
			waitResultChans = append(waitResultChans, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(signingResultChan),
			})
		} else {
			signingResult = &certsign.SigningResult{
				Success: false,
				Message: "Certificate signing operation was queued. To see the results in this response, include \"cert\" in the waits list.",
			}
		}

	}

	// TODO: add environment setting here.

	// Wait for all operations we need to wait on
	waitsComplete := 0
	for waitsComplete < len(waitResultChans) {
		_, rvalue, _ := reflect.Select(waitResultChans)
		switch value := rvalue.Interface().(type) {
		case certsign.SigningResult:
			signingResult = &value
		}
		waitsComplete++
	}

	responseWrapper := struct {
		cert *certsign.SigningResult `json:cert`
	}{
		cert: signingResult,
	}

	response.Header().Set("Content-Type", "application/json")
	jsonWriter := json.NewEncoder(response)
	if err := jsonWriter.Encode(&responseWrapper); err != nil {
		response.WriteHeader(http.StatusInternalServerError)
	}
}
