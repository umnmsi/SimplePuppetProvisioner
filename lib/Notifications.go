package lib

import (
	"bytes"
	"encoding/json"
	"github.com/go-chat-bot/bot"
	"github.com/go-chat-bot/bot/irc"
	_ "github.com/go-chat-bot/plugins/chucknorris" // ;)
	"io/ioutil"
	"net/http"
	"time"
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
				target.bot.SendMessage(bot.OutgoingMessage{
					Target:  channel,
					Message: message,
				})
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
				config.Log.Print("Configuring notifications for IRC\n")
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
					//go irc.Run(nil)
					ircConfigured = true
				}
			case "gchat":
				config.Log.Print("Configuring notifications for Google Chat\n")
				if len(cn.Webhooks) == 0 {
					config.Log.Print("Warning: Invalid Google Chat configuration. No notifications will be sent.\n")
				} else {
					n.enabled = true
					target := notificationTarget{
						bot: bot.New(&bot.Handlers{
							Response: gChatResponseHandlerWrapper(config),
						},
							&bot.Config{
								Protocol: `gchat`,
								Server:   `gchat`,
							}),
						notifyChannels: cn.Webhooks,
					}
					n.targets = append(n.targets, &target)
				}
			default:
				config.Log.Printf("Warning: Unknown notification type: %s. No notifications will be sent.\n", cn.Type)
			}
		}
	}

	return n
}

func gChatResponseHandlerWrapper(config *AppConfig) func(string, string, *bot.User) {
	return func(webhookURL string, message string, sender *bot.User) {
		if message != "" {
			requestArgs := map[string]string{"text": message}
			requestJSON, _ := json.Marshal(requestArgs)
			httpClient := &http.Client{
				Timeout: time.Second * 5,
			}
			resp, err := httpClient.Post(webhookURL, "application/json; charset=UTF-8", bytes.NewBuffer(requestJSON))
			if err != nil {
				config.Log.Printf("HTTP client error: %s\n", err)
			} else {
				body, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					config.Log.Printf("Failed to read resp body: %s\n", err)
				} else {
					if resp.StatusCode < 200 || resp.StatusCode > 299 {
						config.Log.Printf("Failed to post message to %s: %s\n", webhookURL, string(body))
					}
				}
			}
			defer resp.Body.Close()
		}
	}
}

func (ctx *Notifications) injectTestBot(bot *bot.Bot) {
	ctx.enabled = true
	target := notificationTarget{bot: bot, notifyChannels: []string{"testChan"}}
	ctx.targets = append(ctx.targets, &target)
}
