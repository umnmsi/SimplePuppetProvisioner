package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/lib/certsign"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/lib/genericexec"
	"github.com/umnmsi/SimplePuppetProvisioner/v2/lib/nodeconfig"
	"log"
	"net/http"
	"os"
	"reflect"
	"sort"
	"time"
)

type HttpServer struct {
	appConfig                  AppConfig
	notifier                   *Notifications
	certSigner                 *certsign.CertSigner
	execManager                *genericexec.GenericExecManager
	nodeClassifier             *nodeconfig.NodeClassifier
	server                     http.Server
	startTime                  time.Time
	ResultChans, ListenerChans []reflect.SelectCase
	StartChan                  chan struct{}
	stopChan                   chan os.Signal
}

type HandlerWrapper struct {
	router      *http.ServeMux
	log         *log.Logger
	dumpHeaders bool
}

type ResponseWriterWrapper struct {
	rw         http.ResponseWriter
	r          *http.Request
	log        *log.Logger
	statusSent *bool
}

func NewHttpServer(config AppConfig, notifier *Notifications, certSigner *certsign.CertSigner, execManager *genericexec.GenericExecManager, nodeClassifier *nodeconfig.NodeClassifier, stopChan chan os.Signal) *HttpServer {
	server := new(HttpServer)
	server.appConfig = config
	server.notifier = notifier
	server.certSigner = certSigner
	server.execManager = execManager
	server.nodeClassifier = nodeClassifier
	server.ResultChans = []reflect.SelectCase{}
	server.ListenerChans = []reflect.SelectCase{}
	server.StartChan = make(chan struct{}, 1)
	server.stopChan = stopChan

	return server
}

func (w ResponseWriterWrapper) Header() http.Header {
	return w.rw.Header()
}

func (w ResponseWriterWrapper) Write(data []byte) (int, error) {
	count, err := w.rw.Write(data)
	return count, err
}

func (w ResponseWriterWrapper) WriteHeader(statusCode int) {
	w.log.Printf("%p HTTP response status %d %s\n", w.r, statusCode, http.StatusText(statusCode))
	*(w.statusSent) = true
	w.rw.WriteHeader(statusCode)
}

func (h *HandlerWrapper) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	h.log.Printf("%p Connection from %s: %s %s", r, r.RemoteAddr, r.Method, r.URL.Path)
	if h.dumpHeaders {
		h.log.Printf("%p  Headers\n", r)
		headers := make([]string, 0)
		for key, _ := range r.Header {
			headers = append(headers, key)
		}
		sort.Strings(headers)
		for _, key := range headers {
			h.log.Printf("%p   %s=%s\n", r, key, r.Header[key])
		}
	}
	h.log.Printf("%p  Form values\n", r)
	vals := make([]string, 0)
	for key, _ := range r.Form {
		vals = append(vals, key)
	}
	sort.Strings(vals)
	for _, key := range vals {
		h.log.Printf("%p   %s=%s\n", r, key, r.Form[key])
	}
	statusSent := false
	rw := ResponseWriterWrapper{rw: w, r: r, log: h.log, statusSent: &statusSent}
	h.router.ServeHTTP(rw, r)
	if !statusSent {
		h.log.Printf("%p No explicit status code sent. Implicit 200 OK\n", r)
	}
}

func (c *HttpServer) Start() {
	router := http.NewServeMux()
	wrapper := &HandlerWrapper{router: router, log: c.appConfig.Log, dumpHeaders: c.appConfig.DumpHeaders}
	c.createRoutes(router)
	c.server = http.Server{
		Addr:     c.appConfig.BindAddress,
		Handler:  wrapper,
		ErrorLog: c.appConfig.Log,
	}
	c.startTime = time.Now()
	c.StartChan <- struct{}{}
	c.appConfig.Log.Println(c.server.ListenAndServe())
	c.stopChan <- os.Interrupt
}

func (c *HttpServer) Shutdown(ctx context.Context) error {
	return c.server.Shutdown(ctx)
}

func (c *HttpServer) createRoutes(router *http.ServeMux) {
	router.Handle("/stats", http.HandlerFunc(c.internalStatsHandler))

	webhookHandler := NewGithubWebhookHttpHandler(c.appConfig.GithubWebhooks, c.execManager, c.appConfig.Log)
	router.Handle("/webhook", webhookHandler)
	c.ResultChans = append(c.ResultChans, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(webhookHandler.ResultChan),
	})

	router.Handle("/nodeconfig", NewNodeConfigHandler(&c.appConfig, c.certSigner, c.nodeClassifier))

	provisionProtectionMiddlewareFactory := NewHttpProtectionMiddlewareFactory(c.appConfig.ProvisionAuth)
	provisionHandler := NewProvisionHttpHandler(&c.appConfig, c.notifier, c.certSigner, c.execManager, c.nodeClassifier)

	router.Handle("/provision", provisionProtectionMiddlewareFactory.WrapInProtectionMiddleware(provisionHandler))

	protectionMiddlewareFactory := NewHttpProtectionMiddlewareFactory(c.appConfig.HttpAuth)
	protectedRoutes := http.NewServeMux()

	protectedRoutes.Handle("/log", http.HandlerFunc(c.logHandler))

	// If it didn't match an unprotected route, it goes through the protection middleware.
	router.Handle("/", protectionMiddlewareFactory.WrapInProtectionMiddleware(protectedRoutes))
}

func (c *HttpServer) internalStatsHandler(response http.ResponseWriter, request *http.Request) {
	type statsResponseType struct {
		Uptime             string `json:"uptime"`
		CertSigningBacklog int    `json:"cert-signing-backlog"`
	}

	statsResponse := new(statsResponseType)

	// Compute uptime.
	t := time.Now()
	statsResponse.Uptime = t.Sub(c.startTime).String()

	statsResponse.CertSigningBacklog = c.certSigner.ProcessingBacklogLength()

	response.Header().Set("Content-Type", "application/json")
	jsonWriter := json.NewEncoder(response)
	if err := jsonWriter.Encode(&statsResponse); err != nil {
		response.WriteHeader(http.StatusInternalServerError)
	}
}

func (c *HttpServer) logHandler(response http.ResponseWriter, request *http.Request) {
	c.appConfig.logBuffer.ring.Do(func(p interface{}) {
		if p.(string) != "" {
			fmt.Fprintf(response, p.(string))
		}
	})
}
