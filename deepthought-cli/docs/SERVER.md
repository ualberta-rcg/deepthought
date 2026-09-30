# DeepThought Server — API and client contract

The server is optional. The CLI runs fully standalone; when a user connects
it (Ctrl+P → Server synchronization, or the splash), the server adds settings
that roam between machines, chat mirroring, provider-key sync and a web UI.
Every endpoint is additive: the client treats `404` and `501` as "this server
doesn't have the feature" and carries on.

## Where it lives

`deepthought-server/` at the repo root: its own Go module with no imports from
the CLI. `server/` is the HTTP surface, `store/` the MySQL store (user-scoped
tables), `graph/` a byte-compatible copy of the CLI's chat-graph wire shapes
(a shared golden fixture keeps the two in step), `k8s/` the reference
manifests. GitHub Actions tests it against a MySQL 8.4 service and builds the
image; deployment is covered in [DEPLOYMENT.md](../../docs/DEPLOYMENT.md).

Configuration: `DEEPTHOUGHT_SERVER_PASSWORD` (login), `DEEPTHOUGHT_MYSQL_DSN`
(without it the database endpoints answer `503`), `DEEPTHOUGHT_ADMIN_USERS`
(who may change the server defaults), `DEEPTHOUGHT_VAULT_KEY` (32 bytes, hex or
base64; without it only `$VARIABLE` key references are stored),
`--addr`, `--data`.

## Auth

`POST /api/v1/auth/login` with `{"user", "password"}` checks the shared
password and returns `{"token", "user"}`. Tokens are opaque; the server keeps
only their SHA-256 (in MySQL when configured, else in memory). A session lasts
24 h and slides forward when used in the second half of its life; expired
sessions are purged hourly. Every `/api/*` route except login needs
`Authorization: Bearer <token>`. On a `401` the client logs in once more with
the configured password and repeats the request; if that fails too it stops
syncing, keeps every edit local and asks the user to reconnect — no retry
loop. With no password configured `/api` answers `503`, never open.

**Trust assumption:** the password is shared. Anyone who holds it can log in
as any user name and read that user's settings, chats and stored keys. Keep it
to the people who could read those anyway. Per-user claim tokens are the
planned fix and stay deferred until the password is shared more widely.

## Endpoints

| Route | Purpose |
|---|---|
| `GET /healthz`, `GET /readyz` | open probes: status, version, uptime |
| `POST /api/v1/auth/login` | shared password → session token |
| `POST /api/v1/auth/logout` | revoke the calling session |
| `GET /api/v1/user/sessions` | `{current, sessions: [{id, created, last_seen, expires}]}`; ids are short hashes, never tokens |
| `DELETE /api/v1/user/sessions/{id}` | revoke one of the caller's sessions |
| `GET /api/v1/user/settings` | `{settings, revision}` — the roving settings document |
| `PUT /api/v1/user/settings` | `{settings, revision}` based on the last revision seen; a mismatch is `409` (pull, merge, push again) |
| `GET /api/v1/user/credentials` | `{salt, vault, credentials: [{id, name, base_url, wire, kind, fingerprint, updated_at}]}` — never values |
| `GET /api/v1/user/credentials/{id}` | one credential with its value (`Cache-Control: no-store`) |
| `PUT /api/v1/user/credentials/{id}` | `{name, base_url, wire, kind, value}`; `kind` is `env` (a `$VARIABLE` reference) or `secret` (sealed with the vault key; `503` without one) |
| `DELETE /api/v1/user/credentials/{id}` | remove the server copy |
| `GET /api/v1/chats` | the caller's chat summaries |
| `GET /api/v1/chats/{id}` | one chat graph |
| `PUT /api/v1/chats/{id}` | upsert a chat graph; ids owned by another account are `409` |
| `GET/PUT /api/v1/settings/defaults` | the server defaults layer; PUT is admin-only and rejects credential keys |
| `GET /api/v1/version`, `GET /api/v1/crons`, `POST /api/v1/admin/reload` | build info, the cron registry, config/skills reload |
| `GET /api/v1/jobs`, `GET /api/v1/experiments` | `501` placeholders |

A credential id is `CredentialID(name, base_url, wire)`: the first 32 hex
digits of SHA-256 over the lower-cased name, the URL without a trailing slash
and the lower-cased wire. The fingerprint is `HMAC-SHA256(salt, value)` under
a per-user salt, so the client can compare keys without either side sending
one. Secrets are AES-256-GCM with a fresh nonce per row and `user|id` as
additional data, so a row can't be moved to another user or provider. Both
modules carry the same test vectors.

## What the client does

- **Settings:** after login the client pulls, merges (local-only fields such
  as keys and `sync_credential` never leave the machine) and pushes on
  change. A `409` pulls again; a real conflict shows under
  Server synchronization › Needs attention.
- **Provider keys:** after the first successful settings sync of each login
  the client plans upload / download / conflict per synced provider. The first
  time on a machine the plan is reviewed; afterwards only conflicts ask. See
  [SETUP.md](../../docs/SETUP.md#provider-keys).
- **Chats:** saved locally first, then mirrored; failed pushes queue and flush
  on the next connect.
- **Account views:** Server synchronization lists the keys the server holds
  (masked, deletable) and the account's sessions (others revocable).
- **Logout:** Disconnect stops syncing and revokes the session on the server
  (best effort; an unreachable server just lets it expire).

## Web UI

An embedded single page at `/` (no framework, no build step): login, service
info and the defaults editor.

## Next

The execution plane — a headless turn endpoint on `unimatrix.Session`, event
backlog and approval round-trip for attach/resume, job and experiment state —
grows behind the same door. None of it changes the endpoints above.
