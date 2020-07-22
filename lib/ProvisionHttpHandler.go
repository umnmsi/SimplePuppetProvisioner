package lib

import (
	"encoding/json"
	"fmt"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/certsign"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/genericexec"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/nodeconfig"
	"io/ioutil"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

type ProvisionHttpHandler struct {
	appConfig      *AppConfig
	notifier       *Notifications
	certSigner     *certsign.CertSigner
	execManager    *genericexec.GenericExecManager
	nodeClassifier *nodeconfig.NodeClassifier
}

type TaskResult struct {
	Complete bool
	Success  bool
	Message  string
}

func NewProvisionHttpHandler(appConfig *AppConfig, notifier *Notifications, certSigner *certsign.CertSigner, execManager *genericexec.GenericExecManager, nodeClassifier *nodeconfig.NodeClassifier) *ProvisionHttpHandler {
	handler := ProvisionHttpHandler{appConfig: appConfig, notifier: notifier, certSigner: certSigner, execManager: execManager, nodeClassifier: nodeClassifier}

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

	responseWrapper := map[string]TaskResult{}

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

	// Set up slice for response channels we've been asked to wait on.
	waitResultChans := []reflect.SelectCase{}

	// Cert-related tasks
	var certSign, certRevoke = false, false

	// Classification tasks
	var classify = false

	for i := len(tasks) - 1; i >= 0; i-- {
		if tasks[i] == "environment" {
			environment = request.Form.Get("environment")
			if environment == "" {
				response.WriteHeader(http.StatusBadRequest)
				response.Write([]byte("Environment provisioning was listed in tasks, but the target environment was not given."))
				return
			}
			classify = true
			// Remove the environment task from the remaining tasks list.
			tasks = append(tasks[0:i], tasks[i+1:]...)
		} else if tasks[i] == "cert-sign" {
			certSign = true
			// Remove the cert task from the remaining tasks list.
			tasks = append(tasks[0:i], tasks[i+1:]...)
		} else if tasks[i] == "cert-revoke" {
			certRevoke = true
			// Remove the cert task from the remaining tasks list.
			tasks = append(tasks[0:i], tasks[i+1:]...)
		}
	}

	info := fmt.Sprintf("Provisioning %s", hostname)
	if environment != "" {
		info = info + fmt.Sprintf(" in the %s environment", environment)
	}
	ctx.notifier.Notify(fmt.Sprintf("%s...", info))

	if classify {
		primary_role := request.Form.Get("primary_role")
		classifyResultChan := ctx.nodeClassifier.Classify(hostname, environment, primary_role, true, "", "")
		if i := waits.Search("environment"); i < len(waits) && waits[i] == "environment" {
			waitResultChans = append(waitResultChans, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(classifyResultChan),
			})
		} else {
			responseWrapper["environment"] = TaskResult{
				Complete: false,
				Success:  true,
				Message:  "Classification operation was queued. To see the results in this response, include \"environment\" in the waits list.",
			}
		}
	}

	if certRevoke {
		cleaningResultChan := ctx.certSigner.Clean(hostname)
		if i := waits.Search("cert-revoke"); i < len(waits) && waits[i] == "cert-revoke" {
			waitResultChans = append(waitResultChans, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(cleaningResultChan),
			})
		} else {
			responseWrapper["cert-revoke"] = TaskResult{
				Complete: false,
				Success:  true,
				Message:  "Certificate cleaning operation was queued. To see the results in this response, include \"cert-revoke\" in the waits list.",
			}
		}
	}

	if certSign {
		signingResultChan := ctx.certSigner.Sign(hostname, false)
		if i := waits.Search("cert-sign"); i < len(waits) && waits[i] == "cert-sign" {
			waitResultChans = append(waitResultChans, reflect.SelectCase{
				Dir:  reflect.SelectRecv,
				Chan: reflect.ValueOf(signingResultChan),
			})
		} else {
			responseWrapper["cert-sign"] = TaskResult{
				Complete: false,
				Success:  true,
				Message:  "Certificate signing operation was queued. To see the results in this response, include \"cert-sign\" in the waits list.",
			}
		}

	}

	// Process generic exec tasks
	for _, requestTask := range tasks {
		if ctx.execManager.IsTaskConfigured(requestTask) {
			execResultChan := ctx.execManager.RunTask(requestTask, &request.Form, "")
			if i := waits.Search(requestTask); i < len(waits) && waits[i] == requestTask {
				waitResultChans = append(waitResultChans, reflect.SelectCase{
					Dir:  reflect.SelectRecv,
					Chan: reflect.ValueOf(execResultChan),
				})
			}
		} else {
			responseWrapper[requestTask] = TaskResult{
				Success:  false,
				Complete: true,
				Message:  "Task name is not recognized.",
			}
		}
	}
	// Wait for all operations we need to wait on
	waitsComplete := 0
	for waitsComplete < len(waitResultChans) {
		chosen, rvalue, ok := reflect.Select(waitResultChans)
		if !ok {
			waitResultChans[chosen].Chan = reflect.ValueOf(nil)
			continue
		}
		switch value := rvalue.Interface().(type) {
		case nodeconfig.NodeConfigResult:
			responseWrapper["environment"] = TaskResult{
				Complete: true,
				Success:  value.Success,
				Message:  value.Message,
			}
		case certsign.SigningResult:
			responseWrapper[fmt.Sprintf("cert-%s", value.Action)] = TaskResult{
				Complete: true,
				Success:  value.Success,
				Message:  value.Message,
			}
		case genericexec.GenericExecResult:
			responseWrapper[value.Name] = TaskResult{
				Complete: true,
				Success:  value.ExitCode == 0,
				Message:  value.Message,
			}
		}
		waitsComplete++
	}

	// Test parsing
	testWriter := json.NewEncoder(ioutil.Discard)
	if err := testWriter.Encode(&responseWrapper); err != nil {
		response.WriteHeader(http.StatusInternalServerError)
		return
	}

	//response.WriteHeader(http.StatusOK)
	response.Header().Set("Content-Type", "application/json")
	jsonWriter := json.NewEncoder(response)
	jsonWriter.Encode(&responseWrapper)
}
