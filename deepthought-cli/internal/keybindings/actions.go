package keybindings

// ActionInfo describes a bindable action. It is the single registry read by
// the key map, the command palette, the F-key labels and Settings › Shortcuts.
type ActionInfo struct {
	Action Action
	// Short is the compact name used in legends ("model chooser").
	Short string
	// Label is the palette title ("Switch active model").
	Label  string
	Detail string
}

var actionInfos = []ActionInfo{
	{Settings, "settings", "Settings", "Providers, models, roles, appearance"},
	{Help, "help", "Help", "Keys, commands and install instructions"},
	{Model, "model chooser", "Switch active model", ""},
	{Effort, "effort", "Reasoning effort", ""},
	{NewChat, "new chat", "New chat", ""},
	{Resume, "resume", "Continue a saved chat", ""},
	{ContextView, "grid", "Context", "What the model sees this turn"},
	{Cron, "cron", "Cron", "Your crontab"},
	{QueenMode, "mode", "Permission mode", "Cycle safe · safe-auto · auto"},
	{Sidebar, "sidebar", "Toggle sidebar", ""},
	{Models, "models", "Models and provider catalogs", ""},
	{Diagnostics, "status", "Status", "Session, providers, cluster"},
}

// Actions returns every bindable action in display order.
func Actions() []ActionInfo { return append([]ActionInfo(nil), actionInfos...) }

// Info returns the registry entry for a.
func Info(a Action) (ActionInfo, bool) {
	for _, info := range actionInfos {
		if info.Action == a {
			return info, true
		}
	}
	return ActionInfo{}, false
}
