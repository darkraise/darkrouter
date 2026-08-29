// Command darkrouter runs the gateway.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/darkraise/darkrouter/internal/admin"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/server"
	"github.com/darkraise/darkrouter/internal/store"
)

// seedable turns the presets that need no credential into what the seeder
// inserts. Free-models-only because a keyless provider is reached by everyone
// on the same terms: what its free tier covers is what an operator can rely on
// getting, and importing the rest fills the catalogue with models that answer
// 401 to a gateway holding no key.
func seedable(ps catalog.Presets) []store.SeedProvider {
	var out []store.SeedProvider
	for _, id := range ps.SelfServing() {
		p := ps[id]
		out = append(out, store.SeedProvider{
			ID: id, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL,
			AuthStyle: p.Auth.Style, FreeModelsOnly: true,
		})
	}
	return out
}

func main() {
	// Subcommands are dispatched before flag.Parse, which would otherwise
	// reject the bare verb as an unknown flag.
	if len(os.Args) > 1 && os.Args[1] == "rotate-key" {
		if err := runRotateKey(os.Args[2:]); err != nil {
			log.Fatalf("rotate-key: %v", err)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "hash-password" {
		if err := runHashPassword(os.Args[2:]); err != nil {
			log.Fatalf("hash-password: %v", err)
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

	// Seeded before the config overlay and the server: the providers it adds
	// are ordinary rows, and everything downstream — the router's source, the
	// discovery sweep, the console — reads them the same way it reads one an
	// operator added by hand.
	if cfg.Catalog.SeedFreeProvidersEnabled() {
		seedRes, err := store.SeedProviders(context.Background(), db, seedable(catalog.Embedded()))
		if err != nil {
			return err
		}
		if len(seedRes.Added) > 0 {
			log.Printf("added %d providers that need no credential: %s. "+
				"Delete any you do not want — they are not offered twice",
				len(seedRes.Added), strings.Join(seedRes.Added, ", "))
		}
	}

	cfgRes, err := store.ImportConfigOnce(context.Background(), db, cfg)
	if err != nil {
		return err
	}
	if cfgRes.Imported {
		log.Printf("imported %d aliases and %d policy settings from %s into the database; "+
			"edit them through the admin API from now on",
			cfgRes.Aliases, cfgRes.Policy, *path)
	}

	// Installed before the reload below, so the first snapshot any request can
	// see already carries the database's aliases and policy rather than the
	// file's.
	cfgStore.SetOverlay(func(c *config.Config) error {
		return store.OverlayConfig(context.Background(), db, c)
	})
	if err := cfgStore.Reload(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	cfg = cfgStore.Current()
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

// runHashPassword prints a bcrypt hash for DARKROUTER_ADMIN_PASSWORD_HASH.
//
// It exists so an operator can produce one with the binary they already have
// rather than installing a second tool, which is the difference between the
// dashboard being usable on a fresh box and not.
func runHashPassword(args []string) error {
	fs := flag.NewFlagSet("hash-password", flag.ExitOnError)
	password := fs.String("password", "", "the password to hash; read from stdin when empty")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pw := *password
	if pw == "" {
		// Read from stdin so the password does not land in shell history.
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		pw = strings.TrimSpace(string(b))
	}
	h, err := admin.HashPassword(pw)
	if err != nil {
		return err
	}
	// The hash alone on stdout, so the value can be piped straight into an
	// environment file without a sed to strip a label.
	fmt.Println(h)
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
