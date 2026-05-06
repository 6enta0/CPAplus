# CPAplus

English | [中文](README_CN.md) | [日本語](README_JA.md)

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

### 3. Codex Quota Management & Credential Control

**Pain point**: The original project had no visibility into Codex account quota usage. Users had to run a separate Python service to check quotas and refresh tokens, which was cumbersome and required maintaining two processes.

**Changes**:
- Added `internal/codex/quota.go` — OAuth token refresh (reusing `internal/auth/codex` package), quota querying via OpenAI usage API, auto-disable/enable logic, quota data persistence to auth files
- Added management API endpoints:
  - `POST /v0/management/auth-files/quota-check` — batch quota check + token refresh + auto-disable/enable
  - `POST /v0/management/auth-files/refresh-token` — batch token refresh
- Quota fields written to auth JSON files: `quota_plan_type`, `quota_windows` (with usedPercent, resetAtIso), `quota_checked_at`, `quota_error`
- Auto-disable: when quota reaches 100%, the auth file is disabled automatically; re-enabled when quota resets
- Frontend: Quota display per auth file card with plan type badge, usage bars, and reset countdown
- Auth file list now reads quota fields from disk on page load (no manual check required for display)

### 4. Model Pricing & Cost Tracking

**Pain point**: There was no way to track how much each API call costs. Users had to manually look up model prices and calculate expenses themselves.

**Changes**:
- Added `internal/pricing/` package — syncs model prices from [LiteLLM](https://github.com/BerriAI/litellm) on startup and every 72 hours (pricing approach referenced from [agent-usage](https://github.com/briqt/agent-usage))
- Custom prices (e.g., MiMo models) are hardcoded and never overwritten by LiteLLM sync
- Fuzzy model name matching (prefix stripping, substring containment) for price lookup
- `usage_records` table now includes `cost_usd` column — calculated at insertion time using input/output/cache token prices
- `CalcCost()` handles cached tokens separately (cache read price vs. input price)
- Added management API endpoints:
  - `GET /v0/management/pricing` — returns all prices (LiteLLM + custom) in frontend-friendly format
  - `POST /v0/management/pricing/sync` — manual trigger for price sync
- Frontend: Prices fetched from backend API (fallback to localStorage), "Total Cost" column in auth file list view, cost integration in usage statistics

### 5. Auth File List View & Enhanced Table

**Pain point**: The card-only view didn't scale well when managing many auth files. Key metrics like quota status, last call time, and cost required clicking into individual cards.

**Changes**:
- Added table/list view for auth files (togglable, default view)
- Columns: Name, Last Call, Status, Success, Failure, Plan Type (badge), Used Quota (progress bar + %), Total Cost, Actions, Quota Checked, Quota Reset (countdown)
- Sortable headers for all columns
- Time columns show date + relative time (two-line display)
- Quota bar color-coded: green (<60%), orange (60-90%), red (≥90%)
- Plan type badges: free (green), plus (blue), team (orange), pro (red)
- Batch and per-row action buttons: Check Quota, Refresh Token, Enable/Disable, Download, Delete

### 6. Other Improvements

- `last_called_at` persisted per auth index in `usage_records`, survives restarts
- `total_cost_usd` aggregated per auth index via SQL query
- Schema version tracking in SQLite usage store prevents data corruption
- Frontend: usage statistics page layout reordered (request events above charts)
- Frontend: control panel layout improvements (display options in one row, responsive widths)

## Quick Start

```bash
go build -o cli-proxy-api ./cmd/server
./cli-proxy-api --config config.yaml
```

See [config.example.yaml](config.example.yaml) for configuration reference.

## License

Same as the upstream CLIProxyAPI project.
