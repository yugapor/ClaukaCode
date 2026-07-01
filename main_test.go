package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var binPath string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// os.Exit skips deferred calls, so cleanup must happen inside this helper,
// not in TestMain itself.
func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "cc-ukagaka-test-*")
	if err != nil {
		fmt.Println("mkdirtemp:", err)
		return 1
	}
	defer os.RemoveAll(dir)

	binPath = filepath.Join(dir, "cc-ukagaka")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}

	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Printf("go build failed: %v\n%s", err, out)
		return 1
	}

	return m.Run()
}

// --- unit tests (white-box) ---

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"a\r\nb", "ab"},
		{"a\rb\nc", "abc"},
		{"\r\n\r\n", ""},
		{"日本語\r\nテスト", "日本語テスト"},
		{"NOTIFY SSTP/1.1\r\nEvent: Injected\r\n\r\n", "NOTIFY SSTP/1.1Event: Injected"},
	}
	for _, c := range cases {
		if got := sanitize(c.in); got != c.want {
			t.Errorf("sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEnvOr(t *testing.T) {
	const key = "CC_UKAGAKA_TEST_ENVOR"
	os.Unsetenv(key)
	if got := envOr(key, "default"); got != "default" {
		t.Errorf("envOr(unset) = %q, want %q", got, "default")
	}

	os.Setenv(key, "custom")
	defer os.Unsetenv(key)
	if got := envOr(key, "default"); got != "custom" {
		t.Errorf("envOr(set) = %q, want %q", got, "custom")
	}

	os.Setenv(key, "")
	if got := envOr(key, "default"); got != "default" {
		t.Errorf("envOr(empty) = %q, want %q (empty must fall back to default)", got, "default")
	}
}

// --- integration tests (black-box: spawn the built binary) ---
//
// Every test that reaches the network-send path explicitly points
// CC_UKAGAKA_HOST/PORT at a throwaway local listener. None of them may ever
// dial the real default 127.0.0.1:9801, or a live SSP/ghost on the dev
// machine would visibly react during `go test`.

type result struct {
	stdout   string
	stderr   string
	exitCode int
	elapsed  time.Duration
}

func run(t *testing.T, args []string, stdin string, env map[string]string) result {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)

	exitCode := 0
	if err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("failed to run binary: %v", err)
		}
		exitCode = ee.ExitCode()
	}

	return result{stdout: stdout.String(), stderr: stderr.String(), exitCode: exitCode, elapsed: elapsed}
}

type mockServer struct {
	ln       net.Listener
	received chan string
}

// startMockServer accepts one connection, reads until the SSTP blank-line
// header terminator, and optionally writes back a canned response line.
func startMockServer(t *testing.T, respond bool) *mockServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ms := &mockServer{ln: ln, received: make(chan string, 1)}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		r := bufio.NewReader(conn)
		var frame strings.Builder
		for {
			line, err := r.ReadString('\n')
			frame.WriteString(line)
			if line == "\r\n" || err != nil {
				break
			}
		}
		ms.received <- frame.String()

		if respond {
			_, _ = conn.Write([]byte("SSTP/1.4 200 OK\r\n\r\n"))
		}
	}()
	return ms
}

func (ms *mockServer) hostPort() (host, port string) {
	host, port, _ = net.SplitHostPort(ms.ln.Addr().String())
	return
}

func (ms *mockServer) close() { ms.ln.Close() }

func waitForFrame(t *testing.T, ms *mockServer) string {
	t.Helper()
	select {
	case frame := <-ms.received:
		return frame
	case <-time.After(2 * time.Second):
		t.Fatal("mock server never received a NOTIFY frame")
		return ""
	}
}

// freePort binds then immediately releases a port so nothing is listening
// on it, guaranteeing an ECONNREFUSED rather than a filtered/hanging port.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ln.Close()
	return port
}

func hookJSON(t *testing.T, in hookInput) string {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal hook input: %v", err)
	}
	return string(b)
}

func TestNoEventArg_ExitsImmediately(t *testing.T) {
	res := run(t, nil, "", nil)
	if res.exitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.exitCode)
	}
	if res.stdout != "{\"async\":true}\n" {
		t.Errorf("stdout = %q, want async line only", res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
}

func TestEmptyEventArg_ExitsImmediately(t *testing.T) {
	res := run(t, []string{""}, "", nil)
	if res.exitCode != 0 || res.stdout != "{\"async\":true}\n" || res.stderr != "" {
		t.Errorf("got exit=%d stdout=%q stderr=%q, want exit=0 async-only stdout, no stderr",
			res.exitCode, res.stdout, res.stderr)
	}
}

func TestNoEventArg_NeverConnects(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	run(t, nil, "", map[string]string{"CC_UKAGAKA_HOST": host, "CC_UKAGAKA_PORT": port})

	select {
	case frame := <-ms.received:
		t.Errorf("server unexpectedly received a connection/frame: %q", frame)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNotifyFrame_MinimalEvent(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	res := run(t, []string{"OnClaudeTaskComplete"}, "", map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Fatalf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}

	frame := waitForFrame(t, ms)
	want := "NOTIFY SSTP/1.1\r\n" +
		"Sender: " + senderName + "\r\n" +
		"Event: OnClaudeTaskComplete\r\n" +
		"Charset: UTF-8\r\n\r\n"
	if frame != want {
		t.Errorf("frame = %q, want %q", frame, want)
	}
}

func TestNotifyFrame_WithReferences(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	stdin := hookJSON(t, hookInput{HookEventName: "Stop", ToolName: "Bash", SessionID: "abc-123"})
	res := run(t, []string{"OnClaudeTaskComplete"}, stdin, map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Fatalf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}

	frame := waitForFrame(t, ms)
	want := "NOTIFY SSTP/1.1\r\n" +
		"Sender: " + senderName + "\r\n" +
		"Event: OnClaudeTaskComplete\r\n" +
		"Reference0: Bash\r\n" +
		"Reference1: abc-123\r\n" +
		"Charset: UTF-8\r\n\r\n"
	if frame != want {
		t.Errorf("frame = %q, want %q", frame, want)
	}
}

func TestNotifyFrame_MalformedStdinJSON_StillSendsBareEvent(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	res := run(t, []string{"OnClaudeNotification"}, "{not valid json", map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Fatalf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}

	frame := waitForFrame(t, ms)
	want := "NOTIFY SSTP/1.1\r\n" +
		"Sender: " + senderName + "\r\n" +
		"Event: OnClaudeNotification\r\n" +
		"Charset: UTF-8\r\n\r\n"
	if frame != want {
		t.Errorf("frame = %q, want %q (malformed JSON must be ignored, not crash)", frame, want)
	}
}

func TestNotifyFrame_HeaderInjectionIsSanitized(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	malicious := "Bash\r\nEvent: OnClaudeHacked\r\n\r\nGET /evil HTTP/1.1"
	stdin := hookJSON(t, hookInput{ToolName: malicious, SessionID: "s\r\nid"})
	res := run(t, []string{"OnClaudeTaskComplete"}, stdin, map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Fatalf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}

	frame := waitForFrame(t, ms)

	if n := strings.Count(frame, "\r\n\r\n"); n != 1 {
		t.Errorf("frame has %d header terminators, want exactly 1 (injection must not add one): %q", n, frame)
	}
	for _, line := range strings.Split(strings.TrimSuffix(frame, "\r\n"), "\r\n") {
		if line == "Event: OnClaudeHacked" {
			t.Errorf("frame contains an injected standalone Event header line: %q", frame)
		}
	}
	if bare := strings.ReplaceAll(frame, "\r\n", ""); strings.ContainsAny(bare, "\r\n") {
		t.Errorf("frame contains a bare CR or LF outside of CRLF pairs: %q", frame)
	}
	wantRef0 := "Reference0: BashEvent: OnClaudeHackedGET /evil HTTP/1.1\r\n"
	if !strings.Contains(frame, wantRef0) {
		t.Errorf("frame missing sanitized Reference0 line %q, got %q", wantRef0, frame)
	}
}

func TestConnectionRefused_ExitsCleanly(t *testing.T) {
	port := freePort(t)
	res := run(t, []string{"OnClaudeTaskComplete"}, "", map[string]string{
		"CC_UKAGAKA_HOST": "127.0.0.1",
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 {
		t.Errorf("exit code = %d, want 0", res.exitCode)
	}
	if res.stdout != "{\"async\":true}\n" {
		t.Errorf("stdout = %q, want async line only", res.stdout)
	}
	if res.stderr != "" {
		t.Errorf("stderr = %q, want empty", res.stderr)
	}
	if res.elapsed >= ioDeadline {
		t.Errorf("took %v to fail over a refused connection, want fast-fail well under the %v I/O deadline", res.elapsed, ioDeadline)
	}
}

func TestUnresponsiveServer_RespectsIODeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	// Never Accept()/read/write: the TCP handshake still completes via the
	// listen backlog, so the client connects and writes successfully, then
	// must time out waiting for a response that never comes.

	res := run(t, []string{"OnClaudeTaskComplete"}, "", map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Errorf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}
	if res.elapsed < ioDeadline {
		t.Errorf("returned in %v, faster than the %v I/O deadline; deadline may not be applied", res.elapsed, ioDeadline)
	}
	if res.elapsed > ioDeadline+2*time.Second {
		t.Errorf("took %v to hit a %v deadline, want a bounded overshoot", res.elapsed, ioDeadline)
	}
}

func TestCustomHostAndPort(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	res := run(t, []string{"OnClaudeSessionStart"}, "", map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Fatalf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}
	waitForFrame(t, ms)
}

func TestLargeStdinPayload_DoesNotCrash(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	huge := strings.Repeat("x", 2<<20) // 2MiB of garbage, not valid JSON
	res := run(t, []string{"OnClaudeNotification"}, huge, map[string]string{
		"CC_UKAGAKA_HOST": host,
		"CC_UKAGAKA_PORT": port,
	})
	if res.exitCode != 0 || res.stderr != "" {
		t.Errorf("got exit=%d stderr=%q, want exit=0 no stderr", res.exitCode, res.stderr)
	}
	waitForFrame(t, ms)
}

// TestStdinNeverCloses_RespectsDeadline uses a real OS pipe (an *os.File),
// not run()'s in-memory stdin, because exec.Cmd.Wait blocks on its own
// stdin-copy goroutine when Stdin is a plain io.Reader - which would make
// this test hang right along with a buggy implementation instead of
// detecting it. An *os.File is handed to the child directly, so Wait only
// tracks the child process itself.
func TestStdinNeverCloses_RespectsDeadline(t *testing.T) {
	ms := startMockServer(t, true)
	defer ms.close()
	host, port := ms.hostPort()

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer pw.Close()
	defer pr.Close()

	cmd := exec.Command(binPath, "OnClaudeNotification")
	cmd.Stdin = pr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "CC_UKAGAKA_HOST="+host, "CC_UKAGAKA_PORT="+port)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		elapsed := time.Since(start)
		if err != nil {
			t.Errorf("process exited with error: %v", err)
		}
		if stderr.String() != "" {
			t.Errorf("stderr = %q, want empty", stderr.String())
		}
		if elapsed > ioDeadline+2*time.Second {
			t.Errorf("took %v to give up on a stalled stdin, want bounded by ~%v", elapsed, ioDeadline)
		}
		waitForFrame(t, ms)
	case <-time.After(ioDeadline + 3*time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process never exited: stdin read is blocking indefinitely")
	}
}
