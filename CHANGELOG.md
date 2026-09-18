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
