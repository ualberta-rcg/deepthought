# Builds, releases and the test deployment

Status snapshot as of 2026-09-30. This page records *how the pieces are
delivered today*; it is not a production runbook — no production location has
been chosen for the server yet.

## Source of truth

- Repository: `github.com/ualberta-rcg/deepthought`, branch `main` only. No
  feature branches, no PR workflow; every code commit carries a dated root
  `CHANGELOG.md` entry.
- Local working copies of the repo are disposable. Anything worth keeping is on
  `main`; anything on `main` is what CI built.

## CLI delivery

`.github/workflows/build-cli.yml` runs on pushes to `main` that touch
`deepthought-cli/**`, `install.sh` or the workflow itself:

1. `go vet`, `go test ./...`, `go test -race` on the core packages.
2. `CGO_ENABLED=0` static Linux/amd64 build, `-X main.buildVersion=<sha>`.
3. Upload as an artifact **and** republish the rolling GitHub Release tagged
   `edge` (binary + `SHA256SUMS`).
4. Re-run `install.sh` against the fresh release and compare the installed
   binary byte-for-byte with the one just built.

Users install with:

```bash
curl -fsSL https://raw.githubusercontent.com/ualberta-rcg/deepthought/main/install.sh | bash
```

`deepthought-cli --version` prints the commit SHA the binary was built from, so
"which build is this host running" is always answerable.

## Server delivery

`.github/workflows/build-server.yml` runs on pushes to `main` that touch
`deepthought-server/**` or the workflow itself. It does a Docker build of the
self-contained `deepthought-server/` context (multi-stage to distroless), a
smoke run, and pushes `rkhoja/deepthought-server:server-<shortsha>` plus a
moving `latest` tag. A test job runs first (vet and every test, with a
throwaway MySQL 8.4 service so the store tests execute); the image is built
only when it passes. The image tag is stamped into the binary, so `/healthz`
reports `server-<shortsha>`. Publishing an image changes nothing that is
deployed.

Configuration is environment-only: `DEEPTHOUGHT_SERVER_PASSWORD`,
`DEEPTHOUGHT_MYSQL_DSN`, `DEEPTHOUGHT_ADMIN_USERS` (who may replace the
settings defaults; unset = nobody), `DEEPTHOUGHT_VAULT_KEY`, `--addr`, `--data`.
Without a DSN the DB-backed endpoints answer 503 while `/healthz`, `/readyz`,
the web UI and login work (sessions then live in memory and a restart logs
everyone out; with a DSN they are stored hashed in MySQL, slide forward when
used, and expired ones are purged hourly).

### Credential vault

`DEEPTHOUGHT_VAULT_KEY` is 32 random bytes, hex or base64, used as the
AES-256-GCM key for provider API keys synced by clients
(`/api/v1/user/credentials`). Create it once and keep it in a Kubernetes
secret; the deployment reads it optionally:

```sh
kubectl -n deepthought create secret generic deepthought-server-vault \
  --from-literal=key="$(openssl rand -hex 32)"
```

Without it only `$ENV` references can be synced; literal keys are refused
(503). Losing or rotating the key makes stored secret credentials unreadable
(clients re-upload them on their next sync). Each row is bound to its user and
provider, and the index only ever returns HMAC fingerprints.

**Trust assumption.** Login is still the phase-1 shared password with a
client-supplied username, so **anyone who holds the password can log in as any
user and read that user's stored credentials.** This is acceptable only while
every password holder is trusted with every stored key (today: the owner and at
most one colleague). Before the password is shared more widely, the username
claim tokens designed in the improvement plan must land, or credential sync
must be switched off.

## The test deployment (temporary)

A staging copy of the server runs in the `deepthought` namespace of a shared
RKE2 cluster used by the Aleph inference service. It exists purely so the
CI-built image can be exercised end to end (login, settings sync, chat sync,
MySQL persistence) while the product is developed.

- It was applied by hand as two numbered manifests on the cluster's
  control-plane, mirroring `deepthought-server/k8s/{deployment,service,mysql}.yaml`.
  Those manifests are a one-off; they are not reconciled from this repo and can
  disappear.
- Rules for anyone operating it: only the `deepthought` namespace may be
  changed; every other namespace, node and inference workload on that cluster is
  read-only. Both deployments pin to control-plane nodes so they never land on
  the GPU workers. Secrets are created imperatively and never committed.
- Shape: `deepthought-server` (1 replica, ClusterIP 80→8080, Traefik
  IngressRoutes with an HTTP→HTTPS redirect, cert-manager Let's Encrypt
  certificate) and `deepthought-mysql` (mysql:8.4, 10Gi RWX PVC on the
  cluster's NFS class). Public hostname: `deepthought.vulcan.alliancecan.ca`.
- Rolling to a new build is a manual edit of the image tag in the applied
  Deployment to the CI-published `server-<shortsha>`.

Observed on 2026-09-30: server image `server-529d04e` (the latest server-side
commit — no `deepthought-server/` changes have landed since), MySQL bound to its
PVC, certificate valid, `/healthz` and `/readyz` returning 200 from the public
hostname. The CLI `edge` release is built from `da462df`, the current `main`.

## Known gaps

- Images built before 2026-09-30 report `"version":"dev"`; later ones report
  their `server-<shortsha>` tag.
- Probe status numbering changed on 2026-09-30 to match the CLI (`Denied=4`,
  `Failed=5`). Chats stored earlier by the test deployment read old failed
  probes as denied. That data is test-only; wipe the namespace's MySQL volume
  if it matters.
- The rolling `edge` release is the only CLI channel. There is no versioned or
  stable tag yet.
