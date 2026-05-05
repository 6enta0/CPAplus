# CPAplus

[English](README.md) | 中文 | [日本語](README_JA.md)

为 CLI 提供 OpenAI/Gemini/Claude/Codex 兼容 API 接口的代理服务器。

修改自：
- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — 核心代理服务器
- [Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) — 管理前端

## 变更内容

### 1. 使用统计恢复 + SQLite 持久化

**痛点**：上游项目移除了使用统计追踪功能。用户无法了解请求量、Token 消耗、延迟和错误率等关键指标。

**变更**：
- 恢复 `internal/usage/` 中的 `LoggerPlugin` + `RequestStatistics`，注册到 SDK usage 分发管道
- 新增 `SQLitePlugin`，将每条请求记录持久化到 SQLite 数据库（`usage.db`）
- 启动时 `LoadAll()` 从 SQLite 历史记录重建内存统计，重启后数据不丢失
- 新增管理 API 端点：
  - `GET /v0/management/usage-statistics` — 返回统计快照（兼容旧版格式）
  - `GET /v0/management/usage-statistics/export` — 导出完整快照
  - `PUT /v0/management/usage-statistics/import` — 合并导入（去重）
- 新增 `usage-db-path` 配置项（默认在配置文件旁生成 `usage.db`）
- 前端：使用统计页面，包含概览卡片、RPM/TPM 图表、按小时/天柱状图、API 明细、Token 分解和延迟统计

### 2. Auth Index 前缀区分

**痛点**：当多个 OpenAI 兼容条目共享同一 API Key 但使用不同的 `name` + `prefix` 组合时（例如同一上游 Key 通过不同前缀路由到不同模型组），它们会产生相同的 `auth_index`。这导致管理前端将所有请求显示在同一个 provider 名称下，无法区分请求属于哪个前缀/模型组。

**变更**：
- 在配置合成器中为全部 5 个 provider（OpenAI compat、Gemini、Claude、Codex、Vertex）的 `Attributes` map 添加 `prefix`
- 更新 `sdk/cliproxy/auth/types.go` 中的 `indexSeed()`，在哈希计算中加入 `prefix`，使 `auth_index = SHA256(name + prefix + apiKey + ...)` 而非 `SHA256(name + apiKey + ...)`
- 前端 `resolveSourceDisplay` 现在优先通过 `auth_index` 解析来源显示（而非原始 source/API key），确保每个 provider 条目映射到正确的显示名称
- 前端从独立的 `/openai-compatibility` API（包含 `auth-index`）获取数据，而非 `/config`（不包含）
- SQLite 使用统计存储新增 schema 版本管理 — 版本不匹配时自动重建表

### 3. 其他改进

- SQLite 使用统计存储的 schema 版本追踪，防止 schema 变更时数据损坏
- 新增 auth index 前缀区分和文件型 auth 稳定性的单元测试

## 快速开始

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

配置参考见 [config.example.yaml](config.example.yaml)。

## 许可证

与上游 CLIProxyAPI 项目相同。
