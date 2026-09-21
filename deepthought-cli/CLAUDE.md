# deepthought-cli

An agentic system written in **Go**, served as a TUI **over SSH** — JARVIS-style, not
just a coding assistant. Users `ssh` in and land on a title screen; from there they
drive an LLM agent that can write and run code, operate HPC clusters (Slurm, modules,
schedulers), manage Proxmox virtualization, and help with scientific work — planning,
running tools, and executing shell commands under a permission gate.

> Named for **Deep Thought**, the supercomputer from *The Hitchhiker's Guide to the
> Galaxy* — it computed the Answer to the Ultimate Question (42) in 7.5 million
> years; the CLI keeps hunting the Question.
> All naming is Star Trek: component packages use Borg terminology (`queen` — the
> permission gate; `unimatrix` — the model catalog + inference core), and the
> conversation object model inside `internal/history` is fully Borg (Collective,
> Incursion, Transmission, Probe, Pattern, Synapse, Vinculum). CLI flavor is
> Hitchhiker's Guide. Keep those conventions when you add code.

## Status

### Reliability release (2026-09-20)

- Local resident sessions now own the same RootModel tool loop independently of
  terminal attachments. Ctrl+\\ detaches; attach restores screen/input/approvals.
  Process loss restores history as interrupted and never silently replays work.
- `/jobs` shows durable submission intents and reconciliation; every Slurm submit
  and cancel asks. `/plan` shows persisted sequential objectives, attempts,
  predicate validation and artifact staleness. Workflows do not auto-submit jobs.
- Settings merge built-in/server/local/session layers with source metadata; only
  local editing is exposed. The server-defaults interface has no active transport.
  Skill catalog refresh is live; allowed-tools narrows permissions for the turn.
- Explicit scientific manifests register schema-validated, permission-gated tools.
  OpenAPI import is unsupported. `/pin` protects selected probe output from
  automatic shortening and `/compact`.
- The operational contract and limitations are in [LOCAL-OPERATIONS.md](docs/LOCAL-OPERATIONS.md).
  Historical milestone notes below are retained as architecture history.

- Workspace file tools now provide read receipts, bounded literal search, previewed
  edits and atomic replacement. Their root is the launch directory; Bash remains
  separately permission-gated. `/compact`, `/doctor`, `/export <directory>` and
  `/memory [add <fact>|forget <id>]` are implemented locally.
- Live requests include root-to-directory project instructions and a bounded
  skill index. Grid displays the actual context manifest. Token counts are
  estimates, with reserved output capacity; overflowing required instructions
  cause a visible refusal instead of silent truncation.
- Privacy and clearance apply to role requests and configured routes; unknown
  pricing cannot satisfy a configured USD limit. Server settings remain future work.

- Welcome requires Enter on **Run standalone**. **Log in to server — coming soon**
  is informational. No model health request is made at startup.
- F1 opens Settings; F2 shows Help. Models is initialized with the live store.
- Async chat, model, and cron results are routed to their owners when off screen.
  Chat results carry collective/turn ownership; interruption cancels tools and
  invalidates pending approvals. Finish or interrupt a turn before changing chats.
- SSH connections isolate shell and Queen state. Shells recover after cancellation.
- Tools validate JSON Schema before execution. Denies precede grants; new saved
  grants match exact canonical arguments. Ambiguous compound shell calls ask.
- SQLite keeps distinct event identities and body revisions. JSONL migration
  retains resume data. Existing table columns and legacy JSON remain compatible
  with older running sessions; schema migration first backs up the database.
- Settings use revision checks and a file lock. Password fields are masked.
  Providers may explicitly set `anonymous: true` for endpoints without keys.
- See the root review report for remaining implementation stages; older milestone
  descriptions below include historical architecture and are not acceptance tests.

- **Repository:** `ualberta-rcg/deepthought` (GitHub), this product lives in
  `deepthought-cli/` — the repo also holds a `deepthought-server/` scaffold and a
  root `CLAUDE.md` + `CHANGELOG.md` (the root CLAUDE.md carries repo-wide rules,
  incl. the changelog-before-commit rule — read it before committing).
- **Language:** Go. Module path **`deepthought-cli`** (`go.mod` exists).
  `go 1.25.9` in `go.mod`; the on-path system Go is `go1.22.2` and
  `GOTOOLCHAIN=auto` transparently fetches 1.25.x on build.
- **Phase:** v1 core is implemented. `cmd/deepthought-cli/` +
 `internal/{alcove,app,assimilation,babel,commands,config,cvmfs,history,historytools,keybindings,queen,residency,science,skills,slurm,tools,transwarp,tui,unimatrix}/`
 exist; the binary runs
  locally and serves over SSH (`--sub-etha :2323`). The shell is **chat-home with a
  navigation history stack**: splash (Enter on Run standalone) → **New Chat** if an agentic model is
  configured, else → **Settings › Providers › + add**. F-keys/slash commands push a
  screen onto the stack; `esc` pops it (overlay first, then the screen stack, then
  **chat** = home, where esc is a no-op). While a turn streams, the 1st `esc` interrupts; the
  2nd (idle) is a no-op at chat. **Quitting is only** `/quit` (alias `/exit`, `/fish`)
  or **2× `ctrl+c`** — no q/esc-quit. Screens: **Chat** (home, with a live info sidebar on
  very wide terminals), the flat-tabbed **Settings** editor, **Continue**, **Grid**
  (`/vortex`), **Cron** (F8), **Models** (F11), and **Status** (F12).
  The **Status** page (F12, `internal/tui/status.go`) is one unified, scrollable,
  **detection-gated** page built on the **Section kit** (`internal/tui/section.go` — every
  block is a `Section{Title,Extra,Rows,Note,Source}` with bars/meters/chips): Session,
  Login node, Providers (state chips), Models, Usage (context meter), Tools always; Cluster
  / Your jobs / Fairshare / Your dirs only when Slurm (or storage rows) are detected — all
  fed by the background `slurm.Snapshot` poller. The **Cron** screen (F8,
  `internal/tui/cron.go`) manages the user's REAL crontab over `internal/cron` (staged
  edits, diff review, hard y/N gate, backups, first/last-seen tracking registry). The
  **Models** screen (F11, `internal/tui/models.go`) is the catalog's own home (edit, add
  from provider discovery, test, role toggles, delete). The chat gets a transient, clearly-labelled cluster blurb AND a ≤6-line **environment
  brief** each turn (host, slurm + GPU type, modules, fairshare, storage). Framed screens
  share one kit (`internal/tui/frame.go`): `screenTitle` ("DeepThought › X"), `KeyBar`
  chips, `emptyRow` vocabulary, ANSI-safe clipping, `overlayCenter` compositing. The
  **Settings** editor (`internal/tui/settings.go`) is a flat **tabbed** layout (General ·
  Providers · Roles · Perms · Theme · System; ←/→ or digits switch): every field commit
  re-bases on a FRESH config snapshot before saving (concurrent edits never clobber),
  "+ Add" stages in-memory drafts until valid, and the splash drops you straight at
  Providers › + add when nothing is configured. **Overlay pickers** (`overlay.go`) nest:
  the **Effort** picker (F4) and the **Model chooser** (F3). The splash is the big
  **DON'T PANIC** wordmark (go-figure standard, static cyan→violet brand gradient).
  F-keys: `F1` settings · `F2` help · `F3` model · `F4` effort · `F5` new chat · `F6`
  resume · `F7` grid · `F8` cron · `F9` mode · `F10` sidebar · `F11` models · `F12`
  status. **Effort is the sole reasoning
  control** (F4 /
  `/effort`): off = no thinking; any other level = thinking on at that
  level. Top bar (dark-grey band): solid `DeepThought` wordmark · model [F3] · mode ·
  effort [F4] + responsive clock (the rainbow is retired everywhere). Chat chrome: activity line above input + bottom status-line band.

  **Config v2** (`~/.deepthought/config.json`; see `configs/config.example.json`):
  `providers` (unlimited backends — name, base URL, API key or `$ENV_VAR`, wire
  `openai`|`anthropic`, free-form tags like local/external/usa/cad/china), `models`
  (each attached to a provider, with capabilities `chat`/`tools`/`reasoning`/`vision`
  /`embedding` + tags; "agentic" is derived = chat+tools), and `roles`
  (`chat`/`agentic`/`planning`/`summary`/`tombstone` → model ID; unset roles fall back
  to `chat`). Old single-`provider` v1 files migrate transparently on load and rewrite
  as v2 on the next save. The settings **editor** is the flat **tabbed** layout above
  (Overview · General · Providers · Roles · Routing · Perms · Theme · System):
  every field commit **re-bases on a FRESH config snapshot** before validate+save
  (concurrent edits — mode/effort switches — never clobber), "+ Add" stages in-memory
  drafts until valid, providers expose their advanced knobs (timeout/breaker/budget/
  clearance), Routing edits the declarative routes, and System renders the host
  descriptor. Inline fields (`internal/tui/settings_form.go`): textinput + enum/multi
  cyclers; the active field renders with a green ▶. Every commit **auto-saves**
  (atomic, 0600) and hot-swaps via `app.Settings` (`Snapshot`/`Save`/`RoleClient`/
  `ClientFor`/`ProviderClient`). Mixed providers each get a pooled `babel.Client`
  (`unimatrix.Pool`). The **Models** screen (F11) tests a model (`t`) and adds from
  provider discovery (`L` → `GET {base}/models`, pick IDs; tolerant of providers
  without the endpoint).

  **Still placeholders** (the dim roadmap line in Settings): shell & env, memory,
  skills, tools, privacy. Future bigger systems: Hooks, MCP, Plugins.

 The chat round-trip is **live and streaming with thinking** over OpenAI or
 Anthropic wire formats: non-slash input goes to
  the `agentic` role's model via `internal/babel` (OpenAI-compatible SSE to the Vulcan
  KServe gateway). Reasoning-capable models get `chat_template_kwargs.enable_thinking`
  (gated by the `thinking` config flag); the reasoning trace streams in a separate
  `reasoning` field, renders as a dim `∴` block, and is stored as a `Synapse` (never
  replayed into context). **The tool loop** (`bash`, `read`) is live under Queen
  operation modes (`safe` / `safe-auto` / `auto`, F9 cycles; migrated from
  `review`/`always-proceed`): category defaults + hybrid allow/ask/deny rules;
  approval choices are `y` once · `a` this-task · `A` always · `n` deny · `d` never.
  Destructive ops are still refused unconditionally. A read-only `skill` tool
  (auto-allowed) loads a skill's full instructions on demand — the available-skill
  *index* is injected as a per-request system note, bodies fetched only when used
  (progressive disclosure). Skills load from every install location: user
  (`~/.deepthought/skills`, `~/.claude/skills`, `~/.codex/skills`), project
  (`.deepthought-cli/`, `.claude/`, `.codex/`), and org-managed system roots
  (`/etc/claude-code/.claude/skills`, `/etc/deepthought-cli/skills`); symlinks are
  followed and deduped by real path. Gate is **per SSH session**
  (cloned) so task grants never leak. `esc` interrupts an in-flight turn (the
  stream's context is cancelled, the tool loop breaks, the turn is marked
  interrupted); a second `esc` once idle is a no-op at chat. `ctrl+c` twice within
  2s quits. The system prompt tells the model it's **DeepThought (not Claude)**, where
  its settings file is, and its SSH/HPC environment; each request also carries a
  transient cluster-status blurb and the available-skills index (neither persisted).

  **Slash commands** (chat popover): `/help`, `/model` (open the **Model chooser**
  overlay), `/effort` (open the **Effort** overlay), `/settings` (open the settings
  editor), `/resume` (resume an interrupted turn), `/quit` (alias `/fish`).

 **Chats persist to SQLite** (`history.db`, authoritative). `history.SQLiteStore`
  is a `ChatStore` (`Store` + `Resume` + `ListCollectives`); the chat programs against
  a `ChatStoreSource` factory (FileStore needs a fresh single-active instance per chat;
  SQLiteStore is stateless and shared). Every graph object is wrapped as a stateful
  **Drone** at save (`legacyEntityDrone`): `State` (FULL/DIGEST/LINE/TOMBSTONE/ELIDED
  residency), `Outcome` (derived from lifecycle status), `Producer` (the model that drove
  the turn), `Cost` (token usage from `babel.Usage`), `Sensitivity`, and `Links` — in
  structured columns AND the `legacy_json` body. Bodies over 1 MiB spill to
  content-addressed files. After the first exchange the **summary model** generates a
  pretty ≤64-char title (stored on `Collective.Title`); the **Continue** screen lists
  chats by title and resumes one into the full graph. **JSONL is export-only** now
  (`SQLiteStore.Export`; `MigrateLegacyChats` imports legacy JSONL once).

 The chat history is the Borg graph in `internal/history`: `Collective` → `Incursion`
  → `Transmission` → `Probe`, plus `Pattern` (learned facts), `Synapse` (thinking
  blocks), and a base `Vinculum` on every node (IDs, parent/sibling links, age,
  cross-object `Links`, residency `State`). The first Transmission is linked
  bidirectionally to its Incursion (`response_to`/`spawned`). It flattens to
  `[]babel.Message` only at request time via `Collective.MessagesByState()` — each probe
  renders at its own residency (Full today; a demoted probe renders shorter).
  **Demotion is a callable seam, not automatic:** `history.DemoteProbe(store, collID,
  probeID, to)` fills the deterministic summary, sets the State monotonically, and
  persists; a future Queen AI thread (using `internal/residency` + `internal/assimilation`
  + `SummaryEngine`) decides what to demote.

 V1 also includes `unimatrix.Session` (headless loop), Assimilation budgeting
 with manifest Drones and `expand`, deterministic Queen residency, a persistent
 Alcove shell, capability/sensitivity routes, generic ToolServer catalogs,
 structured Slurm tools and reconciliation, layered Claude-compatible skills,
 Directive/Objective/Attempt/Artifact provenance, and user-space resident mode.
 `deepthought-cli --towel` plus `ls`, `attach`, and `stop` speak the closed
 Transwarp Unix socket protocol; it has no shell field.

## Running environment (Vulcan login node)

This is a shared HPC login node. **CI/CD is the build system**: the workflows
vet, test (MySQL store tests run against a service container), race-check, and
build the artifacts. Never build on the login node, and **never use Slurm or
CVMFS/modules** — the compute is kube-backed and has no CVMFS. The dev loop is
edit → push → CI.

- Editing source and `go mod`/`go get`/`go mod tidy` are always fine.
- The **only manual deploy step** is applying the server-side manifests on the
  aleph1 control-plane (namespace `deepthought` only), bumping the image tag to
  the CI-built one.
- Job I/O on `$SCRATCH`, not `$HOME` (50 GB home quota fills fast). SSH host key at
  `$SCRATCH/deepthought-cli/host_ed25519` — never commit it.
- Don't guess module versions or GPU types; don't scan the filesystem from root.

## Where to look

- [`docs/ROADMAP.md`](docs/ROADMAP.md) — the research-copilot capability roadmap (vision; much
  is future work pending the server side).
- [`docs/SERVER.md`](docs/SERVER.md) — the server-side architecture + the live skeleton
  (`cmd/deepthought-server`; destined for deepthought.vulcan.alliancecan.ca).
- [`docs/DESIGN.md`](docs/DESIGN.md) — the design quarry: environment descriptor,
  prompt assembly, plans/attempts/blessing, triggers (Phase A = the host descriptor).
  (`cmd/deepthought-server`; destined for deepthought.vulcan.alliancecan.ca).
- Repo root [`CLAUDE.md`](../CLAUDE.md) — repo-wide rules: two-product layout,
  build/run pointers, and the changelog-before-commit rule.
- Repo root [`CHANGELOG.md`](../CHANGELOG.md) — running change record.
- Repo root [`docs/`](../docs/) — cross-cutting docs; app-specific docs belong in
  this directory (the old tree's `docs/` was intentionally not carried into the
  repo — add new docs here as they earn their keep).

## The client runs standalone

The CLI is the product; the server (docs/SERVER.md) is **additive**. No client code path
dials, waits on, or requires the server — nothing under `internal/` or `cmd/deepthought-cli`
imports or configures it. The server ships in the same module purely for the build, and
every roadmap capability lands client-side first where feasible.

## North stars

- **Terminal-first, SSH-served.** One binary; Wish is the SSH server; no OpenSSH dep.
  Each session is its own Bubble Tea program with the pty wired in.
- **The loop is small; the value is harness quality.** Context management, permission
  gating, clean diffing, streaming UX. Optimize for that, not feature count.
- **Pluggable backend behind the Babel adapter** — Anthropic API *or* any
  OpenAI-/Anthropic-compatible endpoint.
- **Repo-aware + safe-by-default.** Every fs/shell/network tool goes through Queen.
  No exceptions.

## Conventions

- Idiomatic Go; `gofmt`/`goimports`; table-driven tests; wrap errors with `%w`; never
  `panic` in the request path — `recover()` at the session boundary.
- One sub-model per screen state (splash, menu, chat, …) in **`internal/tui/`**; the
  **root model in `internal/app/` is the only `tea.Model`** and routes `Update`/`View`
  to the active screen. Sub-models return their *concrete* type from `Update` (no type
  assertions). v2: `View()` returns `tea.View`; alt-screen is declarative
  (`v.AltScreen = true`), never `tea.WithAltScreen()`. Store width/height on the root
  from `tea.WindowSizeMsg`.
- Loop, tool registry, and provider/transport live in separate packages.
- No secrets in the tree. API keys from the env (`ANTHROPIC_API_KEY` etc.).
- `CGO_ENABLED=0` where possible — one static binary.

## Working agreement

- This file is the CLI's source of truth for its own layout and conventions.
  Update it as decisions land — module path, package layout, config format.
- **Before every commit, add a dated entry to the root `CHANGELOG.md`** (repo
  rule, enforced by the root `CLAUDE.md`).
- Don't silently change direction recorded here; if a north star shifts, edit the
  file to match and say so.
