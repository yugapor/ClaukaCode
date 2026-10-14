package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAdapters_RenderBinaryPath(t *testing.T) {
	pi, xdg := t.TempDir(), t.TempDir()
	if err := os.Mkdir(filepath.Join(xdg, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", pi)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	exe := `C:\Program Files\cc\cc-ukagaka.exe`

	for agent, dst := range map[string]string{
		"pi":       filepath.Join(pi, "extensions", "cc-ukagaka.ts"),
		"opencode": filepath.Join(xdg, "opencode", "plugins", "cc-ukagaka.js"),
	} {
		if err := install(agent, exe); err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		b, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("%s: %v", agent, err)
		}
		if strings.Contains(string(b), "__CC_UKAGAKA_BIN__") || !strings.Contains(string(b), `"C:\\Program Files\\cc\\cc-ukagaka.exe"`) {
			t.Errorf("%s: binary path not rendered as a JS string literal", agent)
		}
	}
}

func TestInstall_MissingAgentDirAndUnknownAgent(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(t.TempDir(), "nope"))
	if err := install("pi", "x"); err == nil {
		t.Error("want error when the agent dir doesn't exist")
	}
	if err := install("bogus", "x"); err == nil {
		t.Error("want error for unknown agent")
	}
}

func TestInstallCodex_MergesIdempotentlyAndKeepsForeignHooks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	path := filepath.Join(dir, "hooks.json")
	foreign := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"other-tool"}]}]},"extra":1}`
	if err := os.WriteFile(path, []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // second run must replace, not duplicate
		if err := install("codex", "/opt/my bin/cc-ukagaka"); err != nil {
			t.Fatal(err)
		}
	}
	var got struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
		Extra int `json:"extra"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	stop := got.Hooks["Stop"]
	if got.Extra != 1 || len(stop) != 2 || stop[0].Hooks[0].Command != "other-tool" ||
		stop[1].Hooks[0].Command != `"/opt/my bin/cc-ukagaka" OnClaudeTaskComplete` {
		t.Errorf("unexpected Stop hooks: %+v (extra=%d)", stop, got.Extra)
	}
	if len(got.Hooks["SessionStart"]) != 1 || len(got.Hooks["UserPromptSubmit"]) != 1 {
		t.Errorf("want exactly one entry each for SessionStart/UserPromptSubmit: %+v", got.Hooks)
	}
}
