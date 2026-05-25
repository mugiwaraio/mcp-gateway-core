package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestNew_EmitsJSON 校验 New 返回的 logger 输出 JSON 结构，含 msg/level/time/extras。
func TestNew_EmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf)
	logger.Warn("auth failed", "request_id", "abc123", "status", 401, "reason", "invalid_pat")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output not valid JSON: %s", buf.String())
	}
	if rec["msg"] != "auth failed" {
		t.Errorf("msg = %v", rec["msg"])
	}
	if rec["level"] != "WARN" {
		t.Errorf("level = %v", rec["level"])
	}
	if rec["request_id"] != "abc123" {
		t.Errorf("request_id = %v", rec["request_id"])
	}
	if rec["status"].(float64) != 401 {
		t.Errorf("status = %v", rec["status"])
	}
	if rec["reason"] != "invalid_pat" {
		t.Errorf("reason = %v", rec["reason"])
	}
	if _, ok := rec["time"]; !ok {
		t.Errorf("time field missing: %s", buf.String())
	}
}

// TestNewStderr 仅校验返回非 nil 与 Info-level 输出可工作（不抓取 stderr）。
func TestNewStderr(t *testing.T) {
	logger := NewStderr()
	if logger == nil {
		t.Fatal("NewStderr returned nil")
	}
	// 不应 panic
	logger.Info("smoke")
}

// staticGetter 返回固定 (value, true)；用于测试 ctx 字段透传。
func staticGetter(v string) CtxGetter {
	return func(_ context.Context) (string, bool) { return v, true }
}

// TestRequestAttrs_AllFieldsFromGetters 校验三个 getter 全有值时五字段都进 JSON。
func TestRequestAttrs_AllFieldsFromGetters(t *testing.T) {
	g := CtxGetters{
		RequestID: staticGetter("req-abc"),
		TraceID:   staticGetter("trace-xyz"),
		RemoteIP:  staticGetter("10.0.0.1"),
	}
	attrs := RequestAttrs(g, context.Background(), "alice", "tok-hash-1")

	var buf bytes.Buffer
	logger := New(&buf)
	logger.Warn("test", attrs...)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("invalid JSON: %s", buf.String())
	}
	want := map[string]string{
		"request_id": "req-abc",
		"trace_id":   "trace-xyz",
		"remote_ip":  "10.0.0.1",
		"user_id":    "alice",
		"token_id":   "tok-hash-1",
	}
	for k, v := range want {
		if got, _ := rec[k].(string); got != v {
			t.Errorf("%s = %q, want %q (full: %s)", k, got, v, buf.String())
		}
	}
}

// TestRequestAttrs_DropsEmpty 校验 getter 返回空 + user/token 为空时全部 drop。
func TestRequestAttrs_DropsEmpty(t *testing.T) {
	emptyGetter := func(_ context.Context) (string, bool) { return "", false }
	g := CtxGetters{
		RequestID: emptyGetter,
		TraceID:   emptyGetter,
		RemoteIP:  emptyGetter,
	}
	attrs := RequestAttrs(g, context.Background(), "", "")
	if len(attrs) != 0 {
		t.Errorf("expected empty attrs, got %d items: %v", len(attrs), attrs)
	}
}

// TestRequestAttrs_NilGettersSafe 校验任意 getter 为 nil 不 panic。
func TestRequestAttrs_NilGettersSafe(t *testing.T) {
	g := CtxGetters{
		RequestID: staticGetter("rid"),
		// TraceID 与 RemoteIP 故意留 nil
	}
	attrs := RequestAttrs(g, context.Background(), "bob", "")
	// 应该只有 request_id 与 user_id
	if len(attrs) != 4 {
		t.Errorf("expected 4 items (2 pairs), got %d: %v", len(attrs), attrs)
	}
}

// TestRequestAttrs_PartialCtx 校验只有 request_id 也能工作（鉴权失败前的中间状态）。
func TestRequestAttrs_PartialCtx(t *testing.T) {
	g := CtxGetters{
		RequestID: staticGetter("req-only"),
		TraceID:   func(_ context.Context) (string, bool) { return "", false },
		RemoteIP:  func(_ context.Context) (string, bool) { return "", false },
	}
	attrs := RequestAttrs(g, context.Background(), "", "")
	if len(attrs) != 2 || attrs[0] != "request_id" || attrs[1] != "req-only" {
		t.Errorf("partial ctx wrong: %v", attrs)
	}
}

// TestRequestAttrs_SplatIntoSlog 校验 attrs 直接 splat 进 slog.Warn 工作（接口契约）。
func TestRequestAttrs_SplatIntoSlog(t *testing.T) {
	g := CtxGetters{
		RequestID: staticGetter("rid"),
		RemoteIP:  staticGetter("1.2.3.4"),
	}
	var buf bytes.Buffer
	logger := New(&buf)
	logger.Warn("entry rejected",
		append(RequestAttrs(g, context.Background(), "bob", ""),
			"status", 401, "reason", "invalid_pat")...)

	var rec map[string]any
	_ = json.Unmarshal(buf.Bytes(), &rec)
	if rec["status"].(float64) != 401 || rec["reason"] != "invalid_pat" {
		t.Errorf("ad-hoc fields lost: %s", buf.String())
	}
	if rec["request_id"] != "rid" || rec["remote_ip"] != "1.2.3.4" {
		t.Errorf("ctx fields lost: %s", buf.String())
	}
	if rec["user_id"] != "bob" {
		t.Errorf("user_id lost: %s", buf.String())
	}
	if _, hit := rec["token_id"]; hit {
		t.Errorf("empty token_id should be dropped: %s", buf.String())
	}
}

// TestRequestAttrs_GetterReturnsFalse 校验 ok=false 时 drop 该字段，即使 value 非空。
func TestRequestAttrs_GetterReturnsFalse(t *testing.T) {
	g := CtxGetters{
		RequestID: func(_ context.Context) (string, bool) { return "should-be-dropped", false },
	}
	attrs := RequestAttrs(g, context.Background(), "", "")
	if len(attrs) != 0 {
		t.Errorf("ok=false should drop: %v", attrs)
	}
}

// TestRequestAttrs_GoroutineSafe 校验并发调用安全（slog 与 helper 都不应共享可变状态）。
func TestRequestAttrs_GoroutineSafe(t *testing.T) {
	g := CtxGetters{
		RequestID: staticGetter("rid"),
		TraceID:   staticGetter("tid"),
		RemoteIP:  staticGetter("1.2.3.4"),
	}
	done := make(chan struct{}, 50)
	var buf bytes.Buffer
	logger := New(&buf)
	for i := 0; i < 50; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			attrs := RequestAttrs(g, context.Background(), "u", "t")
			logger.Info("concurrent", attrs...)
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	// 没崩就行；output 不做严格断言（slog 自身保证 JSON 输出原子性，但 bytes.Buffer 并不并发安全；
	// 这里只是验证 helper / logger 自身不引入数据竞争）
	if !strings.Contains(buf.String(), `"msg":"concurrent"`) {
		t.Error("no output captured")
	}
}
