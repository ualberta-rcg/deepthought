# Hosts and services

The same structured observations feed the sidebar, detailed Status, Settings
inventory, and the chat's execution-context note. The client host and the local
execution target are explicit; inventory does not introduce remote execution.

## Identity and persistence

A host fingerprint combines a hash of the machine ID with the hostname; the raw
machine ID is not saved or sent to the model. When unavailable, hostname matching
is labeled uncertain. A random client identifier is persisted separately for each
host in the local profile. Scheduler-reported cluster identity is separate, so
multiple login hosts can belong to one cluster. Renaming a host or replacing its
machine ID creates a new observation identity; automatic merging is not attempted.

`app_inventory` in `history.db` stores stable host inventory separately from the
latest `telemetry` snapshot, plus service records, setup state, and model catalogs.
It overwrites telemetry snapshots rather than retaining
an unlimited sample history. These records do not synchronize to DeepThought
Server. Hosts & Services shows remembered hosts and last-seen timestamps.

Services distinguish a command being installed, configuration being detected,
and successful access being observed. Slurm, CVMFS, Lmod, and Globus are separate
records; a Globus CLI installation does not prove authentication or connectivity.
No service login is attempted by inventory collection.

## Observation scope

The host display includes CPU and memory capacity, measured utilization when
available, GPU query results, selected filesystem capacity, and current Slurm
allocation. Container/cgroup limits are shown separately. Scheduler allocation
counts are not hardware utilization measurements. Filesystem capacity is not a
personal quota. CPU utilization requires two samples; unknown values stay unknown.

Host type remains unknown unless an allocation or explicit evidence supports a
classification. Merely finding Slurm commands does not identify a login node.

The sidebar defaults to 44 columns and can be adjusted from 32 to 60 in Settings.
When space is insufficient, chat retains a compact host/allocation strip. Status
has the detailed observations and timestamps. Ctrl+P gives access at any width.

## Scheduler and refresh discipline

Host observations refresh no more often than every 30 seconds. Scheduler snapshots
are cached for at least five minutes; accounts/fairshare and quota discovery have
a fifteen-minute minimum interval. Requests share in-process caches. Read-only
commands have a five-second timeout and a 1 MiB output bound; the whole scheduler
snapshot has a bounded deadline. Manual refresh respects cache intervals.

Queries for jobs and accounts are scoped to the current user. Cluster summaries
contain resource/partition information, not other researchers' job details.
Fairshare is a scheduling factor, not an estimated queue position or start time.

A failed controller query preserves the last snapshot with an unavailable/stale
indicator. Restricted accounting and missing tools are recoverable. Linux-only
telemetry and scheduler commands are optional; the application remains usable
without them.

No distributed scheduler, host-to-host communication, or automatic client
cooperation is included. This inventory is the local foundation for later work.
