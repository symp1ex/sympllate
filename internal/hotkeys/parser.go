package hotkeys

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	ModAlt      uint32 = 0x0001
	ModControl  uint32 = 0x0002
	ModShift    uint32 = 0x0004
	ModWin      uint32 = 0x0008
	ModNoRepeat uint32 = 0x4000
)

type Combination struct {
	Modifiers  uint32
	VirtualKey uint32
	Display    string
}

func Parse(value string) (Combination, error) {
	parts := strings.Split(value, "+")
	if len(parts) < 2 {
		return Combination{}, errors.New("hotkey must contain a modifier and a key")
	}
	var modifiers uint32
	seen := map[string]bool{}
	for _, raw := range parts[:len(parts)-1] {
		part := strings.ToLower(strings.TrimSpace(raw))
		if seen[part] {
			return Combination{}, fmt.Errorf("duplicate modifier %q", raw)
		}
		seen[part] = true
		switch part {
		case "ctrl", "control":
			modifiers |= ModControl
		case "alt":
			modifiers |= ModAlt
		case "shift":
			modifiers |= ModShift
		case "win", "windows":
			modifiers |= ModWin
		default:
			return Combination{}, fmt.Errorf("unknown modifier %q", raw)
		}
	}
	if modifiers == 0 {
		return Combination{}, errors.New("no modifier specified")
	}
	keyName := strings.ToUpper(strings.TrimSpace(parts[len(parts)-1]))
	key, err := parseKey(keyName)
	if err != nil {
		return Combination{}, err
	}
	return Combination{Modifiers: modifiers | ModNoRepeat, VirtualKey: key, Display: value}, nil
}

func parseKey(key string) (uint32, error) {
	if len(key) == 1 && ((key[0] >= 'A' && key[0] <= 'Z') || (key[0] >= '0' && key[0] <= '9')) {
		return uint32(key[0]), nil
	}
	if strings.HasPrefix(key, "F") {
		n, err := strconv.Atoi(strings.TrimPrefix(key, "F"))
		if err == nil && n >= 1 && n <= 12 {
			return uint32(0x70 + n - 1), nil
		}
	}
	if key, ok := map[string]uint32{"SPACE": 0x20, "ENTER": 0x0D, "TAB": 0x09, "ESCAPE": 0x1B, "ESC": 0x1B}[key]; ok {
		return key, nil
	}
	return 0, fmt.Errorf("unsupported key %q", key)
}
