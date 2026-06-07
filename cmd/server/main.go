package main

import (
	"io/fs"
	"log"

	"github.com/kaenova/http-tunnels/internal/server"
)

var Version = "dev"

func main() {
	assets, err := fs.Sub(adminAssets, "web/dist")
	if err != nil {
		log.Fatalf("loading embedded admin assets failed: %v", err)
	}

	app, err := server.NewApp(server.LoadConfig(), assets)
	if err != nil {
		log.Fatal(err)
	}
	app.Version = Version
	defer app.Close()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}