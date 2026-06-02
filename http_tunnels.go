package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/kaenova/http-tunnels/internal/client"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update" {
		runUpdateCommand(os.Args[2:])
		return
	}

	host := flag.String("host", "https://t.kaenova.my.id", "Tunnel server URL")
	subdomain := flag.String("subdomain", "", "Requested subdomain")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	showVersion := flag.Bool("version", false, "Show version")
	flag.Parse()

	if *showVersion {
		fmt.Printf("http-tunnels v6 %s\n", version)
		os.Exit(0)
	}

	if !*verbose {
		log.SetOutput(io.Discard)
	}

	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: http-tunnels [flags] <backend_url>")
		fmt.Fprintln(os.Stderr, "  backend_url: http://localhost:3000")
		flag.PrintDefaults()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client.Version = version
	if err := client.Run(ctx, client.Options{
		Host:       *host,
		BackendURL: flag.Arg(0),
		Subdomain:  *subdomain,
	}); err != nil {
		log.Fatalf("Tunnel client failed: %v", err)
	}
}

func runUpdateCommand(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	targetVersion := fs.String("version", "", "Target release tag (defaults to latest)")
	force := fs.Bool("force", false, "Force reinstall even if already up to date")
	_ = fs.Parse(args)

	client.Version = version
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := client.RunUpdate(ctx, client.UpdateOptions{
		TargetVersion: *targetVersion,
		Force:         *force,
	}); err != nil {
		log.Fatalf("Update failed: %v", err)
	}
}
