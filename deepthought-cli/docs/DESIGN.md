# DeepThought — Design: context, prompts, plans, and workflows

The architecture direction for the research copilot (companion to
[ROADMAP.md](ROADMAP.md) — what we're building — and [SERVER.md](SERVER.md) — the
groundwork already running). **Status: Phase A has started client-side** (the host
descriptor behind the Status Host section, the env brief, the sidebar, and Settings ›
System); everything else is design, built when its phase arrives.

## 1. The unification

Three things that look separate are the same data:

- **"What can this machine do?"** — needed to write the system prompt.
- **A cluster profile** — needed to render a portable job spec into a real sbatch script.
- **Capability advertisement** — what an agent reports to the server on connect.

One probe system produces **one structured descriptor**; renderers write over it. A prompt
fragment is a render. An sbatch script is a render. The capability blob is the descriptor
itself. The rule: **detectors emit structured facts, never prose** — the moment a detector
returns a sentence it can only be used for prompts, and the profile gets built twice.

`tui.EnvInfo` (+ `gatherEnv()`) is Phase A's client-side seed of this: one structured
descriptor, four renderers.

## 2. The environment descriptor

Bound to a `(host, user)` pair, versioned, TTL'd. Shape (target):

```jsonc
{
  "descriptor_version": 3, "probed_at": "…", "ttl_seconds": 3600,
  "host": { "fqdn": "…", "os": "RHEL 9.4", "arch": "x86_64", "cpus": 64, "memory_gb": 512 },
  "user": { "name": "…", "uid": 10321, "groups": ["def-x", "rcg"] },
  "filesystems": [ { "role": "scratch", "path": "…", "quota_gb": 20000, "purge_days": 60 } ],
  "capabilities": {
    "slurm":     { "present": true, "version": "…", "accounts": ["def-x"],
                   "partitions": [ { "name": "gpu-a100", "gpu": "A100-80GB", "max_walltime_h": 24 } ] },
    "apptainer": { "present": true, "version": "…", "nv_flag": true, "can_build": false },
    "kubectl":   { "present": true, "context": "…", "verbs": ["get","list","create"] }
  },
  "constraints": { "network": { "outbound": "proxy-only" } }
}
```

### Capability lines + negative capabilities

A rendered line is **capability + version + the constraints that change what you'd do**.
"You have kubectl" is worthless; "kubectl 1.30 → vulcan-prod/rcg-ai, get/list/create, not
cluster-admin" is not. And **negative capabilities are worth more than positive ones** —
most wasted turns are attempts at something the environment forbids. The env brief's
"outbound network via proxy — direct connections fail" line is the first of these.

### Probe discipline

Read-only; individually timeout-bounded; **never fail the session** (a failed probe emits
`{"present":"unknown"}`); detectors independent and parallel; cache by `(host, user)` with
TTL; **large output becomes a tool, not a fragment** (state existence + magnitude + the
query command — never enumerate 1247 modules into a prompt); **secrets never enter a
descriptor**.

## 3. Prompt assembly

The system prompt is **assembled, not written** — a fragment stack, lowest→highest
specificity: harness core → principal (user prefs) → project (goal/conventions) →
environment descriptor → skills → plan context → outcome memory. Each fragment carries
id/source/scope/content_hash/priority/token_estimate/ttl. The assembler is **pure**: same
fragment set → same text → same `prompt_hash`, recorded per turn — an experiment whose
prompt can't be reconstructed isn't reproducible. Store fragment ids + hashes per turn,
not the text. A hard **token budget** allocates per class (core uncapped; constraints
early and capped; descriptor summarized; skills as summaries with on-demand expansion),
and **degraded mode is a prompt line** — "running from cached policy; server unreachable
since 18:22Z" beats silently degrading.

## 4. Execution contexts — the hop problem

A descriptor binds to an **execution context** (`agent, host, user, cwd, environment,
container`), not a session. `ssh`, `srun`, `apptainer exec`, `kubectl exec` each create a
new one; the prompt is **reassembled** with the destination's descriptor on every hop.
Three degrees of visibility on the far side: agent present (full) → agent bootstrappable
(push a static binary, enroll — worth automating early) → remote probe only (minimal
descriptor, marked low-confidence, and the prompt says so).

## 5. Skills

Metadata + prompt fragment + optional files + **declared requirements**
(`requires: capabilities: [slurm, apptainer]; any_of: partition_gpu_memory_gb >= 40`).
Selection = project bindings ∩ requirement satisfaction against the current descriptor.
**Unsatisfiable skills are mentioned with the reason, not silently dropped** — "esmfold:
unavailable here (no GPU partition ≥40GB); available on vulcan" prevents both retries and
dead ends. Placement is an agent operation (materialize by digest, verify hash). Skills
bind at the project level; per-plan overrides recorded as overrides.

## 6. Taxonomy

**Project** (administrative container, `kind`: research/operational/personal/scratch —
*not* a second top-level type; `~/projects/*` is a discovered facet, not the definition) →
**Plan** (versioned DAG of steps; forkable) → **Run** (one execution with an input
binding) → **Step** (typed, content-addressed — identical steps dedupe across plans) →
**Attempt** (one step execution within a run; where outcomes live) alongside **Session**
(live attachment) and **Thread/Entry** (the conversational record; a DAG, not a list —
branching is free, context assembly becomes a query).

## 7. Blessing and failure memory

The mechanism behind "save the working ones, see the old fails, start from the winners":
attempts can be **blessed** (manually, or automatically against a declared success
predicate); a blessed attempt's input binding becomes the default for its step, and
deviating is a deliberate recorded act. Retries inject prior failures **compressed**
(error class + what was varied + the distinguishing stderr — never three full tracebacks),
with an **error-class taxonomy** (`oom`, `walltime`, `missing_dep`, `permission`,
`capability_absent`, `transient`, `logic`) driving retry policy: `oom` bumps memory;
`capability_absent` doesn't retry *here* but may *elsewhere*; `transient` retries with
backoff.

## 8. Workflows and triggers

No separate workflow subsystem: a **step-kind** enum (`shell`, `slurm.batch`,
`slurm.array`, `k8s.job`, `container.run`, `agent.turn`, `tool.<name>`, `human.gate`,
`subplan`, `transfer`) — each kind a small executor with a common interface (validate /
estimate / execute / classify failure / extract outputs); adding ESMFold is one executor.
**Triggers live on the server, not in login-node crontab** — login nodes get drained and
patched, and cron there is where automation dies quietly (`manual`, `cron`, `on_event`,
`on_completion`, `watch`). A workflow is just `plan + trigger + input binding`. (The F8
Cron screen manages today's real crontabs; server-side triggers are the future — both
coexist.)

## 9. Ownership

Server: fragment/skill registry (authoritative), prompt assembly (deterministic,
recorded), descriptor storage/versioning, plan/run/attempt graph, trigger scheduling,
secrets (encrypted at rest). Agent: probing (only it can see the host), execution,
caches by digest — and a degraded-mode assembler that **says when it's degraded**.

## Phasing

**A** descriptor + static assembly (STARTED — EnvInfo/gatherEnv + renderers) · **B**
execution contexts + bootstrap · **C** plans/attempts/blessing (shell + slurm.batch
first) · **D** entry DAG + query-based assembly · **E** skills registry/matching ·
**F** triggers + executors. A and C are independently useful.

## Open questions

Descriptor freshness vs cost (cheap probes per step, expensive on TTL — measure, don't
guess) · how much descriptor belongs in the prompt vs behind tools (bias: constraints and
scheduler facts in; enumerables behind) · blessing granularity (per attempt vs named
configurations across subgraphs — design when a corpus exists) · affect/sentiment as a
weak assembler signal, never a gate · `~/projects` binding must be many-to-many and
user-correctable or it quietly mis-files.
