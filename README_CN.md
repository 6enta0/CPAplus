# CPAplus

[English](README_EN.md) | 中文 | [日本語](README_JA.md)

![CPAplus 管理界面视图](static/x5table-view.jpg)

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
- SQLite 使用统计存储新增 schema 版本管理。版本不匹配时会删除并重建使用量表，现有历史记录将丢失；跨不兼容版本升级前请先导出统计或备份 `usage.db`

### 3. Codex 额度管理与凭证控制

**痛点**：原项目不方便集中查看 Codex 账户额度使用情况。

**变更**：
- 新增 `internal/codex/quota.go` — OAuth token 刷新（复用 `internal/auth/codex` 包）、通过 OpenAI usage API 查询额度、运行时额度恢复状态管理、额度数据持久化到 auth file
- 新增管理 API 端点：
  - `POST /v0/management/auth-files/quota-check` — 批量额度查询；按需或显式刷新 token，并持久化额度字段，但不会修改 auth file 的启用状态
  - `POST /v0/management/auth-files/refresh-token` — 批量 token 刷新
- 额度字段写入 auth JSON 文件：`quota_plan_type`、`quota_windows`（含 usedPercent、resetAtIso）、`quota_checked_at`、`quota_error`
- 手动额度查询不会自动停用或启用 auth file；只有独立的运行时额度恢复流程可能管理凭证状态
- 前端：每个 auth file 卡片显示 plan type 徽章、用量进度条和重置倒计时
- 页面加载时从磁盘读取额度字段（无需手动查询即可显示）

### 4. 模型定价与花费追踪

**痛点**：无法追踪每次 API 调用的花费。用户需要手动查找模型价格并自行计算费用。

**变更**：
- 新增 `internal/pricing/` 包 — SQLite 使用量持久化初始化成功后，启动时尝试从 [LiteLLM](https://github.com/BerriAI/litellm) 同步模型价格；首次同步成功后每 72 小时继续同步（定价方案参考 [agent-usage](https://github.com/briqt/agent-usage)）。首次同步失败不会阻止服务启动，但本次进程不会初始化定价管理或启动周期同步
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
- SQLite 使用统计存储 schema 版本追踪；版本不匹配时会重建表并清除现有历史记录，升级前请先备份或导出
- 前端：使用统计页面布局调整（请求事件明细移至图表上方）
- 前端：控制面板布局优化（显示选项横向排列、响应式宽度）

## 快速开始

### 部署前通用配置

无论使用哪种部署方式，都需要根据实际情况检查以下配置：

- `api-keys`：客户端访问 CPAplus 使用的 API 密钥
- `gemini-api-key`、`claude-api-key`、`codex-api-key`、`openai-compatibility`、`vertex-api-key`：对应的上游 provider 配置
- `auth-dir`：Gemini、Claude、Codex 等 OAuth 凭证文件目录；保留凭证中已有的 `refresh_token`
- `remote-management.secret-key`：管理面板和管理 API 的登录密钥
- `proxy-url`：仅在服务确实需要通过代理访问上游时设置

> **安全提示：** 默认 `host: ""` 会监听所有网络接口。若服务只供本机使用，建议设置 `host: "127.0.0.1"` 并保持 `remote-management.allow-remote: false`。需要远程访问时，请使用强管理密钥，并通过防火墙或反向代理限制管理端口的访问范围。

### 方式一：Docker 部署（无需 Clone）

最简单的方式——无需安装 Go 或 Node.js。
该方式使用预构建镜像 `ghcr.io/6enta0/cpaplus:latest`。

```bash
# 1. 创建工作目录
mkdir cpa-plus && cd cpa-plus

# 2. 下载配置模板和 docker-compose 文件
curl -O https://raw.githubusercontent.com/6enta0/CPAplus/main/config.example.yaml
curl -O https://raw.githubusercontent.com/6enta0/CPAplus/main/docker-compose.yml
mkdir config && mv config.example.yaml config/config.yaml
```

然后按照上述通用配置说明编辑 `config/config.yaml`。容器使用 `network_mode: host`；只有容器确实需要出站代理时才设置 `proxy-url`，此时可以用 `http://127.0.0.1:7890` 指向宿主机本地代理。

下面这些容器路径请按原样保留；以下监听和管理设置是仅本机访问的安全默认值：

```yaml
host: "127.0.0.1"

remote-management:
  allow-remote: false
  disable-auto-update-panel: true
  secret-key: "your-management-key"

auth-dir: "/cpa-plus/auths"
usage-db-path: "/cpa-plus/data/usage.db"
logging-to-file: true
```

`logging-to-file: true` 是可选项。启用后，日志会通过 `docker-compose.yml` 中的 `WRITABLE_PATH=/cpa-plus` 持久化到宿主机的 `./logs` 目录。

请将 `remote-management.secret-key` 设置为你登录 `management.html` 时使用的密钥。只有确实需要远程访问时才修改 `host` 和 `allow-remote`；同时使用强密钥、TLS 反向代理和防火墙或网络访问控制。

```bash
# 3. 创建必要目录并启动
mkdir -p auths logs data

# 4. 将凭证文件复制到宿主机 auths 目录
# ./auths 会挂载到容器内的 /cpa-plus/auths

# 5. 启动服务
docker compose up -d

# 6. 打开管理面板
# http://localhost:8317/management.html
```

启动服务前，请将你的凭证文件复制到宿主机的 `cpa-plus/auths/` 目录中。这个目录会挂载到容器内的 `/cpa-plus/auths`，放进去的文件会被自动识别。如果某个凭证文件里已经带有 `refresh_token`，请保留该字段并留心不要误覆盖，否则自动刷新可能失效。

如果 `docker compose up -d` 拉取 `ghcr.io/6enta0/cpaplus:latest` 时提示 `unauthorized`，说明 GHCR Package 可能仍是 private，需要在 GitHub Package 页面将其可见性改为 public。

后续如果想把现有 Docker 部署更新到最新镜像，可以这样做：

```bash
# 进入你当前的 cpa-plus 目录
docker compose pull
docker compose up -d

# 可选：清理旧的未使用镜像
docker image prune -f
```

`docker compose pull` 会下载最新的 `ghcr.io/6enta0/cpaplus:latest` 镜像，`docker compose up -d` 会用新镜像重建容器，同时保留已挂载的 `config/`、`auths/`、`logs/` 和 `data/` 数据。

> **迁移提示：** `docker-compose.yml` 现在挂载的是 `config/` 目录而非单个 `config.yaml` 文件，这样编辑器保存和管理面板写入都能可靠触发配置热重载（单文件 bind mount 会把容器钉死在一个 inode 上，导致热重载失效）。如果你是在此改动之前部署的，请将文件移入目录一次：`mkdir -p config && mv config.yaml config/config.yaml`，然后执行 `docker compose up -d`。

### 方式二：Go 直接运行（Clone 后运行）

适合已安装 Go 1.26 或更高版本的用户。

按照上述通用配置说明准备 `config.yaml`。只有本地进程确实需要出站代理时才设置 `proxy-url`。

如果你是在仓库根目录执行 `go run ./cmd/server --config config.yaml`，则可以保持 `config.example.yaml` 里的本地路径默认值不变：

```yaml
auth-dir: "./auths"
usage-db-path: "./data/usage.db"
```

如果使用 auth 文件，请把凭证放到仓库根目录的 `auths/` 下，并保留其中已有的 `refresh_token` 字段。

```bash
# 1. Clone 仓库
git clone https://github.com/6enta0/CPAplus.git
cd CPAplus

# 2. 复制并编辑配置
cp config.example.yaml config.yaml
# 编辑 config.yaml — 填入 api-keys、openai-compatibility 等

# 3. 运行
go run ./cmd/server --config config.yaml

# 4. 打开管理面板
# http://localhost:8317/management.html
```

如果你只是运行服务，仓库内自带的 `static/management.html` 已经够用。

如果你修改了管理前端，需要在 CPAplus 的同级目录克隆并构建独立的前端仓库，然后把生成结果拷回：

```bash
# 当前位于 CPAplus 仓库根目录
cd ..
git clone https://github.com/router-for-me/Cli-Proxy-API-Management-Center.git
cd Cli-Proxy-API-Management-Center
npm ci
npm run build

# 将生成结果拷回同级的 CPAplus 仓库
cp dist/index.html ../CPAplus/static/management.html
```

替换 `static/management.html` 后，浏览器强制刷新即可。此类纯前端改动不需要重启 Go 服务。

### 方式三：从源码构建 Docker 镜像

适合想自定义并构建自己镜像的开发者。

这种方式运行时仍然使用与方式一相同的 `docker-compose.yml` 挂载布局和通用配置。容器使用 `network_mode: host`；需要出站代理时，可以用 `http://127.0.0.1:7890` 指向宿主机本地代理。

下面这些容器路径请按原样保留；以下监听和管理设置是仅本机访问的安全默认值：

```yaml
host: "127.0.0.1"

remote-management:
  allow-remote: false
  disable-auto-update-panel: true
  secret-key: "your-management-key"

auth-dir: "/cpa-plus/auths"
usage-db-path: "/cpa-plus/data/usage.db"
logging-to-file: true
```

请将 `remote-management.secret-key` 设置为你登录 `management.html` 时使用的密钥。只有确实需要远程访问时才修改 `host` 和 `allow-remote`；同时使用强密钥、TLS 反向代理和防火墙或网络访问控制。

凭证文件放在仓库根目录的 `auths/` 下即可，容器内会映射到 `/cpa-plus/auths`。如果文件里已有 `refresh_token`，请保留不要覆盖。

```bash
# 1. Clone 仓库
git clone https://github.com/6enta0/CPAplus.git
cd CPAplus

# 2. 复制并编辑配置
mkdir config && cp config.example.yaml config/config.yaml

# 3. 构建并启动
./docker-build.sh   # 选择 2

# 4. 打开管理面板
# http://localhost:8317/management.html
```

### 调用 API

使用 `config.yaml` 中 `api-keys` 配置的一把客户端密钥：

```bash
export CPA_API_KEY="replace-with-one-of-your-api-keys"

curl -sS http://127.0.0.1:8317/v1/models \
  -H "Authorization: Bearer ${CPA_API_KEY}"
```

从 `/v1/models` 返回结果中选择一个模型：

```bash
curl -sS http://127.0.0.1:8317/v1/chat/completions \
  -H "Authorization: Bearer ${CPA_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "replace-with-a-model-from-v1-models",
    "messages": [
      {"role": "user", "content": "Hello"}
    ]
  }'
```

### 配置说明

完整配置参考 [config.example.yaml](config.example.yaml)。关键配置项：

| 配置项 | 说明 |
|--------|------|
| `api-keys` | 客户端访问代理的 API 密钥 |
| `gemini-api-key` | Gemini API Key 凭证与模型配置 |
| `claude-api-key` | Claude API Key 凭证与模型配置 |
| `codex-api-key` | Codex API Key 凭证与模型配置 |
| `openai-compatibility` | 上游 provider 配置，包括 `name`、`base-url`、可选的 `prefix`/`priority`/`disabled`/`headers`、`api-key-entries` 和 `models` |
| `vertex-api-key` | Vertex AI 凭证与模型配置 |
| `auth-dir` | OAuth 凭证文件目录，包括 Gemini、Claude、Codex 等认证文件 |
| `usage-statistics-enabled` | 启用使用量追踪和费用计算 |
| `usage-db-path` | SQLite 数据库路径，持久化使用数据（默认：`usage.db`） |
| `disable-image-generation` | 控制图片生成端点与非图片请求中的 image tool 注入；compact 请求不会自动注入 image tool |
| `proxy-url` | 全局上游代理；仅在确实需要时配置 |
| `remote-management` | 管理面板访问配置（secret-key 用于认证） |

模板中的部分值是有意选择的，不等同于配置项省略时的运行时默认值：

| 配置项 | `config.example.yaml` | 省略时 |
|--------|-----------------------|--------|
| `usage-statistics-enabled` | `true` | `false` |
| `routing.strategy` | `fill-first` | `round-robin` |
| `force-model-prefix` | `true` | `false` |

管理接口与客户端 API 使用不同的密钥。只有配置了 `remote-management.secret-key`、环境变量 `MANAGEMENT_PASSWORD` 或运行时本地/TUI 密码时，`/v0/management/*` 路由才会注册。YAML 中的明文 `remote-management.secret-key` 会在加载时转换为 bcrypt 哈希，并尝试写回 `config.yaml`；配置文件可写时可持久化该哈希，避免后续启动重复处理明文。非空的 `MANAGEMENT_PASSWORD` 会允许远程管理访问，即使 YAML 中设置了 `allow-remote: false`；仅本机部署时不要无意设置该环境变量。

## 社区

欢迎到 [LINUX.DO](https://linux.do/) 社区一起玩 AI！

## 许可证

与上游 CLIProxyAPI 项目相同。
