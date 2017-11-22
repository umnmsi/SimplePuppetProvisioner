package main

import (
	"flag"
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
)

func main() {
	configFile := flag.String("config", "", "Path to the spp configuration file.")
	noDaemonize := flag.Bool("no-daemonize", false, "Do not send the process into the background and log to stdout.")
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
	server := lib.NewHttpServer(appConfig, notifier)

	if *noDaemonize == false {
		appConfig.MoveLoggingToFile()

	}

	server.Start()
}
