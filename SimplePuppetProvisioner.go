package main

import (
	"context"
	"flag"
	"github.com/fsnotify/fsnotify"
	"github.com/mbaynton/SimplePuppetProvisioner/interfaces"
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/certsign"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/genericexec"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

func main() {
	configFile := flag.String("config", "", "Path to the spp configuration file.")
	logStdout := flag.Bool("log-stdout", false, "Log to stdout.")
	flag.Parse()

	var searchDirs []string
	if *configFile != "" {
		searchDirs = []string{}
	} else {
		*configFile = "spp.conf"
		searchDirs = []string{".", "/etc/spp"}
	}

	appConfig := lib.LoadTheConfig(*configFile, searchDirs)
	notifier := lib.NewNotifications(&appConfig)
	csrWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		appConfig.Log.Println("Unable to start certificate signing request watcher. Cannot proceed.")
		os.Exit(1)
	}
	watcher := interfaces.FsnotifyWatcher{
		Add:    csrWatcher.Add,
		Close:  csrWatcher.Close,
		Remove: csrWatcher.Remove,
		Events: csrWatcher.Events,
		Errors: csrWatcher.Errors,
	}
	certSigner, err := certsign.NewCertSigner(*appConfig.PuppetConfig, appConfig.Log, &watcher, notifier.Notify)
	if err != nil {
		appConfig.Log.Println("Unable to start certificate signing manager. Cannot proceed.")
		os.Exit(1)
	}

	execConfigMap := makeExecTaskConfigsMap(&appConfig)
	if appConfig.WebhooksConfig.EnableStandardR10kListener {
		appConfig.WebhooksConfig.Listeners = append(appConfig.WebhooksConfig.Listeners, lib.StandardR10kListenerConfig(appConfig.WebhooksConfig))
	}
	lib.SetWebhookExecTaskConfigMap(appConfig.WebhooksConfig, execConfigMap)

	execManager := genericexec.NewGenericExecManager(execConfigMap, appConfig.PuppetConfig, appConfig.Log, notifier.Notify)

	server := lib.NewHttpServer(appConfig, notifier, certSigner, execManager)

	if *logStdout == false {
		appConfig.MoveLoggingToFile()
	}

	stop := make(chan os.Signal, 1)
	if runtime.GOOS == "windows" {
		signal.Notify(stop, os.Interrupt)
	} else {
		signal.Notify(stop, syscall.SIGTERM)
	}

	go server.Start()

	// Wait for a SIGTERM.
	<-stop

	appConfig.Log.Println("Stopping...")

	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	server.Shutdown(ctx)
	appConfig.Log.Println("HTTP server shutdown.")

	certSigner.Shutdown()
	appConfig.Log.Println("Certificate signing manager shutdown.")

	appConfig.Log.Println("Process will now exit.")
	appConfig.FlushLog()
}

func makeExecTaskConfigsMap(config *lib.AppConfig) map[string]genericexec.GenericExecConfig {
	execTaskDefns := config.GenericExecTasks
	execTaskConfigsByName := make(map[string]genericexec.GenericExecConfig, len(execTaskDefns))
	for _, configuredTask := range execTaskDefns {
		execTaskConfigsByName[configuredTask.Name] = *configuredTask
	}
	return execTaskConfigsByName
}
