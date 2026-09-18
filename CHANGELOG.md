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
