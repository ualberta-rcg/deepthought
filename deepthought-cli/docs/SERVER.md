# DeepThought Server — Architecture & Groundwork

The server side of the research-copilot roadmap ([ROADMAP.md](ROADMAP.md)): the durable,
detachable execution plane that persists state across client disconnects and resumes work.
**Status: skeleton.** What exists today is the front door (health, auth, one real data
endpoint, live reload) plus this document — the execution plane grows in behind the same
door with no client breakage.

## The client runs standalone

The CLI is the product; this server is additive. Nothing in the client dials or waits on
it — the client's full functionality (chat loop, tools, skills, cron, status) runs
client-side, and stays that way: capabilities land client-side first where feasible.

The CLI now has a local resident session runner and layered settings resolver.
The `DefaultsSource` interface is an inactive seam for eventual server settings;
there is no settings fetch or server login in the current client. Local overrides
remain authoritative over future server defaults. See [LOCAL-OPERATIONS.md](LOCAL-OPERATIONS.md).

## Where it lives today

`deepthought-cli/cmd/deepthought-server` + `deepthought-cli/internal/server`. Go
`internal/` packages cannot be imported across modules, so the server starts inside the
`deepthought-cli` module; promotion to the repo-root `deepthought-server/` product dir is
deferred until the internals it needs earn extraction into a shared package.

## Deployment

- **Primary: a container on the Vulcan Kubernetes cluster**, like aleph. TLS terminates
  upstream at **`deepthought.vulcan.alliancecan.ca`**; the server binds plaintext
  (`--addr 0.0.0.0:8080` in the container). Reference manifests: `k8s/deployment.yaml` +
  `k8s/service.yaml` (aleph conventions: never-`:latest` image pins `…:server-<sha>`,
  startup/readiness/liveness probes on `/healthz` + `/readyz`, rolling updates; the
  deployed source of truth will live in the ww-overlays control-plane manifests).
- **CI** builds the image (`.github/workflows/build-server.yml`) — test → static build →
  artifact → optional Docker publish following aleph's secret conventions
  (`DOCKER_HUB_USER`/`DOCKER_HUB_TOKEN`, `DOCKER_HUB_REPO` override). The build+artifact
  path needs zero secrets.
- **Dev/bare-metal**: `make deploy-server`, or the systemd user unit at
  `configs/deepthought-server.service`.
- **Pod state is ephemeral** — the skeleton is stateless by design; real state (SQLite,
  cron registries) will need a PVC (aleph uses RWX NFS for its ledger) before anything
  durable runs in-cluster.

## Auth

A shared bearer token now (`$DEEPTHOUGHT_SERVER_TOKEN` or `--token-file`): every `/api/*`
route requires it; health endpoints stay open for probes; **no token configured → /api
answers 503, never silently open**. Per-user tokens (SSH-key-derived or site SSO) are the
documented next step — the auth middleware is the single place to change.

## API surface (v1)

| Route | Status | Notes |
|---|---|---|
| `GET /healthz`, `GET /readyz` | live | open; status/version/uptime |
| `GET /api/v1/version` | live | build + data dir |
| `GET /api/v1/crons` | live | the local cron tracking registry — the first fleet-aggregation endpoint (each system's registry is host-scoped + versioned JSON) |
| `POST /api/v1/admin/reload` | live | re-reads config + skills; returns what it found |
| `GET /api/v1/jobs` | 501 | roadmap: job lifecycle (#8) |
| `GET /api/v1/experiments` | 501 | roadmap: persistent project state (#2) |
| `GET /api/v1/chats` | 501 | roadmap: will list `history.SQLiteStore.ListCollectives` |

## Live editing

Edits (config.json, skills packs) land via git pull or SSH and apply **without a restart**:
- HTTP: `POST /api/v1/admin/reload` (token-authed) — genuinely re-reads config + skills and
  reports the result.
- Signal: `SIGHUP` (ops habit; the skeleton re-reads on next use).
- Daemon: the Transwarp socket's `refresh_config` / `refresh_skills` verbs now invoke a
  real reload hook (`Manager.OnRefresh`, wired in `resident.go`) instead of acknowledging
  and dropping.

## The seam (what moves where)

The client/server split the roadmap requires, in planned order — deliberately **not** a
big-bang refactor:

1. **State (done):** `history.SQLiteStore` is already a shared, stateless, multi-session
   store (WAL + single-writer via `SetMaxOpenConns(1)`) — the server opens the same
   `DataDir/history.db`. Queen gates clone per session (`app/session.go`).
2. **Loop (next):** the live chat loop is welded into `tui/chat.go` today; the server-side
   runner is `unimatrix.Session` — the headless agent/tool loop designed for exactly this
   ("TUI, daemon, schedule, and plan execution can all observe the same loop without
   owning it"), currently unused. The seam: a `TurnRunner` interface with in-process and
   server-backed implementations; the TUI migrates onto it only once the server proves
   the loop end-to-end (a `POST /api/v1/turn` headless endpoint is the natural first
   proof — it can land server-side *without touching the TUI*).
3. **Events:** `unimatrix.Session.OnEvent` (`history.Drone`s) is the server's event
   source; the Transwarp `WaitApproval`/`ResolveApproval` pair is the approval
   round-trip. Attach/resume needs an event backlog + fan-out — the current 10 s
   single-request socket can't carry a stream; the HTTP API replaces it.
4. **Control plane (exists):** the Transwarp daemon (`--towel` fork + `systemd-run`
   envelope + lifecycle verbs) stays the local control plane; this server is the
   execution plane beside it.

## Cron fleet aggregation (future)

Each system running DeepThought keeps a host-scoped `cron/registry.json` (first/last seen,
hashes, notes). The server's `/api/v1/crons` serves the local one; the fleet view is
agents reporting their registries (push on change + periodic pull) and the server merging
them — "manage huge workflows with many clusters" (roadmap #8/#10 adjacent). The registry
schema is versioned for exactly that merge.
