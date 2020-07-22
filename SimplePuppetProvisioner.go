package main

import (
	"context"
	"flag"
	"github.com/fsnotify/fsnotify"
	"github.com/umnmsi/SimplePuppetProvisioner/interfaces"
	"github.com/umnmsi/SimplePuppetProvisioner/lib"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/certsign"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/genericexec"
	"github.com/umnmsi/SimplePuppetProvisioner/lib/nodeconfig"
	"log"
	"os"
	"os/signal"
	"reflect"
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

	eventResultChans := []reflect.SelectCase{}
	eventListenerChans := []reflect.SelectCase{}

	notifier := lib.NewNotifications(&appConfig)
	csrWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		appConfig.Log.Printf("ERROR: Unable to start certificate signing request watcher: %s\n", err)
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
		appConfig.Log.Printf("Unable to start certificate signing manager: %s\n", err)
		os.Exit(1)
	}

	execConfigMap := makeExecTaskConfigsMap(&appConfig)
	if appConfig.GithubWebhooks.EnableStandardR10kListener {
		appConfig.GithubWebhooks.Listeners = append(appConfig.GithubWebhooks.Listeners, lib.StandardR10kListenerConfig(appConfig.GithubWebhooks))
	}
	lib.SetWebhookExecTaskConfigMap(appConfig.GithubWebhooks, execConfigMap)

	execManager := genericexec.NewGenericExecManager(execConfigMap, appConfig.PuppetConfig, appConfig.Log, notifier.Notify)
	eventResultChans = append(eventResultChans, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(execManager.ResultChan),
	})

	nodeClassifier, err := nodeconfig.NewNodeClassifier(appConfig.NodesDir, appConfig.NodesPrivateKey, appConfig.NodesGitUser, appConfig.PuppetConfig, appConfig.Log, notifier.Notify, appConfig.ClassifyWebhookTimeout, appConfig.ClassifyExecTimeout)
	if err != nil {
		appConfig.Log.Printf("ERROR: Unable to start node classifier: %s\n", err)
		os.Exit(1)
	}
	eventResultChans = append(eventResultChans, reflect.SelectCase{
		Dir:  reflect.SelectRecv,
		Chan: reflect.ValueOf(nodeClassifier.ResultChan),
	})
	eventListenerChans = append(eventListenerChans, nodeClassifier.ListenerChans...)

	stop := make(chan os.Signal, 1)
	if runtime.GOOS == "windows" {
		signal.Notify(stop, os.Interrupt)
	} else {
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	}

	server := lib.NewHttpServer(appConfig, notifier, certSigner, execManager, nodeClassifier, stop)

	go server.Start()

	// Wait for server to start to allow handlers to register event channels
	<-server.StartChan
	eventResultChans = append(eventResultChans, server.ResultChans...)
	eventListenerChans = append(eventListenerChans, server.ListenerChans...)

	appConfig.Log.Println("Received server start message. Starting event watcher...")

	go eventWatcher(appConfig.Log, eventResultChans, eventListenerChans)

	if *logStdout == false {
		appConfig.MoveLoggingToFile()
	}

	// Wait for a SIGTERM.
	<-stop

	appConfig.Log.Println("Stopping...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	server.Shutdown(ctx)
	appConfig.Log.Println("HTTP server shutdown.")

	certSigner.Shutdown()
	appConfig.Log.Println("Certificate signing manager shutdown.")

	appConfig.Log.Println("Process will now exit.")
}

func makeExecTaskConfigsMap(config *lib.AppConfig) map[string]genericexec.GenericExecConfig {
	execTaskDefns := config.GenericExecTasks
	execTaskConfigsByName := make(map[string]genericexec.GenericExecConfig, len(execTaskDefns))
	for _, configuredTask := range execTaskDefns {
		execTaskConfigsByName[configuredTask.Name] = *configuredTask
	}
	return execTaskConfigsByName
}

func eventWatcher(log *log.Logger, eventResultChans, eventListenerChans []reflect.SelectCase) {
	log.Printf("Found %d registered result channels\n", len(eventResultChans))
	for index, resultChan := range eventResultChans {
		log.Printf("eventResultChan %d %T\n", index, resultChan.Chan.Interface())
	}
	log.Printf("Found %d registered listener channels\n", len(eventListenerChans))
	for index, resultChan := range eventListenerChans {
		log.Printf("eventListenerChan %d %T\n", index, resultChan.Chan.Interface())
	}
	for {
		index, event, ok := reflect.Select(eventResultChans)
		if !ok {
			eventResultChans[index].Chan = reflect.ValueOf(nil)
			continue
		}
		for _, listener := range eventListenerChans {
			if listener.Chan.Type().Elem() == event.Type() {
				listener.Chan.Send(event)
			}
		}
	}
}
