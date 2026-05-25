// Package audit 提供 mcp-gateway 系列网关共用的本地审计日志 IO 层。
//
// 设计取舍（CLAUDE.md 第 2 / 8.2 / 8.4 章）：
//   - 只抽 IO 层：把任意可 JSON marshal 的值写成一行 JSON（追加 + fsync + 并发安全）。
//     consumer 各自定义 Event struct 与 Logger interface，Timestamp 自动填充和
//     ctx 透传等 ~30 行胶水留在 consumer wrapper，避免在 core 里固化业务字段。
//   - fail-closed 语义：write / fsync 失败时返回 wrapped ErrUnavailable，
//     调用方（wrapper）应据此拒绝当次业务请求（PRD 10.5 "宁可不可用，不可丢审计"）。
//   - 不做行缓冲：每条都直接 file.Write + Sync，保证客户端可见的成功响应
//     ⇔ 审计已落盘。任何"批量 flush"优化都会破坏该不变式。
//   - marshal 在锁外执行，缩小临界区；write+sync 在锁内串行，保证行边界完整。
//
// 使用示例（consumer wrapper）：
//
//	type Event struct {
//	    Timestamp time.Time `json:"ts"`
//	    RequestID string    `json:"request_id"`
//	    // ... 各 consumer 的业务字段
//	}
//
//	type FileLogger struct{ w *audit.Writer }
//
//	func (l *FileLogger) Log(ctx context.Context, e Event) error {
//	    if e.Timestamp.IsZero() {
//	        e.Timestamp = time.Now().UTC()
//	    }
//	    return l.w.Write(e)
//	}
//
//	func (l *FileLogger) Close() error { return l.w.Close() }
package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrClosed 表示 Writer 已经 Close，不可再写入。
var ErrClosed = errors.New("audit: writer closed")

// ErrUnavailable 表示底层文件写入或 fsync 失败。
// 调用方应据此 fail-closed：拒绝当次业务请求且不向客户端返回数据
// （PRD 10.5 + CLAUDE.md 第 8.2 章）。可用 errors.Is(err, ErrUnavailable) 判定。
var ErrUnavailable = errors.New("audit: backend unavailable")

// Writer 把任意可 JSON marshal 的值以 JSON-line 形式追加写入本地文件，
// 每条 write 后立即 fsync。并发安全（mu 保护 file + closed）。
//
// 字段不对外暴露：consumer 通过 NewWriter / Write / Close 三个公开方法使用。
// 同包测试需要构造"已损坏 file"的失败注入场景，可直接字面量赋值。
type Writer struct {
	mu     sync.Mutex
	file   *os.File
	closed bool
}

// NewWriter 打开或创建 path 指向的审计文件。
//
// 行为：
//   - path 为空 → 返回错误（fail-closed：不应该静默退化为 /dev/null）。
//   - 父目录不存在 → 按 0o700 自动 mkdir -p。
//   - 文件以 O_APPEND|O_CREATE|O_WRONLY 0o600 打开（仅 owner 读写）。
//
// 返回的错误以 "audit: " 为前缀并 wrap 底层 IO error，便于上层定位。
func NewWriter(path string) (*Writer, error) {
	if path == "" {
		return nil, errors.New("audit: path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("audit: create parent dir %q: %w", dir, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("audit: open %q: %w", path, err)
	}
	return &Writer{file: f}, nil
}

// Write 把 v marshal 成一行 JSON 追加写入文件，并 fsync 落盘。并发安全。
//
// 错误分类（按是否 IO 故障决定 fail-closed 行为）：
//   - v 不可 marshal（如含 chan / func）→ 返回带 "marshal" 的普通错误，
//     不应被分类为 ErrUnavailable（这是输入错误，不是后端不可用）。
//   - Writer 已 Close → 返回 ErrClosed（errors.Is 可判定）。
//   - file.Write 或 file.Sync 失败 → 返回 wrapped ErrUnavailable。
//
// marshal 在锁外执行以缩小临界区；write+sync 必须串行以保证行边界完整。
func (w *Writer) Write(v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("audit: marshal: %w", err)
	}
	line := append(buf, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return ErrClosed
	}
	if _, err := w.file.Write(line); err != nil {
		return fmt.Errorf("%w: write: %v", ErrUnavailable, err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("%w: fsync: %v", ErrUnavailable, err)
	}
	return nil
}

// Close 关闭底层文件。重复调用安全：仅第一次会真正关闭并返回底层错误，
// 后续调用直接返回 nil。Close 后再调用 Write 将返回 ErrClosed。
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("audit: close: %w", err)
	}
	return nil
}
