package main

import (
	"log"
	"os"

	"github.com/kaenova/http-tunnels/internal/client"
)

func main() {
	if err := client.Run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
