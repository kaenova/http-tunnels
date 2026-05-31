package main

import (
	"log"

	"github.com/kaenova/http-tunnels/internal/server"
)

func main() {
	app, err := server.NewApp(server.LoadConfig(), nil)
	if err != nil {
		log.Fatal(err)
	}
	defer app.Close()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}