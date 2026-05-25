package audit

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// sample is a small struct used to verify JSON-line encoding behaviour.
type sample struct {
	ID  string `json:"id"`
	Val int    `json:"val"`
}

func TestNewWriter_OpensAndCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	// Nested non-existent parent dirs—NewWriter must mkdir -p them.
	path := filepath.Join(dir, "a", "b", "audit.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter error: %v", err)
	}
	defer w.Close()
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("audit file not created: %v", err)
	}
}

func TestNewWriter_RejectsEmptyPath(t *testing.T) {
	if _, err := NewWriter(""); err == nil {
		t.Fatal("expected error for empty path, got nil")
	}
}

func TestNewWriter_OpenFailureWrapped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission denied path is unreliable")
	}
	dir := t.TempDir()
	// Parent dir with 0o500 (read+exec, no write) → MkdirAll inside it succeeds
	// only for existing dirs, but OpenFile for the new file should fail with EACCES.
	roDir := filepath.Join(dir, "ro")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(roDir, "audit.jsonl")
	_, err := NewWriter(target)
	if err == nil {
		t.Fatal("expected error opening file under read-only dir, got nil")
	}
	if !strings.Contains(err.Error(), "audit") {
		t.Fatalf("error should be wrapped with audit prefix/context: %v", err)
	}
}

func TestWriter_WriteJSONLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Write(sample{ID: "abc", Val: 42}); err != nil {
		t.Fatalf("Write error: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.HasSuffix(s, "\n") {
		t.Fatalf("expected trailing newline, got %q", s)
	}
	line := strings.TrimRight(s, "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("expected exactly one line, got %q", s)
	}
	var got sample
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("invalid JSON: %v (%s)", err, line)
	}
	if got.ID != "abc" || got.Val != 42 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestWriter_WriteConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	const N = 100
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			if err := w.Write(sample{ID: "g", Val: i}); err != nil {
				t.Errorf("goroutine %d Write error: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) != N {
		t.Fatalf("expected %d lines, got %d", N, len(lines))
	}
	seen := make(map[int]bool, N)
	for _, ln := range lines {
		var s sample
		if err := json.Unmarshal([]byte(ln), &s); err != nil {
			t.Fatalf("invalid JSON line %q: %v", ln, err)
		}
		seen[s.Val] = true
	}
	if len(seen) != N {
		t.Fatalf("expected %d unique Val, got %d", N, len(seen))
	}
}

func TestWriter_WriteAfterCloseReturnsErrClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	err = w.Write(sample{ID: "x"})
	if err == nil {
		t.Fatal("expected error after close, got nil")
	}
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected errors.Is(err, ErrClosed), got %v", err)
	}
}

func TestWriter_CloseIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close should be nil, got %v", err)
	}
}

func TestWriter_WriteFailureWrappedUnavailable(t *testing.T) {
	// Construct a Writer whose underlying *os.File is closed at the OS level
	// but whose closed flag is still false. Subsequent Write/Sync will fail with
	// "file already closed" and must be wrapped as ErrUnavailable.
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// Close at OS level so writes return error; do NOT set w.closed.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	w := &Writer{file: f}
	err = w.Write(sample{ID: "fail"})
	if err == nil {
		t.Fatal("expected write to fail, got nil")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected errors.Is(err, ErrUnavailable), got %v", err)
	}
}

func TestWriter_MarshalFailurePropagated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	w, err := NewWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	// chan can not be marshalled by encoding/json.
	err = w.Write(make(chan int))
	if err == nil {
		t.Fatal("expected marshal error, got nil")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("marshal error must not be classified as ErrUnavailable: %v", err)
	}
	if !strings.Contains(err.Error(), "marshal") {
		t.Fatalf("expected error to mention 'marshal', got %v", err)
	}
}
