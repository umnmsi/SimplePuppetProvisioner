package notifications

import (
	"fmt"
	"github.com/go-chat-bot/bot/irc"
	"github.com/go-chat-bot/bot/slack"
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
	"github.com/go-chat-bot/bot"
)

type Notifications struct {
	appConfig *lib.AppConfig
	enabled bool
	notifyCallback bot.ResponseHandler
}

func (ctx *Notifications) Notify(message string) {

}

func NewNotifications(config *lib.AppConfig) {
	n := new(Notifications)
	n.appConfig = config

	if config.Notifications != nil {
		switch config.Notifications.Type {
		case "irc":
			ircConfig := config.Notifications.IrcConfig
			if (ircConfig == nil || ircConfig.Server == "" || len(ircConfig.Channels) == 0) {
				n.enabled = false
				fmt.Errorf("Warning: Invalid IRC configuration. No notifications will be sent.\n")
			} else {
				n.enabled = true

			}
		}
	}
}