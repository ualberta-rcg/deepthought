# deepthought — working notes

Repo: `git@github.com:ualberta-rcg/deepthought.git` → `/scratch/rahimk/repos/deepthought`

## Naming

- This repo is **deepthought**. The project code was developed locally as
  **Annorax** (formerly "Cowabunga") at `/home/rahimk/annorax`.
- **Open decision:** does "deepthought" apply to the whole project (module
  path `annorax`, binary `cmd/annorax`, `~/.config/annorax`, CLAUDE.md) or
  only the repo? Resolve before moving the tree in.

## The code

- Working tree: `/home/rahimk/annorax` — not under git, dir *is* the tree.
  Plan: move/copy wholesale into this repo.
- Go 1.25.9 (`go.mod` says so; on-path go is 1.22.2, GOTOOLCHAIN=auto).
- TUI (Bubble Tea v2) served over SSH (Wish) on `--sub-etha :2323`.
- Build caches on `$SCRATCH/annorax/{gocache,gomodcache}` via `go env -w`.
- Docs in-tree: `docs/{STACK,ARCHITECTURE,BUILD,CLI,ANTIGRAVITY,Reference_app+1}.md`.

## SSH / git access (set up 2026-09-17)

- Key: `~/deepthought_ed25519` (ed25519, no passphrase, comment `deepthought`).
  Fingerprint `SHA256:xDmPMAo2i4+aErtKM2tiW4MYDRlyXCwyyYJbZzEcu10`.
- Config: `~/.git_ssh_config` (IdentityFile + IdentitiesOnly for github.com).
- Use: `GIT_SSH_COMMAND="ssh -F ~/.git_ssh_config" git <cmd>`
  — or fold the config into `~/.ssh/config` manually for bare git.
- Read + write verified (test commit `.write_test` — removable).
- An older key `~/.ssh/annorax_ed25519` exists from earlier this session;
  delete it if unneeded.

## Environment (Vulcan HPC login node)

- Login node: no heavy work, no `pip install`, no scanning from `/`; job
  I/O on `$SCRATCH`, keep the tree out of `$HOME`-sized writes.
- Org policy: credentials/keys never pasted into AI chat — key material
  stays in the user's hands.
- SSH host key for annorax's own server: `$SCRATCH/annorax/host_ed25519` —
  never commit.

## Next steps

1. Decide deepthought rename scope.
2. Move `/home/rahimk/annorax` tree into this repo (first real commit).
3. `.gitignore`: `history.db`, binaries, `.tmp/`, anything secret.
4. Remove `.write_test` once this note lands.
