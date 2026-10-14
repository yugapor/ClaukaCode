package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

// End-to-end: installed adapter -> real cc-ukagaka binary -> mock SSTP server.
// The agent host (pi / opencode / codex) is simulated, so these pin OUR logic
// (event mapping, gating, child-session filter, stdin piping, path quoting),
// not the agents' real plugin contracts.

// frameCollector accepts any number of connections and records "Event|Ref1"
// for each NOTIFY, pointing the (inherited) env at itself.
func frameCollector(t *testing.T) func(want int) []string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	t.Setenv("CC_UKAGAKA_HOST", host)
	t.Setenv("CC_UKAGAKA_PORT", port)

	frames := make(chan string, 64)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				r := bufio.NewReader(conn)
				var event, ref1 string
				for {
					line, err := r.ReadString('\n')
					if v, ok := strings.CutPrefix(line, "Event: "); ok {
						event = strings.TrimSpace(v)
					}
					if v, ok := strings.CutPrefix(line, "Reference1: "); ok {
						ref1 = strings.TrimSpace(v)
					}
					if line == "\r\n" || err != nil {
						break
					}
				}
				frames <- event + "|" + ref1
			}()
		}
	}()

	return func(want int) []string {
		t.Helper()
		var got []string
		deadline := time.After(5 * time.Second)
		for len(got) < want {
			select {
			case f := <-frames:
				got = append(got, f)
			case <-deadline:
				t.Fatalf("got %d frames %v, want %d", len(got), got, want)
			}
		}
		// grace period: anything extra means a gate failed to filter
		for {
			select {
			case f := <-frames:
				got = append(got, f)
			case <-time.After(300 * time.Millisecond):
				sort.Strings(got) // spawn order across processes isn't guaranteed
				return got
			}
		}
	}
}

const nodeHarness = `
import { pathToFileURL } from "node:url";
const [, , file, kind] = process.argv;
const mod = await import(pathToFileURL(file).href);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
if (kind === "pi") {
  const h = {};
  mod.default({ on: (n, f) => { h[n] = f; } });
  const tui = { mode: "tui", isIdle: () => true, sessionManager: { getSessionId: () => "S1" } };
  await h.session_start({}, tui);
  await h.agent_start({}, tui);
  await h.agent_settled({}, { ...tui, isIdle: () => false }); // still busy: ignored
  await h.agent_settled({}, { ...tui, mode: "rpc" });         // headless: ignored
  await h.agent_settled({}, tui);
} else {
  if (mod.default.server !== mod.CcUkagakaPlugin) throw new Error("default export shape");
  const p = await mod.CcUkagakaPlugin();
  const ev = (type, properties) => p.event({ event: { type, properties } });
  await p["chat.message"]({ sessionID: "root" });
  await ev("session.created", { info: { id: "root" } });
  await ev("session.created", { info: { id: "kid", parentID: "root" } }); // child: ignored
  await ev("session.idle", { sessionID: "kid" });                          // child: ignored
  await ev("permission.asked", { sessionID: "root" });
  await ev("session.idle", { sessionID: "root" });
}
await sleep(300); // let the unref'd children flush their stdin
`

func runHarness(t *testing.T, adapter, kind string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	src, err := os.ReadFile(adapter)
	if err != nil {
		t.Fatal(err)
	}
	// pi.ts has no TS syntax; copy to .mjs so older node versions can load it too.
	mod := filepath.Join(dir, "adapter.mjs")
	if err := os.WriteFile(mod, src, 0o644); err != nil {
		t.Fatal(err)
	}
	h := filepath.Join(dir, "harness.mjs")
	if err := os.WriteFile(h, []byte(nodeHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(node, h, mod, kind).CombinedOutput(); err != nil {
		t.Fatalf("harness failed: %v\n%s", err, out)
	}
}

func TestPiAdapter_E2E(t *testing.T) {
	collect := frameCollector(t)
	base := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", base)
	if err := install("pi", binPath); err != nil {
		t.Fatal(err)
	}
	runHarness(t, filepath.Join(base, "extensions", "cc-ukagaka.ts"), "pi")

	want := []string{"OnClaudePromptSubmit|S1", "OnClaudeSessionStart|S1", "OnClaudeTaskComplete|S1"}
	if got := collect(len(want)); !reflect.DeepEqual(got, want) {
		t.Errorf("frames = %v, want %v", got, want)
	}
}

func TestOpenCodeAdapter_E2E(t *testing.T) {
	collect := frameCollector(t)
	xdg := t.TempDir()
	if err := os.Mkdir(filepath.Join(xdg, "opencode"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := install("opencode", binPath); err != nil {
		t.Fatal(err)
	}
	runHarness(t, filepath.Join(xdg, "opencode", "plugins", "cc-ukagaka.js"), "opencode")

	want := []string{"OnClaudePermissionRequest|root", "OnClaudePromptSubmit|root", "OnClaudeSessionStart|root", "OnClaudeTaskComplete|root"}
	if got := collect(len(want)); !reflect.DeepEqual(got, want) {
		t.Errorf("frames = %v, want %v", got, want)
	}
}

// Runs the command string from hooks.json through a shell exactly as a
// Claude-style hook runner would, from a path containing a space.
func TestCodexHooks_E2E(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell quoting of the hook command is only exercised on unix")
	}
	collect := frameCollector(t)

	spaced := filepath.Join(t.TempDir(), "dir with space")
	if err := os.Mkdir(spaced, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(spaced, "cc-ukagaka")
	b, _ := os.ReadFile(binPath)
	if err := os.WriteFile(exe, b, 0o755); err != nil {
		t.Fatal(err)
	}

	codex := t.TempDir()
	t.Setenv("CODEX_HOME", codex)
	if err := install("codex", exe); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	raw, _ := os.ReadFile(filepath.Join(codex, "hooks.json"))
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}

	for ev := range codexEvents {
		cmd := exec.Command("sh", "-c", cfg.Hooks[ev][0].Hooks[0].Command)
		in, _ := cmd.StdinPipe()
		go func() { _, _ = io.WriteString(in, `{"hook_event_name":"`+ev+`","session_id":"C1"}`); in.Close() }()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s hook failed: %v\n%s", ev, err, out)
		}
	}
	want := []string{"OnClaudePromptSubmit|C1", "OnClaudeSessionStart|C1", "OnClaudeTaskComplete|C1"}
	if got := collect(len(want)); !reflect.DeepEqual(got, want) {
		t.Errorf("frames = %v, want %v", got, want)
	}
}
