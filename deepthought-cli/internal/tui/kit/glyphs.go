package kit

import (
	"os"
	"strings"
	"sync/atomic"
)

// Glyphs is the table every status indicator draws from, so an ASCII
// terminal gets a designed fallback instead of tofu.
type Glyphs struct {
	Check, Cross, Warn, Dot, Arrow, Bullet, Cursor, Ellipsis string
	BarFull, BarEmpty                                        string
	Up, Down                                                 string
	// Box corners and edges for Panel/Dialog borders.
	TL, TR, BL, BR, H, V string
}

var unicodeGlyphs = Glyphs{
	Check: "✓", Cross: "✗", Warn: "⚠", Dot: "●", Arrow: "→", Bullet: "•", Cursor: "▶", Ellipsis: "…",
	BarFull: "█", BarEmpty: "░", Up: "↑", Down: "↓",
	TL: "╭", TR: "╮", BL: "╰", BR: "╯", H: "─", V: "│",
}

var asciiGlyphs = Glyphs{
	Check: "*", Cross: "x", Warn: "!", Dot: "@", Arrow: ">", Bullet: "-", Cursor: ">", Ellipsis: "...",
	BarFull: "#", BarEmpty: ".", Up: "^", Down: "v",
	TL: "+", TR: "+", BL: "+", BR: "+", H: "-", V: "|",
}

var asciiMode atomic.Bool

func init() { asciiMode.Store(DetectASCII(os.Environ())) }

// DetectASCII reports whether env asks for ASCII-only output:
// DEEPTHOUGHT_ASCII=1, or a TERM known not to render box/braille glyphs.
func DetectASCII(env []string) bool {
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch {
		case k == "DEEPTHOUGHT_ASCII" && v != "" && v != "0" && v != "false":
			return true
		case k == "TERM" && (v == "dumb" || v == "linux"):
			return true
		}
	}
	return false
}

// SetASCII forces ASCII glyphs on or off (the --ascii flag, per SSH session env).
func SetASCII(on bool) { asciiMode.Store(on) }

// ASCII reports whether ASCII glyphs are in use.
func ASCII() bool { return asciiMode.Load() }

// G returns the active glyph table.
func G() Glyphs {
	if asciiMode.Load() {
		return asciiGlyphs
	}
	return unicodeGlyphs
}
