package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"strings"
	"testing"
)

func lastLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &m); err != nil {
		t.Fatalf("log line is not JSON: %q", lines[len(lines)-1])
	}
	return m
}

func TestSetup_StdlibLogBecomesJSON(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf)
	log.Printf("redis connected on %s", "host")
	m := lastLine(t, &buf)
	if m["msg"] != "redis connected on host" || m["level"] != "INFO" {
		t.Fatalf("unexpected line %v", m)
	}
}

func TestSetup_AddsRequestIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	Setup(&buf)
	slog.ErrorContext(WithRequestID(context.Background(), "req-12345678"), "throttle down")
	if m := lastLine(t, &buf); m["request_id"] != "req-12345678" || m["level"] != "ERROR" {
		t.Fatalf("expected request_id in %v", m)
	}
	slog.Info("no request")
	if m := lastLine(t, &buf); m["request_id"] != nil {
		t.Fatalf("no request_id expected outside a request: %v", m)
	}
}
