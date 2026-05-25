# mcp-gateway-core

Shared framework extracted from `db-mcp-gateway` / `logs-mcp-gateway` / `monitor-mcp-gateway`.

## 设计原则

- **领域无关**：core 只包含三库高度同构的通用框架（日志、审计 IO、HTTP 中间件、熔断器、配置助手），不含任何业务字段（resource schema / tool dispatch / parser 留在各库）
- **依赖单向**：core 不反向依赖任何 consumer 仓库；consumer 通过 import 单向消费 core
- **零外部依赖**：除 `gopkg.in/yaml.v3` 与 `prometheus/client_golang` 等三库共用的成熟生态库外，core 不引新第三方
- **向后兼容**：`v1.x.y` 内只能加字段不能删/改语义；`v0.x.y` 允许 break，需在 CHANGELOG 注明

## 抽取路线（与 consumer 仓库协同 tag）

| Phase | core 内容 | 版本 |
|---|---|---|
| 0 | 骨架（本 commit） | v0.0.1 |
| 1 | `logging`：slog JSON handler + RequestAttrs helper（函数注入 getter） | v0.1.0 |
| 2 | `httpaccess`：Middleware + IPExtractor + ctx 三件套 + traceparent 解析 | v0.2.0 |
| 3 | `audit`：JSON-line + fsync + fail-closed Writer（Event struct 留 consumer） | v0.3.0 |
| 4 | `breaker`：5 失败 / 30s OPEN / HALF_OPEN 探测（仅 logs / monitor 用） | v0.4.0 |
| 5 | `config` 助手：envExpand / PAT 校验 / PATPrefix 常量 | v0.5.0 |

## Consumer 仓库

- [db-mcp-gateway](https://github.com/mugiwaraio/db-mcp-gateway)
- [logs-mcp-gateway](https://github.com/mugiwaraio/logs-mcp-gateway)
- [monitor-mcp-gateway](https://github.com/mugiwaraio/monitor-mcp-gateway)

## 引用方式

```bash
go get github.com/mugiwaraio/mcp-gateway-core@v0.2.0
```

`go.mod` 自动追加 require 行。

## 开发

```bash
go test ./... -race -count=1
go vet ./...
```

发版：

```bash
git tag v0.x.y
git push --tags
```

Consumer 通过 `go get @v0.x.y` 锁定具体版本。

## License

Internal use.
