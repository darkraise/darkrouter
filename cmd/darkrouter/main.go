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
	"path/filepath"
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
	dbPath := fs.String("db", "", "path to the database file (default: darkrouter.db beside the config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		*dbPath = filepath.Join(filepath.Dir(*path), "darkrouter.db")
	}

	cfgStore, err := config.NewStore(*path, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	// Closed last, after Run has drained the log channel and flushed health.
	defer db.Close()

	if err := db.Migrate(context.Background()); err != nil {
		return err
	}
	key, err := store.OpenKeyring(context.Background(), db, os.Getenv("DARKROUTER_MASTER_KEY"))
	if err != nil {
		return err
	}

	cfg := cfgStore.Current()
	var warnings []string

	res, err := store.ImportFromConfig(context.Background(), db, key, cfg)
	if err != nil {
		return err
	}
	if res.Imported {
		log.Printf("imported %d providers from %s into the database", res.Providers, *path)
	}
	stale, err := store.StaleBlockWarning(context.Background(), db, cfg)
	if err != nil {
		return err
	}
	if stale != "" {
		warnings = append(warnings, stale)
	}

	srv, err := server.New(cfgStore, db, key, warnings)
	if err != nil {
		return err
	}

	log.Printf("darkrouter %s listening: proxy %s admin %s",
		server.Version, cfg.Server.ProxyListen, cfg.Server.AdminListen)
	for _, w := range append(warnings, cfg.Warnings...) {
		log.Printf("config warning: %s", w)
	}

	if err := srv.Run(ctx); err != nil {
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
