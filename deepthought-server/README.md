# deepthought-server

The server-side product — **its own Go module, dependent on the CLI in no way**:
no shared packages, no sibling imports, its own Dockerfile and CI lane
(`.github/workflows/build-server.yml`, docker build + push only — deployment
does no testing).

- `server/` — HTTP surface: login (shared password → session tokens), the
  embedded web UI, settings defaults, user settings, chats.
- `store/` — the MySQL store (multi-user, user-scoped tables; server-only).
- `graph/` — the Borg-graph **wire contract**, a deliberate copy of the CLI's
  history shapes so the modules stay independent. Field names must stay
  byte-compatible with `deepthought-cli/internal/history` — change both
  together.
- `k8s/` — reference manifests. The current test deployment (a temporary
  staging namespace on a shared RKE2 cluster, hand-applied as numbered
  manifests on its control-plane) is a one-off copy of these files, not a
  managed deployment; see the root `CLAUDE.md` environment rules. Keep these
  files aligned with whatever is applied so a rebuild is reproducible.

Config: `$DEEPTHOUGHT_SERVER_PASSWORD` (auth), `$DEEPTHOUGHT_MYSQL_DSN`
(database; without it the DB endpoints answer 503),
`$DEEPTHOUGHT_ADMIN_USERS` (comma-separated login names allowed to
`PUT /api/v1/settings/defaults`; empty = nobody), `--addr`, `--data`. The image
stamps its tag as the version (`/healthz`); `$DEEPTHOUGHT_VERSION` overrides it.

Tests: `graph/golden_test.go` decodes the wire fixture shared with the CLI
(`graph/testdata/golden_collective.json`, an identical copy lives in
`deepthought-cli/internal/history/testdata/`; change both together). The store
tests need `$DEEPTHOUGHT_MYSQL_DSN`; CI provides a throwaway MySQL service.
