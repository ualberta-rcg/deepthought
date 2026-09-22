# Startup and configuration

Install the Linux/amd64 rolling release:

```bash
curl -fsSL https://raw.githubusercontent.com/ualberta-rcg/deepthought/main/install.sh | bash
```

The destination defaults to `~/.local/bin`; `DEEPTHOUGHT_INSTALL_DIR` overrides it.
`DEEPTHOUGHT_CHANNEL` overrides the release tag. The installer verifies the checksum
when published and rejects mismatches. It does not configure your shell PATH.

## First use

The interface starts without inference. On a new host, local discovery runs after
the first screen. Review a candidate's endpoint and source, then choose **Accept
and discover models**. No provider request happens merely because a key was found.
Skip is persistent for that host. Retry or repeat discovery through Ctrl+P or
Settings → Setup. A shared home directory can contain records for multiple hosts.

Supported discovery sources are the process environment; `~/.aleph_tyk.env`;
literal assignments in `~/.bashrc`, `~/.bash_profile`, and `~/.profile`; `.env` in the
launch directory and repository root; and the supported Claude settings files.
Inputs are bounded to 512 KiB and must be regular files owned by the current user.
Shell expansion, commands, sourcing, and recursive searches are not supported.
Project candidates are explicitly labeled untrusted. Conflicting candidates are
shown individually, never silently selected.

Discovery recognizes `TYK_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`,
`ANTHROPIC_AUTH_TOKEN`, and `DEEPSEEK_API_KEY`, with their corresponding supported
base-URL variables. Credential values remain masked. Shell expressions such as
`KEY=$(command)` and `$OTHER_VARIABLE` assignments are deliberately not evaluated.

On Vulcan, `~/.aleph_tyk.env` can take several minutes to appear after first login.
Retry later; no shell sourcing is required. One key covers all Aleph models.

| Interface | Base URL |
|---|---|
| OpenAI-compatible | `https://inference.vulcan.alliancecan.ca/v1` |
| Anthropic-compatible | `https://inference.vulcan.alliancecan.ca/anthropic` |

Aleph model metadata comes from `/v1/models`. Discovery preserves advertised
capabilities and context limits, and does not infer capabilities from model names.
For catalogs without metadata, configure capabilities explicitly in the model
editor. Models without tool support use chat without advertising tools.

Catalog requests are cancellable and limited to 120 seconds. The last successful
catalog is cached for 24 hours; explicit refresh bypasses its age. A failed refresh
can show a stale catalog. Unsupported discovery does not prevent manual model-ID
entry. Catalog model identities include the provider, avoiding collisions between
providers serving the same wire ID.

The exact recognized old Aleph URL is offered for migration during setup. Custom
endpoints are preserved. Discovery never transmits credentials across an HTTP
redirect to a different origin.

## State and migration

`DEEPTHOUGHT_CLI_HOME` overrides `~/.deepthought`. `history.db` stores chats plus
separate `app_settings` and `app_inventory` tables. Settings profiles are identified
by their original configuration-file path, so `--config` profiles remain distinct.

On first migration, valid JSON settings are imported and the original is retained
as `config.json.before-database` beside the source. Existing keybindings are
imported. Invalid source files remain untouched and produce a startup notice.
Reopening the application does not repeatedly import the old JSON. SQLite owns
mutable settings; every successful edit or server download also atomically
updates the active `config.json` with private permissions. A missing mirror is
recreated. `--config` selects a persistent profile and its mirror, rather than
overriding every subsequent Settings edit.

Changes made externally to that file are preserved and flagged. Use Settings →
Import / Export to review and import the file, or export portable settings. A
failed mirror write leaves the database save intact and displays a retry notice.
The running application retries pending mirror writes once a minute; retry is
also available in Settings. A corrupt file is never silently overwritten.

Mutable settings save to SQLite with revision checks. A stale editor must reload
rather than overwrite another process's changes. Existing history is retained.
Literal credentials are moved to `secrets.env` (mode 0600); database settings carry
references. Existing environment references remain supported. Discovered keys
are never exported into the application's process environment.

The original migration backup can contain old credentials: keep it private.
Portable export and server settings synchronization exclude provider credentials,
server login details, and local scientific manifest paths. Host inventory and
telemetry are local and do not synchronize to the server.

## Precedence

Highest to lowest:

1. Session choices, including an explicit Settings edit of an overridden value.
2. Supported application environment overrides (`DEEPTHOUGHT_EFFORT`).
3. Saved local database settings, including reviewed file imports and merged
   server changes.
4. Previously cached defaults retained during migration.
5. Built-in defaults, which contain no assumed provider or model.

A discovery candidate has no precedence until accepted. Provider credential
references are resolved separately: an explicitly selected environment reference
uses that environment variable; an accepted file credential uses its private
stored reference. Settings displays source labels. A Settings edit is saved
locally, but an external override can take precedence again on the next launch.

## Client and server synchronization

Open Settings → Server to enter the URL, user and password, then choose
**Connection / Sync now / conflicts**. The navigation menu also opens Server
synchronization. Connect once to enable background reconnection on later launches;
Disconnect disables it. Authentication and synchronization happen after the
interface loads. Offline operation remains available.

On connection, the client fetches the user's server settings, merges local changes,
uploads the combined document if needed, and reads back the accepted result. That
result updates live settings, SQLite, and the config file. An empty server starts
from fleet defaults merged with the client's configuration. Subsequent merges use
a durable baseline, including deletions; named providers and models can be added
independently by different clients. Conflicting edits offer local/server choices
in the synchronization screen. They are never silently resolved by last writer.

Saved edits trigger a debounced transfer; connected clients also check every five
minutes and offer **Sync now**. Revision conflicts retry against a fresh server
document. Offline changes survive restarts. Edits made during a transfer stay
pending for the next transfer. Disconnect cancels outstanding network work and
prevents a late download from applying; it cannot undo a request already accepted
by the server.

Shared settings include inference providers/endpoints, model definitions, roles,
routing, language, appearance, shortcuts, and inference preferences. Credentials,
server login, executable hooks, permissions, scientific tool bindings, host
inventory, and transient environment/session overrides stay local. A provider
downloaded onto another client needs a local credential binding. Changing its
endpoint or protocol clears the old binding so a key is not sent to a new service.
The synchronization view shows pending work, last success, and recoverable errors.

## Navigation

Ctrl+P opens navigation from the welcome screen or any operational screen.
Settings contains Setup, Models, Providers, Shortcuts, Hosts & Services, Server,
Import / Export, and existing advanced sections. F-keys remain shortcuts.
Shortcut editing accepts F1–F12 and modifier keys, rejects collisions, and supports
reset. Ctrl+P remains reserved so navigation cannot be lost.

Scientific tool catalogs are refreshed after startup through the explicit
navigation action. This can contact configured scientific endpoints; it never
runs during construction of the application.

The welcome screen includes an installation details entry. Its copy action uses
terminal clipboard support (OSC 52); the full command is always available in this
guide. DeepThought never executes the installer itself.
