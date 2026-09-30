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
