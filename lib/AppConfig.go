package lib

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
	"github.com/go-chat-bot/bot/irc"
)

type AppConfig struct {
	BindAddress string
	HttpAuth    *HttpAuthConfig
	Notifications *NotificationsConfig
}

type HttpAuthConfig struct {
	Type   string
	Realm  string
	DbFile string
}

type NotificationsConfig struct {
	Type string
	IrcConfig *irc.Config
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
