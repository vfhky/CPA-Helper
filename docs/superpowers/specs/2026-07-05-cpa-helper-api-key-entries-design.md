# CPA-Helper API Key Model Restriction — Integration Design

**Date:** 2026-07-05
**Status:** Draft
**Depends on:** CLIProxyAPI `feat/embeddings` branch (adds `api-key-entries` management API)

---

## 1. Goal

Extend CPA-Helper's API key management UI and backend so that administrators can create
downstream API keys with optional per-key model restrictions (`allowed-models`). This
completes the feature end-to-end: CLIProxyAPI already supports `api-key-entries` with
model restrictions; CPA-Helper must provide the management interface.

## 2. Non-Goals

- CPA-Helper does NOT validate wildcard patterns — validation is CLIProxyAPI's responsibility.
- CPA-Helper does NOT change its own per-user quota / rate-limit model.
- The CPA-Helper frontend does NOT display or manage CLIProxyAPI's internal `api-keys`
  vs `api-key-entries` distinction — the user sees a unified key list.

## 3. Approach: Unified List with Optional Restrictions

**Chosen approach (A — progressive enhancement):** CPA-Helper presents a single,
unified API key list. Keys without restrictions continue to sync to CLIProxyAPI's
`api-keys` (flat `[]string`). Keys with `allowed-models` sync to CLIProxyAPI's
`api-key-entries`. The user never sees the underlying split.

```
┌ API 密钥列表 ────────────────────────────────────────────────┐
│ sk-A...abc  VSCode      [gemini-*] [gpt-5-codex]  编辑 删除 │
│ sk-B...def  WebStorm    (无限制)                    编辑 删除 │
│ sk-C...ghi  Terminal    [claude-opus-*]             编辑 删除 │
└──────────────────────────────────────────────────────────────┘
```

## 4. Changes Overview

| Layer | File(s) | Change |
|-------|---------|--------|
| DB | `migrations/20260705_001_add_allowed_models.sql` | **New** — add `allowed_models TEXT` column to `user_api_keys` |
| Backend types | `internal/app/users.go` | Add `AllowedModels []string` to `apiKeyPayload`, `UserAPIKey`, `UserApiKeySummary` |
| Backend sync | `internal/app/auth_settings.go` | Add `remoteAPIKeyEntries`, `putRemoteAPIKeyEntries`, `deleteRemoteAPIKeyEntry`; update `addRemoteAPIKey` / `removeRemoteAPIKeyHash` to route to correct endpoint |
| Backend handlers | `internal/app/users.go` | Update `createCurrentUserAPIKey`, `updateCurrentUserAPIKey`, `handleCurrentUserAPIKeys`, `handleCurrentUserAPIKeyByHash` to read/write `allowed_models` |
| Frontend types | `src/shared/types/api.ts` | Add `allowed_models?: string[]` to relevant interfaces |
| Frontend API | `src/features/api-keys/api/apiKeysApi.ts` | No new endpoints — existing `createApiKey` / `updateApiKey` transparently pass new field |
| Frontend view | `src/features/api-keys/views/ApiKeysView.vue` | New "模型限制" column in table; collapsible model-picker in create/edit dialog |

## 5. Database Migration

```sql
-- migrations/20260705_001_add_allowed_models.sql
ALTER TABLE user_api_keys ADD COLUMN allowed_models TEXT NOT NULL DEFAULT '';
```

- Stored as a JSON string array: `'["gemini-*","gpt-5-codex"]'`
- Empty string `''` = no restriction (backward compatible with all existing rows)
- CPA-Helper stores and passes through; it does NOT validate wildcard syntax

## 6. Backend: Sync Logic (`auth_settings.go`)

### 6.1 New CLIProxyAPI client methods

```go
func (a *App) remoteAPIKeyEntries(ctx context.Context, cfg AppConfig) ([]config.APIKeyEntry, error)
func (a *App) putRemoteAPIKeyEntries(ctx context.Context, cfg AppConfig, entries []config.APIKeyEntry) error
func (a *App) deleteRemoteAPIKeyEntry(ctx context.Context, cfg AppConfig, key string) error
```

These call CLIProxyAPI's `/v0/management/api-key-entries` endpoints (GET/PUT/DELETE).

### 6.2 Modified sync methods

```
addRemoteAPIKey(ctx, apiKey, allowedModels)
  ├── allowedModels 为空/nil → 维持现有: PATCH /api-keys
  └── allowedModels 非空 → GET /api-key-entries → 合并 → PUT /api-key-entries

removeRemoteAPIKeyHash(ctx, apiKeyHash)
  ├── DELETE /api-keys?value=<key>  （现有逻辑）
  └── DELETE /api-key-entries?value=<key>  （新增，幂等忽略 404）
```

### 6.3 CLIProxyAPI APIKeyEntry type (local mirror)

```go
type CLIProxyAPIKeyEntry struct {
    Key           string   `json:"key"`
    AllowedModels []string `json:"allowed-models,omitempty"`
}
```

## 7. Backend: Handlers (`users.go`)

### 7.1 Create key

```
createCurrentUserAPIKey(ctx, user, payload {description, allowed_models})
  1. Generate key, hash it
  2. INSERT INTO user_api_keys (..., allowed_models) VALUES (..., json_encode(payload.allowed_models))
  3. Sync to CLIProxyAPI: addRemoteAPIKey(ctx, key, payload.allowed_models)
  4. Return UserApiKeySummary (with allowed_models)
```

### 7.2 Update key

```
updateCurrentUserAPIKey(ctx, user, apiKeyHash, payload {description, allowed_models})
  1. UPDATE user_api_keys SET description=?, allowed_models=json_encode(?), updated_at=? WHERE api_key_hash=?
  2. Re-sync to CLIProxyAPI: removeRemoteAPIKeyHash(old) then addRemoteAPIKey(new) if allowed_models changed
  3. Return updated UserApiKeySummary
```

### 7.3 List / detail

Existing queries add `allowed_models` column. On read, JSON-decode the TEXT column back to `[]string`. Empty string → `nil`.

## 8. Frontend: View (`ApiKeysView.vue`)

### 8.1 Table column

New column between "描述" and "创建时间":

```typescript
{
  title: t('模型限制', 'Model restriction'),
  key: 'allowed_models',
  width: 200,
  render: (row) => {
    const models = row.allowed_models
    if (!models || models.length === 0) {
      return h('span', { class: 'text-muted' }, '—')
    }
    return h('div', { class: 'model-tags' },
      models.map(m => h('span', { class: 'model-tag' }, m))
    )
  }
}
```

### 8.2 Create / Edit dialog

Below the description input, add a collapsible section:

1. **Toggle:** `▶ 模型限制（可选）` / `▼ 模型限制（可选）` — collapsed by default
2. **Checkbox grid:** load available models via existing `listAvailableModels()`, render as checkbox items
3. **Textarea:** below the checkbox grid, for manual wildcard patterns (one per line)
4. **Merge logic on submit:** `allowed_models = dedupe([...checkedModelIds, ...textareaLines.trim()])`
5. **Empty result:** if nothing checked and textarea is blank → `allowed_models = []` → creates unrestricted key

### 8.3 State management

```typescript
const allowedModels = ref<string[]>([])          // bound to checkbox grid + textarea
const allowedModelsManual = ref('')              // bound to textarea
const allowedModelsExpanded = ref(false)          // collapsible toggle
```

## 9. Testing Plan

| Test | Type | Covers |
|------|------|--------|
| Migration: `allowed_models` column added, existing rows have `''` | unit | DB migration |
| Create key with `allowed_models: ["gemini-*"]` stored and synced to CLIProxyAPI `api-key-entries` | integration | Create path |
| Create key with empty `allowed_models` synced to CLIProxyAPI `api-keys` (unchanged behavior) | integration | Backward compat |
| Update key: change `allowed_models` triggers re-sync | integration | Update path |
| Delete key: removed from both `api-keys` and `api-key-entries` | integration | Delete path |
| Frontend: model checkboxes populated from `listAvailableModels` | unit | UI rendering |
| Frontend: manual wildcard text merged with checkbox selections | unit | UI logic |
| Frontend: collapsed section stays collapsed on re-open | unit | UI state |

## 10. Design Decisions

| Decision | Rationale |
|----------|-----------|
| Unified key list (not separate tabs) | Lower cognitive load; user doesn't need to know about `api-keys` vs `api-key-entries` |
| Checkbox grid + manual textarea for model selection | Checkboxes are intuitive for exact model names; textarea covers wildcard patterns like `gemini-*` |
| CPA-Helper does NOT validate wildcard syntax | Single source of truth: CLIProxyAPI validates; duplicating validation would risk divergence |
| `allowed_models` stored as JSON TEXT in SQLite | SQLite has no array type; JSON is the standard portable encoding |
| Allowed models sync via PUT (full replace) not PATCH | CLAProxyAPI's `api-key-entries` list is small per key; PUT is simpler and avoids merge conflicts |
