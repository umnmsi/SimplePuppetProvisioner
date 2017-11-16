package lib

import (
	"testing"
)

func TestHttpAuthConfig(t *testing.T) {
	testConfig := LoadTheConfig("NoRealm.conf", []string{"../TestFixtures/configs"})
	if testConfig.HttpAuth == nil {
		t.Errorf("Http Authentication configuration was not unmarshaled\n")
	}
	if testConfig.HttpAuth.Type != "basic" {
		t.Errorf("Http Authentication type was not loaded from config. Expected 'basic', got '%s'\n", testConfig.HttpAuth.Type)
	}
}

func TestDefaultHttpAuthRealm(t *testing.T) {
	testConfig := LoadTheConfig("NoRealm.conf", []string{"../TestFixtures/configs"})
	if testConfig.HttpAuth.Realm == "" {
		t.Errorf("No default http authentication realm was set.\n")
	}
}
