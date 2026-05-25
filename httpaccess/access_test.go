package httpaccess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJSONLogger_WritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.jsonl")
	lg, err := NewJSONLogger(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	lg.Log(Record{Timestamp: time.Unix(1, 0).UTC(), RequestID: "r1", Method: "POST", Path: "/mcp", Status: 200})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"request_id":"r1"`) {
		t.Fatalf("file content missing request_id: %s", b)
	}
	if !strings.Contains(string(b), `"ts":`) {
		t.Fatalf("expected ts field (not timestamp), got %s", b)
	}
	var rec map[string]any
	line := strings.TrimRight(string(b), "\n")
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("invalid JSON line: %v (%s)", err, line)
	}
}

func TestJSONLogger_FileAndStderr_BothEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.jsonl")
	lg, err := NewJSONLogger(path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	lg.Log(Record{RequestID: "r2"})
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"request_id":"r2"`) {
		t.Fatalf("file missing line: %s", b)
	}
}

func TestJSONLogger_StderrOnly(t *testing.T) {
	lg, err := NewJSONLogger("", true)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	lg.Log(Record{RequestID: "r3"})
}

func TestJSONLogger_EmptyConfig_Error(t *testing.T) {
	if _, err := NewJSONLogger("", false); err == nil {
		t.Fatal("expected error when both sinks disabled")
	}
}

func TestJSONLogger_Concurrent_NoLineMixing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.jsonl")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	var wg sync.WaitGroup
	const N = 100
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			lg.Log(Record{RequestID: "r", Status: i})
		}(i)
	}
	wg.Wait()
	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != N {
		t.Fatalf("got %d lines, want %d", len(lines), N)
	}
	for i, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("line %d not valid JSON: %v (%q)", i, err, ln)
		}
	}
}

func TestJSONLogger_LogAfterClose_NoPanic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "access.jsonl")
	lg, _ := NewJSONLogger(path, false)
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
	lg.Log(Record{RequestID: "r"})
}

func TestNopLogger_NoPanic(t *testing.T) {
	var lg Logger = NopLogger{}
	lg.Log(Record{RequestID: "r"})
	if err := lg.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecord_TraceID_OmitEmpty(t *testing.T) {
	// trace_id 为空时 JSON 中不出现该字段
	rec := Record{RequestID: "r1"}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"trace_id"`) {
		t.Errorf("empty trace_id should be omitted, got %s", b)
	}
}

func TestRecord_TraceID_Serialized(t *testing.T) {
	// trace_id 非空时出现在 JSON 中
	rec := Record{RequestID: "r2", TraceID: "deadbeefcafebabedeadbeefcafebabe"}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"trace_id":"deadbeefcafebabedeadbeefcafebabe"`) {
		t.Errorf("trace_id missing from JSON: %s", b)
	}
}
