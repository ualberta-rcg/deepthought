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

- **Repository:** `ualberta-rcg/deepthought` (GitHub), this product lives in
  `deepthought-cli/` — the repo also holds a `deepthought-server/` scaffold and a
  root `CLAUDE.md` + `CHANGELOG.md` (the root CLAUDE.md carries repo-wide rules,
  incl. the changelog-before-commit rule — read it before committing).
- **Language:** Go. Module path **`deepthought-cli`** (`go.mod` exists).
  `go 1.25.9` in `go.mod`; the on-path system Go is `go1.22.2` and
  `GOTOOLCHAIN=auto` transparently fetches 1.25.x on build.
- **Phase:** v1 core is implemented. `cmd/deepthought-cli/` +
 `internal/{alcove,app,assimilation,babel,commands,config,history,historytools,keybindings,queen,residency,science,skills,slurm,tools,transwarp,tui,unimatrix}/`
 exist; the binary runs
  locally and serves over SSH (`--sub-etha :2323`). The shell is **chat-home with a
  navigation history stack**: splash (any key) → **New Chat** if an agentic model is
  configured, else → **Settings › Providers › + add**. F-keys/slash commands push a
  screen onto the stack; `esc` pops it (overlay first, then the screen stack, then
  **chat** = home, where esc is a no-op). So `settings → models(F3) → effort(e) → esc`
  walks models → settings → chat. While a turn streams, the 1st `esc` interrupts; the
  2nd (idle) is a no-op at chat. **Quitting is only** `/quit` (alias `/exit`, `/fish`)
  or **2× `ctrl+c`** — no q/esc-quit. Screens: **Chat** (home), a **drill-down settings
  editor** (incl. a new **General/Profile** section), **Continue** (the "which chat to
  rejoin" picker), **Grid** (`/vortex`), and a **Status page**. (The old hub is gone —
  `hub.go` is a stub.) The Status page (F12, `internal/tui/status.go`) is one unified,
  **scrollable** screen: Session (model·effort·mode·health·clock/date/tz), Providers
  (cached reachability from circuit breakers — no network), Models + per-model tokens
  (now populated for Anthropic-wire models too), Tools, Environment
  (CVMFS/module/Slurm/shell/host/user), and Cluster (nodes/CPUs/GPUs/mem/fairshare/
  storage/your jobs from a background `slurm.Snapshot` poller at login + every 5 min,
  so opening the page never blocks). **Settings/Continue/Status/Grid** use a full-terminal bordered frame
  with a pinned bottom **keybar**; **Settings** is a **two-pane drill-down**
  (lazygit/k9s-style: left = read-only section/entity tree; right = actionable list →
  inline field editor). **Overlay pickers** (`internal/tui/overlay.go`) float centered
  over the active screen and nest: the **Effort** picker (`/effort`, F4 — HHGTTG labels
  Autopilot→Infinite Improbability) and the **Model chooser** (F3 — switch the *running*
  model; press `e` to branch into Effort, which returns to the chooser). Splash shows the
  **DeepThought** mark + drifting rainbow, **"Don't Panic."**, a rotating HHGTTG subtitle,
  and a model·provider status line. F-keys: `F2` settings · `F3` model chooser · `F4`
  effort · `F5` new chat · `F6` resume · `F7` context grid · `F8` stats · `F9` permission
  mode · `F12` status (F10/F11 free). **Effort is the sole reasoning control** (F4 /
  `/effort`): off (Autopilot) = no thinking; any other level = thinking on at that
  level. Top bar (dark-grey band): rainbow `DeepThought · model [F3] · mode · effort [F4]`
  + responsive clock. Chat chrome: activity line above input + bottom status-line band.

  **Config v2** (`~/.deepthought/config.json`; see `configs/config.example.json`):
  `providers` (unlimited backends — name, base URL, API key or `$ENV_VAR`, wire
  `openai`|`anthropic`, free-form tags like local/external/usa/cad/china), `models`
  (each attached to a provider, with capabilities `chat`/`tools`/`reasoning`/`vision`
  /`embedding` + tags; "agentic" is derived = chat+tools), and `roles`
  (`chat`/`agentic`/`planning`/`summary`/`tombstone` → model ID; unset roles fall back
  to `chat`). Old single-`provider` v1 files migrate transparently on load and rewrite
  as v2 on the next save. The settings **editor** is a **two-pane drill-down**
  (`internal/tui/settings.go`): the left pane is a read-only section/entity tree; the
  right pane is the actionable list → entity → **inline field edit**
  (`internal/tui/settings_form.go`, a textinput for text fields + cyclers for enum/multi
  — no modal forms; the active field renders with a distinct green ▶ so the typed value
  is visible, not buried in the selected-row color). Every field commit **auto-saves**
  to disk immediately (atomic, mode 0600) and hot-swaps the running config with no
  restart via `app.Settings` (`Snapshot`/`Save`/`RoleClient`/`ClientFor`/
  `ProviderClient`). Mixed providers each get a pooled `babel.Client`
  (`unimatrix.Pool`). The editor also **tests a model** (`t` on the Models list — pings
  it and reports latency) and **lists models from a provider** (`L` on the Providers
  list — `GET {base}/models`, pick IDs to add; tolerant of providers that don't
  implement the endpoint). List rows are compact (`id · provider · caps`;
  `name · wire · tags`) — full URLs/detail live in the field editor.

  **Section roadmap** (the left tree; live vs placeholder — mapped from the Claude Code
  reference, `reference/src/utils/settings/types.ts` ~80 keys): live now = **Overview,
  Providers, Models, Roles, Permissions** (operation modes safe/safe-auto/auto + rule
  counts), **Appearance** (status-line toggle). Placeholders (select → "coming soon") =
  **Behavior** (thinking ✓, temperature, max-tokens), **Shell & Env** (default shell,
  extra env), **Memory, Skills, Tools, Clusters** (SSH/Slurm profiles), **Privacy**
  (telemetry opt-out). Future bigger systems: Hooks, MCP, Plugins, Keybindings.

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
  Destructive ops are still refused unconditionally. Gate is **per SSH session**
  (cloned) so task grants never leak. `esc` interrupts an in-flight turn (the
  stream's context is cancelled, the tool loop breaks, the turn is marked
  interrupted); a second `esc` once idle is a no-op at chat. `ctrl+c` twice within
  2s quits. The system prompt tells the model it's **DeepThought (not Claude)**, where
  its settings file is, and its SSH/HPC environment.

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

This box is a shared HPC login node, **not** a laptop. The org policy
(`/etc/claude-code/CLAUDE.md`) says "never compile on the login node; offload to a
job or `salloc`." **For this project the user has explicitly chosen to build on the
login node** for the dev loop; keep the footprint small: **module/build caches on
`$SCRATCH`, not `$HOME`** (`go env -w GOCACHE/GOMODCACHE` already set), and keep
compiles to this tree. An interactive `salloc` shell is the compliant
alternative if the node ever feels it.

- Editing source and `go mod`/`go get`/`go mod tidy` are always fine.
- Caches: `GOCACHE=$SCRATCH/deepthought-cli/gocache`,
  `GOMODCACHE=$SCRATCH/deepthought-cli/gomodcache` (set persistently via `go env -w`).
- Job I/O on `$SCRATCH`, not `$HOME` (50 GB home quota fills fast). SSH host key at
  `$SCRATCH/deepthought-cli/host_ed25519` — never commit it.
- Don't guess module versions or GPU types; don't scan the filesystem from root.

## Where to look

- Repo root [`CLAUDE.md`](../CLAUDE.md) — repo-wide rules: two-product layout,
  build/run pointers, and the changelog-before-commit rule.
- Repo root [`CHANGELOG.md`](../CHANGELOG.md) — running change record.
- Repo root [`docs/`](../docs/) — cross-cutting docs; app-specific docs belong in
  this directory (the old tree's `docs/` was intentionally not carried into the
  repo — add new docs here as they earn their keep).

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
