package tui

// Layout breakpoints shared by every screen. They follow the 80/120 column
// convention of Crush, Textual and GitHub's TUIKit: below BreakCompact the
// layout is compact (no sidebar, single-pane lists); from BreakWide up — and
// with at least MinSidebarRows rows — the sidebar shows in "auto" mode.
const (
	// MinChatCols is the narrowest the chat column may get when the user
	// forces the sidebar on; below it the sidebar auto-hides.
	MinChatCols = 60
	// BreakCompact is the first "normal" width.
	BreakCompact = 80
	// BreakWide is the first "wide" width.
	BreakWide = 120
	// MinSidebarRows is the height the sidebar needs in "auto" mode.
	MinSidebarRows = 30
	// SidebarGutter is the blank column between the chat and the sidebar.
	SidebarGutter = 1
)
