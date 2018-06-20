package lib

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvalidHttpAuthTypePanics(t *testing.T) {
	appConfig := LoadTheConfig("InvalidAuthType.conf", []string{"../TestFixtures/configs"})
	sut := NewHttpProtectionMiddlewareFactory(appConfig.HttpAuth)
	expectMiddlewareThrows(sut, t, "Configuration error: HttpAuth Type \"foo\" is unsupported.\n")
}

func TestMissingAuthDbFilePanics(t *testing.T) {
	appConfig := LoadTheConfig("InvalidDbFile.conf", []string{"../TestFixtures/configs"})
	sut := NewHttpProtectionMiddlewareFactory(appConfig.HttpAuth)
	expectMiddlewareThrows(sut, t, "stat /dev/notafile: no such file or directory")
}

func TestValidAuthConfigResultsInAuthenticationRequired(t *testing.T) {
	appConfig := LoadTheConfig("NoRealm.conf", []string{"../TestFixtures/configs"})
	sut := NewHttpProtectionMiddlewareFactory(appConfig.HttpAuth)
	var called = false
	testHandler := func(response http.ResponseWriter, request *http.Request) {
		called = true
	}
	protectedHandler := sut.WrapInProtectionMiddleware(http.HandlerFunc(testHandler))

	testRequest, _ := http.NewRequest("POST", "http://0.0.0.0/", strings.NewReader(""))
	monitor := httptest.NewRecorder()
	protectedHandler.ServeHTTP(monitor, testRequest)
	if monitor.Code != 401 || called == true {
		t.Errorf("Authorization-protected request did not get protected by middleware (HTTP %d).\n", monitor.Code)
	}
}

func TestProvisionAuthDefaultsToHttpAuth(t *testing.T) {
	appConfig := LoadTheConfig("NoRealm.conf", []string{"../TestFixtures/configs"})
	if appConfig.ProvisionAuth != appConfig.HttpAuth {
		t.Errorf("ProvisionAuth did not default to HttpAuth parameters.\n")
	}
}

func TestValidProvisionAuthResultsInAuthenticationRequired(t *testing.T) {
	appConfig := LoadTheConfig("ProvisionAuth.conf", []string{"../TestFixtures/configs"})
	sut := NewHttpProtectionMiddlewareFactory(appConfig.ProvisionAuth)
	var called = false
	testHandler := func(response http.ResponseWriter, request *http.Request) {
		called = true
	}
	protectedHandler := sut.WrapInProtectionMiddleware(http.HandlerFunc(testHandler))

	testRequest, _ := http.NewRequest("POST", "http://0.0.0.0/", strings.NewReader(""))
	monitor := httptest.NewRecorder()
	protectedHandler.ServeHTTP(monitor, testRequest)
	if monitor.Code != 401 || called == true {
		t.Errorf("Authorization-protected request did not get protected by middleware (HTTP %d).\n", monitor.Code)
	}
}

func TestNoAuthConfigResultsInNoAuthenticationRequired(t *testing.T) {
	appConfig := LoadTheConfig("NoAuth.conf", []string{"../TestFixtures/configs"})
	sut := NewHttpProtectionMiddlewareFactory(appConfig.HttpAuth)
	var called = false
	testHandler := func(response http.ResponseWriter, request *http.Request) {
		called = true
	}
	protectedHandler := sut.WrapInProtectionMiddleware(http.HandlerFunc(testHandler))

	testRequest, _ := http.NewRequest("POST", "http://0.0.0.0/", strings.NewReader(""))
	monitor := httptest.NewRecorder()
	protectedHandler.ServeHTTP(monitor, testRequest)
	if monitor.Code != 200 || called == false {
		t.Errorf("Disabled HTTP protection middleware did not allow unauthenticated request through (HTTP %d).\n", monitor.Code)
	}
}

func TestValidAuthResultsInProperlyServedResponse(t *testing.T) {
	appConfig := LoadTheConfig("NoRealm.conf", []string{"../TestFixtures/configs"})
	sut := NewHttpProtectionMiddlewareFactory(appConfig.HttpAuth)
	var called = false
	testHandler := func(response http.ResponseWriter, request *http.Request) {
		called = true
	}
	protectedHandler := sut.WrapInProtectionMiddleware(http.HandlerFunc(testHandler))
	testRequest, _ := http.NewRequest("POST", "http://0.0.0.0/", strings.NewReader(""))
	testRequest.SetBasicAuth("test", "password")
	monitor := httptest.NewRecorder()
	protectedHandler.ServeHTTP(monitor, testRequest)

	if monitor.Code != 200 || called == false {
		t.Errorf("Enabled http authentication middleware did not allow properly authenticated request through (HTTP %d).\n", monitor.Code)
	}
}

func expectMiddlewareThrows(sut HttpProtectionMiddlewareFactory, t *testing.T, expect string) {
	defer func() {
		err := recover()
		if err == nil {
			t.Errorf("Http protection middleware with an expected invalid configuration did not panic.\n")
		} else {
			errT := err.(error)
			if errT.Error() != expect {
				t.Errorf("Unexpected panic message. Got '%s', expected '%s'", errT.Error(), expect)
			}
		}
	}()

	sut.WrapInProtectionMiddleware(http.NewServeMux())
}
