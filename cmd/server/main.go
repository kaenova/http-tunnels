package main

import (
	"io/fs"
	"log"

	"github.com/kaenova/http-tunnels/internal/server"
)

var Version = "dev"

func main() {
	log.Printf("http-tunnels server %s starting...", Version)

	assets, err := fs.Sub(adminAssets, "web/dist")
	if err != nil {
		log.Fatalf("loading embedded admin assets failed: %v", err)
	}

	app, err := server.NewApp(server.LoadConfig(), assets)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := app.Close(); err != nil {
			log.Printf("closing app: %v", err)
		}
	}()

	log.Printf("http-tunnels server %s listening on %s", Version, app.Config.ListenAddr)

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
