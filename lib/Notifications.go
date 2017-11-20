package lib

import (
	"github.com/go-chat-bot/bot"
	"github.com/go-chat-bot/bot/irc"
	_ "github.com/go-chat-bot/plugins/chucknorris" // ;)
)

type Notifications struct {
	appConfig *AppConfig
	enabled   bool
	targets   []*notificationTarget
}

type notificationTarget struct {
	bot            *bot.Bot
	notifyChannels []string
}

func (ctx *Notifications) Notify(message string) {
	if ctx.enabled {
		for _, target := range ctx.targets {
			for _, channel := range target.notifyChannels {
				target.bot.SendUnsolicitedMessage(channel, message, nil)
			}
		}
	}
}

func NewNotifications(config *AppConfig) *Notifications {
	n := new(Notifications)
	n.enabled = false
	n.appConfig = config
	n.targets = make([]*notificationTarget, 0, len(config.Notifications))

	ircConfigured := false

	for _, cn := range config.Notifications {
		if cn != nil {
			switch cn.Type {
			case "irc":
				if ircConfigured {
					config.Log.Print("Only one irc notification type is supported. The first will be used.\n")
					continue
				}
				ircConfig := cn.IrcConfig
				if ircConfig == nil || ircConfig.Nick == "" || ircConfig.Server == "" || len(ircConfig.Channels) == 0 {
					config.Log.Print("Warning: Invalid IRC configuration. No notifications will be sent.\n")
				} else {
					// Set some defaults if they're nil, to make configuration less nasty.
					if ircConfig.User == "" {
						ircConfig.User = ircConfig.Nick
					}
					n.enabled = true
					// Set up a bot for irc.
					target := notificationTarget{bot: irc.SetUp(ircConfig), notifyChannels: ircConfig.Channels}
					n.targets = append(n.targets, &target)
					// Run the full irc plugin in a separate goroutine.
					go irc.Run(nil)
					ircConfigured = true
				}
			default:
				config.Log.Printf("Warning: Unknown notification type: %s. No notifications will be sent.\n", cn.Type)
			}
		}
	}

	return n
}

func (ctx *Notifications) injectTestBot(bot *bot.Bot) {
	ctx.enabled = true
	target := notificationTarget{bot: bot, notifyChannels: []string{"testChan"}}
	ctx.targets = append(ctx.targets, &target)
}
