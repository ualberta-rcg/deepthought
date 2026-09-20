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

## 2026-09-20 · deepthought-cli — dead code out, overlays composite for real, last guards

- **Overlays finally float over the active screen:** RootModel.View composites
  `overlay.View()` through `OverlayCenter` (now exported) — the old version placed the
  card on an empty canvas, hiding the screen underneath while claiming to float. Added a
  tiny-parent regression test.
- **Dead code removed:** the fully orphaned `MenuModel` (+ menu.go; the `--no-splash` help
  no longer claims a menu screen exists — it starts straight in chat), `TwoPane` (zero
  callers since the flat Settings redesign), the unused `styleTab`/`styleTabActive` pair,
  Settings orphans (`tabKey()`, the `modelFieldDefs()` method wrapper, `modelExists`, the
  empty leftover section header, the `fieldRows`≡`entityRows` duplicate now delegating),
  Chat orphans (`onOffValue`, `slashEcho`, `SessionUsage`, `SessionStats`) — and the
  now-unused `styleSlashEcho`.
- **Guards:** Status `sessionRows` handles a nil store (F12 used to panic on one);
  the Models/Settings/Cron editors all close on their exit paths.
- Verified: grep gates — no `MenuModel|TwoPane|styleTabActive|onOffValue|slashEcho`
  references; `OverlayCenter` has its production caller in app/model.go; full suite green
  (20 pkgs).

## 2026-09-20 · deepthought-cli — theme/chrome truth sweep: real commands, real labels, one note style

Everything the UI claims is now true, and the one-off styles are gone.
- **Command registry reconciled:** the popover suggested ~10 commands submit() rejected
  (/theme, /cost, /doctor, /clear, /rewind, /keybindings, /expand, /pin, /annotate —
  gone); `/status` `/models` `/cron` navigate to their screens (F12/F11/F8 equivalents,
  joining /settings and /context). `/help` and the F1 notice are accurate (F1 now lists
  F10 sidebar, calls F7 grid — and switches to chat first so the notice is actually
  visible from any screen).
- **F-key labels derive from the real bindings:** `functionKeyLabel` reads
  `keybindings.DefaultAction` (new export) — the System tab's reference said F7 context /
  F8 free / F10 free while the real bindings were grid/cron/sidebar; one source of truth,
  never a second table.
- **One note style:** `Section.Render` adds the standard indent and callers pass bare
  text; the long vulcan-status footnote paragraphs are now one-liners ("scratch is fast
  but NOT backed up"), keeping data + bars + the single "→ cmd" hint.
- **Palette discipline:** the sidebar gutter renders via `tui.RenderSidebarGutter`
  (colBarBg — app stopped hardcoding ANSI 238); `barFill`'s anonymous color "2" is named
  (`barEmpty`).
- **Empty states + truncation:** the Models catalog and Settings Providers show
  `emptyRow` when empty; Models/provider rows truncate their columns (a 30-char name
  used to shift every column and hide tags).
- **Chat small fixes:** interrupt clears queued lines (the toast always claimed "esc
  clears"); the queued-toast double-styling dies; the env brief drops fairshare/storage
  (the cluster blurb already sends them — the model read duplicate facts twice a turn).
- **Continue:** the toast rides inside the frame (it used to push the screen one row past
  the terminal); "N turns" plural.
- Files: internal/commands/commands.go, keybindings/keybindings.go, tui/{chat,settings,
  models,cluster,section,bars,sidebar,status,grid,continue}.go, app/model.go.
- Verified: full suite green (20 pkgs); greps — no phantom commands in the registry, no
  hardcoded 238 outside styles.go, functionKeyLabel has no literal key table.

## 2026-09-20 · deepthought-cli — Settings refilled: Overview + Routing tabs, advanced knobs, host System

"It feels empty" — fixed with real substance, not padding.
- **Overview tab (new, first):** config health (live validation), the settings file path,
  model/provider/route counts, the running model, and every role assignment at a glance —
  the at-a-glance page the redesign lost. Read-only (Roles edits roles; F11 manages models).
- **Routing tab (new):** the declarative routes that `RouteClient` consumes — until now
  editable only by hand in config.json, with ZERO UI anywhere. List + add/edit/delete with
  the same draft discipline as providers (name, capability, needs-tools, max-cost, prefer,
  and a deliberate DELETE enum).
- **Provider advanced fields (entity editor):** timeout, max-failures, cooldown (circuit
  breaker), max-usd + max-tokens (budgets), clearance — all consumed by routing/breakers
  with no way to view or edit them before.
- **Models (F11):** `reasoning_style` (load-bearing — drives the babel request wire
  format; hand-edit-only until now) + per-model `effort` override fields.
- **Theme tab:** the `sidebar` field (auto/on/off — only reachable via F10 before).
- **System tab:** the host descriptor (short name, FQDN, OS, kernel, arch, cpus·mem) +
  storage paths + F-key map. Settings gains `SetEnv` (root passes the descriptor).
- **Fixes:** `gotoTab` closes any open editor (F-keys fire during enum/multi edits and
  could leave a stale editor rendering over a different tab's fields); the inline editor
  width matches the frame's content width (was 4 over).
- Files: internal/tui/settings.go + settings_test.go, internal/app/model.go.
- Verified: TestSettingsOverviewAndRouting (overview renders; route add→commit→esc saves
  through the store; DELETE removes) + TestProviderAdvancedFields (all six knobs present);
  tab-switch test updated for the 8-tab row; full suite green (20 pkgs).

## 2026-09-20 · deepthought-cli — the host descriptor rendered: Status "Host", richer env brief

"Login node" was the wrong name and the wrong data (it showed the binary's build target as
the OS). The Status page now renders the full **Host** descriptor: short name · FQDN · OS
pretty name · kernel · arch · cpus · memory · user · shell · tz + detection chips — this box
may not be a login node at all; it's just the host.
- The **env brief** leads with the host line (short name + arch/cpus/mem) and OS+kernel,
  and gains the first **negative capability**: a configured outbound proxy renders as
  "outbound network via proxy … — direct connections fail" (a line that prevents three
  failed pip/curl attempts pays for itself). Still hard-capped at 6 lines.
- Probe parsers split into pure test seams (`osPrettyNameOf`/`memTotalGBOf`).
- Files: internal/tui/{status.go,chat.go,sidebar_test.go,status_test.go},
  internal/app/{model.go,model_test.go}.
- Verified: TestHostProbeParsers (parsers + live gatherEnv on this host), TestEnvBriefHostFacts
  (host line, OS/kernel, proxy negative), TestStatusAdaptiveSections updated for "Host";
  full suite green (20 pkgs).

## 2026-09-20 · deepthought-cli — sidebar actually works: clipping fixed, live data, host descriptor probes

The sidebar never appeared because every chat (re)build except WindowSizeMsg sized the chat
at FULL width — the joined sidebar overflowed the terminal and was clipped away entirely
(deterministic ≥160 after splash); it was also one column over budget and showed dead data.
- **chatResize funnel:** one helper computes the narrowed chat width (terminal − column −
  gutter) and is now called from WindowSizeMsg, splash advance, ResumeChatMsg, F5 new chat,
  AND the F10 toggle (toggling now re-sizes live instead of wrapping the screen).
- **Width off-by-one:** RenderSidebar pads to exactly its width (the extra PaddingLeft made
  every join one column too wide); the test now asserts EXACT width.
- **F10 safety:** nil-guards the settings handle (it panicked with no Live deps) and the
  auto threshold drops to ≥120 cols (160 was too far away to ever feel alive).
- **Live data:** the cache now actually populates Providers (cached breaker states, on the
  1 Hz tick) and ContextWindow (the active model's window, per session-usage tick) — both
  were never set, leaving a blank row and a bare token count. Polling is adaptive: 30s while
  the sidebar or Status is open, 3min background; opening F12 forces an instant poll.
  Non-Slurm hosts render a Host section instead of a permanent "(cluster n/a)".
- **Host descriptor (design-doc Phase A begins):** EnvInfo grows ShortName/LongName
  (FQDN)/OSName (PRETTY_NAME from /etc/os-release)/Kernel (/proc/sys/kernel/osrelease)/
  Arch/CPUs/MemGB, probed pure-stdlib in gatherEnv (failed probe = missing fact, never an
  error). Found-bug fix: NewRootModel never set m.env — splash/F5 chats silently lost the
  whole env brief; ResumeChatMsg now also SetEnvs (it dropped the brief on resume).
- **Root hardening:** pushScreenOnce on EVERY screen-push site (F12/F6/F7/ScreenChangeMsg
  still double-stacked; esc then popped into the same screen).
- Files: internal/tui/{sidebar.go,status.go,sidebar_test.go}, internal/app/model.go.
- Verified: sidebar exact-width test (was ≤+1, baking the bug in); full build/vet/test
  green (20 pkgs). Live pty: F10 at 121+ cols shows the full column with nothing clipped.

## 2026-09-20 · deepthought-cli — Cron screen repaired: crash, cursor, navigation, data-loss traps

The F8 screen crashed deterministically on open (View rendered before the async crontab
load landed → nil-pending deref), hid its cursor, and could destroy staged work with a
reflex key.
- **Crash:** every pending-table deref is nil-guarded; View renders a loading placeholder
  pre-load and the actual error on a hard load failure (no crontab binary / corrupt
  registry used to leave the screen panicking on EVERY frame, with the error toast never
  painting). `updateConfirm`'s apply branch is nil-safe too.
- **Cursor:** entries render with the standard `▶` marker (the list previously showed no
  cursor at all while advertising ↑↓ move); `rowCount` matches the real navigable rows
  (the old phantom +2 let the cursor park where enter/d/e silently no-op'd);
  `stage()` seeds the cursor as an entries index (leading env/comment lines mis-placed
  it); `keepCursorVisible` wired.
- **Data-loss traps:** esc/q from the diff review now goes BACK to the list — it used to
  open the discard gate, where a reflexive `y` destroyed all staged edits; `u` (undo) is
  unreachable from the diff; an invalid add KEEPS the editor open (the seeded default row
  is never left staged as junk) with an explanatory toast.
- The pending-changes hint triplication (note + prose row + keybar) collapses into the
  section title; the keybar is the one hint surface.
- Files: internal/tui/cron.go, cron_test.go.
- Verified: new TestCronPreloadNoPanic / TestCronLoadFailureRenders / TestCronCursorVisible
  (incl. j-then-d deleting the SECOND entry) / TestCronEscFromDiffGoesBack /
  TestCronInvalidAddKeepsEditor; the four carried cron tests still pass; full suite green.

## 2026-09-20 · deepthought-cli — the deepthought-server skeleton + live editing (server groundwork starts)

The server side of the roadmap now has a real front door. Deploys as a container on the
Vulcan kube cluster (TLS terminates upstream at deepthought.vulcan.alliancecan.ca; binds
plaintext); Go `internal/` can't cross modules, so it lives at `cmd/deepthought-server`
with helpers in `internal/server` (promotion to the repo-root product dir is deferred —
documented in SERVER.md).
- **Endpoints:** `GET /healthz` + `GET /readyz` (open, for probes — aleph's startup/
  readiness/liveness trio), `GET /api/v1/version`, `GET /api/v1/crons` (the local cron
  tracking registry — the first fleet-aggregation endpoint, real), 501 placeholders for
  jobs/experiments/chats. **Auth:** shared bearer token (`$DEEPTHOUGHT_SERVER_TOKEN` or
  `--token-file`) on every /api route; **no token → 503, never silently open**. Graceful
  SIGINT/SIGTERM drain (the pattern the resident daemon lacks).
- **Live editing (start the server, edit it live):** `POST /api/v1/admin/reload`
  genuinely re-reads config + skills and reports what it found; SIGHUP is the signal
  form; the Transwarp daemon's `refresh_config`/`refresh_skills` verbs now invoke a real
  reload hook (`Manager.OnRefresh`, wired in resident.go) instead of acknowledging and
  dropping.
- **Ops:** `Dockerfile` (multi-stage → distroless/static), `k8s/{deployment,service}.yaml`
  reference copies mirroring aleph's conventions (never-:latest pins, probes, secrets),
  `configs/deepthought-server.service` systemd user unit, `make deploy-server`.
- Files: cmd/deepthought-server/main.go, internal/server/{server,http,http_test}.go,
  internal/transwarp/server.go, cmd/deepthought-cli/resident.go, Dockerfile, k8s/,
  configs/, Makefile.
- Verified: TestHealthOpen / AuthGatesAPI (401 wrong+none, 200 right) / NoTokenRefusesAPI
  (503) / Placeholders501 / CronsEndpointServesRegistry — plus the full suite green (20
  pkgs). Live pty/kube checks: run `./deepthought-server --addr 127.0.0.1:7990` and curl
  the trio; SIGTERM drains.
## 2026-09-20 · deepthought-cli — docs/SERVER.md + roadmap/CLAUDE refresh

- **New `docs/SERVER.md`:** the server architecture — where the skeleton lives (and why
  the module boundary defers the product-dir promotion), deployment (kube container +
  endpoint + TLS assumption, CI, dev/bare-metal, the pod-state-is-ephemeral PVC note),
  auth model (shared bearer now, per-user later, 503-never-open), the v1 API table, live
  editing (admin/reload + SIGHUP + the Transwarp refresh hooks), **the seam migration
  path** (state done via the shared SQLiteStore; the chat loop's TurnRunner seam with
  `unimatrix.Session` as the server-side runner; OnEvent as the event source; the
  Transwarp daemon as the control plane), and the cron fleet-aggregation design.
- **ROADMAP.md:** the MCP note + reference links (Claude Code MCP docs, Slurm arrays,
  Nextflow resume, NERSC long-running jobs), the endpoint, and a "groundwork started"
  marker pointing at SERVER.md.
- **CLAUDE.md (product):** the Status narrative catches up with the once-over — flat-tab
  Settings, the Section kit, Cron/Models/sidebar screens, the DON'T PANIC splash, the
  current F-key map, solid brand colors, the environment brief. Root CLAUDE.md's
  two-product table notes where the skeleton actually starts.
- Files: docs/SERVER.md (new), docs/ROADMAP.md, CLAUDE.md, ../CLAUDE.md.
- Verified: docs render; cross-references resolve.
## 2026-09-20 · repo — GitHub Actions: build deepthought-server (aleph pattern)

- **New `.github/workflows/build-server.yml`** (repo root): path-filtered push to main
  (`deepthought-cli/**` + the workflow) + `workflow_dispatch`. Jobs: `go vet` + `go test`
  (go-version-file pinned to our go.mod) → `CGO_ENABLED=0` static build of
  `./cmd/deepthought-server` (smoke-run `--help`) → upload-artifact
  `deepthought-server-<sha>`. The **optional image publish** follows aleph's exact
  conventions (`DOCKER_HUB_USER`/`DOCKER_HUB_TOKEN` secrets, `DOCKER_HUB_REPO` var
  override, immutable `server-<shortsha>` + moving `latest` tags) and is skipped when the
  secrets are absent — the build+artifact path needs zero secrets.
- Verified: YAML parses; mirrors the working aleph workflow's structure (its deploy-gateway
  runs the same job shape for the gateway image). First real run happens on the next push
  to main.
## 2026-09-20 · deepthought-cli — live info sidebar (F10) + the environment brief

**Sidebar:** on very wide terminals the chat screen grows a compact live info column on
the right — Cluster (GPU bar + running count), Your jobs, the context meter, Provider
chips, dim "F12 for detail". Pure rendering over the existing ticks (clock, cluster
poll, session usage) — no new timers. `auto` (default): shows at ≥160 cols; `on`:
forces at ≥120; `off`: hides. **F10 cycles** the mode and persists
(`appearance.sidebar`); the legend + F1 help now say "F10 sidebar" (F10 was free since
the Cluster merge). Chat width shrinks by column+gutter; the top/bottom bands stay
full-width.
- **Environment brief (the "detected cluster info in the system prompt, not super big"
  ask):** `ChatModel.envBrief()` — host, slurm + GPU type, lmod modules, fairshare,
  storage rows — hard-capped at 6 lines, "" when nothing detected — injected per
  request right after the cluster blurb (transient, never persisted).
- Files: internal/tui/sidebar.go + sidebar_test.go (new), chat.go (env + brief),
  config.go (Appearance.Sidebar), keybindings.go, topbar.go, app/settings.go
  (SidebarMode/SetSidebar), app/model.go (cache, layout math, F10).
- Verified: TestSidebarRender (sections, clip-to-column, no-cluster degrade),
  TestEnvBrief (facts present, 6-line cap, empty degrade); full suite green (19 pkgs).
  The side-by-side composition needs a live wide pty to eyeball.

## 2026-09-20 · deepthought-cli — the F8 Cron screen: manage + track the real crontab

Cron management lands as its own screen, built on internal/cron, with a hard safety gate:
replacing a crontab is destructive-class, so apply/undo ALWAYS require an explicit y/N —
no op-mode shortcut exists.
- **Staging model:** `a` add / `enter`·`e` edit (raw-line editor with validation) / `d`
  delete mutate a PENDING table only. `P` reviews the old-vs-new diff (green +, red −);
  `y` from the diff enters the confirm gate; `y` applies through internal/cron (which
  backs the current table up first) and reloads. `u` undo goes through the same gate
  (undo is itself undoable). Dirty-esc prompts before discarding. `r` refreshes.
- **Tracking view:** entries render humanized (`daily 09:00`) with new-since-last-visit
  marked green `+`; a "Removed since last visit" section lists entries the registry still
  remembers. The Section-kit idiom; fits 80 cols.
- Wiring: F8 (`app:cron`), legend + F1 help updated, root wiring with pushScreenOnce and
  the key-capture guard (editor + confirm states).
- Files: internal/tui/cron.go + cron_test.go (new), nav.go, keybindings.go, topbar.go,
  app/model.go.
- Verified: TestCronScreenRenders (humanized rows, 80-col fit), AddDiffApplyFlow (aborts
  on non-y, applies exactly once through the fake client), DiscardGate, UndoGated — plus
  the full suite green. Real `crontab` flows need a live pty on a scratch user.

## 2026-09-20 · deepthought-cli — internal/cron: parse, safely rewrite, and track the user's crontab

The foundation for cron management (F8 screen next) and the server's later fleet view.
- **cron.go:** `Parse` (blank/comment/env/entry kinds, `@shortcuts`, stable 12-hex sha256
  Hash per line, round-trips verbatim), `Diff` (by hash, position-independent), `Humanize`
  ("every 5 min", "daily 09:00", "weekdays 09:00", "weekends 11:00", "@reboot" → "every
  reboot"; raw fallback for rare shapes — honesty over cleverness).
- **crontab.go:** `Client` with a `Runner` exec seam — `List` ("no crontab for" = empty,
  not an error), `Install` (backs up the current table to Dir/backups first — newest 10
  kept, 0600 — then pipes the new table to `crontab -` on stdin; no temp files, no shell
  interpolation), `Undo` (restore newest backup; undo is itself undoable).
- **registry.go:** `registry.json` in the cron dir (atomic, 0600) — `EntryRecord{Hash,
  Schedule, Command, FirstSeen, LastSeen, Note}`, host-scoped + versioned for the future
  multi-cluster aggregation; `Observe` folds parses in (removed entries kept for history),
  `DiffSinceLast` reports added/removed since the last visit.
- Files: internal/cron/{cron,crontab,exec,registry}.go + cron_test.go.
- Verified: parse kinds/round-trip, humanize table, hash diff, registry observe+diff
  round-trip, fake-Runner List/Install/Undo, backup rotation — all pass; build/vet green.

## 2026-09-20 · deepthought-cli — one streaming indicator: the bottom strip only

The animated lowercase verb ("thinking"…"pondering") rendered in TWO places during a turn —
the bottom activity strip and a duplicate row inside the transcript under the streaming
text. The in-transcript pending row is now a deliberate blank placeholder; the animated
verb lives only in the bottom strip (the one with the animation worth keeping). Streamed
text and the dim ∴ thinking block render unchanged.
- Files: chat.go (pendingView).
- Verified: full build/vet/test green; the activity strip still renders the spinner verb.

## 2026-09-20 · deepthought-cli — unification sweep: one title, one keybar, one empty vocabulary

Every screen now speaks the same chrome language.
- **Titles:** Continue adopts `screenTitle` → `DeepThought › Continue` (was the lone
  "Continue Chat").
- **Keybars:** the menu card and both overlay pickers (choice overlay, model chooser) use
  KeyBar chips instead of inline footer sentences; esc is uniformly "cancel" on overlays.
- **Empty states:** the 9 wordings collapse to `emptyRow(noun)` — Status
  providers/models/tools, Cluster active-jobs/fairshare/storage, Continue saved-chats.
- **Status keybar:** the dangling empty-label PgUp/PgDn chip now reads "page"; the model
  chooser's empty text points at F11 ("no agentic models yet — F11 to add one").
- Files: continue.go, status.go, cluster.go, menu.go, overlay.go, model_chooser.go.
- Verified: full build/vet/test green; grep gates clean ((enter cycles)/esc close/esc
  quit/Continue Chat/old empty wordings all gone).

## 2026-09-20 · deepthought-cli — the F11 Models screen (the catalog gets its own home)

Models move out of Settings into a dedicated screen — the home for everything the project
plans to do with models. F11 now opens it (the legend/F1 help already said "models").
- **`internal/tui/models.go` (new):** the catalog list (`id · provider · caps` + dim
  `[chat][agentic]` role badges) with `enter` edit · `L` add-from-provider (provider pick →
  discovered-ids list, ✓ marks existing, no duplicates) · `t` test (latency ping) · `r`
  role toggle (assign/unassign per role) · `d` delete (role-in-use guard) · esc back. All
  writes re-base on a fresh snapshot like Settings (freshSave); the editor kit and
  modelFieldDefs are shared with Settings (providerFieldDefs/modelFieldDefs are now free
  functions keyed by the entity's stable reference).
- **Settings slimmed to 6 tabs** (General · Providers · Roles · Perms · Theme · System):
  the Models tab, its test/list/picker machinery, and the dead `L` action are gone. Also
  fixed a latent case bug: the d/t/L action guards compared against tab LABELS
  (`"providers"` ≠ `"Providers"`), so the keys never fired.
- Root: `ScreenModels` wired (field/init/resize/update/view/activeInit/action via
  pushScreenOnce) + included in the key-capture guard.
- Files: models.go + models_test.go (new), settings.go (trimmed, defs extracted),
  settings_test.go (model tests moved), nav.go, app/model.go.
- Verified: TestModelsListRenders (80-col fit), EditRoundTrip (fresh-snapshot store
  round-trip), RoleToggle (both directions), DeleteGuard, AddFromList (+ no-duplicate),
  carried caps/id-rewrite field tests, SettingsHasNoModelsTab; full build/vet/test green.

## 2026-09-20 · deepthought-cli — Settings redesigned: flat tabs, all seven bugs fixed

The cramped two-pane 4-level drill-down is replaced by a full-width **tabbed editor**
(lazygit/NN-g-style: peer sections as tabs, ≤2 perceptual levels), and every confirmed bug
is fixed.
- **New shape:** tab row (General · Providers · Models · Roles · Perms · Theme · System) +
  one dim roadmap line (the dead "coming soon" sections die; Overview dies — Roles covers
  it) + a scrollable body (viewport) + KeyBar. Tabs switch on ←/→/[/]/digits; General
  absorbs Behavior (effort/max-tokens/temperature as real enum fields — the "(enter
  cycles)" baked hints are gone); System is read-only reference (cluster/storage/F-keys).
- **Bug 1 (dead space-toggle):** fMulti now matches `"space"` (bubbletea v2's name) as well
  as `" "`, and gains left/right nav parity with fEnum — model capabilities were literally
  unchangeable before.
- **Bug 2 (add-provider impossible):** "+ Add" stages an in-memory **draft**; draft commits
  skip whole-file validation; leaving the entity view validates — valid ⇒ save, invalid ⇒
  drop with an explanatory toast. The flow name → base_url → key → esc now works.
- **Bug 3 (stale snapshot clobbering):** field defs take the target `*config.File`
  explicitly; every commit re-bases on a FRESH `store.Snapshot()` before validate+save, so
  concurrent changes (F9 mode, F4 effort, /model) always survive. Non-entity edits go
  through the same `persistRebase` path. Renames update the entity reference atomically.
- **Bug 4 (picker wiped the screen):** the list-models picker composites via the real
  `overlayCenter` splicer.
- **Bug 5 (F2 double-push):** new `pushScreenOnce` guard for screen-push actions.
- **Bug 6 (frozen caret):** blink ticks reach the open text editor.
- **Bug 7 (overflow + nits):** the frame fix below clips/pads correctly; the `context`
  setter rejects non-numeric instead of zeroing; the root skips global keybinding
  resolution while a screen reports `CapturingKeys()` (typing can't be swallowed by
  user-rebound letter keys).
- **Frame fix (all screens):** lipgloss v2 `Width(n)` is TOTAL width — the frames boxed at
  `Width(inner)` leaving 4 cells of headroom that word-wrap silently split words into.
  Boxes now render at `Width(w)`, making content capacity exactly the padded `inner`. This
  kills the latent wrap class on every framed screen.
- Files: settings.go (rewritten ~1,670→~1,180), settings_form.go, frame.go (box widths),
  app/model.go (pushScreenOnce, screenCapturesKeys), settings_test.go (rewritten),
  splash onboarding path unchanged (`NewSettingsModelAt(..., "providers", true)`).
- Verified: new regression tests — space toggle, add-provider draft flow (traverse + early
  drop), out-of-band-write survives a commit, esc ladder, tab switch/wrap, 80-col fit —
  plus the carried field-semantics tests (provider rename rewires models, caps, model id
  rename rewires roles, rename updates the ref, persistRebase). Full build/vet/test green
  (18 pkgs); eyeballed Providers + General tabs at 80×24 and 120×30 (no wraps, keybar on
  one line). Live pty still needed for the caret + onboarding feel.

## 2026-09-20 · deepthought-cli — shared design-system primitives (frame kit)

The groundwork commit for the unification sweep: one title convention, one empty-state
vocabulary, ANSI-safe clipping everywhere, and a real overlay compositor.
- **`screenTitle(name)`** — every framed screen titles as `DeepThought › Name` (migrations
  land in the sweep commit).
- **`emptyRow(noun)`** — the one dim `(no <noun> yet)` empty-state wording.
- **`clipLine(s, w)`** — ANSI-aware truncate (lipgloss MaxWidth); **`padLines`/`padBlock`
  now CLIP overwide lines** instead of letting rows blow out a bordered frame — the fix for
  the Settings overflow class and the safety net for the coming sidebar.
- **`overlayCenter(parent, child)`** — a real per-line splice compositor (child centered
  over the parent's canvas; escapes survive) replacing the broken picker `overlay()` that
  composited over an empty canvas. Wired into Settings in the redesign commit.
- `truncatePad` moved to frame.go (shared by Status/Cluster/Continue/model chooser).
- Files: frame.go, continue.go (truncatePad removed), frame_test.go (new).
- Verified: TestScreenTitle/EmptyRow/TruncatePad/ClipLine/PadBlockClips/OverlayCenter(+ANSI)
  pass; full build/vet/test green (18 pkgs).

## 2026-09-20 · deepthought-cli — remove the F11 Software screen + internal/cvmfs

The Software screen (module search/load helper) is gone per the once-over decision: its
static guidance blocks duplicated the alliance-cvmfs skill, and the agent path (skill +
`module spider` via the bash tool) covers discovery. F11 passes to the new Models screen
(arriving next in the series).
- **Deleted:** `internal/tui/software.go` + `software_test.go`, and the entire
  `internal/cvmfs` package — it was imported only by the screen; `app.gatherEnv` probes
  `/cvmfs` + LMOD env directly, so the Status page's detection line is unaffected.
- **Rewired:** `keybindings.Software` → `keybindings.Models` (`"app:models"`, still F11 —
  existing rebinding files naming `app:software` simply stop matching; noted for anyone
  with a custom keybindings.json). Legend + F1 help now say "F11 models". F11 no-ops until
  the Models screen lands (next commits in this series).
- Files: software.go/software_test.go/cvmfs/ (deleted), nav.go, keybindings.go, topbar.go,
  app/model.go, topbar_test.go.
- Verified: full go build/vet/test green (18 pkgs); grep gate — no ScreenSoftware/
  softwareScr/internal-cvmfs references; remaining "cvmfs" hits are the Status detection
  chip + gatherEnv probe, which are intentional.

## 2026-09-20 · deepthought-cli — solid brand colors + the big DON'T PANIC splash

The rainbow is retired everywhere (top-bar wordmark, spinner, splash mark); the splash
centerpiece becomes the Hitchhiker's-Guide cover instruction itself.
- **Splash:** big **DON'T PANIC** wordmark (go-figure "colossal" — one line wide, stacked
  `DON'T`/`PANIC` at 80 cols, "small"-font and plain-text fallbacks) painted with a
  restrained static cyan→violet brand gradient (`brandGradient`/`paintGradient`). The old
  slanted-bar mark, the drifting spectrum, and the separate "Don't Panic." tagline are gone;
  the rotating HHGTTG subtitle, boot status line, version, spinner, and hint stay.
- **Top bar:** the DeepThought wordmark is the solid `styleStatusApp` chip (was a per-rune
  recolored word drifting every clock second). **Spinner:** one solid brand-color glyph (was
  cycling six spectrum colors per frame).
- **Deleted:** `internal/tui/logo.go` entirely (markBands/bandForRow/markSpec — the spectrum
  had exactly one remaining consumer each), `doubleRows` (test-only), stale "rainbow"
  comments in app/color.go + app/session.go.
- Files: splash.go (rewritten), bigtext.go (font variants; bigTextIn), topbar.go, styles.go,
  app/color.go, app/session.go (comments), splash_test.go (new), logo.go + logo_test.go
  (deleted).
- Verified: new TestBrandGradient / TestDontPanicHeaderFits (fits at 200/100/80/60/40 cols,
  plain-text fallback carries the wordmark) / TestSplashViewRenders pass; full
  go build/vet/test green; grep gate `markBands|bandForRow|rainbow|doubleRows|markFull` empty;
  eyeballed the render at 100x30 and 80x24 (stacked colossal fits 24 rows). Gradient colors
  still need a live truecolor pty to appreciate.

## 2026-09-20 · deepthought-cli — add the research-copilot roadmap (docs/ROADMAP.md)

Captures the HPC research-assistant capability roadmap as durable project direction. It is a
**vision, not yet built** — much of it is future work pending a server side that does not exist
yet. No capability is implemented by this change; it only records direction.
- **New `docs/ROADMAP.md`:** 22 capability areas (know the cluster, remember the project, plan
  the work, check before the queue, environments, size with measurements, explain scheduling, job
  lifecycle, survive disconnects, sweeps/workflows, diagnose with evidence, recover
  intelligently, scientific progress, data management, provenance, validate the result, compare &
  analyze, interactive→batch, scientific models as tools, reusable lab knowledge, autonomy &
  spend control, iterative research); the 5-step priority build sequence; the "durable storage +
  model as interpreter" architecture principle; which v1 foundations it leans on; client-side
  near-term candidates; what is blocked on the server side; and pilot success metrics.
- `CLAUDE.md`: links the roadmap under "Where to look"; updates the effort-picker description to
  the neutral Off→Max labels; fixes the stale F-key line (F8/F10 were freed into F12 Status).
- Files: `docs/ROADMAP.md` (new), `CLAUDE.md`.
- Verified: doc renders; the CLAUDE.md cross-reference resolves.

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
