package lib

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/certsign"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/genericexec"
	"net/http"
	"time"
)

type HttpServer struct {
	appConfig   AppConfig
	notifier    *Notifications
	certSigner  *certsign.CertSigner
	execManager *genericexec.GenericExecManager
	server      http.Server
	startTime   time.Time
}

func NewHttpServer(config AppConfig, notifier *Notifications, certSigner *certsign.CertSigner, execManager *genericexec.GenericExecManager) *HttpServer {
	server := new(HttpServer)
	server.appConfig = config
	server.notifier = notifier
	server.certSigner = certSigner
	server.execManager = execManager

	return server
}

func (c *HttpServer) Start() {
	router := http.NewServeMux()
	c.createRoutes(router)
	c.server = http.Server{Addr: c.appConfig.BindAddress, Handler: router, ErrorLog: c.appConfig.Log}
	c.startTime = time.Now()
	c.server.ListenAndServe()
}

func (c *HttpServer) Shutdown(ctx context.Context) error {
	return c.server.Shutdown(ctx)
}

func (c *HttpServer) createRoutes(router *http.ServeMux) {
	router.Handle("/stats", http.HandlerFunc(c.internalStatsHandler))

	router.Handle("/webhook", NewGithubWebhookHttpHandler(c.appConfig.GithubWebhooks, c.execManager, c.appConfig.Log))

	router.Handle("/nodeconfig", NewNodeConfigHandler(&c.appConfig, c.notifier, c.certSigner, c.execManager))

	provisionProtectionMiddlewareFactory := NewHttpProtectionMiddlewareFactory(c.appConfig.ProvisionAuth)
	provisionHandler := NewProvisionHttpHandler(&c.appConfig, c.notifier, c.certSigner, c.execManager)

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
