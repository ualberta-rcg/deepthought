# DeepThought

This repo holds **two products**, one directory each:

| Dir | Product | Status |
|---|---|---|
| `deepthought-cli/` | Agentic terminal assistant in **Go**, served as a TUI over SSH. Users `ssh` in, land on a splash, and drive an LLM agent (code, HPC/Slurm, shell) under a permission gate. | v1 implemented, live |
| `deepthought-server/` | The server-side product: its OWN Go module with zero CLI dependencies (the Borg-graph wire contract is deliberately copied into `deepthought-server/graph/`; keep it byte-compatible with `deepthought-cli/internal/history`). Own Dockerfile + CI lane. | live |

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
- No secrets in the tree: API keys use explicit environment references or private
  local credential storage; real config
  (`deepthought-cli/configs/config.json`) is gitignored, only the example is
  committed; SSH keys never enter the repo.

## Environment (Vulcan HPC login node)

- **CI/CD is the build system** (`.github/workflows/build-cli.yml` vets, tests,
  race-checks, and
  publishes the static CLI binary as an artifact; `build-server.yml` builds and
  pushes the server image to Docker Hub). Never build on the shared login node,
  and do not use Slurm or CVMFS/modules for this repository's builds. The separate
  server deployment is Kubernetes-backed; this does not describe the clients' hosts.
- **The only manual deploy step is applying the server-side YAML**: the numbered
  manifests on the aleph1 control-plane (`57-deepthought.yaml`,
  `58-deepthought-mysql.yaml`), bumping the image tag to the CI-built one.
  Everything inside the `deepthought` namespace only.
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

Edit → push implementation branch → GitHub Actions. The `improve/**` and PR
lanes vet, test, race-check and build without publishing; only a push to main
publishes the rolling CLI release. The server has its own module and workflow.

Run `deepthought-cli` standalone, or serve the TUI over SSH with
`deepthought-cli --sub-etha :2323`. Inference configuration is optional at startup.
See [setup](docs/SETUP.md) and [host inventory](docs/HOSTS.md).
