// Command darkrouter runs the gateway.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

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

// configureLogging installs the process logger from the environment: JSON
// for a log pipeline, text for a terminal, at the level the operator asked
// for. It runs before anything can log, subcommands included.
func configureLogging() {
	var level slog.Level
	switch strings.ToLower(os.Getenv("DARKROUTER_LOG_LEVEL")) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if strings.EqualFold(os.Getenv("DARKROUTER_LOG_FORMAT"), "json") {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

func main() {
	configureLogging()
	// Subcommands are dispatched before flag.Parse, which would otherwise
	// reject the bare verb as an unknown flag.
	if len(os.Args) > 1 && os.Args[1] == "rotate-key" {
		if err := runRotateKey(os.Args[2:]); err != nil {
			slog.Error("rotate-key failed", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := runServer(os.Args[1:]); err != nil {
		slog.Error("darkrouter exited", "err", err)
		os.Exit(1)
	}
}

// parseFlags reads the command line into the two values the process acts on.
//
// The flag set is the caller's so a test can parse without ExitOnError taking
// the process down on a bad argument.
func parseFlags(fs *flag.FlagSet, args []string) (dbPath, legacyConfig string, err error) {
	// -config is accepted and ignored for one release. An operator who
	// overrode the container's command still passes it, and an unknown flag
	// would otherwise refuse to start with a message explaining nothing.
	legacy := fs.String("config", "", "deprecated; configuration now lives in the database")
	db := fs.String("db", "", "path to the database file (default: darkrouter.db in the working directory)")
	if err := fs.Parse(args); err != nil {
		return "", "", err
	}
	return *db, *legacy, nil
}

// startupWarnings are the notices that explain state the stored settings
// cannot: a file the process no longer reads, and a flag it now ignores. An
// ignored flag with no notice would leave an operator believing the file
// they passed is still being read.
//
// Returned rather than only logged. They reach admin.Deps.Warnings, and from
// there /healthz and the settings screen — which is where an operator asking
// why their settings reverted is looking, rather than in the container log.
func startupWarnings(dbPath, legacyConfig string) []string {
	var out []string

	// The file stopped being read in this release. Saying so once is what
	// turns "my settings reverted" into an obvious morning rather than a
	// confusing one.
	legacy := filepath.Join(filepath.Dir(dbPath), "darkrouter.yaml")
	// dbPath is commonly a bare filename, which would otherwise name a path
	// with no directory -- useless to an operator reading docker logs on a
	// host they did not set up themselves.
	if abs, err := filepath.Abs(legacy); err == nil {
		legacy = abs
	}
	if _, err := os.Stat(legacy); err == nil {
		out = append(out, fmt.Sprintf(
			"a configuration file is present at %s but no longer read; settings now live in the database and are changed in the console. Delete or rename it to silence this on the next start.",
			legacy))
	}

	if legacyConfig != "" {
		out = append(out, fmt.Sprintf(
			"-config %s was ignored; configuration now lives in the database. Drop the flag from the command that starts the gateway.",
			legacyConfig))
	}
	return out
}

// warnUnclaimed says loudly that an existing deployment is claimable.
//
// A database with data but no accounts is an upgrade that has not been
// claimed yet, and until somebody claims it anyone who can reach the admin
// port can become admin. A brand-new install is the same state for a good
// reason and is not warned about, or every first start would cry wolf.
//
// slog only, not startupWarnings: /healthz already discloses the same fact
// through unclaimedWarning (server.go), driven by the same UserCount. This
// warning exists for the operator reading container logs at boot, not to add
// a second copy of a disclosure that endpoint already makes.
func warnUnclaimed(ctx context.Context, db *store.DB) {
	// A failed count is not a claimed console. Returning silently on the error
	// would drop the one warning protecting an upgrade, and drop it precisely
	// when the database is in trouble.
	users, err := db.UserCount(ctx)
	if err != nil {
		slog.Warn("cannot tell whether this console has been claimed", "error", err)
		return
	}
	if users > 0 {
		return
	}
	providers, err := db.ProviderCount(ctx)
	if err != nil {
		slog.Warn("cannot tell whether this console has been claimed", "error", err)
		return
	}
	if providers == 0 {
		return
	}
	slog.Warn("no account has claimed this console; the first visitor to reach " +
		"the admin port will become its administrator. Claim it now.")
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("darkrouter", flag.ExitOnError)
	dbPath, legacyConfig, err := parseFlags(fs, args)
	if err != nil {
		return err
	}
	if dbPath == "" {
		if v, ok := os.LookupEnv("DARKROUTER_DB"); ok && strings.TrimSpace(v) != "" {
			dbPath = v
		} else {
			dbPath = "darkrouter.db"
		}
	}
	warnings := startupWarnings(dbPath, legacyConfig)
	for _, w := range warnings {
		slog.Warn("startup warning", "warning", w)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	armSecondSignal(ctx, stop)

	// Before SQLite, whose error for this names neither the directory nor the
	// uid: a bind-mounted data directory owned by the wrong user is the first
	// thing a container deployment can get wrong.
	if err := store.CheckWritable(dbPath); err != nil {
		return err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	// Closed last, after Run has drained the log channel and flushed health.
	defer db.Close()

	if err := db.Migrate(context.Background()); err != nil {
		return err
	}
	warnUnclaimed(context.Background(), db)
	key, err := store.OpenKeyring(context.Background(), db, os.Getenv("DARKROUTER_MASTER_KEY"))
	if err != nil {
		return err
	}

	// Before the first load, so a row an earlier release stored that now only
	// repeats the compiled default does not read back as an operator's choice.
	if n, err := store.ReconcileConfig(context.Background(), db); err != nil {
		return err
	} else if n > 0 {
		slog.Info("dropped stored settings that matched the default", "count", n)
	}

	boot := config.BootstrapFrom(os.LookupEnv)
	cfgStore, err := config.NewStoreFrom(func() (*config.Config, error) {
		return store.LoadConfig(context.Background(), db, boot)
	})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	cfg := cfgStore.Current()

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
			slog.Info("added providers that need no credential; delete any you do not want, they are not offered twice", "count", len(seedRes.Added), "providers", strings.Join(seedRes.Added, ", "))
		}
	}

	// Installed before the reload below, so the first snapshot any request can
	// see already carries the database's aliases.
	cfgStore.SetOverlay(func(c *config.Config) error {
		return store.OverlayConfig(context.Background(), db, c)
	})
	// Installed with the overlay, before the first reload: the admin API is
	// reachable as soon as the server starts, and a write that arrived with no
	// writer installed would be refused for a reason nobody could act on.
	cfgStore.SetWriter(func(ctx context.Context, p config.Patch) ([]string, error) {
		return store.WriteConfig(ctx, db, boot, p)
	})
	if err := cfgStore.Reload(); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	// The constructor captured its snapshot before SetOverlay, so until this
	// runs "pending restart" is measured against a config the process never
	// ran.
	cfgStore.MarkBoot()
	cfg = cfgStore.Current()

	srv, err := server.New(cfgStore, db, key, warnings)
	if err != nil {
		return err
	}

	slog.Info("darkrouter listening", "version", server.Version, "proxy", cfg.Server.ProxyListen, "admin", cfg.Server.AdminListen)
	// The startup warnings were logged when they were produced: they explain a
	// failure that can happen before this point is reached, so waiting until
	// the gateway is listening would lose them exactly when they matter most.
	for _, w := range cfg.Warnings {
		slog.Warn("config warning", "warning", w)
	}

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("server: %w", err)
	}
	slog.Info("darkrouter stopped")
	return nil
}

// armSecondSignal restores default signal handling once the first signal has
// started the drain, so a second Ctrl-C kills the process instead of being
// absorbed by a context that is already cancelled.
func armSecondSignal(ctx context.Context, stop func()) {
	go func() {
		<-ctx.Done()
		stop()
	}()
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
