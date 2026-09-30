# DeepThought TUI standard

One kit, `internal/tui/kit`, holds the shared building blocks. New surfaces are
built on it; existing screens move onto it one per commit, each with a
fixture test.

| Piece | Contract |
|---|---|
| `Panel` | Rounded card. Title sits in the top border; status on the right is dropped when it can't fit with a 1-cell gap; footnote is the first row dropped when short. Always exactly `w × h`. |
| `FitHeights` | Shares rows between cards in priority order: every card gets its minimum, then grows to preferred; lowest-priority cards are dropped first. |
| `List` | Cursor list with `/` fuzzy (subsequence) filter and highlighted matches, empty phrase (`None` by default), right-aligned key column hidden when wider than a quarter of the row, `↑ N more` / `↓ N more` overflow rows. |
| `KeyBar` | The current pane's keys, packed to the width; when truncated it ends with `…`, never a dangling separator. |
| `Dialog` | One centered overlay chrome: title truncated (never wrapped), info dropped if it doesn't fit, body clipped to the area with `…`, packed help line. |
| `Fit` / `Block` | Exact-width (ANSI-aware) truncation and padding. |

Rules:

- **Breakpoints** (`internal/tui/layout.go`): compact below 80 columns, wide at
  120+ (the sidebar shows in `auto` only at ≥120×30), chat keeps ≥60 columns
  when the sidebar is forced on.
- **Mnemonics** on every list: `n` new, `d` delete (confirm), `e` edit,
  `r` refresh, `t` test, `/` filter, `?` keys, `esc` back, `enter` select.
  In the chat input `/` stays the slash-command prefix.
- **Glyphs** come from `kit.G()`. ASCII fallbacks apply with `--ascii`,
  `DEEPTHOUGHT_ASCII=1`, or `TERM=dumb|linux`.
- **No SGR dim/faint**: muted text is a colour (it is inconsistent over SSH
  terminals). Colour follows the palette in `styles.go`; green/amber/red only
  for utilisation (70/90) and health.
- **Numerals that tick** use fixed-width formatters (`num.go`); estimates are
  prefixed `~`; the context meter always pairs the bar with the exact percent.
- **ctrl+c is progressive**: close overlay → interrupt the running turn →
  clear the typed prompt → (press again within 2 s) quit.

## Command palette

Ctrl+P (and `/menu`) opens the palette (`internal/tui/palette.go`), a `Dialog`
around a `List`. Empty input shows recent picks, then a short discover set;
typing fuzzy-searches every entry. Tab / Shift+Tab cycle
All · Actions · Screens · Settings · Chats · Slash; ↑↓ or Ctrl+P/Ctrl+N move;
Esc clears the query, then closes. Rows carry the bound key.

Entries come from one registry: bindable actions and their labels live in
`internal/keybindings/actions.go` (also read by the F-key labels and
Settings › Shortcuts), and `internal/app/actions.go` adds screens, settings
sections, the ten most recent chats and slash commands. Every entry dispatches
through the same `WorkspaceAction` handler as the menus. The last twelve
picks are stored in the local database (`palette-recent` record) and lead the
list.

## Sidebar

Boxed kit cards, top to bottom: **Session** (model · effort · permission mode,
the context meter with its exact percentage, standalone or connected server,
chat title, working directory), **Host** (meters, "updated Ns" footnote),
**Slurm** (queue, allocation, fairshare), **Your jobs** (state dot, id, name,
elapsed or pending reason; `None` when empty; long lists end
`…and N more`), and **Alerts** only when something needs the user (disk
≥ 90 %, settings sync needs attention, server session expired). When rows run
short every card shrinks to its minimum first, then cards drop in a fixed
order: Slurm, then jobs, then host. Session and Alerts stay. All data comes
from the background pollers; rendering never probes. Focusing the sidebar is
not implemented yet.

## Status

Every section is a kit card (`Section.Card`): title and summary in the top
border, rows with one cell of padding, the plain-language note wrapped to the
card, and the data source (`→ sinfo · squeue`) as the footnote. At an inner
width of 116+ (a 120-column terminal) the cards flow into two columns, each
card going to the shorter column. The page scrolls as one viewport; the clock
lives in the title so ticks never move the scroll.

## Jobs, Plans, Cron

Jobs and Plans share one list + detail layout (`listscreen.go`): a kit `List`
(`/` filter, ↑↓ / j k / Ctrl+P Ctrl+N) above a detail card for the selection
(retry advice and script hash for a submission; objectives, linked
submissions and artifacts for a plan). The card takes up to half the rows and
the list keeps at least three. Empty lists say `No … yet` with the next step
as the notice. Cron keeps its staged editor but uses the kit key bar, glyphs
and `n` for a new entry (`a` still works).
