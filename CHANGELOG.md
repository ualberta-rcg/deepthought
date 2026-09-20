# DeepThought — Change Log

Single running record for the whole repo, newest entry at the top. Entries are
dated (absolute, UTC) and **tagged with the app directory(ies) they touch**.

**Rule:** every commit adds an entry here describing what it changes, the files
touched, and how it was verified. A commit without a changelog entry is
incomplete. (Enforced by the repo `CLAUDE.md` — read it before committing.)

Entry format:

```
## YYYY-MM-DD · <app> — short title
- what changed
- files touched
- how verified
```

---

## 2026-09-20 · deepthought-cli — use neutral thinking-level labels

The effort (thinking-level) labels were HHGTTG-flavored (Autopilot → Common Sense → Pondering →
Deep Thought → Infinite Improbability). Per the move away from themed naming, they're now plain.
- `effort_overlay.go` effortLevels → Off / Low / Medium / High / Max; flows through `EffortLabel`
  to the top bar, the Status Session row, and the F4 picker automatically.
- Files: `effort_overlay.go`, `effort_overlay_test.go` (new `TestEffortLabelsNeutral`),
  `chat.go` (comment). (The CLAUDE.md label wording lands with the docs commit.)
- Verified: `TestEffortLabelsNeutral` passes; full `go build`/`vet`/`test` green.

## 2026-09-20 · deepthought-cli — standardize the F12 Status page on one Section kit + meters/chips

The Status page rendered in two visual languages: the six plain sections used
`styleSettingsTitle` + `kv()` (values unaligned), the four Slurm sections were near-verbatim
`vulcan-status` blocks. Now every section is one reusable `Section` (header + aligned body +
optional note + optional "→ cmd" source) built from a small viz kit, so the page reads as one
surface — and the plain sections gain the data-driven meters/chips the Slurm ones already had.
- **New `internal/tui/section.go`:** `Section{Title, Extra, Rows, Note, Source}` + `Render()`, and
  the viz kit — `healthChip` / `stateChip` / `detChip` (one-line colored tokens, no border) and
  `contextMeter` (a `healthBar` meter that degrades to a plain count when the model declares no
  context window).
- **Aligned label column:** `kv` now pads the raw label to a fixed width *before* styling so
  values line up.
- **Session:** health is a `healthChip`. **Login node:** the detection line uses `detChip`.
  **Providers:** state uses `stateChip` (degraded is now amber, not red). **Usage:** a new
  context meter (`▓▓▓░…  NN%  used/window`) driven by the active model's `Model.Context`.
- **cluster.go:** the four Slurm renderers build a `Section` too (same data/output, one idiom).
  `renderState` removed (superseded by `stateChip`); Models token totals use `formatTokens` to
  match the Usage section.
- **Cleanup:** deleted the dead single-color `bar()` from styles.go; fixed the stale "Cluster
  screen (F10)" comment in bars.go.
- Files: `section.go` (new), `status.go`, `cluster.go`, `styles.go`, `bars.go`,
  `section_test.go` (new).
- Verified: new `TestSectionRows` / `TestStateChip` / `TestHealthChip` / `TestDetChip` /
  `TestContextMeter` / `TestUsageContextMeter` pass; existing `TestStatusAdaptiveSections` and
  `TestClusterSectionRenderers` still pass; full `go build`/`vet`/`test` green; grep gate confirms
  no `renderState` / single-color `bar()` / `styleSettingsTitle`-in-status remain. Eyeballed a
  rendered page (Slurm snapshot): all sections share the look; the context meter and chips render.

## 2026-09-18 · deepthought-cli — adaptive F12 Status: merge F8 (Stats) + F10 (Cluster) into it; free those keys

One status screen instead of three, and it **adapts to the host**. The page is
now an ordered **section registry** — each block shows only when a predicate says
its data is detected, so adding a section is one entry in a slice (no new screen).
- **Always-on sections:** Session (model·effort·mode·health·clock), **Login node**
  (new: host·user·shell·os/arch·tz + a `cvmfs ✓ · module ✓ · slurm ✓` detection
  line), Providers, Models + per-model tokens, **Usage** (ex-F8: this-session
  in/out/total/context/rounds/est.-cost + lifetime per-model), Tools.
- **Detection-gated sections:** Cluster (nodes/CPUs/mem/GPUs/queue), Your jobs,
  Fairshare (per-account bars + LevelFS) all show **only when Slurm is detected and
  the snapshot has gathered**; the **Your dirs** (home/scratch/projects) disk bars
  show when storage rows exist. `vulcan-status` was the reference for *what data to
  show and how to make it legible* (bars, plain-language notes) — not a screen to
  clone; those renderers were extracted as free functions Status calls.
- **Keys freed:** F8 (Stats) and F10 (Cluster) are gone; F12 Status is the single
  status screen. The `stats.go` `StatsModel` and the `cluster.go` `ClusterModel`
  screen are deleted (only their data renderers survive, in `cluster.go`).
- Files: `internal/tui/status.go` (section registry + new Login-node/Usage
  sections + `SetSession`), `internal/tui/cluster.go` (screen → free block
  renderers), `internal/tui/cluster_test.go`, `internal/tui/status_test.go`
  (new: adaptive-section gating), `internal/tui/stats.go` (deleted),
  `internal/tui/nav.go` (drop `ScreenStats`/`ScreenCluster`),
  `internal/tui/topbar.go` + `topbar_test.go` (legend drops F8/F10),
  `internal/keybindings/keybindings.go` (drop `Usage`/`Cluster` + f8/f10 defaults),
  `internal/app/model.go` (drop `statsScr`/`clusterScr`, redirect session feeding
  to `statusScr`, remove the two screen cases + key actions + F1 help line).
- Verified: new `TestStatusAdaptiveSections` (no-Slurm shows only always-on;
  Slurm + full snapshot shows all; Slurm with no fairshare/storage rows drops just
  those two) + repointed `TestClusterSectionRenderers` pass; full `go
  build`/`vet`/`test` green; grep gate confirms no `clusterScr`/`statsScr`/
  `ScreenCluster`/`ScreenStats` refs and no `"f8"`/`"f10"` bindings remain.
  (Live Slurm vs. non-Slurm rendering needs a real pty on a login node.)

## 2026-09-18 · deepthought-cli — fix F11 Software search (couldn't type)

The Software screen's search box silently dropped every keystroke — "you can't
type, the search doesn't work."
- **Root cause:** bubbletea v2 `textinput.Update` returns early when `!m.focus`, and
  `textinput.New()` defaults to `focus:false` with `Focus()` as a *pointer* receiver.
  `NewSoftwareModel` never called `Focus()`, and `SoftwareModel.Init()`'s
  `m.input.Focus()` ran on a discarded value copy (no-op). Chat works because
  `NewChatModel` calls `ti.Focus()` at construction *before* storing the input — the
  software screen simply missed that. Fix: `ti.Focus()` in `NewSoftwareModel`.
- **Layout:** the search field was wrapped in a 3-row bordered box that overflowed
  the frame by rows and, more subtly, rendered 7 cols wider than the frame's content
  area (it wrapped). Replaced with a single-line field sized to the content width,
  and corrected the frame to the sibling `AppScreen` width (`w-4`) with content
  `w-8`. The frame now renders exactly `h` rows, no wrap.
- Files: `internal/tui/software.go`, `internal/app/model.go` (cursor comment),
  `internal/tui/software_test.go`.
- Verified: new `TestSoftwareInputFocused` (regression) + a frame-fits check (exact
  row count, `w-4` width, no search wrap) pass; full `go build`/`vet`/`go test` green.
  (Typing + the visible caret still need a real pty to eyeball.)

## 2026-09-18 · deepthought-cli — wire the skills loader into the running app

Skills were loaded-and-tested but never surfaced. Now they are, with the same
**progressive disclosure** the loader was designed for: only a compact index goes
into every request; a skill's full body is fetched on demand.
- **`skill` tool** (`internal/tools/skill.go`, read-only so Queen auto-allows it):
  `skill(name)` returns that skill's body; `skill()` (no name) lists the
  available ones. It is deliberately decoupled from the `skills` package (which
  imports `config` → … → `tools`, an import cycle) via an injected names list +
  lookup callback, wired in `main`.
- **System-prompt index**: `skills.Listing()` renders one line per skill (name,
  description, collapsed when-to-use). Loaded once at startup from every install
  location, handed through `Deps.Skills`, stored on `ChatModel` (`SetSkills`), and
  appended as a transient system message in `requestMessages` (like the cluster
  blurb — sent to the model, not persisted; works for new + resumed chats).
- **Symlink fix**: the loader used `entry.IsDir()`, which is false for a symlink
  to a directory — so skills installed as symlinks (as `~/.codex/skills` does,
  pointing at the org copies) were silently skipped. Now `os.Stat` (which follows
  symlinks) decides dir-vs-file, and `EvalSymlinks` dedups a symlink against its
  target by real path (found once, at the higher-precedence layer).
- Files: `internal/tools/skill.go`, `internal/skills/listing.go`,
  `internal/skills/loader.go`, `cmd/deepthought-cli/main.go`,
  `internal/app/model.go`, `internal/tui/chat.go` (+ tests).
- Verified: live check on this host loads the 3 alliance packs and renders the
  index; new tests `TestListing`, `TestSkillTool*`, `TestSymlinkedSkillDiscovered
  AndDeduped` pass; `gofmt`/`go build`/`vet`/full `go test` green.

## 2026-09-18 · deepthought-cli — docs: F-key map + new screens in the CLI CLAUDE.md

- `deepthought-cli/CLAUDE.md`: the F-key line now reads `F10 cluster · F11 software ·
  F12 status` (was "F12 status, F10/F11 free"); the screen list gains the dedicated
  **Cluster** (F10) and **Software** (F11) pages; the Status (F12) description now
  says its cluster section is a one-line summary pointing to F10; a sentence covers
  the transient chat cluster blurb. The internal-package line gains `cvmfs`.
- Files: `deepthought-cli/CLAUDE.md`.
- Verified: grep gate — `ScreenCluster`/`ScreenSoftware` each wired exactly once in
  `app/model.go` (Update/View/activeInit/handleAction) + the `nav.go` enum;
  `"f10"`/`"f11"`/`"f12"` bound in `keybindings.go`; root `CLAUDE.md` has no stale
  F-key/cluster text. `make check` green.

## 2026-09-18 · deepthought-cli — F11 Software screen: searchable CVMFS modules

- **New `internal/cvmfs/` package** (mirrors `internal/slurm/`): `Detected()`
  (cached; cheap `MODULESHOME` env check, then a login-shell `type module`
  probe), and a `Client` with a `Runner` seam that runs `module spider …`
  **headlessly via `bash -lc`** — `module` is a shell function, not a binary.
  `Spider(name)` → versions + related matches; `SpiderDetail(name,ver)` → the
  "You will need to load" prerequisite lines + a copyable `module load …` line
  (handles the no-dep "can be loaded directly" case). A module name is
  validated against a conservative charset **before** it is interpolated into a
  shell command line (no metacharacter injection). "Unable to find" is a normal
  outcome, not an error.
- **F11 = Software screen** (`internal/tui/software.go`, `ScreenSoftware` already
  reserved in `nav.go`). A focused search box runs `module spider` in a
  background `tea.Cmd` (the UI never blocks on CVMFS); results list the versions
  with a `↑/↓` cursor, and Enter opens the exact load line for the picked version.
  Static **Common stacks / CVMFS roots / Notes** reference blocks are drawn from
  the alliance-cvmfs skill. Graceful: no Lmod → a single hint line. Typing at any
  point starts a fresh search; `esc` goes back.
- Bound `"f11": Software` in the keybinding defaults (the `keybindings.Software`
  action and `ScreenSoftware` were already declared); wired through the root
  (field/init/Update/View/activeInit/handleAction, resize).
- Files: new `internal/cvmfs/{cvmfs,cvmfs_test}.go`,
  `internal/tui/software.go`, `internal/tui/software_test.go`,
  `internal/app/model.go`, `internal/keybindings/keybindings.go`.
- Verified: parser tests table-driven on **real captured Lmod output** (versions,
  dep lines, no-dep, not-found); a live smoke run confirmed `cuda` → 5 versions,
  `cuda/13.2` → `module load StdEnv/2023 gcc/12.3 cuda/13.2`, missing module →
  graceful; TUI tests cover reference blocks, results, detail, not-found, and
  the no-modules degradation. `gofmt`/`go build`/`vet`/full `go test` green.

## 2026-09-18 · deepthought-cli — skills loader scans codex, claude-project, and system roots

The skills `Loader` now discovers packs from the other tools' standard locations
and the org-managed system dir, so a researcher's existing Claude/Codex skills are
reused rather than duplicated:
- **New roots** (all first-wins dedup by name, highest-precedence wins): personal
  codex `~/.codex/skills`, and project `.claude/skills` / `.codex/skills` alongside
  the existing project `.deepthought-cli/skills`. Precedence is now
  user › user-claude › user-codex › project › project-claude › project-codex ›
  system › site.
- **`DefaultSystemRoots()`** — org-managed locations scanned at the lowest
  precedence: `/etc/claude-code/.claude/skills` (where the alliance-* packs live on
  a managed node) and `/etc/deepthought-cli/skills`. Exposed as a `SystemRoots`
  field on `Loader` (set by default in `NewLoader`), so callers/tests can override.
- Nonexistent roots are skipped (no behaviour change for hosts without them).
- Files: `internal/skills/loader.go`, `internal/skills/loader_test.go`.
- Verified: new `TestCodexClaudeAndSystemRoots` (codex-beats-system dedup,
  system-only discovery, project-claude discovery) passes; the two prior loader
  tests now pin `SystemRoots = nil` to stay hermetic from the host's live org
  skills; `go build`/`vet`/full `go test` green.
- Note: the `skills` package is still **not wired into the running app** — this
  only widens where the loader would look once it is invoked.

## 2026-09-18 · deepthought-cli — F10 Cluster screen, slim F12, AI cluster blurb

- **F10 = dedicated Cluster screen** (`internal/tui/cluster.go`, reviving the
  old empty file + unbound `keybindings.Cluster` action; `ScreenCluster` in
  `nav.go`). Renders the live snapshot in the `vulcan-status` idiom via new
  `internal/tui/bars.go` helpers: `▓░` bars where fill-length is primary on a
  colorblind-safe palette (`healthBar` teal→orange→bold-red, `diskBar`,
  `tierColor`), `»` section headers (`sectionHead`), a dim "what this means"
  line under each block (`dimNote`), and dim `→ run: X` hints (`hintNote`).
  Blocks: Cluster (nodes/queue/CPUs/mem/GPUs incl. avail·usable), Your jobs
  (with hold reason), Fairshare (per-account bar + tier + LevelFS), Storage
  (per-mount bars). Graceful: no Slurm → a single hint line; `r` re-polls.
  Wired through the root (field/init/Update/View/activeInit/handleAction,
  snapshot fan-out, resize).
- **F12 Status slimmed** — its detailed cluster section is now a one-line
  summary + "F10 → Cluster for detail" (kills the duplication / bad label).
- **AI cluster blurb** — the chat appends a transient, clearly-labelled
  `[Cluster status … may be stale]` system message to each request
  (`chat.go` `clusterBlurb`, injected in `requestMessages`, not persisted) so
  the model knows GPU/fairshare/queue/scratch state without running `squeue`.
  The root caches the latest snapshot and seeds freshly-built/resumed chats.
- F1 help notice now lists F10 cluster · F11 software.
- Files: `internal/tui/{cluster,cluster_test,bars,status,nav}.go`,
  `internal/app/model.go`, `internal/tui/chat.go`,
  `internal/keybindings/keybindings.go`.
- Verified: `gofmt -l` clean, `go build`/`vet`/full `go test` green; new tests
  `TestClusterScreenRendersBlocks`, `TestClusterScreenNoSlurm`,
  `TestClusterBlurb` pass.

## 2026-09-18 · deepthought-cli — enrich `slurm.ClusterSnapshot` to MOTD richness

The background snapshot (polled every 5 min) now captures the details the
cluster screen needs, matching `/usr/local/bin/vulcan-status`:
- **`FairshareRows []FairshareRow`** — every account the user belongs to with
  its factor + LevelFS (`gatherFairshareRows`, `sshare -ahP -o
  Account,User,Fairshare,LevelFS`). Plus `FairshareTier(f)` (boosted/ahead/
  nominal/behind/throttled at 0.80/0.60/0.40/0.20) and `LevelFSTier(v)`
  (good/nominal/bad at 1.25/0.75) helpers.
- **`GPUUsable int`** — host-feasible GPU count: per node,
  min(free gpus, free CPU/cpus-per-gpu, free mem/mem-per-gpu), summed from
  `scontrol show nodes` TRES (`gatherGPUUsable`; ported from the MOTD's awk).
  A free GPU on a CPU/RAM-starved node counts as 0.
- **`StorageRows []StorageRow`** — home/scratch/projects usage parsed from
  `df -h -P` for bars (`gatherStorageRows` → pure `parseStorageRows`, split out
  for testability). The raw `diskusage_report` rows stay for the hint line.
- New `gresGPU`/`cpuFromTRES` TRES parsers.
- Files: `internal/slurm/client.go`, new `internal/slurm/cluster_test.go`.
- Verified: `go build`/`vet` clean; new table tests pass (tier boundaries +
  clamp, LevelFS bands, TRES parsing, GPU-usable 2+0+4 fixture, fairshare user
  filter, storage positional parse).

## 2026-09-18 · deepthought-cli — F-key legend row in the top bar (toggleable)

- Added a dedicated 2nd top-bar row listing all 12 F-keys
  (`F1 help · F2 settings … F10 cluster · F11 software · F12 status`), shown
  above the chat. Degrades gracefully on narrow terminals (drops labels, then
  truncates; never wraps). F10/F11 preview the cluster + software screens that
  land in the following commits.
- Toggle: new `config.Appearance.TopBarLegend` (`*bool`, nil = on) with
  `File.TopBarLegendOn()` defaulting to true; a live cheap accessor
  `Settings.TopBarLegend()`; and an on/off field in Settings › Appearance
  (new `ensureAppearance`). Hiding it reclaims the row.
- Row accounting: `ChatChromeHeight(legend bool)` (was no-arg) — the root and
  the SSH PTY seed now subtract top + optional legend + bottom.
- Files: `internal/config/config.go`, `internal/app/settings.go`,
  `internal/app/model.go`, `internal/app/session.go`,
  `internal/tui/{topbar,topbar_test,styles,settings}.go`,
  `configs/config.example.json`.
- Verified: `go build`/`go vet` clean; new tests `TestChatChromeHeightLegendToggle`
  (3 vs 2 rows) and `TestRenderKeyLegendRow` (full/keys-only/truncate, exact
  width, zero-width no-crash) pass.

## 2026-09-18 · deepthought-cli — naming fix-up + unify user state under `~/.deepthought/`

- **One directory per home**: new `config.BaseDir()`
  (`$DEEPTHOUGHT_CLI_HOME` → `~/.deepthought`) holds everything —
  `DefaultPath()` = `BaseDir()/config.json`, `DataDir()` = `BaseDir`. The two
  XDG derivations and `DEEPTHOUGHT_CLI_CONFIG`/`DEEPTHOUGHT_CLI_DATA` are gone,
  replaced by the single `DEEPTHOUGHT_CLI_HOME`. User skills now load from
  `BaseDir()/skills` (`skills.Loader.BaseDir`); project skills stay per-project
  at `<root>/.deepthought-cli/skills`.
- **Latent data-loss fix**: `config.writeSecrets` ignored the existing-secrets
  load error, so a failed read followed by a write would have clobbered
  previously imported secrets — it now aborts the merge on read failure
  (missing file still means "none yet").
- **Naming/comment fix-ups**: "an DeepThought" → "a" (alcove), two stale hub
  comments (chat, model_chooser), three broken `docs/ARCHITECTURE.md` pointers
  (babel, queen, unimatrix), stderr prefix `deepthought-cli daemon:` →
  `deepthought-cli:` (resident), dead Makefile snapshot excludes
  (`cowabunga`/`render`/`smoke`), stale "Phase N" comments reworded to
  describe behavior.
- **Once-over cleanups**: deleted the empty `internal/tui/hub.go` stub;
  `gofmt`'d the tree (13 files had drifted during the rename); the transwarp
  stale-socket removal and the alcove teardown now document their intentional
  error ignores; a failed Queen-mode persist surfaces in the chat notice
  ("…(not saved: …)") instead of being swallowed.
- **Tests**: `TestDefaultPathEnv` → `TestBaseDirEnv` + new `TestBaseDirDefault`;
  skills layering test updated to the `BaseDir`-based user root.
- Files: `internal/config/{config,config_test,import}.go`,
  `internal/skills/{loader,loader_test}.go`, `internal/app/model.go`,
  `internal/alcove/shell.go`, `internal/transwarp/server.go`,
  `internal/tui/{chat,model_chooser}.go`, `cmd/deepthought-cli/{main,
  resident}.go`, `Makefile`, `README.md`, `deepthought-cli/CLAUDE.md`;
  deleted `internal/tui/hub.go`. Comment-only: `internal/{babel,queen,
  unimatrix,history}/…` and `cmd`/`app`/`config` phase notes. Formatted via
  `gofmt`: 13 files that had drifted during the rename.
- Verified: `gofmt -l` empty; `go build ./...`, `go vet ./...`,
  `go test ./...` all green; grep gates for old paths/env vars/hub refs clean.

## 2026-09-17 · repo — two-product scaffolding

- Root `CLAUDE.md`: repo layout, the changelog-before-commit rule, env and
  conventions notes, build/run pointers.
- Root `README.md` rewritten as the two-product overview; `docs/README.md` and
  `deepthought-server/README.md` placeholders added.
- Removed session scratch from the repo: `NOTES.md`, `.write_test`.
- Files: `CLAUDE.md`, `README.md`, `docs/README.md`,
  `deepthought-server/README.md`, `NOTES.md` (−), `.write_test` (−).
- Verified: tree grep shows no former-name references outside changelog
  history; CLI still builds/tests green (unchanged in this commit).

## 2026-09-17 · deepthought-cli — rename annorax → deepthought-cli

- Module `annorax` → `deepthought-cli`; all internal imports rewritten;
  `cmd/annorax/` → `cmd/deepthought-cli/`.
- Env vars: `ANNORAX_{CONFIG,DATA,PERSIST,TEST_KEY,Z_AI_API_KEY}` →
  `DEEPTHOUGHT_CLI_*`; shell marker `__ANNORAX_`/`__annorax_rc` →
  `__DEEPTHOUGHT_CLI_`/`__deepthought_cli_rc`.
- Runtime paths: config `~/.config/deepthought-cli/`, data
  `~/.local/share/deepthought-cli/`, Transwarp socket `deepthought-cli.sock`,
  temp root `deepthought-cli-<uid>`, skills dirs `.deepthought-cli/`, skills
  marker `DEEPTHOUGHT_CLI.md`.
- Display: splash/topbar/screen titles/system prompt/"exit" verb now say
  **DeepThought** (HHGTTG supercomputer); stderr prefix `deepthought-cli:`.
- Makefile: build `./cmd/deepthought-cli`, deploy to
  `$(SCRATCH)/deepthought-cli/deepthought-cli`, snapshots to
  `~/deepthought-cli-snapshots`.
- `CLAUDE.md` retitled and re-lore'd (Deep Thought, HHGTTG); "Where to look"
  now points at the repo-root files (the old `docs/` were not carried over).
- Splash wordmark is 11 colossal rows for the longer name; `TestWordmarkHeight`
  and layout comments updated (the layout itself was already height-generic).
- Files: 63 Go files, `go.mod`, `Makefile`, `CLAUDE.md`.
- Verified: `go build ./...`, `go vet ./...`, `go test ./...` all pass;
  `grep -ri annorax` over the tree returns nothing outside this changelog's
  history.

## 2026-09-17 · deepthought-cli — move CLI in verbatim

- Copied the working tree from `/home/rahimk/annorax` into `deepthought-cli/`
  unchanged (module still `annorax`, binary still `annorax`). Code only: `cmd/`,
  `internal/`, `configs/config.example.json`, `go.mod`, `go.sum`, `Makefile`,
  `CLAUDE.md`. The old `docs/`, `reference/`, `.tmp/`, and dot-dirs were left
  out on purpose.
- Added repo-root `.gitignore` and this `CHANGELOG.md`.
- Files: `deepthought-cli/**`, `.gitignore`, `CHANGELOG.md`.
- Verified: `go build ./...` passes on the copy (exit 0); no binaries, `*.db`,
  or key material in-tree.
