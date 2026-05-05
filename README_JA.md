# CPAplus

[English](README.md) | [中文](README_CN.md) | 日本語

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

**ペインポイント**：複数のOpenAI互換エントリが同じAPIキーを共有しつつ異なる`name` + `prefix`の組み合わせを使用する場合（例：同じ上流キーを異なるプレフィックスで異なるモデルグループにルーティング）、同一の`auth_index`が生成されていました。これにより管理UIでは全リクエストが単一のプロバイダー名で表示され、どのプレフィックス/モデルグループに属するかを判別できませんでした。

**変更点**：
- 設定シンセサイザで全5プロバイダー（OpenAI compat、Gemini、Claude、Codex、Vertex）の`Attributes`マップに`prefix`を追加
- `sdk/cliproxy/auth/types.go`の`indexSeed()`を更新し、ハッシュ計算に`prefix`を含め、`auth_index = SHA256(name + prefix + apiKey + ...)`ではなく`SHA256(name + apiKey + ...)`を生成
- フロントエンド`resolveSourceDisplay`は、生のsource/APIキーではなく`auth_index`を優先ルックアップキーとしてソース表示を解決し、各プロバイダーエントリが正しい表示名にマッピングされることを保証
- フロントエンドは`/config`（auth-indexなし）ではなく、専用の`/openai-compatibility` API（auth-index含む）からデータを取得
- SQLite使用統計ストアにスキーマバージョン管理を追加 — バージョン不一致時にテーブルを自動再構築

### 3. その他の改善

- SQLite使用統計ストアのスキーマバージョン追跡により、スキーマ変更時のデータ破損を防止
- auth indexプレフィックス区別とファイルベースauth安定性のユニットテストを追加

## クイックスタート

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

設定リファレンスは[config.example.yaml](config.example.yaml)を参照してください。

## ライセンス

上流CLIProxyAPIプロジェクトと同一。
