# Spec: Session Search, Directory Filter & Session Title

## 1. Session Title (Backend — Go)

### 1.1 New column

Add a nullable `title` column to the `sessions` table. This is a user-defined name for a session.

**Display priority:** `title` > `last_user_message` > `shortCwd` — first non-null wins.

**Migration:**

```sql
ALTER TABLE sessions ADD COLUMN title TEXT
```

Added via the existing column-migration pattern in `store.go`.

### 1.2 Struct change

Add `Title *string` to the `Session` struct with JSON tag `"title,omitempty"`.

Include `title` in all SELECT, INSERT, and UPDATE queries that touch sessions.

### 1.3 PATCH endpoint

The existing `PATCH /api/sessions/:id` accepts a new optional `title` field:

```json
{"title": "Auth refactor v2"}
```

Setting `title` to `""` clears it (server stores NULL, falls back to `last_user_message`).

### 1.4 Files changed

- `internal/store/store.go` — Add column migration for `title`.
- `internal/store/sessions.go` — Add `Title` field to `Session` struct. Update `UpsertSession`, `InsertDiscoveredSession`, `GetSession`, `SearchSessions` queries to include `title`. Add `UpdateSessionTitle(sessionID, title string)`.
- `internal/server/api.go` — `handlePatchSession` accepts `title`.

---

## 2. Session Search API (Backend — Go)

**Goal:** Allow clients to search and filter sessions via query parameters on the existing `GET /api/sessions` endpoint.

### 2.1 Search endpoint

**Endpoint:** `GET /api/sessions` (existing, backward-compatible)

**New query parameters:**

| Param    | Type   | Description                                                                 |
| -------- | ------ | --------------------------------------------------------------------------- |
| `q`      | string | Free-text search (tokenized, see algorithm below)                           |
| `status` | string | Exact match on session status (`active`, `idle`, `ended`, `suspended`, etc.)|
| `filter` | string | Flag-based filter: `all` (default, excludes archived), `pinned`, `archived` |
| `cwd`    | string | Exact match on session CWD path                                             |

**Behavior:**

- All params are optional. Omitting everything returns all non-archived sessions (current behavior).
- Params combine with AND logic.
- Results ordered by `COALESCE(last_event_at, created_at) DESC` — recent matches first.
- Limit: 1000 (bumped from current 100).
- Response shape stays identical: `{"sessions": [...]}`

### 2.2 Search algorithm

Tokenize the query string by splitting on spaces. Each token becomes a `LIKE '%token%'` condition against the concatenation of searchable fields. All tokens must match.

Searchable fields (in priority order): `title`, `last_user_message`, `project`, `cwd`, `session_id`.

```sql
-- Example: q = "auth helios"
-- Tokens: ["auth", "helios"]

SELECT ... FROM sessions
WHERE (COALESCE(title,'') || ' ' || COALESCE(last_user_message,'') || ' ' || project || ' ' || cwd || ' ' || session_id) LIKE '%auth%'
  AND (COALESCE(title,'') || ' ' || COALESCE(last_user_message,'') || ' ' || project || ' ' || cwd || ' ' || session_id) LIKE '%helios%'
  AND archived = 0
ORDER BY COALESCE(last_event_at, created_at) DESC
LIMIT 1000
```

Case-insensitive by default (SQLite LIKE is case-insensitive for ASCII).

### 2.3 Filter param mapping

| `filter` value  | SQL condition                  |
| ---------------- | ------------------------------ |
| `all` (default)  | `archived = 0`                 |
| `pinned`         | `pinned = 1 AND archived = 0`  |
| `archived`       | `archived = 1`                 |

### 2.4 Directories endpoint (new)

**Endpoint:** `GET /api/sessions/directories`

Returns all distinct CWDs from the sessions table with counts, ordered by most recent activity.

**Response:**

```json
{
  "directories": [
    {
      "cwd": "/Users/kamrul/workspace/helios",
      "project": "helios",
      "session_count": 45,
      "active_count": 2
    },
    {
      "cwd": "/Users/kamrul/workspace/other",
      "project": "other",
      "session_count": 12,
      "active_count": 0
    }
  ]
}
```

**SQL:**

```sql
SELECT cwd, project,
       COUNT(*) as session_count,
       SUM(CASE WHEN status IN ('active','waiting_permission','compacting','starting') THEN 1 ELSE 0 END) as active_count
FROM sessions
GROUP BY cwd
ORDER BY MAX(COALESCE(last_event_at, created_at)) DESC
```

### 2.5 Files changed (backend)

- `internal/store/sessions.go` — Add `SearchSessions(query, status, filter, cwd string)` and `ListDirectories()`. `ListSessions()` becomes a thin wrapper calling `SearchSessions("", "", "", "")`.
- `internal/server/api.go` — `handleListSessions` reads query params and calls `SearchSessions`. Add `handleListDirectories`.
- `internal/server/server.go` — Register `GET /api/sessions/directories` route.

---

## 3. Mobile App (Flutter)

### 3.1 Session title

- Add `title` field to `Session` model.
- Add `displayTitle` getter: `title ?? lastUserMessage ?? shortCwd`.
- Session card displays `displayTitle` instead of `lastUserMessage ?? shortCwd`.
- `patchSession` accepts optional `title` param.
- Session detail screen: tappable title to edit. Long-press context menu: "Rename" option.
- `copyWith` includes `title`.

### 3.2 Search UI

A search icon sits at the end of the filter chips row. Tapping it transitions the chips row into a text field. Clearing or pressing X collapses back to chips.

```
Default state:
┌──────────────────────────────────┐
│ [All (5)] [Pinned] [Arch]   🔍   │
└──────────────────────────────────┘

Search expanded:
┌──────────────────────────────────┐
│ [🔍 Search sessions...        ✕] │
└──────────────────────────────────┘
```

- Search calls the API with `?q=` param.
- Debounced: waits 300ms after the user stops typing before making the API call.

### 3.3 Active filters row

When a CWD filter is active, a removable chip appears in a second row below the search/chips bar.

```
Search active + CWD filter:
┌──────────────────────────────────┐
│ [🔍 auth bug                  ✕] │
│ [Pinned ✕] [📁 helios ✕]        │
└──────────────────────────────────┘

No search, CWD filter active:
┌──────────────────────────────────┐
│ [All] [Pinned] [Arch]       🔍   │
│ [📁 helios ✕]                    │
└──────────────────────────────────┘
```

All active filters (text query, filter chip, CWD) compose together — they all map to API query params combined with AND.

### 3.4 CWD filter

Users set a CWD filter by:

- A directory picker (populated from `GET /api/sessions/directories`) accessible from the sessions screen.
- Tapping a project/cwd label on a session card sets the CWD filter.

Removing the filter: tap X on the CWD chip.

### 3.5 New session sheet

The CWD input in the new session sheet uses the directories endpoint to show a dropdown of known project directories. User can still type a custom path.

### 3.6 Files changed (mobile)

- `mobile/lib/models/session.dart` — Add `title` field, `displayTitle` getter, update `fromJson`/`copyWith`.
- `mobile/lib/screens/sessions_screen.dart` — Search icon toggle, search text field, CWD filter chip, active filters row, debounced API search. Card displays `displayTitle`.
- `mobile/lib/screens/session_detail_screen.dart` — Editable title, rename option.
- `mobile/lib/services/daemon_api_service.dart` — Update `fetchSessions` to accept optional `q`, `status`, `filter`, `cwd` params. `patchSession` accepts `title`. Add `fetchDirectories()`.
- `mobile/lib/screens/new_session_sheet.dart` — CWD picker from directories API.
