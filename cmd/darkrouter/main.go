// Command darkrouter runs the gateway.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/server"
)

func main() {
	path := flag.String("config", "darkrouter.yaml", "path to the configuration file")
	flag.Parse()

	store, err := config.NewStore(*path, os.LookupEnv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := store.Current()
	log.Printf("darkrouter %s listening: proxy %s admin %s",
		server.Version, cfg.Server.ProxyListen, cfg.Server.AdminListen)
	for _, w := range cfg.Warnings {
		log.Printf("config warning: %s", w)
	}

	if err := server.New(store).Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
	log.Print("darkrouter stopped")
}
