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
