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

### 3. Codex 额度管理与凭证控制

**痛点**：原项目无法查看 Codex 账户额度使用情况。用户需要额外运行 Python 服务来查询额度和刷新凭证，操作繁琐且需要同时维护两个进程。

**变更**：
- 新增 `internal/codex/quota.go` — OAuth token 刷新（复用 `internal/auth/codex` 包）、通过 OpenAI usage API 查询额度、自动停用/启用逻辑、额度数据持久化到 auth file
- 新增管理 API 端点：
  - `POST /v0/management/auth-files/quota-check` — 批量额度查询 + token 刷新 + 自动停用/启用
  - `POST /v0/management/auth-files/refresh-token` — 批量 token 刷新
- 额度字段写入 auth JSON 文件：`quota_plan_type`、`quota_windows`（含 usedPercent、resetAtIso）、`quota_checked_at`、`quota_error`
- 自动停用：额度达 100% 时自动停用 auth file；额度重置后自动重新启用
- 前端：每个 auth file 卡片显示 plan type 徽章、用量进度条和重置倒计时
- 页面加载时从磁盘读取额度字段（无需手动查询即可显示）

### 4. 模型定价与花费追踪

**痛点**：无法追踪每次 API 调用的花费。用户需要手动查找模型价格并自行计算费用。

**变更**：
- 新增 `internal/pricing/` 包 — 启动时及每 72 小时从 [LiteLLM](https://github.com/BerriAI/litellm) 同步模型价格（定价方案参考 [agent-usage](https://github.com/briqt/agent-usage)）
- 自定义价格（如 MiMo 模型）通过 API 管理，不会被 LiteLLM 同步覆盖
- 模糊模型名匹配（前缀剥离、子串包含）用于价格查找
- `usage_records` 表新增 `cost_usd` 列 — 插入时根据 input/output/cache token 价格自动计算
- `CalcCost()` 分别处理缓存 token（缓存读取价格 vs. 输入价格）
- 导入旧版数据（无 `cost_usd`）时，pricing store 自动补算价格
- 大数据集分批导入（>1000 条按每 1000 条拆分），单条通知实时更新进度
- 新增管理 API 端点：
  - `GET /v0/management/pricing` — 返回所有价格（LiteLLM + 自定义），前端友好格式
  - `POST /v0/management/pricing/sync` — 手动触发价格同步
  - `PUT /v0/management/pricing/custom` — 保存自定义模型价格（持久化，不被 LiteLLM 同步覆盖）
- 前端：价格设置卡片通过后端 API 读写自定义价格（不再依赖 localStorage），auth file 列表视图新增"总消费"列，使用统计集成花费数据

### 5. 认证文件列表视图与增强表格

**痛点**：纯卡片视图在管理大量 auth file 时不够高效。额度状态、上次调用时间、花费等关键指标需要逐一点开卡片才能查看。

**变更**：
- 新增表格/列表视图（可切换，默认视图）
- 列：名称、上次调用、状态、成功、失败、类型（徽章）、已用额度（进度条+%）、总消费、操作、额度检查于、重置倒计时
- 所有列支持排序
- 时间列显示日期 + 相对时间（两行显示）
- 额度进度条颜色编码：绿色（<60%）、橙色（60-90%）、红色（≥90%）
- Plan type 徽章：free（绿色）、plus（蓝色）、team（橙色）、pro（红色）
- 批量和逐行操作按钮：额度查询、刷新凭证、启用/停用、下载、删除

### 6. 其他改进

- `last_called_at` 按 auth index 持久化到 `usage_records`，重启后数据不丢失
- `total_cost_usd` 通过 SQL 聚合查询按 auth index 统计
- SQLite 使用统计存储 schema 版本追踪，防止 schema 变更时数据损坏
- 前端：使用统计页面布局调整（请求事件明细移至图表上方）
- 前端：控制面板布局优化（显示选项横向排列、响应式宽度）

## 快速开始

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

配置参考见 [config.example.yaml](config.example.yaml)。

## 许可证

与上游 CLIProxyAPI 项目相同。
