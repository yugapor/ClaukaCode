package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed adapters/*
var adapters embed.FS

// codexEvents maps Codex hook events to the SSTP events the ghost listens for.
var codexEvents = map[string]string{
	"SessionStart":     "OnClaudeSessionStart",
	"UserPromptSubmit": "OnClaudePromptSubmit",
	"Stop":             "OnClaudeTaskComplete",
}

// installMain handles `cc-ukagaka install <agent>` and exits.
func installMain(agent string) {
	exe, err := os.Executable()
	if err == nil {
		err = install(agent, exe)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "cc-ukagaka install:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// install wires agent's lifecycle events to the binary at exe (absolute path,
// so no PATH setup is needed on Windows or macOS).
func install(agent, exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	switch agent {
	case "pi":
		base := envOr("PI_CODING_AGENT_DIR", filepath.Join(home, ".pi", "agent"))
		return installAdapter("adapters/pi.ts", base, filepath.Join("extensions", "cc-ukagaka.ts"), exe)
	case "opencode":
		base := filepath.Join(envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config")), "opencode")
		return installAdapter("adapters/opencode.js", base, filepath.Join("plugins", "cc-ukagaka.js"), exe)
	case "codex":
		return installCodex(envOr("CODEX_HOME", filepath.Join(home, ".codex")), exe)
	}
	return fmt.Errorf("unknown agent %q (want: pi, opencode, codex)", agent)
}

// installAdapter renders an embedded adapter into base/rel. base must already
// exist: if it doesn't, the agent isn't installed and we shouldn't invent it.
func installAdapter(src, base, rel, exe string) error {
	if _, err := os.Stat(base); err != nil {
		return fmt.Errorf("%s not found; run the agent once first", base)
	}
	tmpl, err := adapters.ReadFile(src)
	if err != nil {
		return err
	}
	quoted, _ := json.Marshal(exe) // JSON string literal is also a valid JS string literal
	out := strings.ReplaceAll(string(tmpl), `"__CC_UKAGAKA_BIN__"`, string(quoted))
	dst := filepath.Join(base, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dst, []byte(out), 0o644); err != nil {
		return err
	}
	fmt.Println("installed", dst)
	return nil
}

// installCodex merges our hooks into dir/hooks.json, replacing any earlier
// cc-ukagaka entries and leaving everything else untouched.
func installCodex(dir, exe string) error {
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("%s not found; run codex once first", dir)
	}
	path := filepath.Join(dir, "hooks.json")
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &root); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	hooks, _ := root["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	root["hooks"] = hooks

	bin := exe
	if strings.ContainsAny(bin, " \t") {
		bin = `"` + bin + `"`
	}
	for ev, sstp := range codexEvents {
		groups, _ := hooks[ev].([]any)
		kept := []any{}
		for _, g := range groups {
			if g = withoutOurs(g); g != nil {
				kept = append(kept, g)
			}
		}
		hooks[ev] = append(kept, map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": bin + " " + sstp}},
		})
	}

	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Println("installed", path)
	if cfg, _ := os.ReadFile(filepath.Join(dir, "config.toml")); !strings.Contains(string(cfg), "hooks = true") {
		fmt.Printf("note: add `[features]\\nhooks = true` to %s\n", filepath.Join(dir, "config.toml"))
	}
	return nil
}

// withoutOurs drops cc-ukagaka commands from a hook group; nil if none remain.
func withoutOurs(g any) any {
	m, ok := g.(map[string]any)
	if !ok {
		return g
	}
	inner, _ := m["hooks"].([]any)
	kept := []any{}
	for _, h := range inner {
		if hm, ok := h.(map[string]any); ok {
			if c, _ := hm["command"].(string); strings.Contains(c, "cc-ukagaka") {
				continue
			}
		}
		kept = append(kept, h)
	}
	if len(kept) == 0 && len(inner) > 0 {
		return nil
	}
	m["hooks"] = kept
	return m
}
