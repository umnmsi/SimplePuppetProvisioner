package main

import (
	"flag"
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
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
	server := lib.NewHttpServer(appConfig, notifier)

	if *logStdout == false {
		appConfig.MoveLoggingToFile()
	}

	server.Start()
}
