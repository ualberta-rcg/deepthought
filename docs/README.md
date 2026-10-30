# docs/

Cross-cutting documentation for the deepthought repo — things that span both
products or describe the repo itself (layout, naming, release process).

App-specific docs live with the app (`deepthought-cli/CLAUDE.md` for the CLI's
architecture and conventions today). Add files here as they earn their keep.

- [`AUTH.md`](AUTH.md) — DRAC OIDC / 2FA registration details (`idp.alliancecan.ca`), URLs & claims discovered from Vulcan Open OnDemand, what to request from the IAM/Auth team, and how Web + CLI Browser-Link authentication works.
- [`DEPLOYMENT.md`](DEPLOYMENT.md) — CI pipelines, release channels, and test cluster deployment.
- [`HOSTS.md`](HOSTS.md) — SystemUUID (`SystemID`), multi-cluster capability probing, and Slurm job binding.
- [`SETUP.md`](SETUP.md) — First-run provider discovery, server sync, and credential vault.
