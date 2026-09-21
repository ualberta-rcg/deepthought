# deepthought

Two products, one repo:

- **`deepthought-cli/`** — an agentic terminal assistant in Go, served as a TUI
  over SSH. `ssh` in, land on a splash, and drive an LLM agent that can write and
  run code, operate HPC clusters (Slurm, modules, schedulers), and help with
  scientific work — every fs/shell/network action gated by a permission model.
- **`deepthought-server/`** — product scaffold; the HTTP skeleton lives under
  `deepthought-cli/cmd/deepthought-server` in the shared Go module.

The CLI runs standalone. `--towel` keeps a local session running across terminal
disconnects; `ls` and `attach <id>` reconnect. `/jobs` tracks durable Slurm
submissions and `/plan` shows scientific objectives and validation evidence.
The server answers at deepthought.vulcan.alliancecan.ca with a login (shared
password phase) and a starter web UI; the CLI's splash has a working "Log in to
server" that fetches the server's settings defaults. See [local operations](deepthought-cli/docs/LOCAL-OPERATIONS.md)
for settings layers, resident recovery, workflows, manifests and releases.

Start with [`CLAUDE.md`](CLAUDE.md) for the repo rules (including the
changelog-before-commit rule) and [`deepthought-cli/CLAUDE.md`](deepthought-cli/CLAUDE.md)
for the CLI's architecture and conventions.

## Build & run the CLI

Run these inside a Slurm CPU allocation on Vulcan, with caches and output on scratch:

```
make -C deepthought-cli check     # go vet + go test
make -C deepthought-cli deploy    # static binary → $SCRATCH/deepthought-cli/deepthought-cli
```

Then `ssh -p 2323 <host>` (the CLI runs its own Wish SSH server via
`--sub-etha :2323`; no OpenSSH involvement).

The CLI speaks both OpenAI- and Anthropic-compatible wire formats; configure
providers, models, and roles in `~/.deepthought/config.json`
(example in `deepthought-cli/configs/`).

## Change log

All changes are recorded in [`CHANGELOG.md`](CHANGELOG.md), newest first, tagged
by app.
