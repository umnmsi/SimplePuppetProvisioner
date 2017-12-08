package lib

import (
	"testing"
)

func TestHttpAuthConfig(t *testing.T) {
	testConfig := LoadTheConfig("../TestFixtures/configs/NoRealm.conf.yml", []string{})
	if testConfig.HttpAuth == nil {
		t.Errorf("Http Authentication configuration was not unmarshaled\n")
	}
	if testConfig.HttpAuth.Type != "basic" {
		t.Errorf("Http Authentication type was not loaded from config. Expected 'basic', got '%s'\n", testConfig.HttpAuth.Type)
	}
}

func TestDefaultHttpAuthRealm(t *testing.T) {
	testConfig := LoadTheConfig("../TestFixtures/configs/NoRealm.conf.yml", []string{})
	if testConfig.HttpAuth.Realm == "" {
		t.Errorf("No default http authentication realm was set.\n")
	}
}

func TestGenericExecTasks(t *testing.T) {
	testConfig := LoadTheConfig("../TestFixtures/configs/ExecTasks.conf.yml", []string{})
	if len(testConfig.GenericExecTasks) != 2 {
		t.Errorf("Expected two GenericExecTask items\n.")
	}

	expect := "Command1"
	if testConfig.GenericExecTasks[0].Command != expect {
		t.Errorf("Expected to read Generic exec task command %s, got %s", expect, testConfig.GenericExecTasks[0].Command)
	}

	expect = "Command2"
	if testConfig.GenericExecTasks[1].Command != expect {
		t.Errorf("Expected to read Generic exec task command %s, got %s", expect, testConfig.GenericExecTasks[0].Command)
	}
}
