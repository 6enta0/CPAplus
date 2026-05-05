# CPAplus

A proxy server that provides OpenAI/Gemini/Claude/Codex compatible API interfaces for CLI.

Modified from:
- [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — the core proxy server
- [Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) — the management web UI

## What's Changed

### 1. Usage Statistics Restoration with SQLite Persistence

**Pain point**: The upstream project removed usage statistics tracking. Users had no visibility into request volumes, token consumption, latency, or error rates across their API keys and models.

**Changes**:
- Restored `LoggerPlugin` + `RequestStatistics` in `internal/usage/`, registered into the SDK usage distribution pipeline
- Added `SQLitePlugin` that persists every request record to a SQLite database (`usage.db`)
- On startup, `LoadAll()` rebuilds in-memory statistics from historical SQLite records, so stats survive restarts
- Added management API endpoints:
  - `GET /v0/management/usage-statistics` — returns statistics snapshot (backward-compatible format)
  - `GET /v0/management/usage-statistics/export` — export full snapshot
  - `PUT /v0/management/usage-statistics/import` — merge import with deduplication
- Added `usage-db-path` config option (defaults to `usage.db` next to config file)
- Frontend: Usage Statistics page with overview cards, RPM/TPM charts, hourly/daily bar charts, API breakdown, token breakdown, and latency stats

### 2. Auth Index Prefix Differentiation

**Pain point**: When multiple OpenAI compatibility entries shared the same API key but used different `name` + `prefix` combinations (e.g., same upstream key routed under different prefixes for different model groups), they produced identical `auth_index` values. This caused the management UI to display all requests under a single provider name, making it impossible to distinguish which prefix/model group a request belonged to.

**Changes**:
- Added `prefix` to the `Attributes` map in the config synthesizer for all 5 providers (OpenAI compat, Gemini, Claude, Codex, Vertex)
- Updated `indexSeed()` in `sdk/cliproxy/auth/types.go` to include `prefix` in the hash computation, so `auth_index = SHA256(name + prefix + apiKey + ...)` instead of `SHA256(name + apiKey + ...)`
- Frontend `resolveSourceDisplay` now resolves source display via `auth_index` as the primary lookup key (instead of raw source/API key), ensuring each provider entry maps to its correct display name
- Frontend fetches OpenAI compatibility data from the dedicated `/openai-compatibility` API (which includes `auth-index`) instead of `/config` (which does not)
- SQLite usage store now has schema versioning — when schema version mismatches, tables are automatically rebuilt

### 3. Other Improvements

- Schema version tracking in SQLite usage store prevents data corruption when schema changes
- Tests for auth index differentiation and file-based auth stability

## Quick Start

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

See [config.example.yaml](config.example.yaml) for configuration reference.

## License

Same as the upstream CLIProxyAPI project.
