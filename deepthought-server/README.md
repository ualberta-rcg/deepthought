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
- `k8s/` — reference manifests (deployed source of truth: the numbered
  manifests on the aleph1 control-plane).

Config: `$DEEPTHOUGHT_SERVER_PASSWORD` (auth), `$DEEPTHOUGHT_MYSQL_DSN`
(database; without it the DB endpoints answer 503), `--addr`, `--data`.
