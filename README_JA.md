# CPAplus

[English](README_EN.md) | [中文](README_CN.md) | 日本語

![CPAplus 管理画面ビュー](static/x5table-view.jpg)

CLI向けにOpenAI/Gemini/Claude/Codex互換APIインターフェースを提供するプロキシサーバー。

改変元：
- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — コアプロキシサーバー
- [Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) — 管理Web UI

## 変更内容

### 1. 使用統計の復元 + SQLite永続化

**ペインポイント**：上流プロジェクトが使用統計トラッキング機能を削除したため、ユーザーはリクエスト量、トークン消費、レイテンシ、エラー率などの主要指標を把握できなくなりました。

**変更点**：
- `internal/usage/`の`LoggerPlugin` + `RequestStatistics`を復元し、SDK usage配信パイプラインに登録
- `SQLitePlugin`を追加し、各リクエスト記録をSQLiteデータベース（`usage.db`）に永続化
- 起動時に`LoadAll()`でSQLite履歴からメモリ統計を再構築、再起動後もデータが消失しない
- 管理APIエンドポイントを追加：
  - `GET /v0/management/usage-statistics` — 統計スナップショットを返す（旧フォーマット互換）
  - `GET /v0/management/usage-statistics/export` — フルスナップショットのエクスポート
  - `PUT /v0/management/usage-statistics/import` — 重複排除付きマージインポート
- `usage-db-path`設定オプションを追加（デフォルトは設定ファイル横の`usage.db`）
- フロントエンド：概要カード、RPM/TPMチャート、時間別/日別棒グラフ、API明細、トークン内訳、レイテンシ統計を含む使用統計ページ

### 2. Auth Indexプレフィックス区別

**ペインポイント**：複数のOpenAI互換エントリが同じAPIキーを共有しつつ異なる`name` + `prefix`の組み合わせを使用する場合、同一の`auth_index`が生成されていました。これにより管理UIでは全リクエストが単一のプロバイダー名で表示され、どのプレフィックス/モデルグループに属するかを判別できませんでした。

**変更点**：
- 設定シンセサイザで全5プロバイダーの`Attributes`マップに`prefix`を追加
- `indexSeed()`を更新し、ハッシュ計算に`prefix`を含め、`auth_index = SHA256(name + prefix + apiKey + ...)`
- フロントエンド`resolveSourceDisplay`は`auth_index`を優先ルックアップキーとして使用
- フロントエンドは専用の`/openai-compatibility` APIからデータを取得
- SQLite使用統計ストアにスキーマバージョン管理を追加

### 3. Codexクォータ管理とクレデンシャル制御

**ペインポイント**：元プロジェクトではCodexアカウントのクォータ使用状況を一元的に確認するのが不便でした。

**変更点**：
- `internal/codex/quota.go`を追加 — OAuthトークンリフレッシュ、OpenAI usage APIによるクォータ照会、自動無効化/有効化ロジック、クォータデータのauth fileへの永続化
- 管理APIエンドポイントを追加：
  - `POST /v0/management/auth-files/quota-check` — バッチクォータ確認 + トークンリフレッシュ + 自動無効化/有効化
  - `POST /v0/management/auth-files/refresh-token` — バッチトークンリフレッシュ
- クォータフィールドをauth JSONファイルに書き込み：`quota_plan_type`、`quota_windows`（usedPercent、resetAtIso含む）、`quota_checked_at`、`quota_error`
- 自動無効化：クォータが100%に達した場合、auth fileを自動的に無効化；クォータリセット後に自動的に再有効化
- フロントエンド：各auth fileカードにplan typeバッジ、使用量プログレスバー、リセットカウントダウンを表示
- ページ読み込み時にディスクからクォータフィールドを読み込み（手動確認不要で表示可能）

### 4. モデル価格設定とコスト追跡

**ペインポイント**：各API呼び出しのコストを追跡する方法がありませんでした。ユーザーは手動でモデル価格を調べ、自ら費用を計算する必要がありました。

**変更点**：
- `internal/pricing/`パッケージを追加 — 起動時および72時間ごとに[LitellM](https://github.com/BerriAI/litellm)からモデル価格を同期（価格アプローチは[agent-usage](https://github.com/briqt/agent-usage)を参照）
- カスタム価格（MiMoモデルなど）はAPIで管理され、LiteLLM同期で上書きされない
- 価格検索のためのファジーモデル名マッチング（プレフィックス除去、部分文字列包含）
- `usage_records`テーブルに`cost_usd`列を追加 — 挿入時にinput/output/cacheトークン価格から自動計算
- `CalcCost()`はキャッシュトークンを個別に処理（キャッシュ読み取り価格 vs. 入力価格）
- 旧データのインポート時（`cost_usd`なし）、pricing storeが自動的に価格を補完計算
- 大量データセットのバッチインポート（>1000件は1000件ずつ分割）、単一通知でリアルタイム進捗表示
- 管理APIエンドポイントを追加：
  - `GET /v0/management/pricing` — 全価格（LiteLLM + カスタム）をフロントエンド向け形式で返す
  - `POST /v0/management/pricing/sync` — 手動価格同期トリガー
  - `PUT /v0/management/pricing/custom` — カスタムモデル価格を保存（永続化、LiteLLM同期で上書きされない）
- フロントエンド：価格設定カードがバックエンドAPIでカスタム価格を読み書き（localStorageに依存しない）、auth fileリストビューに「合計コスト」列を追加、使用統計にコストデータを統合

### 5. 認証ファイルリストビューと拡張テーブル

**ペインポイント**：カードのみのビューでは多くのauth fileを管理する際にスケーラビリティが不足していました。クォータステータス、最終呼び出し時刻、コストなどの主要指標を確認するには、各カードを個別にクリックする必要がありました。

**変更点**：
- auth fileのテーブル/リストビューを追加（切り替え可能、デフォルトビュー）
- 列：名前、最終呼び出し、ステータス、成功、失敗、プランタイプ（バッジ）、使用済みクォータ（プログレスバー+%）、合計コスト、アクション、クォータ確認日時、リセットカウントダウン
- 全列でソート可能なヘッダー
- 時間列は日付+相対時間の2行表示
- クォータプログレスバーの色分け：緑（<60%）、オレンジ（60-90%）、赤（≥90%）
- Plan typeバッジ：free（緑）、plus（青）、team（オレンジ）、pro（赤）
- バッチおよび行単位のアクションボタン：クォータ確認、トークンリフレッシュ、有効化/無効化、ダウンロード、削除

### 6. その他の改善

- `last_called_at`をauth indexごとに`usage_records`に永続化、再起動後も維持
- `total_cost_usd`をSQL集計クエリでauth indexごとに集計
- SQLite使用統計ストアのスキーマバージョン追跡により、スキーマ変更時のデータ破損を防止
- フロントエンド：使用統計ページレイアウト調整（リクエストイベント詳細をチャート上部に移動）
- フロントエンド：コントロールパネルレイアウト最適化（表示オプションを1行に配置、レスポンシブ幅）

## クイックスタート

### 共通設定

どのデプロイ方法でも、実際の環境に合わせて次の設定を確認してください。

- `api-keys`：クライアントが CPAplus へアクセスするための API キー
- `gemini-api-key`、`claude-api-key`、`codex-api-key`、`openai-compatibility`、`vertex-api-key`：各上流 provider の設定
- `auth-dir`：Gemini、Claude、Codex などの OAuth 認証情報ファイルを置くディレクトリ。既存の `refresh_token` は保持してください
- `remote-management.secret-key`：管理ダッシュボードと管理 API のログインキー
- `proxy-url`：サービスが上流 provider へ接続する際にプロキシが必要な場合のみ設定

> **セキュリティ:** デフォルトの `host: ""` はすべてのネットワークインターフェースで待ち受けます。ローカル利用のみの場合は `host: "127.0.0.1"` を設定し、`remote-management.allow-remote: false` を維持してください。リモートアクセスを許可する場合は、強力な管理キーを使用し、ファイアウォールまたはリバースプロキシで管理ポートへのアクセスを制限してください。

### 方法1：Dockerデプロイ（Clone不要）

最も簡単な方法—GoやNode.jsのインストール不要。
この方法は事前ビルド済みイメージ `ghcr.io/6enta0/cpaplus:latest` を使用します。

```bash
# 1. 作業ディレクトリを作成
mkdir cpa-plus && cd cpa-plus

# 2. 設定テンプレートとdocker-composeファイルをダウンロード
curl -O https://raw.githubusercontent.com/6enta0/CPAplus/main/config.example.yaml
curl -O https://raw.githubusercontent.com/6enta0/CPAplus/main/docker-compose.yml
mkdir config && mv config.example.yaml config/config.yaml
```

上記の共通設定に従って `config/config.yaml` を編集してください。コンテナは `network_mode: host` を使用します。外向きプロキシが必要な場合のみ `proxy-url` を設定し、ホスト上のローカルプロキシには `http://127.0.0.1:7890` のようなアドレスを使用できます。

次の Docker 用パスとオプションはそのまま維持してください:

```yaml
remote-management:
  allow-remote: true
  disable-auto-update-panel: true
  secret-key: "your-management-key"

auth-dir: "/cpa-plus/auths"
usage-db-path: "/cpa-plus/data/usage.db"
logging-to-file: true
```

`logging-to-file: true` は任意です。有効にすると、`docker-compose.yml` の `WRITABLE_PATH=/cpa-plus` 経由でログがホストの `./logs` ディレクトリへ永続化されます。

`remote-management.secret-key` には、`management.html` へログインするときに使うキーを設定してください。

```bash
# 3. 必要なディレクトリを作成して起動
mkdir -p auths logs data

# 4. 認証情報ファイルをホスト側の auths ディレクトリへコピー
# ./auths はコンテナ内の /cpa-plus/auths にマウントされます

# 5. サービスを起動
docker compose up -d

# 6. 管理ダッシュボードを開く
# http://localhost:8317/management.html
```

サービスを起動する前に、認証情報ファイルをホスト側の `cpa-plus/auths/` ディレクトリへコピーしてください。このディレクトリはコンテナ内の `/cpa-plus/auths` にマウントされるため、置いたファイルは自動的に認識されます。認証情報ファイルに `refresh_token` が含まれている場合は、その項目を保持したままにしてください。誤って上書きすると自動リフレッシュが動かなくなる可能性があります。

`docker compose up -d` で `ghcr.io/6enta0/cpaplus:latest` の取得時に `unauthorized` が表示される場合、GHCR Package がまだ private の可能性があります。GitHub Package ページで可視性を public に変更してください。

既存の Docker デプロイを後で最新イメージへ更新したい場合は、次を実行してください。

```bash
# 現在の cpa-plus ディレクトリで実行
docker compose pull
docker compose up -d

# 任意: 未使用の古いイメージを削除
docker image prune -f
```

`docker compose pull` は最新の `ghcr.io/6enta0/cpaplus:latest` イメージを取得し、`docker compose up -d` はそのイメージでコンテナを再作成します。マウント済みの `config/`、`auths/`、`logs/`、`data/` は保持されます。

> **移行に関する注意:** `docker-compose.yml` は単一の `config.yaml` ファイルではなく `config/` ディレクトリをマウントするようになりました。これにより、エディタでの保存や管理パネルからの書き込みが設定のホットリロードを確実にトリガーします（単一ファイルの bind mount は 1 つの inode に固定され、ホットリロードが壊れます）。この変更より前にデプロイした場合は、一度だけファイルをディレクトリへ移動してください: `mkdir -p config && mv config.yaml config/config.yaml`、その後 `docker compose up -d` を実行します。

### 方法2：Goで直接実行（Cloneして実行）

Goがインストール済みのユーザー向け。

上記の共通設定に従って `config.yaml` を準備してください。ローカルプロセスが外向きプロキシを必要とする場合のみ `proxy-url` を設定します。

リポジトリのルートで `go run ./cmd/server --config config.yaml` を実行する場合は、`config.example.yaml` のローカルパス既定値をそのまま使えます:

```yaml
auth-dir: "./auths"
usage-db-path: "./data/usage.db"
```

auth ファイルを使う場合は、認証情報をリポジトリの `auths/` ディレクトリへ配置し、既存の `refresh_token` フィールドは保持してください。

```bash
# 1. リポジトリをClone
git clone https://github.com/6enta0/CPAplus.git
cd CPAplus

# 2. 設定をコピーして編集
cp config.example.yaml config.yaml
# config.yamlを編集 — api-keys、openai-compatibilityなどを入力

# 3. 実行
go run ./cmd/server --config config.yaml

# 4. 管理ダッシュボードを開く
# http://localhost:8317/management.html
```

サーバーを動かすだけなら、同梱の `static/management.html` で十分です。

管理フロントエンドを変更した場合は、別リポジトリのフロントエンドを再ビルドし、生成物を CPAplus にコピーし直してください。

```bash
# 別リポジトリで管理フロントエンドをビルド
cd ~/projects/github_repos/Cli-Proxy-API-Management-Center
npm run build

# 生成物を CPAplus にコピー
cp dist/index.html ~/projects/github_repos/CPAplus/static/management.html
```

`static/management.html` を置き換えた後は、ブラウザをハードリロードしてください。この種のフロントエンドのみの変更では Go サーバーの再起動は不要です。

### 方法3：ソースからDockerイメージをビルド

カスタマイズして独自イメージをビルドしたい開発者向け。

この方法も、方法1と同じ `docker-compose.yml` のマウント構成と共通設定を使用します。コンテナは `network_mode: host` で動作し、外向きプロキシが必要な場合は `http://127.0.0.1:7890` のようなアドレスでホスト上のプロキシを指定できます。

この Docker ベースの方法では、次のコンテナパスとオプションをそのまま維持してください:

```yaml
remote-management:
  allow-remote: true
  disable-auto-update-panel: true
  secret-key: "your-management-key"

auth-dir: "/cpa-plus/auths"
usage-db-path: "/cpa-plus/data/usage.db"
logging-to-file: true
```

`remote-management.secret-key` には、`management.html` へログインするときに使うキーを設定してください。

認証情報ファイルはリポジトリの `auths/` ディレクトリへ置いてください。コンテナ内では `/cpa-plus/auths` にマウントされます。既存の `refresh_token` フィールドは上書きしないでください。

```bash
# 1. リポジトリをClone
git clone https://github.com/6enta0/CPAplus.git
cd CPAplus

# 2. 設定をコピーして編集
mkdir config && cp config.example.yaml config/config.yaml

# 3. ビルドして起動
./docker-build.sh   # 2を選択

# 4. 管理ダッシュボードを開く
# http://localhost:8317/management.html
```

### 設定

完全な設定リファレンスは[config.example.yaml](config.example.yaml)を参照してください。主な設定項目：

| 設定項目 | 説明 |
|----------|------|
| `api-keys` | プロキシにアクセスするためのクライアントAPIキー |
| `gemini-api-key` | Gemini API Key 認証情報とモデル設定 |
| `claude-api-key` | Claude API Key 認証情報とモデル設定 |
| `codex-api-key` | Codex API Key 認証情報とモデル設定 |
| `openai-compatibility` | 上流 provider 設定（name、base-url、prefix、api-key、models） |
| `vertex-api-key` | Vertex AI 認証情報とモデル設定 |
| `auth-dir` | Gemini、Claude、Codex などの OAuth 認証情報ファイルを置くディレクトリ |
| `usage-statistics-enabled` | 使用量トラッキングとコスト計算を有効化 |
| `usage-db-path` | SQLiteデータベースパス、使用データを永続化（デフォルト：`usage.db`） |
| `disable-image-generation` | 画像生成エンドポイントと非画像リクエストへの image tool 注入を制御。compact リクエストには image tool を自動注入しません |
| `proxy-url` | 上流 provider 向けのグローバルプロキシ。必要な場合のみ設定 |
| `remote-management` | 管理ダッシュボードアクセス設定（secret-keyで認証） |

## コミュニティ

[LINUX.DO](https://linux.do/) コミュニティで一緒に AI を楽しみましょう！

## ライセンス

上流CLIProxyAPIプロジェクトと同一。
