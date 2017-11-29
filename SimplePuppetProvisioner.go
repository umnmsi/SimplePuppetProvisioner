package main

import (
	"context"
	"flag"
	"github.com/mbaynton/SimplePuppetProvisioner/lib"
	"github.com/mbaynton/SimplePuppetProvisioner/lib/certsign"
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
	certSigner := certsign.NewCertSigner(*appConfig.PuppetConfig, appConfig.Log, notifier.Notify)

	server := lib.NewHttpServer(appConfig, notifier, certSigner)

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
