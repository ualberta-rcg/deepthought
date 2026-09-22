# deepthought-cli

DeepThought is a standalone research-computing harness written in Go. The same
Bubble Tea terminal interface can run locally, in a resident session, or over
Wish SSH. The separate `deepthought-server/` Go module is additive; the CLI never
requires it or a model to start.

Named for Deep Thought in *The Hitchhiker's Guide to the Galaxy*. Keep the existing
Borg package terminology (`queen`, `unimatrix`, `assimilation`, `alcove`) and history
objects (Collective, Incursion, Transmission, Probe, Pattern, Synapse, Vinculum).

## Working agreement

Read the root [CLAUDE.md](../CLAUDE.md). Before **every commit**, add a dated root
CHANGELOG entry with the change, files, and verification. Update these instructions
and operational docs as decisions land. Never commit credentials or SSH host keys.

This is a shared Vulcan login node. Edit and format here; all vet, test, race and
build work runs in GitHub Actions. No login-node compilation or Slurm/CVMFS builds
for this repository. The server deployment is Kubernetes-backed; any manual server
operation is limited to the `deepthought` namespace. No deployment is implied by
editing the CLI. Work directly on main; never create branches. CI builds and
publishes a static Linux/amd64 binary after verification. There are no database
service containers in CLI CI.

## Current architecture

- `internal/app` owns RootModel (the only tea.Model), navigation, live Settings,
  client pooling, server login, and background discovery/observation commands.
- `internal/tui` owns concrete screen models. Bubble Tea v2 uses `View() tea.View`
  with `AltScreen = true`. Route asynchronous chat/model/cron results to their
  owners even when their screen is inactive. Reject stale catalog/turn results.
- `internal/config` owns defaults, validation, SQLite settings, legacy migration,
  documented layer precedence, reviewed discovery, and portable exports.
- `internal/credential` owns private process-local resolution and known-value
  redaction. Never place resolved credentials in snapshots, model context, status,
  logs, portable exports, or server settings. No shell sourcing for discovery.
- `internal/host` owns bounded local observations and persistent inventory;
  `internal/slurm` owns typed scheduler tools and conservative status caches.
- `internal/babel` adapts OpenAI and Anthropic transports and bounded provider
  catalogs. `internal/unimatrix` owns models, roles, policy routing and headless
  sessions. Model identity includes the provider; RequestID is the remote wire ID.
- `internal/history` stores the Borg graph and resumable chats in SQLite.
  `app_settings` and `app_inventory` share history.db through separate tables.
  Large history bodies can spill to content-addressed files. The server graph wire
  shapes are intentionally copied into its module; maintain compatibility.
- Tools, skills, scientific endpoints, Queen permissions, resident sessions,
  durable Slurm submission intents, and local workflows retain their existing
  package boundaries. Every executable tool remains permission-gated.

## Startup and settings

See [SETUP.md](../docs/SETUP.md) for the public contract. Default state is
`~/.deepthought`, overridden by DEEPTHOUGHT_CLI_HOME. Startup renders without a
provider, key, model, network, or server. Missing inference gates only model work.
Provider discovery runs after the first screen; candidates are reviewed before
adoption or connection. Setup can be skipped and reopened. Never execute shell
configuration, recursively scan, or send discovered values to a provider.

Settings is the configuration home. Ctrl+P and `/menu` expose every F-key action;
F1–F12 remain optional shortcuts and are editable in Settings. Chat is home, Esc
returns through overlays/screens, and quitting remains `/quit` or two Ctrl+C.
Inline text editors own their keystrokes. Operational jobs, plans, cron, status,
context, and host views belong in navigation rather than configuration forms.

Mutable settings use revision-checked SQLite writes and an atomic config.json
mirror. Preserve original JSON on migration and keep credentials in 0600 secrets.env.
--config selects the file/profile; it is not an immutable override of saved edits.
External file changes require reviewed import. Precedence is session choices →
supported environment overrides → saved settings → fleet defaults → built-ins.
Portable server settings are merged against a durable baseline and applied to the
database, mirror and live config through one save path. Do not reintroduce
fire-and-forget uploads or a permanently lower-priority user-settings cache.
Credential-source choice is explicit and separate from ordinary settings layers.
Concurrent stale revisions must not overwrite newer settings.

Aleph defaults to https://inference.vulcan.alliancecan.ca/v1 and discovers models
at /v1/models. Its verified Anthropic base is /anthropic. TYK_KEY commonly comes
from ~/.aleph_tyk.env; it may not exist immediately after first login. One key
covers all models and both protocols. Preserve custom endpoints; offer migration
only for recognized obsolete defaults. Use advertised capabilities, never guesses
from model names. Unknown capabilities remain user-configurable.

## Host context and privacy

The sidebar remains visible during chat when space permits; small terminals keep a
compact host strip. Status is the detailed view. Keep physical capacity, measured
utilization, cgroup limits, and job allocations distinct. Missing data is unknown,
not zero utilization. Fairshare is a scheduler factor, never queue position.

Keep client, host, cluster and service identities distinct. Store stable host
inventory separately from the latest telemetry snapshot. Installed commands do
not prove usable services. Queries are bounded, read-only, current-user scoped
where applicable, cached, and optional. Failures preserve honest freshness labels.
Execution context is concise JSON data, not instructions, with an explicit local
host/target and uncertainty labels. This release does not orchestrate other hosts.

## Conventions and existing reliability contracts

Use idiomatic Go, gofmt, wrapped errors and focused regressions. No panics in
request paths. Tools and transports stay separate. Settings stores expose fresh
snapshots; screens rebase every edit instead of saving stale copies.

Every Slurm submit/cancel asks. Plans do not auto-submit jobs. Queen mode/rules,
canonical argument matching, skill allowed-tools restrictions, interruption and
turn ownership remain enforced. Process loss marks work interrupted and never
silently replays it. History bodies and reasoning remain distinct; raw reasoning
is not replayed as conversation. Tool result shortening keeps protected content.

See [LOCAL-OPERATIONS.md](docs/LOCAL-OPERATIONS.md) for existing execution contracts,
[HOSTS.md](../docs/HOSTS.md) for refresh/identity scope, and the root CHANGELOG for
historical implementation notes. Older design/roadmap documents describe future
work and do not override the current startup and settings contract above.
