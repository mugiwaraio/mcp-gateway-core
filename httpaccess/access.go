// Package httpaccess 提供 HTTP 访问日志的 middleware 与落地能力，是 mcp-gateway
// 系列网关共用的入口层运维观测基线（CLAUDE.md 第 6 / 8.1 章）。
//
// 设计取舍（与 core/logging 一致的演进逻辑）：
//   - 与业务/合规审计（consumer 的 internal/audit）正交：access log 偏运维视角，
//     写失败 fail-open（仅 stderr 上报），不阻断业务请求；audit 偏合规视角，
//     写失败 fail-closed。两者不共用 sink。
//   - 接口照搬 logs-mcp-gateway / monitor-mcp-gateway 已稳定的实装；
//     db-mcp-gateway 老版本存在字段命名 drift（user_id vs pat_id），
//     由 consumer 侧适配，不在 core 重新设计。
//   - IdentityFn 由 consumer 注入：core 不感知 auth 包，避免反向依赖。
//   - ctx 三件套（request_id / trace_id / remote_ip）就近放在本包，
//     与 core/logging.CtxGetters 天然契合 —— consumer 可把本包的 getter
//     直接拼成 corelog.CtxGetters{...}。
//
// 使用示例（consumer 侧）：
//
//	import (
//	    "github.com/mugiwaraio/mcp-gateway-core/httpaccess"
//	    corelog "github.com/mugiwaraio/mcp-gateway-core/logging"
//	)
//
//	ext, _ := httpaccess.NewIPExtractor(cfg.TrustedProxies)
//	lg, _  := httpaccess.NewJSONLogger(cfg.AccessLogPath, cfg.AlsoStderr)
//	identity := func(h string) (string, string) {
//	    pat, _, _ := auth.ParseBearer(h)
//	    if pat == "" { return "", "" }
//	    return pat, auth.TokenIDOf(pat)
//	}
//	srv := httpaccess.Middleware(lg, ext, identity)(mux)
//
//	getters := corelog.CtxGetters{
//	    RequestID: httpaccess.RequestIDFromContext,
//	    TraceID:   httpaccess.TraceIDFromContext,
//	    RemoteIP:  httpaccess.RemoteIPFromContext,
//	}
package httpaccess

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record 是一条 HTTP 访问日志记录。
// JSON 时间字段名为 "ts"，与 consumer 的 audit.Event 一致，方便统一索引。
type Record struct {
	Timestamp  time.Time `json:"ts"`
	RequestID  string    `json:"request_id"`
	TraceID    string    `json:"trace_id,omitempty"`
	RemoteIP   string    `json:"remote_ip"`
	Method     string    `json:"method"`
	Path       string    `json:"path"` // r.URL.Path（不含 query string）
	Status     int       `json:"status"`
	DurationMs int       `json:"duration_ms"`
	PATID      string    `json:"pat_id,omitempty"`
	TokenID    string    `json:"token_id,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
	Error      string    `json:"error,omitempty"`
}

// Logger 抽象 access log 落地后端。
type Logger interface {
	Log(r Record)
	io.Closer
}

// NopLogger 在 access log 被禁用时使用，所有方法空实现。
type NopLogger struct{}

func (NopLogger) Log(Record)   {}
func (NopLogger) Close() error { return nil }

// JSONLogger 串行写入一个 io.Writer（file / stderr / MultiWriter）。
type JSONLogger struct {
	mu     sync.Mutex
	w      io.Writer
	file   *os.File
	closed bool
}

// NewJSONLogger 至少要开 file 或 stderr 一个；都关返回错误。
func NewJSONLogger(filePath string, alsoStderr bool) (*JSONLogger, error) {
	if filePath == "" && !alsoStderr {
		return nil, errors.New("httpaccess: file_path 与 stderr 至少需开一个")
	}
	var f *os.File
	var sinks []io.Writer
	if filePath != "" {
		dir := filepath.Dir(filePath)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("httpaccess: create dir %q: %w", dir, err)
		}
		var err error
		f, err = os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("httpaccess: open %q: %w", filePath, err)
		}
		sinks = append(sinks, f)
	}
	if alsoStderr {
		sinks = append(sinks, os.Stderr)
	}
	var w io.Writer
	if len(sinks) == 1 {
		w = sinks[0]
	} else {
		w = io.MultiWriter(sinks...)
	}
	return &JSONLogger{w: w, file: f}, nil
}

// Log 失败仅打到 stderr，不返回错误也不阻断主流程。
func (l *JSONLogger) Log(r Record) {
	if r.Timestamp.IsZero() {
		r.Timestamp = time.Now().UTC()
	}
	buf, err := json.Marshal(r)
	if err != nil {
		fmt.Fprintf(os.Stderr, "httpaccess: marshal failed: %v\n", err)
		return
	}
	buf = append(buf, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return
	}
	if _, err := l.w.Write(buf); err != nil {
		fmt.Fprintf(os.Stderr, "httpaccess: write failed: %v\n", err)
	}
}

func (l *JSONLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}
