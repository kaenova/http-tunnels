package main

import (
	"io/fs"
	"log"

	"github.com/kaenova/http-tunnels/internal/server"
)

func main() {
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

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
