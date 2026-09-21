# Standalone implementation review

Review baseline: `a277894`. This review covered the production entrypoints,
TUI routing and agent loop, shell, permission gate, provider adapters, settings,
history, skills, scheduler integration, resident protocol, and server skeleton.
The saved reference tree was indexed and relevant architecture and tool/context
implementations were inspected; this was not a line-by-line audit of every
vendored reference file. No credentials or user conversation data were inspected.

## Findings and implementation order

| Priority | Finding | Required regression |
|---|---|---|
| High | Tool cancellation used independent background contexts; approvals survived interruption | Cancel while running/awaiting approval; reject events from older turns |
| High | Off-screen streaming results went to the active screen | Navigate during streaming and continue receiving the owned events |
| High | SSH closure mutated shared dependencies; shell state crossed connections | Independent sessions and race checks |
| High | Grants overrode explicit deny; persistent approval widened command scope | Deny precedence, compound commands, exact argument grants |
| High | Tool arguments lacked schema validation | Invalid inputs cannot reach execution |
| High | Content deduplication dropped distinct history events; migration omitted resume data | Identity-based persistence, changed hash, legacy reopen |
| High | Settings snapshots shared pointers and saves could overwrite newer edits | Pointer isolation and conflicting edits |
| Medium | Cron undo backed up the target instead of current state | Two undos; failed install; external changes |
| Medium | Shell cancellation made the shell permanently unusable; unbroken output grew unbounded | Recovery, pre-cancel, bounded unbroken output |
| Medium | Stream EOF could look successful; direct replies omitted tool calls | Truncated/malformed/error SSE and complete tool calls |
| Medium | Models screen had no initialized store; welcome lettering/options were unclear | Fixture navigation and terminal dimensions |
| Medium | Context assembly, resident execution, workflow and reconciliation primitives were not integrated | Real end-to-end paths, not isolated package tests |

## Reference lessons

Saved PTY examples cover welcome/chat navigation, inline settings, provider
discovery, thinking streams and resume. Convert them to fixture-backed Go tests:
they must not copy personal settings, require API keys, submit real jobs, or
modify the user's crontab. Saved rendering examples inform terminal-size and
wordmark coverage.

The reference tool contract separates schema validation, permission, execution,
and presentation. Context references favor measured budgets, bounded tool output,
lazy skills, and preserving complete tool exchanges. Skill and memory references
keep static instructions separate from learned evidence. These are design inputs;
the implementations here are original Go code. Unverified product notes are not
treated as authoritative behavior.

## Release gates

1. Reliability and welcome screen; preserve existing storage compatibility.
2. Workspace tools, bounded live context, instruction/skill loading and diagnostics.
3. Layered local settings and resident execution across terminal disconnects.
4. Durable scheduler workflows, approval-based recovery and provenance.

Each code commit requires a dated root changelog entry with verification. Compile,
test and race-check in Slurm, push tested commits to main without force, retain
the previous executable during atomic deployment, and leave existing sessions
running. Server login, identity federation and server deployment remain deferred.

## Implemented follow-through (2026-09-20)

The release gates above now have implementation paths: shared resident execution,
layered local configuration, scientific manifest registration, durable scheduler
submission and reconciliation, sequential workflow records, real predicates and
artifact staleness. The accompanying tests exercise disconnect during approval,
lost submission responses without duplicate sbatch calls, two objectives across
a database reopen, invalid scientific input/output, and concurrent plan edits.
See [local operations](../deepthought-cli/docs/LOCAL-OPERATIONS.md) for exact limits.

## Storage compatibility

The history identity migration retains existing table columns and legacy JSON
for older processes, removing only the incorrect content uniqueness constraint.
It takes a consistent SQLite backup before changing an existing schema. New
revision/metadata tables are additive. Large legacy JSON duplication is retained
while old sessions may still read it; destructive storage cleanup is deferred.

The shell envelope limits resources when available; it is not a complete OS
sandbox. Permission parsing likewise cannot prove the safety of arbitrary code.
