# DeepThought

This repo holds **two products**, one directory each:

| Dir | Product | Status |
|---|---|---|
| `deepthought-cli/` | Agentic terminal assistant in **Go**, served as a TUI over SSH. Users `ssh` in, land on a splash, and drive an LLM agent (code, HPC/Slurm, shell) under a permission gate. | v1 implemented, live |
| `deepthought-server/` | Placeholder for the server-side product. | no code yet |

Read the app's own `CLAUDE.md` before working in it (`deepthought-cli/CLAUDE.md`
carries the TUI status, naming lore, and conventions). Cross-cutting docs go in
`docs/`.

## The changelog rule (non-negotiable)

**Before every commit, add a dated entry to the root `CHANGELOG.md`** describing
what the commit changes, the files touched, and how it was verified. Newest entry
at the top, tagged with the app directory(ies) touched:

```
## YYYY-MM-DD · deepthought-cli — short title
- what changed
- files touched
- how verified
```

A commit that changes code but not the changelog is incomplete — do not commit
it. Historical entries may name the CLI's former names; *new* code, docs, and
identifiers must not reintroduce them.

## Working agreement

- Update `CLAUDE.md` files and `docs/` as decisions land. Don't silently change
  recorded direction — if a north star shifts, edit the file to match and say so.
- No secrets in the tree: API keys come from env vars; real config
  (`deepthought-cli/configs/config.json`) is gitignored, only the example is
  committed; SSH keys never enter the repo.

## Environment (Vulcan HPC login node)

- Builds happen on the login node by explicit user choice for this project's dev
  loop (org policy would say offload to Slurm). Keep compiles to the app trees;
  Go caches live on `$SCRATCH/deepthought-cli/{gocache,gomodcache}` via
  `go env -w`.
- Job I/O on `$SCRATCH`, not `$HOME` (50 GB quota). The CLI's deploy dir is
  `$SCRATCH/deepthought-cli/`; its SSH host key (`host_ed25519`) never gets
  committed.
- Don't guess module versions or GPU types; don't scan the filesystem from root.

## Conventions

- Idiomatic Go: `gofmt`, table-driven tests, wrap errors with `%w`, no `panic`
  in the request path.
- `CGO_ENABLED=0` static binaries where possible.
- Component naming inside the CLI follows its own lore (Borg package names,
  Hitchhiker's Guide flavor) — see `deepthought-cli/CLAUDE.md`.

## Build & run (deepthought-cli)

```
make -C deepthought-cli build      # → $SCRATCH/deepthought-cli/deepthought-cli.new
make -C deepthought-cli deploy     # atomic swap into $SCRATCH/deepthought-cli/deepthought-cli
make -C deepthought-cli check      # go vet + go test
```

The binary serves the TUI over SSH (`deepthought-cli --sub-etha :2323`).
