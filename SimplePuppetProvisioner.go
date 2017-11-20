package main

import (
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
)

func main() {
	appConfig := lib.LoadTheConfig("spp.conf", []string{".", "/etc/spp"})

	notifier := lib.NewNotifications(&appConfig)

	server := lib.NewHttpServer(appConfig, notifier)
	server.Start()
}
