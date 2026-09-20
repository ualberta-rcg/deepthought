# DeepThought — Research-Copilot Roadmap

What DeepThought is building toward: an **HPC research copilot** that knows the cluster,
remembers the project, plans work, checks jobs before the queue, manages environments and
resources, explains and manages the full job lifecycle (surviving disconnects), handles
sweeps/workflows, diagnoses and recovers failures with evidence, tracks data and provenance,
validates and compares results, supports interactive→batch transitions and scientific models as
tools, shares lab knowledge, and gives the researcher control over autonomy and spend — all
within an explicit objective.

> **Status: vision, not yet built.** Much of this is future work and depends on the **server
> side** — groundwork has now started: a skeleton HTTP server + live reload + this document's
> architecture live at `cmd/deepthought-server` (see [SERVER.md](SERVER.md)), destined for
> `deepthought.vulcan.alliancecan.ca` as a container on the Vulcan Kubernetes cluster.
> Individual capabilities are pulled forward one at a time, not as a batch.
>
> **MCP note:** Claude Code already supports external tools through MCP and automation
> through hooks — some of these capabilities could also be exposed to researchers using it.
> The advantage here is how well the HPC capabilities work together. (Claude Code MCP docs:
> https://code.claude.com/docs/en/mcp; Slurm arrays:
> https://slurm.schedmd.com/job_array.html; Nextflow resume:
> https://docs.seqera.io/nextflow/cache-and-resume; NERSC long-running jobs:
> https://docs.nersc.gov/jobs/best-practices/#long-running-jobs)

## Architecture principle

Job state, budgets, permissions, and experiment records live in **ordinary software + durable
storage**. The model *interprets evidence, proposes changes, and selects actions*; the
**execution system enforces constraints and tracks what actually happened.** This lets you swap
models without losing research history.

The missing piece is the **server side**: a durable, detachable daemon that persists state
across client disconnects and restarts and resumes work. v1's Transwarp socket
(`--towel` / `ls` / `attach` / `stop`) is a primitive for this, not the thing itself.

## Capability areas

Each is a distinct deliverable.

1. **Know the cluster** — discover partitions, hardware, modules, containers, filesystems,
   accounts, limits, and site policies; combine maintained documentation with live information;
   know what *this* researcher can access; detect when its own information is stale.
2. **Remember the project** — preserve the objective, datasets, methods, decisions, past
   attempts, known problems, and next steps; return after a week and answer "where were we, and
   what did we learn"; structured project information with links to evidence.
3. **Plan the work** — turn a research task into preparation → small tests → production runs →
   validation → analysis; surface missing inputs and assumptions (e.g. inspect the existing
   pipeline, test three samples, estimate the full workload, then stage the rest for execution).
4. **Check before the queue** — validate paths, permissions, input formats, required packages,
   output directories, quotas, resource requests, and launch commands; run a small test in a
   suitable allocation when necessary. Catching an error before an overnight queue wait is
   immediately valuable.
5. **Environments** — reuse suitable site software; otherwise help create a pinned environment
   or container and test it on the intended hardware; record what worked. Changes produce a *new*
   environment version so an experiment already in progress remains reproducible.
6. **Size with measurements** — run small benchmarks and inspect previous usage to recommend CPU
   count, RAM, GPU requirements, and walltime; explain the trade-offs between turnaround,
   throughput, and resource consumption; recommend fractional GPUs only when the site supports
   them and the workload fits.
7. **Explain scheduling** — translate pending reasons, account limits, dependencies, and
   fairshare into an understandable explanation; suggest legitimate alternatives (a shorter test,
   fewer resources, or another eligible partition); treat predicted start times as estimates.
8. **Job lifecycle** — submit, track, cancel, and reconnect to jobs; associate each job with the
   experiment that created it; import jobs submitted manually too. If a connection breaks during
   submission, **reconcile with the scheduler before trying again** so the harness never
   accidentally double-submits.
9. **Survive disconnects** — preserve pending actions and resume them when jobs finish, even if
   the laptop closes or the client restarts; a completed simulation can trigger validation →
   analysis → a clear update. Routine monitoring runs **without repeatedly invoking the LLM**.
10. **Sweeps & multi-stage workflows** — expand parameter combinations, manage concurrency and
    dependencies, identify missing results, and rerun only the necessary tasks. Integrate with
    existing workflow tools where appropriate: Slurm supports arrays and dependencies; Nextflow
    provides task caching and resume.
11. **Diagnose with evidence** — bring together application logs, scheduler state, exit codes,
    resource measurements, and recent changes; explain the likely cause, show the evidence, and
    distinguish a confirmed diagnosis from a hypothesis. "This job exceeded its RAM allocation"
    should lead to a specific, reviewable recovery action.
12. **Recover intelligently** — find valid checkpoints, resume supported applications, and retry
    failed workflow stages within agreed limits; remember unsuccessful fixes so it does not
    repeat them indefinitely. Checkpoint support must be verified for the application — a restart
    does not automatically recover progress.
13. **Scientific progress + resources** — report samples processed, simulation steps,
    convergence, training metrics, or completed parameter combinations **alongside** CPU/GPU
    activity; use application-specific progress signals. Low GPU utilization alone is
    insufficient evidence that a job has stalled.
14. **Data management** — track where inputs and outputs live, stage data to appropriate storage,
    verify transfers, monitor capacity, and preserve important results before temporary storage
    expires. Keep references and manifests for large datasets so the researcher can navigate them
    without copying everything into the harness.
15. **Provenance** — automatically capture the code revision and uncommitted changes,
    environment, inputs or dataset versions, parameters, seeds, commands, job IDs, and outputs;
    make it possible to select a figure and trace it back to the exact runs and analysis that
    generated it.
16. **Validate the result** — execute explicit checks for missing samples, malformed outputs,
    NaNs, units, convergence, expected ranges, or agreement with a baseline; let researchers
    define what success means. Record scheduler completion and scientific validation **separately**,
    with the checks and their outcomes visible.
17. **Compare & analyze** — assemble results into tables and plots, compare parameter choices,
    identify failures and outliers, and explain what changed between runs; generate analysis code
    that can be rerun; preserve negative results and uncertainty so the project history remains
    useful.
18. **Interactive → batch** — help launch notebooks, RStudio, visualization tools, and debugging
    sessions inside appropriate allocations; carry the same environment, inputs, and parameters
    into a batch run when the researcher is ready to scale up.
19. **Scientific models as tools** — expose model APIs for tasks such as embeddings, image
    analysis, protein prediction, and inference alongside conventional scientific software; record
    model versions and parameters as part of the experiment; allow different assistant models
    while respecting the project's rules about where data may be processed.
20. **Reusable lab knowledge** — let groups share tested workflows, environment recipes,
    troubleshooting knowledge, and experiment records with appropriate access controls; help a new
    student understand an existing project. When human support is needed, prepare a concise
    diagnostic bundle containing the relevant job, environment, logs, and attempted fixes.
21. **Autonomy & spend control** — let researchers set limits on concurrent jobs, retries,
    GPU-hours, inference spending, and the changes the harness may make automatically; show usage
    using the site's actual accounting model. A useful instruction: "Run this sweep overnight,
    retry infrastructure failures once, and stop if validation fails."
22. **Iterative research** — once execution and validation are dependable, let the harness propose
    another experiment, run it within the agreed budget, evaluate it, and update the research
    record; preserve the original objective and evaluation criteria, and keep changes to the
    scientific method visible to the researcher.

## Build sequence (priority order)

| # | Deliverable | Researcher benefit |
|---|---|---|
| 1 | Cluster knowledge, environment setup, and checks before submission | Get the first run working with fewer avoidable failures |
| 2 | Persistent experiment records and reliable job tracking | Leave work running and return without reconstructing its state |
| 3 | Evidence-based diagnosis and bounded recovery | Recover from failures with less manual investigation |
| 4 | Provenance, output validation, and result comparison | Know what ran, whether it worked, and what changed |
| 5 | Workflow automation and iterative experimentation | Complete more of the research process unattended |

## v1 foundations this builds on (already in the tree)

- `slurm.ClusterSnapshot` + the background poller (the F12 Status page).
- The alliance skills (`alliance-slurm` / `alliance-cvmfs` / `alliance-docs`) — cluster knowledge
  and lab know-how.
- **Structured Slurm tools + reconciliation** (submit/track/cancel with double-submit guards).
- `Directive` / `Objective` / `Attempt` / `Artifact` provenance + the SQLite history graph.
- `unimatrix.Session` (the headless loop).
- Queen budgets / permissions.
- The Transwarp `--towel` / `ls` / `attach` / `stop` socket (a primitive for the server side).

## Near-term candidates (client-side, no server needed)

Pull forward one at a time — none are in scope until chosen.

- Pre-submit job checks (#4); explain scheduling / fairshare / pending (#7); cluster-knowledge
  staleness (#1); provenance capture (#15); output validation (#16); result comparison / analysis
  (#17, with the dataviz skill); reusable lab knowledge via skills (#20); client-side autonomy
  limits (#21, the Queen subset).

## Blocked on the (not-yet-built) server side

Persistent project state across sessions (#2); full job-lifecycle durability + import (#8);
survive-disconnect resume (#9); sweeps / workflows at scale (#10); checkpoint recovery (#12);
iterative experimentation (#22).

## Pilot success metrics

Time to first valid result · avoidable failed runs · researcher interventions per experiment ·
ability to reproduce an earlier result. These tell us which features are worth developing further.
