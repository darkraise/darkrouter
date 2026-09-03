package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// nineEntry is one 9router registry module. Only the fields darkrouter reads
// are declared; the upstream object carries more.
type nineEntry struct {
	ID        string        `json:"id"`
	Alias     string        `json:"alias"`
	Category  string        `json:"category"`
	AuthType  string        `json:"authType"`
	Display   nineDisplay   `json:"display"`
	Transport nineTransport `json:"transport"`
	// ServiceKinds names the non-chat surfaces an entry serves. Absent means
	// chat by convention; an explicit "llm" means the same thing said out
	// loud. Either routes through phase A; anything else defers to phase C.
	ServiceKinds []string    `json:"serviceKinds"`
	Models       []nineModel `json:"models"`
}

type nineDisplay struct {
	Name     string     `json:"name"`
	Color    string     `json:"color"`
	TextIcon string     `json:"textIcon"`
	Website  string     `json:"website"`
	Notice   nineNotice `json:"notice"`
}

type nineNotice struct {
	APIKeyURL string `json:"apiKeyUrl"`
}

type nineTransport struct {
	BaseURL     string   `json:"baseUrl"`
	ValidateURL string   `json:"validateUrl"`
	Auth        nineAuth `json:"auth"`
	// Format names the wire dialect: absent or "openai" is the only shape
	// this phase's "openaicompat" kind can serve. Everything else ("claude",
	// "openai-responses", "ollama", "cursor", "kiro", "gemini-cli",
	// "commandcode", ...) is a different protocol entirely.
	Format string          `json:"format"`
	Quirks map[string]bool `json:"quirks"`
}

// nineAuth is the header block upstream publishes per credential kind. It
// comes in two shapes: flat, where one header serves every kind, and nested,
// naming a header per kind. Only the API-key half is declared, because the
// categories whose credential is an OAuth token or a browser cookie never
// reach a preset.
type nineAuth struct {
	Header string       `json:"header"`
	APIKey nineAuthKind `json:"apiKey"`
}

type nineAuthKind struct {
	Header string `json:"header"`
}

// apiKeyHeader resolves the two shapes to the one header an API key travels in.
func (a nineAuth) apiKeyHeader() string {
	if a.APIKey.Header != "" {
		return a.APIKey.Header
	}
	return a.Header
}

type nineModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// dumpScript evaluates every module and prints one JSON array. Node resolves
// the imports 14 of the upstream files rely on, which is why this shells out
// rather than parsing: those files are programs, not data. ESM imports are
// required here because --input-type=module disables require().
const dumpScript = `
import fs from "node:fs";
import path from "node:path";
const dir = process.argv[1];
const out = [];
for (const f of fs.readdirSync(dir).filter(f => f.endsWith(".js") && f !== "index.js").sort()) {
  const m = await import(path.resolve(dir, f));
  if (m.default && typeof m.default === "object" && !Array.isArray(m.default)) out.push(m.default);
}
console.log(JSON.stringify(out));
`

func scrapeNineRouter(dir string) ([]nineEntry, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("9router registry: %w", err)
	}
	cmd := exec.Command("node", "--input-type=module", "-e", dumpScript, dir)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("evaluate 9router registry with node (is node installed?): %w: %s", err, stderr.String())
	}
	var entries []nineEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("decode 9router dump: %w", err)
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.ID != "" {
			kept = append(kept, e)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return kept, nil
}

// routable reports whether phase A ingests this entry. An absent
// serviceKinds means chat by convention; an explicit "llm" means the same
// thing said out loud. Everything else (tts, image, webSearch, ...) names a
// non-chat surface that belongs to phase C.
func (e nineEntry) routable() bool {
	if len(e.ServiceKinds) == 0 {
		return true
	}
	for _, k := range e.ServiceKinds {
		if k == "llm" || k == "embedding" {
			return true
		}
	}
	return false
}
