package lib

import (
	"fmt"
	"os"

	"bufio"
	"github.com/go-chat-bot/bot/irc"
	"github.com/spf13/viper"
	"io"
	"io/ioutil"
	"log"
)

type AppConfig struct {
	BindAddress   string
	LogFile       string
	HttpAuth      *HttpAuthConfig
	Notifications []*NotificationsConfig

	Log *log.Logger
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
	viper.SetConfigName(configName) // So spp.conf.json, spp.conf.yml, ...
	for _, path := range configPaths {
		viper.AddConfigPath(path)
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
}

func (ctx *AppConfig) establishLogger() {
	var logOutput io.Writer
	if ctx.LogFile != "" {
		fileOutput, err := os.OpenFile(ctx.LogFile, os.O_APPEND|os.O_CREATE, 0640)
		if err != nil {
			fmt.Errorf("Unable to create or open logfile: %s\n", err.Error())
			os.Exit(1)
		}
		logOutput = bufio.NewWriter(fileOutput)
	} else {
		// A null writer
		logOutput = ioutil.Discard
	}

	ctx.Log = log.New(logOutput, "", log.LstdFlags)
}
