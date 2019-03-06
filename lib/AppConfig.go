package lib

import (
	"fmt"
	"os"

	"container/ring"
	"io"
	"io/ioutil"
	"log"
	"runtime"

	"github.com/go-chat-bot/bot/irc"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	"github.com/mbaynton/go-genericexec"
	"github.com/spf13/viper"
)

type AppConfig struct {
	BindAddress      string
	LogFile          string
	HttpAuth         *HttpAuthConfig
	ProvisionAuth    *HttpAuthConfig
	PuppetExecutable string
	PuppetConfDir    string
	PuppetConfig     *puppetconfig.PuppetConfig
	GenericExecTasks []*genericexec.GenericExecConfig
	GithubWebhooks   *WebhooksConfig

	Notifications []*NotificationsConfig
	Log           *log.Logger
	logBuffer     *RingLog
}

type HttpAuthConfig struct {
	Type   string
	Realm  string
	DbFile string
}

type NotificationsConfig struct {
	Type       string
	IrcConfig  *irc.Config
	SlackToken *string
	Webhooks   []string
}

type RingLog struct {
	out  io.Writer
	ring *ring.Ring
}

func (logBuffer *RingLog) Write(p []byte) (n int, err error) {
	n, err = logBuffer.out.Write(p)
	logBuffer.ring.Value = string(p)
	logBuffer.ring = logBuffer.ring.Next()
	return n, err
}

func LoadTheConfig(configName string, configPaths []string) AppConfig {
	if len(configPaths) == 0 {
		viper.SetConfigFile(configName)
	} else {
		viper.SetConfigName(configName) // So spp.conf.json, spp.conf.yml, ...
		for _, path := range configPaths {
			viper.AddConfigPath(path)
		}
	}

	// Can we find a properly formatted file?
	err := viper.ReadInConfig()
	if err != nil {
		panic(fmt.Errorf("Configuration file error: %s\n", err))
	}

	// Does the properly formatted file have the needed data?
	var C AppConfig
	err = viper.Unmarshal(&C)
	if err != nil {
		panic(fmt.Errorf("Configuration file error: %s\n", err))
	}

	C.setDefaults()
	C.establishLogger()

	configLoader := puppetconfig.NewPuppetConfigParser(C.Log)
	puppetConfig := configLoader.LoadPuppetConfig(C.PuppetExecutable, C.PuppetConfDir)
	if puppetConfig == nil {
		C.Log.Println("Invalid puppet installation, cannot proceed.")
		os.Exit(1)
	}
	C.PuppetConfig = puppetConfig

	return C
}

func (ctx *AppConfig) setDefaults() {
	if ctx.ProvisionAuth == nil {
		ctx.ProvisionAuth = ctx.HttpAuth
	}
	for _, authConfig := range []*HttpAuthConfig{ctx.HttpAuth, ctx.ProvisionAuth} {
		if authConfig != nil && authConfig.Realm == "" {
			hostname, err := os.Hostname()
			if err == nil {
				authConfig.Realm = hostname
			} else {
				authConfig.Realm = "[realm not configured]"
			}
		}
	}

	if ctx.PuppetExecutable == "" {
		ctx.PuppetExecutable = "/opt/puppetlabs/bin/puppet"
	}
	if ctx.PuppetConfDir == "" {
		if runtime.GOOS == "windows" {
			ctx.PuppetConfDir = "C:\\ProgramData\\PuppetLabs\\puppet\\etc"
		} else {
			ctx.PuppetConfDir = "/etc/puppetlabs/puppet"
		}
	}

	if ctx.GithubWebhooks == nil {
		ctx.GithubWebhooks = &WebhooksConfig{
			EnableStandardR10kListener: false,
			Listeners:                  make([]ExecListener, 0),
		}
	}
}

func (ctx *AppConfig) establishLogger() {
	r := ring.New(50)
	for i := 0; i < r.Len(); i++ {
		r.Value = ""
		r = r.Next()
	}
	ctx.logBuffer = &RingLog{ring: r, out: os.Stdout}
	ctx.Log = log.New(ctx.logBuffer, "", log.LstdFlags)
}

func (ctx *AppConfig) MoveLoggingToFile() {
	var logOutput io.Writer
	var newLocation string
	if ctx.LogFile != "" {
		fileOutput, err := os.OpenFile(ctx.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
		if err != nil {
			fmt.Errorf("Unable to create or open logfile: %s\n", err.Error())
			os.Exit(1)
		}
		logOutput = fileOutput
		newLocation = ctx.LogFile
	} else {
		// A null writer
		logOutput = ioutil.Discard
		newLocation = "a black hole (log location not configured.)"
	}
	ctx.Log.Printf("This log is moving to %s", newLocation)
	ctx.logBuffer.out = logOutput
	ctx.Log.Printf("--- continuation of logging that began on stdout ---")
}
