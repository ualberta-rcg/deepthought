package tui

import (
	"deepthought-cli/internal/config"
	"deepthought-cli/internal/keybindings"
	"fmt"
	"strconv"
	"strings"
)

func keyboardFields() []fieldDef {
	actions := []keybindings.Action{keybindings.Settings, keybindings.Help, keybindings.Model, keybindings.Effort, keybindings.NewChat, keybindings.Resume, keybindings.ContextView, keybindings.Cron, keybindings.QueenMode, keybindings.Sidebar, keybindings.Models, keybindings.Diagnostics}
	var out []fieldDef
	for _, action := range actions {
		a := action
		out = append(out, fieldDef{label: strings.TrimPrefix(string(a), "app:"), kind: fText,
			get: func(f *config.File) string {
				v := map[string]keybindings.Action{}
				for k, a := range f.Keybindings {
					v[k] = keybindings.Action(a)
				}
				return keybindings.WithOverrides(v).KeyFor(a)
			},
			set: func(f *config.File, e *fieldEdit) error {
				k := strings.ToLower(strings.TrimSpace(e.value()))
				if keybindings.Reserved(k) {
					return fmt.Errorf("reserved key; Ctrl+P always opens navigation")
				}
				valid := false
				if n, err := strconv.Atoi(strings.TrimPrefix(k, "f")); strings.HasPrefix(k, "f") && err == nil && n >= 1 && n <= 12 {
					valid = true
				}
				if (strings.HasPrefix(k, "ctrl+") && len(k) == 6) || (strings.HasPrefix(k, "alt+") && len(k) == 5) {
					valid = true
				}
				if !valid {
					return fmt.Errorf("use F1–F12, ctrl+letter, or alt+letter")
				}
				existing := keybindings.WithOverrides(func() map[string]keybindings.Action {
					m := map[string]keybindings.Action{}
					for k, v := range f.Keybindings {
						m[k] = keybindings.Action(v)
					}
					return m
				}())
				if other, ok := existing.Resolve(keybindings.Global, k); ok && other != a {
					return fmt.Errorf("key already assigned to %s", other)
				}
				if f.Keybindings == nil {
					f.Keybindings = map[string]string{}
				}
				for old, v := range f.Keybindings {
					if v == string(a) {
						delete(f.Keybindings, old)
					}
				}
				f.Keybindings[k] = string(a)
				return nil
			}})
	}
	out = append(out, fieldDef{label: "Reset all shortcuts", kind: fEnum, options: []string{"keep", "RESET"}, get: func(*config.File) string { return "keep" }, set: func(f *config.File, e *fieldEdit) error {
		if e.value() == "RESET" {
			f.Keybindings = nil
		}
		return nil
	}})
	return out
}

func serverFields() []fieldDef {
	var out []fieldDef
	for _, name := range []string{"URL", "User", "Password"} {
		n := name
		out = append(out, fieldDef{label: n, kind: fText, password: n == "Password", get: func(f *config.File) string {
			if f.Server == nil {
				return ""
			}
			switch n {
			case "URL":
				return f.Server.URL
			case "User":
				return f.Server.User
			default:
				return f.Server.Password
			}
		}, set: func(f *config.File, e *fieldEdit) error {
			if f.Server == nil {
				f.Server = &config.ServerConfig{}
			}
			switch n {
			case "URL":
				f.Server.URL = strings.TrimSpace(e.value())
			case "User":
				f.Server.User = strings.TrimSpace(e.value())
			default:
				f.Server.Password = e.value()
			}
			return nil
		}})
	}
	return out
}
