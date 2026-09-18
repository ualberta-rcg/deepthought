# deepthought

Two products, one repo:

- **`deepthought-cli/`** — an agentic terminal assistant in Go, served as a TUI
  over SSH. `ssh` in, land on a splash, and drive an LLM agent that can write and
  run code, operate HPC clusters (Slurm, modules, schedulers), and help with
  scientific work — every fs/shell/network action gated by a permission model.
- **`deepthought-server/`** — scaffold for the server-side product (no code yet).

Start with [`CLAUDE.md`](CLAUDE.md) for the repo rules (including the
changelog-before-commit rule) and [`deepthought-cli/CLAUDE.md`](deepthought-cli/CLAUDE.md)
for the CLI's architecture and conventions.

## Build & run the CLI

```
make -C deepthought-cli check     # go vet + go test
make -C deepthought-cli deploy    # static binary → $SCRATCH/deepthought-cli/deepthought-cli
```

Then `ssh -p 2323 <host>` (the CLI runs its own Wish SSH server via
`--sub-etha :2323`; no OpenSSH involvement).

The CLI speaks both OpenAI- and Anthropic-compatible wire formats; configure
providers, models, and roles in `~/.config/deepthought-cli/config.json`
(example in `deepthought-cli/configs/`).

## Change log

All changes are recorded in [`CHANGELOG.md`](CHANGELOG.md), newest first, tagged
by app.
