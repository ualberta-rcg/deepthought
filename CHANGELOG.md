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
