package lib

import (
	"fmt"
	"os"

	"bufio"
	"github.com/go-chat-bot/bot/irc"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/genericexec"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/puppetconfig"
	"github.com/spf13/viper"
	"io"
	"io/ioutil"
	"log"
	"runtime"
)

type AppConfig struct {
	BindAddress      string
	LogFile          string
	HttpAuth         *HttpAuthConfig
	PuppetExecutable string
	PuppetConfDir    string
	PuppetConfig     *puppetconfig.PuppetConfig
	GenericExecTasks []*genericexec.GenericExecConfig
	WebhooksConfig   *WebhooksConfig

	Notifications []*NotificationsConfig
	Log           *log.Logger
	logBuffer     *bufio.Writer
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
	if ctx.HttpAuth != nil && ctx.HttpAuth.Realm == "" {
		hostname, err := os.Hostname()
		if err == nil {
			ctx.HttpAuth.Realm = hostname
		} else {
			ctx.HttpAuth.Realm = "[realm not configured]"
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
}

func (ctx *AppConfig) establishLogger() {
	ctx.Log = log.New(os.Stdout, "", log.LstdFlags)
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
		// Using a buffer seems like it might theoretically offer performance benefits, but would require a synchronized goroutine to flush it regularly.
		//ctx.logBuffer = bufio.NewWriter(fileOutput)
		//logOutput = ctx.logBuffer
		logOutput = fileOutput
		newLocation = ctx.LogFile
	} else {
		// A null writer
		logOutput = ioutil.Discard
		newLocation = "a black hole (log location not configured.)"
	}
	ctx.Log.Printf("This log is moving to %s", newLocation)
	ctx.Log.SetOutput(logOutput)
	ctx.Log.Printf("--- continuation of logging that began on stdout ---")
}

func (ctx *AppConfig) FlushLog() {
	if ctx.logBuffer != nil {
		ctx.logBuffer.Flush()
	}
}
