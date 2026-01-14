# Browser Extension GraphQL Passthrough (Plan)

This document bridges the paused Claude work into an owned plan for completing the “Browser Extension GraphQL Passthrough” effort end-to-end.

## Goal

Enable XGrabba to use a **logged-in browser session** (via the existing extension) to:

- Make authenticated **GraphQL** requests to X when needed (e.g. long-form tweet text / note tweets).
- Optionally fetch **Bookmarks** server-side without requiring X API v2 OAuth tokens.
- Keep GraphQL **query IDs** and **feature flags** fresh via passive interception in the extension.

## Current status (as of this plan)

### Completed (Phase 1)

- Extension:
  - `extension/content/credentials.js` captures `ct0` and requests HttpOnly `auth_token` from background.
  - Popup setting: **Forward X credentials** (opt-in).
  - Background endpoints:
    - `GET_AUTH_TOKEN` (cookies API)
    - `SYNC_CREDENTIALS` (POST to backend)
    - `GET_CREDENTIALS_STATUS`
- Server:
  - `POST /api/v1/extension/credentials`
  - `GET /api/v1/extension/credentials/status`
  - `POST /api/v1/extension/credentials/clear`
  - `pkg/twitter/browser_credentials.go` stores credentials in memory with TTL.

### In progress (Phase 2 – partially done)

- `pkg/twitter/client.go` now prefers browser credentials headers for GraphQL tweet calls (fallback to guest token).
- Still needed:
  - Use **browser-supplied query IDs** when available (avoid scraping `main.js`).
  - Use **browser-supplied feature flags** when available (avoid hardcoded feature sets).

## Remaining work

### Phase 2: Integrate browser credentials into GraphQL calls (complete)

- Prefer browser-supplied query id for `TweetResultByRestId` when present.
- Prefer browser-supplied `features` JSON blob when present.
- Keep existing guest-token behavior as fallback.

### Phase 3: Server-side GraphQL bookmark fetch

- Implement a `bookmarkLister` compatible client that calls:
  - `GET https://x.com/i/api/graphql/<queryId>/Bookmarks?...`
- Parse timeline response to extract tweet IDs and pagination cursor.
- Integrate into `internal/bookmarks/monitor` via a config flag (so existing OAuth/v2 bookmarks flow keeps working).

### Phase 4: GraphQL intercept + auto-sync from extension

- Passively observe X GraphQL requests from the extension (MV3 service worker) to capture:
  - `operationName` (path segment)
  - `queryId` (path segment)
  - `features` param (decoded JSON)
- Periodically sync to backend (rate-limited) along with `auth_token` + `ct0`.

### Phase 5: UI cleanup

- Move “Bookmarks monitor status” entrypoint into the **Settings/Stats** modal in the server UI
  - Remove always-visible header badge/button.
  - Keep monitor controls available (open modal from settings).

## Test plan (must pass before release)

- `go test ./...`
- `go test -race ./...` (if time allows)
- `make lint` (or `golangci-lint run`)
- Manual smoke:
  - Start server, load extension unpacked, enable “Forward X credentials”.
  - Visit `x.com` logged in and confirm:
    - `/api/v1/extension/credentials/status` returns `has_credentials=true`.
  - Archive a known note tweet and confirm full text is preserved.
  - (If bookmarks graphQL mode enabled) verify bookmarks monitor can fetch IDs.

