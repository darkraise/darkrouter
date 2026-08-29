package localcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// AuggieScheme is the URL scheme an auggie preset names. It is not a hostname
// darkrouter resolves: the "host" in auggie://cli/v1 exists only because a URL
// needs one.
const AuggieScheme = "auggie"

// Auggie runs Augment's CLI as a provider.
//
// Augment publishes no gateway endpoint. The operator installs `auggie` and
// runs `auggie login`, and the tool holds the session in its own state
// directory. darkrouter therefore stores no credential for this provider and
// could not send one if it had it — which is why its preset is keyless rather
// than a key nobody can supply.
type Auggie struct {
	// Bin is the program to run. Empty resolves it at first use.
	Bin string
	// Timeout bounds one completion. A CLI that hangs would otherwise hold an
	// attempt open until the executor's own deadline, with a process still
	// running behind it.
	Timeout time.Duration
	// ListTimeout bounds `auggie model list`, which the discovery sweep calls
	// on a schedule and must not stall.
	ListTimeout time.Duration

	// session is the operator's Augment session, handed to the child as
	// AUGMENT_SESSION_AUTH. Empty means the CLI uses whatever `auggie login`
	// left in its own state directory.
	session string
}

// WithSession returns the CLI running under a configured session. The receiver
// is shared, so the copy carries the resolution state rather than repeating the
// binary search per request.
func (a *Auggie) WithSession(secret string) CLI {
	if secret == "" {
		return a
	}
	c := *a
	c.session = secret
	return &c
}

// env is the child's environment: this process's, plus the session when one is
// configured.
//
// AUGMENT_SESSION_AUTH rather than the CLI's --augment-session-json flag: an
// argument is visible in the process table to every other process on the box,
// and this value is the whole credential.
func (a *Auggie) env() []string {
	if a.session == "" {
		return nil
	}
	return append(os.Environ(), "AUGMENT_SESSION_AUTH="+a.session)
}

// NewAuggie returns the CLI with the timeouts the gateway uses.
func NewAuggie() *Auggie {
	return &Auggie{Timeout: 10 * time.Minute, ListTimeout: 15 * time.Second}
}

func (a *Auggie) Scheme() string { return AuggieScheme }

// resolveBin finds the installed CLI, matching where Augment's own installers
// put it. AUGGIE_BIN wins because a container is exactly the case the fixed
// paths do not cover: the binary is mounted wherever the operator chose.
// Resolved per call rather than cached: the two os.Stat calls are nothing
// beside spawning a process, and a cache would mean an operator who installs
// the CLI, or mounts it, has to restart the gateway before it is found.
func (a *Auggie) resolveBin() string {
	if a.Bin != "" {
		return a.Bin
	}
	if env := strings.TrimSpace(os.Getenv("AUGGIE_BIN")); env != "" {
		return env
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, c := range []string{
			filepath.Join(home, ".local", "share", "auggie", "bin", "auggie"),
			filepath.Join(home, ".auggie", "bin", "auggie"),
		} {
			if st, err := os.Stat(c); err == nil && !st.IsDir() {
				return c
			}
		}
	}
	// PATH last. LookPath failing is not decided here: the spawn reports it,
	// with the message the operator needs.
	return "auggie"
}

// modelName is the shape a model id may take before it is placed in argv.
//
// This is the argument-injection guard, and it is a guard rather than a
// nicety: the model reaches a process argument, so a value beginning with a
// dash would be read by the CLI as a flag. Requiring an alphanumeric first
// character and a conservative remainder means no input of ours can become an
// option, and the prompt itself never goes near argv — it is written to stdin.
var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

// ErrNoModel is returned for a model the CLI could not be asked about.
var ErrNoModel = errors.New("unusable model name")

func validateModel(model string) (string, error) {
	m := strings.TrimSpace(model)
	if m == "" {
		// The CLI picks its own default, which is the honest answer for a
		// request that named no model.
		return "", nil
	}
	if !modelName.MatchString(m) {
		return "", fmt.Errorf("%w: %q", ErrNoModel, model)
	}
	return m, nil
}

// Models lists what the installed CLI recognizes.
//
// There is no static fallback list on purpose. A hardcoded roster would go
// stale silently and claim models the operator's install cannot serve; the
// program itself is the only authority on what it answers to, and a failure
// here surfaces as a discovery failure the console already displays.
func (a *Auggie) Models(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout(a.ListTimeout, 15*time.Second))
	defer cancel()

	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, a.resolveBin(), "model", "list")
	cmd.Env = a.env()
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, a.runError(err, errb.String())
	}
	ids := parseModelList(out.String())
	if len(ids) == 0 {
		// `auggie model list` exits 0 when it is not logged in and says so on
		// stderr, so an empty roster is usually an authentication state rather
		// than an empty account. Passing its own words on is the difference
		// between the console saying "no models" and saying "run auggie login".
		if diag := strings.TrimSpace(errb.String()); diag != "" {
			return nil, fmt.Errorf("auggie listed no models: %s", firstLines(diag, 3))
		}
		return nil, fmt.Errorf("auggie listed no models")
	}
	return ids, nil
}

// modelEntry matches the bracketed id in a line of `auggie model list`, whose
// output is a human roster — "Sonnet 4.6 [sonnet4.6]" — rather than a machine
// format.
var modelEntry = regexp.MustCompile(`\[([^\]]+)\]`)

func parseModelList(s string) []string {
	var ids []string
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		m := modelEntry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := strings.TrimSpace(m[1])
		if id == "" || seen[id] || !modelName.MatchString(id) {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids
}

// Run spawns one completion and streams its output.
//
// The prompt goes to stdin and never to argv: it is arbitrary user text, and
// an argument list is the one place that would matter.
func (a *Auggie) Run(ctx context.Context, model, prompt string, out io.Writer) error {
	safe, err := validateModel(model)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, a.timeout(a.Timeout, 10*time.Minute))
	defer cancel()

	// The trailing -- ends the options, so nothing after it can be read as a
	// flag even if the CLI's parser changes.
	args := []string{"--print", "--quiet"}
	if safe != "" {
		args = append(args, "--model", safe)
	}
	args = append(args, "--")

	cmd := exec.CommandContext(ctx, a.resolveBin(), args...)
	cmd.Env = a.env()
	// Kill the whole run rather than only the parent: a CLI that spawns a
	// helper leaves it holding the pipe, and the read below would block on a
	// process nobody is waiting for.
	cmd.WaitDelay = 5 * time.Second
	cmd.Stdin = strings.NewReader(prompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return a.runError(err, errb.String())
	}
	copyErr := scanChunks(stdout, out)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return a.runError(waitErr, errb.String())
	}
	return copyErr
}

// firstLines trims a CLI's diagnostic to something an error message can carry.
// The useful sentence is always at the top; the rest is a stack trace or a
// repeat of it.
func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

func (a *Auggie) timeout(v, fallback time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return fallback
}

// runError says which of the two failures happened, because they need
// different things from the operator: a missing binary is an install or a
// mount, and a non-zero exit is usually a login that has expired.
func (a *Auggie) runError(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if len(stderr) > 400 {
		stderr = stderr[:400] + "…"
	}
	var ee *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("the auggie CLI was not found at %q: install it and run "+
			"`auggie login`, or set AUGGIE_BIN to its path", a.resolveBin())
	case errors.As(err, &ee) && stderr != "":
		return fmt.Errorf("auggie exited %d: %s", ee.ExitCode(), stderr)
	case errors.As(err, &ee):
		return fmt.Errorf("auggie exited %d with no diagnostic; "+
			"`auggie login` on this machine is what usually fixes it", ee.ExitCode())
	case stderr != "":
		return fmt.Errorf("auggie: %v: %s", err, stderr)
	}
	return fmt.Errorf("auggie: %w", err)
}
