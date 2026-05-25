package httpaccess

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type ctxKey int

const (
	ctxKeyRequestID ctxKey = iota
	ctxKeyRemoteIP
	ctxKeyTraceID
)

// IdentityFn 把 Authorization header 解析为 (pat_id, token_id)，用于写入访问日志。
// 由调用方注入（典型实现：调用 auth.ParseBearer + auth.Lookup + auth.TokenID）。
// 调用时机：handler 执行完毕后（post-handler），在写 access log 前 —— 不影响鉴权决策。
// 未鉴权或解析失败返回两个空串。
// 并发安全要求：函数在每个请求的 goroutine 内串行调用，闭包自身须保证对其捕获状态的并发安全。
type IdentityFn func(authHeader string) (patID, tokenID string)

// WithRequestID 把 request_id 注入 ctx。
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyRequestID, id)
}

// RequestIDFromContext 取 request_id；未注入时 ok=false。
func RequestIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyRequestID).(string)
	return v, ok
}

// WithRemoteIP 注入 remote_ip。
func WithRemoteIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, ctxKeyRemoteIP, ip)
}

// RemoteIPFromContext 取 remote_ip；未注入时 ok=false。
func RemoteIPFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyRemoteIP).(string)
	return v, ok
}

// WithTraceID 把 trace_id 注入 ctx。
func WithTraceID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKeyTraceID, id)
}

// TraceIDFromContext 取 trace_id；未注入时 ok=false。
func TraceIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(ctxKeyTraceID).(string)
	return v, ok
}

// parseTraceparent 解析 W3C traceparent header（version-traceid-spanid-flags）。
// 返回 trace_id（32 hex 小写）；任何格式异常或全零 trace_id 返回空串。
// 仅支持 version "00"。
func parseTraceparent(h string) string {
	parts := strings.Split(h, "-")
	if len(parts) != 4 {
		return ""
	}
	if parts[0] != "00" {
		return ""
	}
	tid := parts[1]
	if len(tid) != 32 {
		return ""
	}
	for _, c := range tid {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return ""
		}
	}
	if tid == "00000000000000000000000000000000" {
		return ""
	}
	if len(parts[2]) != 16 || len(parts[3]) != 2 {
		return ""
	}
	return tid
}

// NewSpanID 返回 8 字节 hex（16 字符），用于出口 traceparent 的 span_id 占位。
// scheme B 不引 OTel SDK，没有真实 span 拓扑；这里仅满足 W3C 格式要求。
// 导出供下游 HTTP 客户端（如 consumer 的 lokiplugin）复用。
func NewSpanID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status = code
		s.written = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.written {
		s.status = http.StatusOK
		s.written = true
	}
	return s.ResponseWriter.Write(b)
}

// Middleware 返回一个 HTTP 包装器：注入 request_id + remote_ip + trace_id 到 ctx，handler 完成后写 access log。
// logger 为 nil 时使用 NopLogger；identity 为 nil 时 access log 不带 pat_id/token_id。
func Middleware(logger Logger, ip *IPExtractor, identity IdentityFn) func(http.Handler) http.Handler {
	if logger == nil {
		logger = NopLogger{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			reqID := NewRequestID()
			remoteIP := ip.ClientIP(r)
			ctx := WithRequestID(r.Context(), reqID)
			ctx = WithRemoteIP(ctx, remoteIP)
			traceID := parseTraceparent(r.Header.Get("traceparent"))
			if traceID == "" {
				traceID = NewRequestID() // 32 hex 与 W3C trace_id 长度一致
			}
			ctx = WithTraceID(ctx, traceID)
			r = r.WithContext(ctx)

			sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sr, r)

			rec := Record{
				Timestamp:  start.UTC(),
				RequestID:  reqID,
				TraceID:    traceID,
				RemoteIP:   remoteIP,
				Method:     r.Method,
				Path:       r.URL.Path, // 不含 query string
				Status:     sr.status,
				DurationMs: int(time.Since(start) / time.Millisecond),
				UserAgent:  r.Header.Get("User-Agent"),
			}
			if identity != nil {
				rec.PATID, rec.TokenID = identity(r.Header.Get("Authorization"))
			}
			logger.Log(rec)
		})
	}
}

// NewRequestID 返回 16 字节 crypto/rand hex 字符串（32 个 hex 字符）。
// 供无 HTTP 上下文的调用点生成与 Middleware 格式一致的 request_id。
func NewRequestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
