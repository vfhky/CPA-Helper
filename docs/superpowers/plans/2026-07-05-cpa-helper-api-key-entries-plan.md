# CPA-Helper API Key Model Restriction — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend CPA-Helper to support creating API keys with optional per-key model restrictions (`allowed-models`), syncing to CLIProxyAPI's new `api-key-entries` management endpoints.

**Architecture:** CPA-Helper presents a unified API key list. Keys without restrictions sync to CLIProxyAPI's `api-keys` (unchanged); keys with `allowed-models` sync to `api-key-entries`. DB stores `allowed_models` as a JSON-encoded TEXT column. Frontend adds a collapsible model picker (checkbox grid from available models + freeform wildcard textarea) to the create/edit dialog.

**Tech Stack:** Go (backend), Vue 3 + Naive UI + TypeScript (frontend), SQLite (goose migrations)

## Global Constraints

- CPA-Helper does NOT validate wildcard syntax — CLIProxyAPI is the single source of truth
- `allowed_models`: nil/empty = no restriction (backward compatible)
- Keys with restrictions are excluded from CPA-Helper's own `api-keys` sync and managed via `api-key-entries` endpoints
- Existing keys must continue working without migration (column defaults to `''`)

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `backend/migrations/202607050001_add_allowed_models.sql` | Create | DB migration: add `allowed_models` column |
| `backend/migrations/migrations.go` | Modify | Bump `LatestVersion` |
| `backend/internal/app/users.go` | Modify | Types (`apiKeyPayload`, `UserAPIKey`, `UserApiKeySummary`), handlers, DB queries |
| `backend/internal/app/auth_settings.go` | Modify | New sync methods for `api-key-entries`, modify `addRemoteAPIKey`/`removeRemoteAPIKeyHash` |
| `frontend/src/shared/types/api.ts` | Modify | Add `allowed_models` to `UserApiKeySummary`, `ApiKeyCreatePayload`, `ApiKeyUpdatePayload` |
| `frontend/src/features/api-keys/views/ApiKeysView.vue` | Modify | New table column, collapsible model picker in create/edit dialog |

---

### Task 1: Database Migration

**Files:**
- Create: `backend/migrations/202607050001_add_allowed_models.sql`
- Modify: `backend/migrations/migrations.go:5`

**Interfaces:**
- Produces: `user_api_keys.allowed_models TEXT NOT NULL DEFAULT ''` column available for all subsequent tasks

- [ ] **Step 1: Create migration SQL file**

```sql
-- +goose Up
ALTER TABLE user_api_keys ADD COLUMN allowed_models TEXT NOT NULL DEFAULT '';
```

Save to `backend/migrations/202607050001_add_allowed_models.sql`.

- [ ] **Step 2: Bump LatestVersion**

In `backend/migrations/migrations.go`, change line 5:

```go
const LatestVersion int64 = 202607050001
```

- [ ] **Step 3: Run backend tests to verify migration applies cleanly**

```bash
cd backend && go test -v -run TestMigration ./internal/app/
```

Expected: tests pass, migration runs without error on fresh DB.

- [ ] **Step 4: Commit**

```bash
git add backend/migrations/202607050001_add_allowed_models.sql backend/migrations/migrations.go
git commit -m "feat: add allowed_models column to user_api_keys"
```

---

### Task 2: Backend Types and DB Queries

**Files:**
- Modify: `backend/internal/app/users.go:32-34` (apiKeyPayload)
- Modify: `backend/internal/app/users.go:57-64` (UserAPIKey)
- Modify: `backend/internal/app/users.go:66-91` (UserApiKeySummary)
- Modify: `backend/internal/app/users.go:838-883` (keySummaries)
- Modify: `backend/internal/app/users.go:652-665` (upsertUserAPIKey)
- Modify: `backend/internal/app/users.go:617-638` (updateCurrentUserAPIKey)

**Interfaces:**
- Consumes: `user_api_keys.allowed_models` column (from Task 1)
- Produces: `apiKeyPayload.AllowedModels []string`, `UserApiKeySummary.AllowedModels []string`, `UserAPIKey.AllowedModels []string`
- Produces: `encodeAllowedModels(models []string) string`, `decodeAllowedModels(raw string) []string` helpers

- [ ] **Step 1: Add AllowedModels to apiKeyPayload**

In `users.go`, change the `apiKeyPayload` struct (line 32-34):

```go
type apiKeyPayload struct {
	Description   string   `json:"description"`
	AllowedModels []string `json:"allowed_models"`
}
```

- [ ] **Step 2: Add AllowedModels to UserAPIKey**

In `users.go`, change the `UserAPIKey` struct (line 57-64):

```go
type UserAPIKey struct {
	APIKeyHash    string
	UserID        int
	APIKey        *string
	Description   string
	AllowedModels []string
	CreatedAt     *time.Time
	UpdatedAt     *time.Time
}
```

- [ ] **Step 3: Add AllowedModels to UserApiKeySummary**

In `users.go`, add to the `UserApiKeySummary` struct (after `Description` line 69):

```go
AllowedModels []string `json:"allowed_models"`
```

- [ ] **Step 4: Add "encoding/json" to imports**

In `users.go`, add `"encoding/json"` to the import block (after `"database/sql"`):

```go
import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	...
)
```

- [ ] **Step 5: Add JSON encode/decode helpers**

Add after the `UserApiKeySummary` struct in `users.go`:

```go
func encodeAllowedModels(models []string) string {
	if len(models) == 0 {
		return ""
	}
	data, err := json.Marshal(models)
	if err != nil {
		return ""
	}
	return string(data)
}

func decodeAllowedModels(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil
	}
	var models []string
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil
	}
	return models
}
```

- [ ] **Step 6: Update keySummaries query to read allowed_models**

In `users.go`, change the `keySummaries` SQL query (line 839-844) to include `k.allowed_models`:

```go
rows, err := a.db.QueryContext(ctx, `
	SELECT k.api_key_hash, k.user_id, k.api_key, k.description, k.allowed_models, CAST(k.created_at AS TEXT), CAST(k.updated_at AS TEXT),
	       u.nickname, u.username
	FROM user_api_keys k
	LEFT JOIN users u ON u.id = k.user_id
`)
```

And in the Scan (line 854), add `&rawAllowedModels`:

```go
var apiKey, createdAt, updatedAt, nickname, username, rawAllowedModels sql.NullString
var userID int
if err := rows.Scan(&summary.APIKeyHash, &userID, &apiKey, &summary.Description, &rawAllowedModels, &createdAt, &updatedAt, &nickname, &username); err != nil {
	return nil, err
}
```

And after line 857 (`summary.APIKey = nullableString(apiKey)`), add:

```go
summary.AllowedModels = decodeAllowedModels(rawAllowedModels.String)
```

- [ ] **Step 7: Update upsertUserAPIKey to write allowed_models**

In `users.go`, change `upsertUserAPIKey` signature (line 652) to accept `allowedModels []string`:

```go
func (a *App) upsertUserAPIKey(ctx context.Context, userID int, apiKeyHash, apiKey, description string, allowedModels []string) error {
	now := dbTime(time.Now())
	encoded := encodeAllowedModels(allowedModels)
	_, err := a.db.ExecContext(ctx, `
		INSERT INTO user_api_keys (api_key_hash, user_id, api_key, description, allowed_models, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(api_key_hash) DO UPDATE SET user_id = excluded.user_id,
			api_key = excluded.api_key, description = excluded.description, allowed_models = excluded.allowed_models, updated_at = excluded.updated_at
	`, apiKeyHash, userID, apiKey, description, encoded, now, now)
	...
```

- [ ] **Step 8: Update updateCurrentUserAPIKey to accept and write allowed_models**

In `users.go`, change `updateCurrentUserAPIKey` signature (line 617):

```go
func (a *App) updateCurrentUserAPIKey(ctx context.Context, user *AuthUser, apiKeyHash, description string, allowedModels []string) (UserApiKeySummary, error) {
```

Change the UPDATE query (line 622):

```go
encoded := encodeAllowedModels(allowedModels)
result, err := a.db.ExecContext(ctx, `UPDATE user_api_keys SET description = ?, allowed_models = ?, updated_at = ? WHERE user_id = ? AND api_key_hash = ?`, description, encoded, dbTime(time.Now()), user.ID, apiKeyHash)
```

- [ ] **Step 9: Run tests to verify compilation and existing tests pass**

```bash
cd backend && go build ./... && go test -v -run TestAPI ./internal/app/
```

Expected: build succeeds, API key tests pass.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/app/users.go
git commit -m "feat: add allowed_models to API key types, queries, and handlers"
```

---

### Task 3: Backend Sync Logic (CLIProxyAPI integration)

**Files:**
- Modify: `backend/internal/app/auth_settings.go:760-788` (addRemoteAPIKey)
- Modify: `backend/internal/app/auth_settings.go:790-817` (removeRemoteAPIKeyHash)
- Modify: `backend/internal/app/auth_settings.go:819-828` (remoteAPIKeys — unchanged, for reference)
- Create: new methods in same file

**Interfaces:**
- Consumes: `apiKeyPayload.AllowedModels []string` (from Task 2)
- Produces: `remoteAPIKeyEntries()`, `putRemoteAPIKeyEntries()`, `deleteRemoteAPIKeyEntry()`

- [ ] **Step 1: Add CLIProxyAPIKeyEntry type**

After `parseStringList` in `auth_settings.go` (after line 888), add:

```go
type cliProxyAPIKeyEntry struct {
	Key           string   `json:"key"`
	AllowedModels []string `json:"allowed-models,omitempty"`
}
```

- [ ] **Step 2: Add remoteAPIKeyEntries (GET)**

```go
func (a *App) remoteAPIKeyEntries(ctx context.Context, cfg AppConfig) ([]cliProxyAPIKeyEntry, error) {
	response, payload, err := doJSON(ctx, httpClient(apiKeySyncTimeout), http.MethodGet,
		makeURL(cfg.Collector.CLIProxyURL, "/v0/management/api-key-entries", nil),
		managementHeaders(cfg.Collector.ManagementKey), nil)
	if err != nil {
		return nil, remoteAPIKeyError("读取 CPA API KEY 模型限制", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, validationError(fmt.Sprintf("读取 CPA API KEY 模型限制失败：HTTP %d", response.StatusCode))
	}
	var result struct {
		Entries []cliProxyAPIKeyEntry `json:"api-key-entries"`
	}
	if err := json.Unmarshal(payload, &result); err != nil {
		return nil, remoteAPIKeyError("解析 CPA API KEY 模型限制", err)
	}
	return result.Entries, nil
}
```

- [ ] **Step 3: Add putRemoteAPIKeyEntries (PUT)**

```go
func (a *App) putRemoteAPIKeyEntries(ctx context.Context, cfg AppConfig, entries []cliProxyAPIKeyEntry) error {
	body := map[string]interface{}{"api-key-entries": entries}
	response, _, err := doJSON(ctx, httpClient(apiKeySyncTimeout), http.MethodPut,
		makeURL(cfg.Collector.CLIProxyURL, "/v0/management/api-key-entries", nil),
		managementHeaders(cfg.Collector.ManagementKey), body)
	if err != nil {
		return remoteAPIKeyError("写入 CPA API KEY 模型限制", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return validationError(fmt.Sprintf("写入 CPA API KEY 模型限制失败：HTTP %d", response.StatusCode))
	}
	return nil
}
```

- [ ] **Step 4: Add deleteRemoteAPIKeyEntry (DELETE)**

```go
func (a *App) deleteRemoteAPIKeyEntry(ctx context.Context, cfg AppConfig, key string) (bool, error) {
	response, _, err := doJSON(ctx, httpClient(apiKeySyncTimeout), http.MethodDelete,
		makeURL(cfg.Collector.CLIProxyURL, "/v0/management/api-key-entries", url.Values{"value": {key}}),
		managementHeaders(cfg.Collector.ManagementKey), nil)
	if err != nil {
		return false, remoteAPIKeyError("删除 CPA API KEY 模型限制", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, validationError(fmt.Sprintf("删除 CPA API KEY 模型限制失败：HTTP %d", response.StatusCode))
	}
	return true, nil
}
```

Note: requires adding `"net/url"` to imports.

- [ ] **Step 5: Modify addRemoteAPIKey to route based on allowedModels**

Change `addRemoteAPIKey` signature (line 760):

```go
func (a *App) addRemoteAPIKey(ctx context.Context, apiKey string, allowedModels []string) error {
```

Replace the body (lines 761-788) with:

```go
	cfg, err := a.loadConfig(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Collector.ManagementKey) == "" {
		return validationError(apiKeySyncMissingConfigMessage)
	}
	syncCtx, cancel := context.WithTimeout(ctx, apiKeySyncTimeout)
	defer cancel()

	if len(allowedModels) == 0 {
		// No restriction — use existing api-keys sync path
		unsupported, err := a.patchRemoteAPIKey(syncCtx, cfg, apiKey)
		if err != nil {
			return err
		}
		if !unsupported {
			return nil
		}
		keys, err := a.remoteAPIKeys(syncCtx, cfg)
		if err != nil {
			return err
		}
		for _, existing := range keys {
			if existing == apiKey {
				return nil
			}
		}
		keys = append(keys, apiKey)
		return a.putRemoteAPIKeys(syncCtx, cfg, keys)
	}

	// Has model restrictions — use api-key-entries sync path
	entries, err := a.remoteAPIKeyEntries(syncCtx, cfg)
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].Key == apiKey {
			entries[i].AllowedModels = allowedModels
			return a.putRemoteAPIKeyEntries(syncCtx, cfg, entries)
		}
	}
	entries = append(entries, cliProxyAPIKeyEntry{Key: apiKey, AllowedModels: allowedModels})
	return a.putRemoteAPIKeyEntries(syncCtx, cfg, entries)
```

- [ ] **Step 6: Modify removeRemoteAPIKeyHash to clean up both endpoints**

Replace the body of `removeRemoteAPIKeyHash` (line 790-817, keeping the first 8 lines of setup then replacing the loop):

After line 799 (`defer cancel()`), replace everything after:

```go
	// Remove from api-keys (existing path)
	keys, err := a.remoteAPIKeys(syncCtx, cfg)
	if err != nil {
		return err
	}
	filtered := make([]string, 0, len(keys))
	for _, existing := range keys {
		if hashAPIKey(existing) != apiKeyHash {
			filtered = append(filtered, existing)
		}
	}
	if len(filtered) < len(keys) {
		if err := a.putRemoteAPIKeys(syncCtx, cfg, filtered); err != nil {
			return err
		}
	}

	// Also remove from api-key-entries (best-effort, ignore 404)
	entries, err := a.remoteAPIKeyEntries(syncCtx, cfg)
	if err != nil {
		// If the endpoint is not yet available (older CLIProxyAPI), ignore
		return nil
	}
	filteredEntries := make([]cliProxyAPIKeyEntry, 0, len(entries))
	for _, entry := range entries {
		if hashAPIKey(entry.Key) != apiKeyHash {
			filteredEntries = append(filteredEntries, entry)
		}
	}
	if len(filteredEntries) < len(entries) {
		_ = a.putRemoteAPIKeyEntries(syncCtx, cfg, filteredEntries)
	}
	return nil
```

- [ ] **Step 7: Ensure "net/url" and "encoding/json" are imported in auth_settings.go**

Check imports, add if missing:

```go
import (
	"encoding/json"
	"net/url"
	// ... existing imports
)
```

- [ ] **Step 8: Run tests**

```bash
cd backend && go build ./... && go test -v ./internal/app/
```

Expected: build succeeds, all tests pass.

- [ ] **Step 10: Commit**

```bash
git add backend/internal/app/auth_settings.go
git commit -m "feat: add api-key-entries sync methods for CLIProxyAPI model restrictions"
```

---

### Task 4: Backend Handler Wiring

**Files:**
- Modify: `backend/internal/app/users.go:267-326` (handleCurrentUserAPIKeys, handleCurrentUserAPIKeyByHash)
- Modify: `backend/internal/app/users.go:585-615` (createGeneratedAPIKeyForUser)

**Interfaces:**
- Consumes: `addRemoteAPIKey(ctx, key, allowedModels)`, `upsertUserAPIKey(ctx, ..., allowedModels)`, `updateCurrentUserAPIKey(ctx, ..., allowedModels)` (from Tasks 2, 3)
- Produces: Updated HTTP handlers that pass `allowed_models` through

- [ ] **Step 1: Update handleCurrentUserAPIKeys POST handler**

In `handleCurrentUserAPIKeys` (line 280-290), change the POST case to pass `payload.AllowedModels`:

```go
case http.MethodPost:
	var payload apiKeyPayload
	if err := decodeJSON(r, &payload); err != nil {
		return err
	}
	summary, err := a.createGeneratedAPIKeyForUser(r.Context(), user.ID, user.Username, payload.Description, payload.AllowedModels)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, summary)
	return nil
```

- [ ] **Step 2: Update handleCurrentUserAPIKeyByHash PUT handler**

In `handleCurrentUserAPIKeyByHash` (line 306-316), change the PUT case:

```go
case http.MethodPut:
	var payload apiKeyPayload
	if err := decodeJSON(r, &payload); err != nil {
		return err
	}
	summary, err := a.updateCurrentUserAPIKey(r.Context(), user, apiKeyHash, payload.Description, payload.AllowedModels)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, summary)
	return nil
```

- [ ] **Step 3: Update createGeneratedAPIKeyForUser**

Change signature (line 585) and body to accept and pass `allowedModels`:

```go
func (a *App) createGeneratedAPIKeyForUser(ctx context.Context, userID int, username, description string, allowedModels []string) (UserApiKeySummary, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return UserApiKeySummary{}, validationError("API KEY 描述不能为空")
	}
	user, err := a.getActiveUser(ctx, userID)
	if err != nil {
		return UserApiKeySummary{}, err
	}
	if err := a.ensureUserQuotaReadyForKeys(ctx, user.ID); err != nil {
		return UserApiKeySummary{}, err
	}
	apiKey, err := a.generateUniqueAPIKey(ctx)
	if err != nil {
		return UserApiKeySummary{}, err
	}
	if err := a.addRemoteAPIKey(ctx, apiKey, allowedModels); err != nil {
		return UserApiKeySummary{}, err
	}
	apiKeyHash := hashAPIKey(apiKey)
	if err := a.upsertUserAPIKey(ctx, user.ID, apiKeyHash, apiKey, description, allowedModels); err != nil {
		_ = a.removeRemoteAPIKeyHash(ctx, apiKeyHash)
		return UserApiKeySummary{}, err
	}
	summary, err := a.keySummaryByHash(ctx, apiKeyHash, &apiKey)
	if err != nil {
		return UserApiKeySummary{}, err
	}
	summary.UserName = &username
	return summary, nil
}
```

- [ ] **Step 4: Verify build and run tests**

```bash
cd backend && go build ./... && go test -v ./internal/app/
```

Expected: build succeeds, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/app/users.go
git commit -m "feat: wire allowed_models through API key handlers and sync"
```

---

### Task 5: Frontend Types

**Files:**
- Modify: `frontend/src/shared/types/api.ts:474-476` (UserApiKeySummary)
- Modify: `frontend/src/shared/types/api.ts:618-624` (ApiKeyCreatePayload, ApiKeyUpdatePayload)

**Interfaces:**
- Produces: `UserApiKeySummary.allowed_models`, `ApiKeyCreatePayload.allowed_models`, `ApiKeyUpdatePayload.allowed_models`

- [ ] **Step 1: Add allowed_models to UserApiKeySummary**

In `api.ts`, after line 476 (`api_key: string | null`), add:

```typescript
  allowed_models: string[]
```

- [ ] **Step 2: Add allowed_models to ApiKeyCreatePayload**

In `api.ts`, at the `ApiKeyCreatePayload` interface (line 618-620), add:

```typescript
export interface ApiKeyCreatePayload {
  description: string
  allowed_models?: string[]
}
```

- [ ] **Step 3: Add allowed_models to ApiKeyUpdatePayload**

```typescript
export interface ApiKeyUpdatePayload {
  description: string
  allowed_models?: string[]
}
```

- [ ] **Step 4: Commit**

```bash
git add frontend/src/shared/types/api.ts
git commit -m "feat: add allowed_models to frontend API key types"
```

---

### Task 6: Frontend View — Table Column

**Files:**
- Modify: `frontend/src/features/api-keys/views/ApiKeysView.vue:618-698` (columns computed)

**Interfaces:**
- Consumes: `UserApiKeySummary.allowed_models` (from Task 5)

- [ ] **Step 1: Add model restriction column to the table**

In `ApiKeysView.vue`, in the `columns` computed (after the description column, before the "创建时间" column), add:

```typescript
  {
    title: t('模型限制', 'Model restriction'),
    key: 'allowed_models',
    width: 260,
    render: (row: UserApiKeySummary) => {
      const models = row.allowed_models
      if (!models || models.length === 0) {
        return h('span', { class: 'model-restriction-none' }, '—')
      }
      return h(
        'div',
        { class: 'model-tags' },
        models.slice(0, 3).map((m: string) =>
          h('span', { class: 'model-tag' }, m)
        ).concat(
          models.length > 3
            ? [h('span', { class: 'model-tag model-tag-more' }, `+${models.length - 3}`)]
            : []
        )
      )
    },
  },
```

- [ ] **Step 2: Add CSS for model tags**

At the end of the `<style scoped>` block, add:

```css
.model-restriction-none {
  color: var(--cpa-text-muted);
  font-size: 13px;
}

.model-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.model-tag {
  display: inline-block;
  padding: 1px 8px;
  border-radius: 999px;
  background: color-mix(in srgb, var(--cpa-primary) 12%, transparent);
  color: var(--cpa-primary);
  font-size: 12px;
  font-weight: 600;
  line-height: 1.6;
  white-space: nowrap;
}

.model-tag-more {
  background: var(--cpa-surface-muted);
  color: var(--cpa-text-muted);
}
```

- [ ] **Step 3: Commit**

```bash
git add frontend/src/features/api-keys/views/ApiKeysView.vue
git commit -m "feat: add model restriction column to API keys table"
```

---

### Task 7: Frontend View — Model Picker in Create/Edit Dialog

**Files:**
- Modify: `frontend/src/features/api-keys/views/ApiKeysView.vue:507-591` (create/edit dialog logic)
- Modify: `frontend/src/features/api-keys/views/ApiKeysView.vue:766-795` (create/edit dialog template)

**Interfaces:**
- Consumes: `ApiKeyCreatePayload.allowed_models`, `ApiKeyUpdatePayload.allowed_models` (from Task 5)
- Consumes: `listAvailableModels()` (existing API, `frontend/src/features/models/api/availableModelsApi.ts`)
- Consumes: `AvailableModel` type from `frontend/src/shared/types/api.ts`

- [ ] **Step 1: Add state variables for model picker**

In the `<script setup>` section, after line 85 (`const visibleApiKeyHashes`), add:

```typescript
const allowedModelsExpanded = ref(false)
const allowedModelsChecked = ref<Set<string>>(new Set())
const allowedModelsManual = ref('')
```

- [ ] **Step 2: Add computed property for merged allowed_models**

```typescript
const mergedAllowedModels = computed<string[]>(() => {
  const manual = allowedModelsManual.value
    .split('\n')
    .map(s => s.trim())
    .filter(s => s !== '')
  const merged = [...new Set([...allowedModelsChecked.value, ...manual])]
  return merged.length > 0 ? merged : []
})
```

- [ ] **Step 3: Update openCreateDialog to reset model picker state**

In `openCreateDialog` (line 507-517), add resets:

```typescript
function openCreateDialog() {
  // ... existing checks ...
  editingApiKeyHash.value = null
  apiKeyDescription.value = 'VSCode'
  allowedModelsExpanded.value = false
  allowedModelsChecked.value = new Set()
  allowedModelsManual.value = ''
  generatedApiKey.value = null
  generatedApiKeyHash.value = null
  editorVisible.value = true
}
```

- [ ] **Step 4: Update editApiKey to populate model picker from existing key**

In `editApiKey` (line 524-530), add:

```typescript
function editApiKey(row: UserApiKeySummary) {
  editingApiKeyHash.value = row.api_key_hash
  apiKeyDescription.value = row.description || 'VSCode'
  const models = row.allowed_models || []
  // Separate exact model names from wildcard patterns
  const exactModels = models.filter(m => !m.includes('*'))
  const wildcardModels = models.filter(m => m.includes('*'))
  allowedModelsChecked.value = new Set(exactModels)
  allowedModelsManual.value = wildcardModels.join('\n')
  allowedModelsExpanded.value = models.length > 0
  generatedApiKey.value = null
  generatedApiKeyHash.value = null
  editorVisible.value = true
}
```

- [ ] **Step 5: Update saveApiKey to pass allowed_models**

In `saveApiKey` (line 559-591), update both create and update calls:

```typescript
async function saveApiKey() {
  // ... existing early returns ...
  isSaving.value = true
  try {
    const payload = {
      description: apiKeyDescription.value.trim(),
      allowed_models: mergedAllowedModels.value.length > 0 ? mergedAllowedModels.value : undefined,
    }
    if (editingApiKeyHash.value) {
      await updateApiKey(editingApiKeyHash.value, payload)
      // ...
    } else {
      const created = await createApiKey(payload)
      // ...
    }
    // ...
  }
}
```

- [ ] **Step 6: Add model picker UI to the dialog template**

In the `<NModal>` for create/edit (line 766-795), after the description `<NFormItem>`, add:

```html
<NFormItem>
  <NButton
    text
    @click="allowedModelsExpanded = !allowedModelsExpanded"
    style="padding: 0; font-weight: 600;"
  >
    {{ allowedModelsExpanded ? '▼' : '▶' }}
    {{ t('模型限制（可选）', 'Model restriction (optional)') }}
  </NButton>
</NFormItem>

<div v-if="allowedModelsExpanded" class="model-picker-section">
  <div class="model-picker-label">
    {{ t('从可用模型中选择', 'Select from available models') }}
  </div>
  <div class="model-checkbox-grid">
    <NButton
      v-for="model in (availableModels?.models ?? [])"
      :key="model.id"
      :type="allowedModelsChecked.has(model.id) ? 'primary' : 'default'"
      size="tiny"
      round
      @click="
        allowedModelsChecked.has(model.id)
          ? allowedModelsChecked.delete(model.id)
          : allowedModelsChecked.add(model.id);
        allowedModelsChecked = new Set(allowedModelsChecked)
      "
    >
      {{ model.id }}
    </NButton>
  </div>
  <div class="model-picker-label" style="margin-top: 12px;">
    {{ t('或输入通配符模式（每行一个）', 'Or enter wildcard patterns (one per line)') }}
  </div>
  <NInput
    v-model:value="allowedModelsManual"
    type="textarea"
    :autosize="{ minRows: 2, maxRows: 4 }"
    :placeholder="t('例如: gemini-*\nclaude-sonnet-*\n*-preview', 'Example: gemini-*\nclaude-sonnet-*\n*-preview')"
  />
  <div v-if="mergedAllowedModels.length > 0" class="model-chips-preview">
    <span
      v-for="model in mergedAllowedModels"
      :key="model"
      class="model-tag"
    >{{ model }}</span>
  </div>
</div>
```

- [ ] **Step 7: Add CSS for model picker**

At the end of the `<style scoped>` block, add:

```css
.model-picker-section {
  display: grid;
  gap: 8px;
  padding: 10px 12px;
  border: 1px solid var(--cpa-border);
  border-radius: var(--cpa-radius);
  background: var(--cpa-surface-muted);
}

.model-picker-label {
  color: var(--cpa-text-muted);
  font-size: 12px;
  font-weight: 700;
}

.model-checkbox-grid {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.model-chips-preview {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
```

Note: The `.model-tag` class is already defined in Task 6 Step 2.

- [ ] **Step 8: Verify frontend compiles**

```bash
cd frontend && npx vue-tsc --noEmit
```

Expected: no type errors.

- [ ] **Step 10: Commit**

```bash
git add frontend/src/features/api-keys/views/ApiKeysView.vue
git commit -m "feat: add model restriction picker to API key create/edit dialog"
```

---

### Task 8: End-to-End Verification

- [ ] **Step 1: Start CLIProxyAPI with api-key-entries support**

```bash
cd ../CLIProxyAPI && go run ./cmd/server --config config.yaml
```

Verify the management API responds:
```bash
curl -s http://localhost:8317/v0/management/api-key-entries -H "Authorization: Bearer <mgmt-key>" | jq .
```

- [ ] **Step 2: Start CPA-Helper backend**

```bash
cd backend && go run .
```

- [ ] **Step 3: Create a restricted API key through the frontend**

1. Open CPA-Helper UI
2. Create a new API key with description "Test Restricted" and select some models + add a wildcard pattern
3. Verify the key appears in the list with model tags displayed
4. Verify the key was synced to CLIProxyAPI:

```bash
curl -s http://localhost:8317/v0/management/api-key-entries -H "Authorization: Bearer <mgmt-key>" | jq '.["api-key-entries"]'
```

Expected: the new key appears with the correct `allowed-models`.

- [ ] **Step 4: Verify unrestricted key still works**

1. Create a key WITHOUT expanding the model restriction section
2. Verify it appears in `api-keys` (not `api-key-entries`):

```bash
curl -s http://localhost:8317/v0/management/api-keys -H "Authorization: Bearer <mgmt-key>" | jq '.["api-keys"]'
```

- [ ] **Step 5: Verify model restriction enforcement**

```bash
# Should succeed (allowed model)
curl -s http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer <restricted-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}'

# Should return 403 (not in allowed-models)
curl -s http://localhost:8317/v1/chat/completions \
  -H "Authorization: Bearer <restricted-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"claude-opus-4","messages":[{"role":"user","content":"hi"}]}'
```

- [ ] **Step 6: Verify delete cleans up both endpoints**

Delete the restricted key through CPA-Helper UI, then verify it's gone from both:

```bash
curl -s http://localhost:8317/v0/management/api-key-entries -H "Authorization: Bearer <mgmt-key>" | jq '.["api-key-entries"]'
```

- [ ] **Step 7: Commit any final fixes**
