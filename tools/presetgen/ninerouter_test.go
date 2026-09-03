package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScrapeNineRouterReadsAPlainEntry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "cerebras.js", `export default {
  id: "cerebras",
  display: { name: "Cerebras", website: "https://www.cerebras.ai",
             notice: { apiKeyUrl: "https://cloud.cerebras.ai/platform" } },
  category: "apikey",
  authType: "apikey",
  transport: { baseUrl: "https://api.cerebras.ai/v1/chat/completions",
               quirks: { dropClientMetadata: true } },
  models: [{ id: "gpt-oss-120b", name: "GPT OSS 120B" }],
};`)

	got, err := scrapeNineRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.ID != "cerebras" {
		t.Errorf("ID = %q", e.ID)
	}
	if e.Transport.BaseURL != "https://api.cerebras.ai/v1/chat/completions" {
		t.Errorf("BaseURL = %q", e.Transport.BaseURL)
	}
	if e.Display.Notice.APIKeyURL != "https://cloud.cerebras.ai/platform" {
		t.Errorf("APIKeyURL = %q", e.Display.Notice.APIKeyURL)
	}
	if len(e.Transport.Quirks) != 1 || !e.Transport.Quirks["dropClientMetadata"] {
		t.Errorf("Quirks = %v", e.Transport.Quirks)
	}
}

// 14 of the 120 upstream files import from elsewhere in the repository. A
// static parser reads those wrong; evaluating them is the whole point.
func TestScrapeNineRouterResolvesImports(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "shared.js", `export const BASE = "https://api.example.com/v1/messages";`)
	write(t, dir, "claude.js", `import { BASE } from "./shared.js";
export default { id: "claude", transport: { baseUrl: BASE }, models: [] };`)

	got, err := scrapeNineRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	var claude *nineEntry
	for i := range got {
		if got[i].ID == "claude" {
			claude = &got[i]
		}
	}
	if claude == nil {
		t.Fatal("claude entry not found")
	}
	if claude.Transport.BaseURL != "https://api.example.com/v1/messages" {
		t.Errorf("BaseURL = %q, want the imported constant", claude.Transport.BaseURL)
	}
}

// index.js is a barrel of imports with no default export of its own.
func TestScrapeNineRouterSkipsTheBarrel(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.js", `import a from "./a.js"; export default [a];`)
	write(t, dir, "a.js", `export default { id: "a", transport: { baseUrl: "https://a.example/v1" }, models: [] };`)

	got, err := scrapeNineRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %v, want only the a entry", got)
	}
}

// The real registry's serviceKinds vocabulary includes an explicit "llm"
// value alongside the absent case, not just non-chat kinds like "tts". A
// predicate that misses "llm" silently drops 18 real providers (anthropic,
// groq, codex, ollama-local among them) from phase A.
func TestNineEntryRoutable(t *testing.T) {
	cases := []struct {
		name string
		e    nineEntry
		want bool
	}{
		{"absent serviceKinds", nineEntry{}, true},
		{"llm", nineEntry{ServiceKinds: []string{"llm"}}, true},
		{"embedding", nineEntry{ServiceKinds: []string{"embedding"}}, true},
		{"tts", nineEntry{ServiceKinds: []string{"tts"}}, false},
		{"webSearch and webFetch", nineEntry{ServiceKinds: []string{"webSearch", "webFetch"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.routable(); got != tc.want {
				t.Errorf("routable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
