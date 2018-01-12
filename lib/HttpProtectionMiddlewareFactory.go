package lib

// Middleware enforcing authentication of requests according to the configuration.

import (
	"fmt"
	"net/http"

	"github.com/abbot/go-http-auth"
)

type HttpProtectionMiddlewareFactory struct {
	config *AppConfig

	protectingMiddleware http.Handler
	protectedHandler     http.Handler
}

func NewHttpProtectionMiddlewareFactory(config AppConfig) HttpProtectionMiddlewareFactory {
	handler := new(HttpProtectionMiddlewareFactory)
	handler.config = &config
	return *handler
}

func (ctx *HttpProtectionMiddlewareFactory) WrapInProtectionMiddleware(nestedHandler http.Handler) http.Handler {
	authConfig := ctx.config.HttpAuth
	if authConfig == nil { // No authentication required.
		return nestedHandler
	} else {
		switch authConfig.Type {
		case "basic":
			secrets := auth.HtpasswdFileProvider(authConfig.DbFile)
			// Verify the secrets provider doesn't panic before we start using it to service requests.
			secrets("test", "realm")
			authenticator := auth.NewBasicAuthenticator(authConfig.Realm, secrets)
			ctx.protectingMiddleware = authenticator.Wrap(ctx.handle)
		case "digest":
			secrets := auth.HtdigestFileProvider(authConfig.DbFile)
			secrets("test", "realm")
			authenticator := auth.NewDigestAuthenticator(authConfig.Realm, secrets)
			ctx.protectingMiddleware = authenticator.Wrap(ctx.handle)
		default:
			panic(fmt.Errorf("Configuration error: HttpAuth Type \"%s\" is unsupported.\n", authConfig.Type))
		}

		ctx.protectedHandler = nestedHandler
		return ctx
	}
}

func (ctx *HttpProtectionMiddlewareFactory) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	ctx.protectingMiddleware.ServeHTTP(response, request)
}

// This func exists just because it provides the signature the authenticator.Wrap method is looking for.
func (ctx *HttpProtectionMiddlewareFactory) handle(w http.ResponseWriter, request *auth.AuthenticatedRequest) {
	ctx.protectedHandler.ServeHTTP(w, &request.Request)
}
