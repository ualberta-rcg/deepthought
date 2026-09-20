// Package keybindings maps terminal keys to stable harness actions.
package keybindings

import (
	"encoding/json"
	"fmt"
	"os"
)

type Context string
type Action string

const (
	Global Context = "global"

	Help        Action = "app:help"
	Settings    Action = "app:settings"
	Model       Action = "app:model"
	Effort      Action = "app:effort"
	NewChat     Action = "app:new-chat"
	Resume      Action = "app:resume"
	ContextView Action = "app:context"
	Models      Action = "app:models"
	QueenMode   Action = "app:queen-mode"
	Diagnostics Action = "app:diagnostics"
)

var reserved = map[string]bool{"ctrl+c": true, "ctrl+d": true}

var defaults = map[string]Action{
	"f1": Help, "f2": Settings, "f3": Model, "f4": Effort,
	"f5": NewChat, "f6": Resume, "f7": ContextView,
	"f9":  QueenMode,
	"f11": Models,
	"f12": Diagnostics,
}

type File map[Context]map[string]Action

type Map struct{ bindings File }

func Defaults() *Map {
	global := make(map[string]Action, len(defaults))
	for key, action := range defaults {
		global[key] = action
	}
	return &Map{bindings: File{Global: global}}
}

// Load overlays user bindings onto defaults. Reserved keys cannot be rebound.
func Load(path string) (*Map, error) {
	out := Defaults()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	var file File
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("keybindings: %w", err)
	}
	for context, bindings := range file {
		if out.bindings[context] == nil {
			out.bindings[context] = map[string]Action{}
		}
		for key, action := range bindings {
			if reserved[key] {
				return nil, fmt.Errorf("keybindings: %s is reserved", key)
			}
			out.bindings[context][key] = action
		}
	}
	return out, nil
}

func (m *Map) Resolve(context Context, key string) (Action, bool) {
	if m != nil {
		if action, ok := m.bindings[context][key]; ok {
			return action, true
		}
		if action, ok := m.bindings[Global][key]; ok {
			return action, true
		}
	}
	return "", false
}

func Reserved(key string) bool { return reserved[key] }
