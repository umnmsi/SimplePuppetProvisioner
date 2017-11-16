package main

import (
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
)

func main() {
	appConfig := lib.LoadTheConfig("spp.conf", []string{".", "/etc/spp"})

	server := lib.NewHttpServer(appConfig)
	server.Start()
}
