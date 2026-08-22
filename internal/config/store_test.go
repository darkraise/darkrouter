package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestStore(t *testing.T, body string) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "darkrouter.yaml")
	writeFile(t, path, body)
	s, err := NewStore(path, env(map[string]string{"GROQ_KEY": "sk-x"}))
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestStoreServesCurrentConfig(t *testing.T) {
	s, _ := newTestStore(t, minimal)
	if s.Current().Providers[0].ID != "groq" {
		t.Fatal("unexpected config")
	}
}

func TestReloadAppliesValidChange(t *testing.T) {
	s, path := newTestStore(t, minimal)
	writeFile(t, path, strings.Replace(minimal, "id: groq", "id: renamed", 1))
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Current().Providers[0].ID != "renamed" {
		t.Fatal("reload did not apply")
	}
}

func TestReloadRejectsInvalidAndKeepsPrevious(t *testing.T) {
	s, path := newTestStore(t, minimal)
	writeFile(t, path, "server:\n  nonsense: true\n")
	if err := s.Reload(); err == nil {
		t.Fatal("expected reload to fail")
	}
	if s.Current().Providers[0].ID != "groq" {
		t.Fatal("a rejected reload must leave the previous config live")
	}
	if s.LastError() == nil {
		t.Fatal("expected LastError to record the rejection")
	}
}

func TestReloadWarnsOnRestartOnlyChange(t *testing.T) {
	s, path := newTestStore(t, minimal)
	writeFile(t, path, strings.Replace(minimal, "proxy_listen: :8080", "proxy_listen: :9090", 1))
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range s.Current().Warnings {
		if strings.Contains(w, "proxy_listen") && strings.Contains(w, "restart") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a restart-required warning, got %v", s.Current().Warnings)
	}
}

// Editors that save by rename deliver a rename event for the old inode and
// nothing for the new file. Watching the file itself silently stops working
// after the first save, so the watcher must watch the parent directory.
func TestWatchDetectsRenameStyleSave(t *testing.T) {
	s, path := newTestStore(t, minimal)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	go func() { _ = s.watch(ctx, ready) }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not start")
	}

	tmp := path + ".tmp"
	writeFile(t, tmp, strings.Replace(minimal, "id: groq", "id: vimstyle", 1))
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(3 * time.Second)
	for {
		if s.Current().Providers[0].ID == "vimstyle" {
			return
		}
		select {
		case <-deadline:
			t.Fatal("watcher did not observe a rename-style save")
		case <-time.After(20 * time.Millisecond):
		}
	}
}
