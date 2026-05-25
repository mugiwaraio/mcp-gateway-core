package httpaccess

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestMiddleware_GeneratesAndInjectsRequestID(t *testing.T) {
	dir := t.TempDir()
	lg, _ := NewJSONLogger(filepath.Join(dir, "a.log"), false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	var gotID, gotIP string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := RequestIDFromContext(r.Context())
		ip, _ := RemoteIPFromContext(r.Context())
		gotID, gotIP = id, ip
		w.WriteHeader(http.StatusOK)
	})
	mw := Middleware(lg, ext, nil)(h)

	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if gotID == "" {
		t.Fatal("expected request_id injected into ctx")
	}
	if gotIP != "1.2.3.4" {
		t.Fatalf("got remote_ip %q, want 1.2.3.4", gotIP)
	}
}

func TestMiddleware_RequestIDFormat_16HexBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	b, _ := os.ReadFile(path)
	var rec struct {
		RequestID string `json:"request_id"`
	}
	line := strings.TrimRight(string(b), "\n")
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(rec.RequestID) {
		t.Fatalf("request_id %q does not match 32 hex chars (16 bytes)", rec.RequestID)
	}
}

func TestMiddleware_CapturesStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("GET", "/whatever", nil)
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"status":404`) {
		t.Fatalf("expected status 404 in log, got %s", b)
	}
}

func TestMiddleware_DefaultStatus200_WhenHandlerDoesntWriteHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	})
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"status":200`) {
		t.Fatalf("want status:200, got %s", b)
	}
}

func TestMiddleware_HandlerCalledOnce(t *testing.T) {
	dir := t.TempDir()
	lg, _ := NewJSONLogger(filepath.Join(dir, "a.log"), false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)
	count := 0
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { count++ })
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)
	if count != 1 {
		t.Fatalf("handler called %d times, want 1", count)
	}
}

func TestMiddleware_NilLogger_UsesNop(t *testing.T) {
	ext, _ := NewIPExtractor(nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mw := Middleware(nil, ext, nil)(h)
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)
}

func TestMiddleware_IdentityFn_CalledWithAuthHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	var gotHeader string
	identity := func(h string) (string, string) {
		gotHeader = h
		return "pat_alice", "tok_abc"
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mw := Middleware(lg, ext, identity)(h)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "1.2.3.4:1"
	req.Header.Set("Authorization", "Bearer mytoken")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if gotHeader != "Bearer mytoken" {
		t.Fatalf("identity called with %q, want %q", gotHeader, "Bearer mytoken")
	}
	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, `"pat_id":"pat_alice"`) || !strings.Contains(s, `"token_id":"tok_abc"`) {
		t.Fatalf("pat_id/token_id missing in log: %s", s)
	}
}

func TestMiddleware_IdentityFn_ReturnsEmpty_OmitsFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	identity := func(h string) (string, string) { return "", "" }
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mw := Middleware(lg, ext, identity)(h)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"pat_id"`) || strings.Contains(string(b), `"token_id"`) {
		t.Fatalf("pat_id/token_id should be omitted when empty, got %s", b)
	}
}

func TestMiddleware_IdentityFn_PartialReturn_OmitsOnlyEmptyField(t *testing.T) {
	// pat_id 有但 token_id 空（或反之）时，omitempty 应只省略空字段，另一个保留。
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	identity := func(h string) (string, string) { return "pat_alice", "" }
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mw := Middleware(lg, ext, identity)(h)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "1.2.3.4:1"
	req.Header.Set("Authorization", "Bearer x")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	b, _ := os.ReadFile(path)
	s := string(b)
	if !strings.Contains(s, `"pat_id":"pat_alice"`) {
		t.Fatalf("expected pat_id preserved, got %s", s)
	}
	if strings.Contains(s, `"token_id"`) {
		t.Fatalf("empty token_id should be omitted, got %s", s)
	}
}

func TestMiddleware_NilIdentityFn_SkipsFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "1.2.3.4:1"
	req.Header.Set("Authorization", "Bearer mytoken")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"pat_id"`) || strings.Contains(string(b), `"token_id"`) {
		t.Fatalf("pat_id/token_id should not appear when identity is nil, got %s", b)
	}
}

func TestParseTraceparent_ValidHeader(t *testing.T) {
	h := "00-deadbeefcafebabedeadbeefcafebabe-0123456789abcdef-01"
	got := parseTraceparent(h)
	if got != "deadbeefcafebabedeadbeefcafebabe" {
		t.Fatalf("parseTraceparent(%q) = %q, want deadbeefcafebabedeadbeefcafebabe", h, got)
	}
}

func TestParseTraceparent_InvalidFormats(t *testing.T) {
	cases := []string{
		"",
		"invalid",
		"01-deadbeefcafebabedeadbeefcafebabe-0123456789abcdef-01", // 非 00 版本
		"00-DEADBEEFCAFEBABEDEADBEEFCAFEBABE-0123456789abcdef-01", // 大写 trace_id
		"00-deadbeef-0123456789abcdef-01",                         // trace_id 太短
		"00-00000000000000000000000000000000-0123456789abcdef-01", // 全零 trace_id
		"00-deadbeefcafebabedeadbeefcafebabe-short-01",            // span_id 太短
		"00-deadbeefcafebabedeadbeefcafebabe-0123456789abcdef-x",  // flags 格式不对
	}
	for _, c := range cases {
		if got := parseTraceparent(c); got != "" {
			t.Errorf("parseTraceparent(%q) = %q, want empty", c, got)
		}
	}
}

func TestMiddleware_TraceID_FromHeader(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	var gotTraceID string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid, _ := TraceIDFromContext(r.Context())
		gotTraceID = tid
		w.WriteHeader(200)
	})
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:1"
	req.Header.Set("traceparent", "00-deadbeefcafebabedeadbeefcafebabe-0123456789abcdef-01")
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if gotTraceID != "deadbeefcafebabedeadbeefcafebabe" {
		t.Fatalf("trace_id in ctx = %q, want deadbeefcafebabedeadbeefcafebabe", gotTraceID)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"trace_id":"deadbeefcafebabedeadbeefcafebabe"`) {
		t.Fatalf("trace_id missing from access log: %s", b)
	}
}

func TestMiddleware_TraceID_GeneratedWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	var gotTraceID string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tid, ok := TraceIDFromContext(r.Context())
		if !ok || tid == "" {
			t.Error("expected trace_id injected when header absent")
		}
		gotTraceID = tid
		w.WriteHeader(200)
	})
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("GET", "/x", nil) // traceparent header 未设置
	req.RemoteAddr = "1.2.3.4:1"
	mw.ServeHTTP(httptest.NewRecorder(), req)

	if len(gotTraceID) != 32 {
		t.Fatalf("generated trace_id = %q, want 32 hex chars", gotTraceID)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"trace_id":"`+gotTraceID+`"`) {
		t.Fatalf("generated trace_id missing from access log: %s", b)
	}
}

func TestNewSpanID_Format(t *testing.T) {
	id := NewSpanID()
	if len(id) != 16 {
		t.Fatalf("NewSpanID() = %q, want 16 hex chars", id)
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Fatalf("NewSpanID() contains non-lowercase-hex char %q: %s", c, id)
		}
	}
}

func TestMiddleware_TimestampIsRequestArrival(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	lg, _ := NewJSONLogger(path, false)
	defer lg.Close()
	ext, _ := NewIPExtractor(nil)

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	mw := Middleware(lg, ext, nil)(h)
	req := httptest.NewRequest("GET", "/x", nil)
	req.RemoteAddr = "1.2.3.4:1"

	before := time.Now()
	mw.ServeHTTP(httptest.NewRecorder(), req)
	after := time.Now()

	b, _ := os.ReadFile(path)
	var rec struct {
		Timestamp  time.Time `json:"ts"`
		DurationMs int       `json:"duration_ms"`
	}
	line := strings.TrimRight(string(b), "\n")
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Timestamp.Before(before) {
		t.Fatalf("timestamp %v earlier than before %v", rec.Timestamp, before)
	}
	end := rec.Timestamp.Add(time.Duration(rec.DurationMs) * time.Millisecond)
	if end.After(after) {
		t.Fatalf("timestamp+duration %v after caller end %v", end, after)
	}
	if rec.DurationMs < 50 {
		t.Fatalf("duration_ms = %d, want >= 50 (handler slept 50ms)", rec.DurationMs)
	}
}
