// Package logging 提供 mcp-gateway 系列网关共用的结构化日志能力。
//
// 设计取舍（CLAUDE.md 第 6 / 8.1 章）：
//   - JSON 输出，便于运维统一索引（who/what/why 字段化）
//   - 通过 CtxGetters 函数注入解耦具体 httpaccess 实现，避免本包反向依赖
//     consumer 的 internal/httpaccess（在 Phase 2 抽取 httpaccess 后，consumer
//     可以传入 core/httpaccess 的同名 getter，无需再写 shim）
//   - 空字段自动 drop，保持 JSON 紧凑
//
// 使用示例（consumer 侧）：
//
//	import (
//	    corelog "github.com/mugiwaraio/mcp-gateway-core/logging"
//	    "github.com/mugiwaraio/db-mcp-gateway/internal/httpaccess"
//	)
//
//	var getters = corelog.CtxGetters{
//	    RequestID: httpaccess.RequestIDFromContext,
//	    TraceID:   httpaccess.TraceIDFromContext,
//	    RemoteIP:  httpaccess.RemoteIPFromContext,
//	}
//
//	slog.SetDefault(corelog.New(os.Stderr))
//	slog.Warn("invalid PAT",
//	    append(corelog.RequestAttrs(getters, ctx, "", ""),
//	        "status", 401, "reason", "invalid_pat")...)
package logging

import (
	"context"
	"io"
	"log/slog"
	"os"
)

// CtxGetter 从 ctx 读一个字符串字段；ok=false 表示未注入（应被 drop）。
type CtxGetter func(ctx context.Context) (string, bool)

// CtxGetters 把 request 相关的三个标准 getter 打包，便于一次注入到多次调用。
// 任一字段为 nil 视为"该 key 不参与日志"，安全跳过。
type CtxGetters struct {
	RequestID CtxGetter
	TraceID   CtxGetter
	RemoteIP  CtxGetter
}

// New 构造一个 JSON slog.Logger，写到 w。
//
// 级别固定 Info；WARN/ERROR/INFO 由调用方按方法名区分。后续按需再加 Level 字段
// 参数化，避免 V0.x 阶段配置项膨胀。
func New(w io.Writer) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(h)
}

// NewStderr 是默认 sink 便捷构造：写 os.Stderr。
func NewStderr() *slog.Logger {
	return New(os.Stderr)
}

// RequestAttrs 返回请求级日志的通用结构化字段（slog ...any splat 形式）。
//
// 字段集合（CLAUDE.md 第 6 章 + 三库统一日志规格）：
//   - request_id：经 g.RequestID(ctx) 拿到；统一与 access log / audit 对齐
//   - trace_id：经 g.TraceID(ctx) 拿到；W3C Trace Context，跨服务关联
//   - remote_ip：经 g.RemoteIP(ctx) 拿到；含 XFF 解析的真实客户端 IP
//   - user_id：caller 直接传（如 PAT 人类标识 "alice"），未鉴权时传空
//   - token_id：caller 直接传（sha256 派生 hex，不入明文）
//
// getter 为 nil 或返回 ok=false 时该字段自动 drop；userID / tokenID 为空字符串
// 时也 drop。caller 再 append 调用点专属字段后整体 splat 进 slog：
//
//	slog.Warn("msg", append(RequestAttrs(g, ctx, "alice", "abc"),
//	    "status", 401, "reason", "invalid_pat")...)
func RequestAttrs(g CtxGetters, ctx context.Context, userID, tokenID string) []any {
	var attrs []any
	if g.RequestID != nil {
		if v, ok := g.RequestID(ctx); ok && v != "" {
			attrs = append(attrs, "request_id", v)
		}
	}
	if g.TraceID != nil {
		if v, ok := g.TraceID(ctx); ok && v != "" {
			attrs = append(attrs, "trace_id", v)
		}
	}
	if g.RemoteIP != nil {
		if v, ok := g.RemoteIP(ctx); ok && v != "" {
			attrs = append(attrs, "remote_ip", v)
		}
	}
	if userID != "" {
		attrs = append(attrs, "user_id", userID)
	}
	if tokenID != "" {
		attrs = append(attrs, "token_id", tokenID)
	}
	return attrs
}
