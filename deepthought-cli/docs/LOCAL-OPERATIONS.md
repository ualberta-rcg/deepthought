# Standalone and resident operation

The CLI works without DeepThought Server. Welcome offers **Run standalone**;
**Log in to server — coming soon** is informational. F1 opens Settings and F2 Help.

## Sessions

`deepthought-cli --towel` starts a private user daemon and attaches a new session.
`deepthought-cli ls` lists sessions; `deepthought-cli attach <id>` reconnects.
Ctrl+\\ detaches the terminal. An active turn, including a pending approval, stays
with the daemon. `/quit` ends that session. `stop <id>` cancels one session;
`stop` stops the daemon. Existing standalone processes do not become resident.

The daemon owns the launch directory and configuration selected at startup.
Attachments use that workspace. The Unix socket requires a private directory and
same-user peer credentials. Approval responses identify the currently displayed
prompt; stale responses are rejected. Resident mode uses the same chat runtime,
tool gate and history as standalone mode.

After process or node loss, attach restores persisted conversation state and
marks unfinished work interrupted. It does not replay a tool or submit a job.
Check uncertain side effects before `/resume`. Resident mode is a user process,
not an HPC batch allocation or a system service with guaranteed uptime.

## Settings and skills

Effective settings resolve built-in defaults → optional server defaults → local
overrides → session overrides. The resolver and `DefaultsSource` interface support
future server integration; no server fetch or login is active. The editor shows
source labels and saves changed local values with conflict detection. There is
currently no session-override editor. Server defaults cannot supply API keys.
Anonymous providers require the explicit `anonymous` setting.

The daemon's `refresh_config` and `refresh_skills` controls update shared state.
Loaded skill `allowed-tools` restrictions narrow the rest of the current turn;
they never grant permission. A completed or interrupted turn clears them. Skill
reload updates subsequent lookups and request indexes; invalid packs report errors
while valid packs remain available. Project instructions load from repository root
to the launch directory. `/memory add <fact>` stores an explicitly curated fact.

`/pin <probe-id>` preserves a tool result in full context and `/unpin <probe-id>`
releases it. Pins survive history reopen; a request that cannot fit required and
pinned content refuses with a budget error. Token counts remain estimates.

## Jobs and scientific workflows

`slurm_submit` requires a stable `submission_id`, an owned absolute script path,
account, CPUs, walltime and memory. It discovers account/GPU inventory, rejects
partition directives, stages the exact script on scratch, and journals intent
before submission. Output goes beside the staged script. Every submit and cancel
requires approval. Reusing an ID reconciles its outcome; changing inputs requires
a new ID. Unknown outcomes are never automatically resubmitted. GRES validation
currently accepts full GPUs in `gpu:type:count` form; fractional requests need
the site skill and an explicitly reviewed shell command.

`/jobs` shows the journal, refreshed scheduler state and retry advice. The resident
daemon reconciles jobs every three minutes without model calls. The shared cluster
snapshot cache has a three-minute minimum interval; quota discovery runs at most
every fifteen minutes. Filesystem bars report mount capacity, not personal quota.
Accounting reconciliation looks back seven days; older unresolved submissions
require manual investigation. Bounded log reads only accept owned regular files.

The `workflow` tool creates sequential objectives with explicit predicates.
Use `slurm_submit`, then `workflow link` with the plan, objective and submission IDs.
Use `workflow validate` to check scheduler completion or a real result file and
optionally record an artifact. `/plan` shows saved plans, attempts and stale counts.
Dependency order is enforced. Parameter changes invalidate downstream artifacts;
optimistic revisions prevent concurrent edits from overwriting newer plans.

Artifacts retain SHA256, storage tier, consumed hashes and objective links. Attempts
capture the local harness environment and linked script/job identifiers. They do
not claim to capture remote modules or container digests unless present in the
execution evidence. Regex validation is limited to 1 MiB; hashing to 64 MiB.
Hash larger artifacts in a compute job. Model-judged success stays unverified.
There is no automatic workflow executor, array/sweep builder or checkpoint replay.

## Scientific endpoints

Configure a provider with `kind: "tool_server"`, a base URL, explicit credentials
or `anonymous: true`, and `catalog_source: "skill_manifest"` with a local `manifest`
path, or `rich_manifest` with a relative URL path. Manifest `models` entries need
`id`, `endpoint` and JSON `input_schema`; `output_schema` is optional. Tools register
as `<provider>__<model>`, validate inputs and supplied output schemas, and require
approval. Discovery and responses are bounded. OpenAPI import is explicitly
unsupported. Endpoint configuration changes require a fresh runtime.

## Releases

Canonical checkout: `/scratch/rahimk/repos/deepthought`. Deployed CLI:
`/scratch/rahimk/deepthought-cli/deepthought-cli`. Build/test only in a CPU Slurm job;
set account, CPUs, memory and time explicitly, with job I/O and caches on scratch.
Push tested commits to main. Build a clean committed revision with
`-X main.buildVersion=<commit>`, verify `--version` and SHA256, retain the old
executable as `.previous`, and atomically rename `.new` into place. Existing
processes continue on their loaded executable; do not restart sessions.

Rollback uses the same copy-to-`.new` and atomic rename procedure with `.previous`.
The additive history schema remains readable by older processes. DeepThought
Server deployment and login remain separate future work.
