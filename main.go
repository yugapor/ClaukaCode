// cc-ukagaka: Claude Code hook -> SSTP NOTIFY emitter (fire-and-forget).
// Usage: cc-ukagaka <SSTP_EVENT_NAME>   (hook JSON on stdin, optional)
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

const (
	defaultHost = "127.0.0.1"
	defaultPort = "9801"
	senderName  = "ClaudeCode"
	ioDeadline  = 1000 * time.Millisecond // < SSTP server 2s limit
)

type hookInput struct {
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	SessionID     string `json:"session_id"`
}

func main() {
	// (1) Never block Claude: background immediately.
	fmt.Println(`{"async":true}`)

	// (2) SSTP Event name from argv[1]; exit silently if absent.
	if len(os.Args) < 2 || os.Args[1] == "" {
		os.Exit(0)
	}
	event := sanitize(os.Args[1])

	// (3) Read hook JSON from stdin (optional).
	var in hookInput
	if data, err := io.ReadAll(os.Stdin); err == nil && len(data) > 0 {
		_ = json.Unmarshal(data, &in) // best-effort; ignore errors
	}

	// (4) Build NOTIFY (CRLF framed, blank-line terminated).
	host := envOr("CC_UKAGAKA_HOST", defaultHost)
	port := envOr("CC_UKAGAKA_PORT", defaultPort)

	msg := "NOTIFY SSTP/1.1\r\n" +
		"Sender: " + senderName + "\r\n" +
		"Event: " + event + "\r\n"
	if in.ToolName != "" {
		msg += "Reference0: " + sanitize(in.ToolName) + "\r\n"
	}
	if in.SessionID != "" {
		msg += "Reference1: " + sanitize(in.SessionID) + "\r\n"
	}
	msg += "Charset: UTF-8\r\n\r\n"

	// (5) Fire-and-forget. Any error -> exit 0.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), ioDeadline)
	if err != nil {
		os.Exit(0)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(ioDeadline))
	if _, err := conn.Write([]byte(msg)); err != nil {
		os.Exit(0)
	}
	_, _ = bufio.NewReader(conn).ReadString('\n') // read & discard response
	os.Exit(0)
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// sanitize removes CR/LF so header values cannot break SSTP framing.
func sanitize(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r != '\r' && r != '\n' {
			out = append(out, r)
		}
	}
	return string(out)
}
