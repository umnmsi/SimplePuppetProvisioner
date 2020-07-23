package lib

import (
	"bytes"
	"github.com/go-chat-bot/bot"
	"github.com/go-chat-bot/bot/irc"
	"log"
	"strings"
	"testing"
	"time"
)

func TestNewNotifications_UnknownTypeLogs(t *testing.T) {
	var buf bytes.Buffer
	testLog := log.New(&buf, "", 0)

	config := AppConfig{
		Notifications: []*NotificationsConfig{
			&NotificationsConfig{Type: "foo"},
		},
		Log: testLog,
	}

	NewNotifications(&config)

	if !strings.Contains(buf.String(), "Unknown notification type: foo") {
		t.Errorf("Notification of unknown type did not generate the expected log message.")
	}
}

func TestNewNotifications_MultipleIrcLogs(t *testing.T) {
	var buf bytes.Buffer
	testLog := log.New(&buf, "", 0)

	config := AppConfig{
		Notifications: []*NotificationsConfig{
			&NotificationsConfig{Type: "irc", IrcConfig: &irc.Config{Nick: "fred", Server: "127.0.0.1:6667", Channels: []string{"#chan"}}},
			&NotificationsConfig{Type: "irc", IrcConfig: &irc.Config{Nick: "fred", Server: "127.0.0.2:6667", Channels: []string{"#chan"}}},
		},
		Log: testLog,
	}

	NewNotifications(&config)

	if !strings.Contains(buf.String(), "Only one irc notification type is supported.") {
		t.Errorf("Multiple irc notification configurations did not generate the expected log message.")
	}
}

func TestNotificationsAreDispatched(t *testing.T) {
	dispatchedMessage := ""
	testBot := bot.New(&bot.Handlers{
		Response: func(target, message string, sender *bot.User) {
			dispatchedMessage = message
		},
	},
		&bot.Config{
			Protocol: `test`,
			Server:   `test`,
		})

	config := AppConfig{
		Notifications: []*NotificationsConfig{},
	}

	sut := NewNotifications(&config)
	sut.injectTestBot(testBot)
	expect := "test notification"
	sut.Notify(expect)
	time.Sleep(200 * time.Millisecond)

	if dispatchedMessage != expect {
		t.Errorf("Expected the message \"%s\" to be dispatched, but got \"%s\"", expect, dispatchedMessage)
	}
}
