package lib

import (
	"encoding/json"
	"net/http"
	"time"
)

type HttpServer struct {
	appConfig AppConfig
	notifier  *Notifications
	server    http.Server
	startTime time.Time
}

func NewHttpServer(config AppConfig, notifier *Notifications) *HttpServer {
	server := new(HttpServer)
	server.appConfig = config
	server.notifier = notifier

	return server
}

func (c *HttpServer) Start() {
	router := http.NewServeMux()
	c.createRoutes(router)
	c.server = http.Server{Addr: c.appConfig.BindAddress, Handler: router, ErrorLog: c.appConfig.Log}
	c.startTime = time.Now()
	c.server.ListenAndServe()
}

func (c *HttpServer) createRoutes(router *http.ServeMux) {
	router.Handle("/stats", http.HandlerFunc(c.internalStatsHandler))

	protectionMiddlewareFactory := NewHttpProtectionMiddlewareFactory(c.appConfig)
	protectedRoutes := http.NewServeMux()

	provisionHandler := NewProvisionHttpHandler(&c.appConfig, c.notifier)
	protectedRoutes.Handle("/provision", provisionHandler)

	// If it didn't match an unprotected route, it goes through the protection middleware.
	router.Handle("/", protectionMiddlewareFactory.WrapInProtectionMiddleware(protectedRoutes))
}

func (c *HttpServer) internalStatsHandler(response http.ResponseWriter, request *http.Request) {
	type statsResponseType struct {
		Uptime string `json:"uptime"`
	}

	statsResponse := new(statsResponseType)

	// Compute uptime.
	t := time.Now()
	statsResponse.Uptime = t.Sub(c.startTime).String()

	response.Header().Set("Content-Type", "application/json")
	jsonWriter := json.NewEncoder(response)
	if err := jsonWriter.Encode(&statsResponse); err != nil {
		response.WriteHeader(http.StatusInternalServerError)
	}
}
