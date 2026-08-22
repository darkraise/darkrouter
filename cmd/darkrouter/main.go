// Command darkrouter runs the gateway.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/server"
	"github.com/darkraise/darkrouter/internal/store"
)

func main() {
	// Subcommands are dispatched before flag.Parse, which would otherwise
	// reject the bare verb as an unknown flag.
	if len(os.Args) > 1 && os.Args[1] == "rotate-key" {
		if err := runRotateKey(os.Args[2:]); err != nil {
			log.Fatalf("rotate-key: %v", err)
		}
		return
	}
	if err := runServer(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("darkrouter", flag.ExitOnError)
	path := fs.String("config", "darkrouter.yaml", "path to the configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfgStore, err := config.NewStore(*path, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cfg := cfgStore.Current()
	log.Printf("darkrouter %s listening: proxy %s admin %s",
		server.Version, cfg.Server.ProxyListen, cfg.Server.AdminListen)
	for _, w := range cfg.Warnings {
		log.Printf("config warning: %s", w)
	}

	if err := server.New(cfgStore).Run(ctx); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	log.Print("darkrouter stopped")
	return nil
}

// runRotateKey re-encrypts every credential under a new master key. The old key
// comes from the environment and the new one from stdin, because rotation needs
// both at once and only a CLI can hold both.
func runRotateKey(args []string) error {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	dbPath := fs.String("db", "darkrouter.db", "path to the database file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	oldMaster := os.Getenv("DARKROUTER_MASTER_KEY")
	if oldMaster == "" {
		return errors.New("DARKROUTER_MASTER_KEY must hold the current master key")
	}

	fmt.Fprint(os.Stderr, "New master key: ")
	newMaster, err := readLine(os.Stdin)
	if err != nil {
		return err
	}
	if newMaster == "" {
		return errors.New("the new master key is empty")
	}
	if newMaster == oldMaster {
		return errors.New("the new master key is identical to the current one")
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		return err
	}
	oldKey, err := store.OpenKeyring(ctx, db, oldMaster)
	if err != nil {
		return err
	}
	if err := store.RotateMasterKey(ctx, db, oldKey, newMaster); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr,
		"Rotation complete. Set DARKROUTER_MASTER_KEY to the new value before restarting.")
	return nil
}

func readLine(f *os.File) (string, error) {
	s := bufio.NewScanner(f)
	if !s.Scan() {
		if err := s.Err(); err != nil {
			return "", err
		}
		return "", errors.New("no input on stdin")
	}
	return strings.TrimRight(s.Text(), "\r\n"), nil
}
