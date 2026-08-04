package hotkeys

import "testing"

func TestParse(t *testing.T) {
	t.Parallel()
	for input, key := range map[string]uint32{"Ctrl+Alt+T": 'T', "Ctrl+Shift+Space": 0x20, "Alt+F8": 0x77, "Win+Shift+R": 'R'} {
		got, err := Parse(input)
		if err != nil {
			t.Errorf("Parse(%q): %v", input, err)
			continue
		}
		if got.VirtualKey != key || got.Modifiers&ModNoRepeat == 0 {
			t.Errorf("Parse(%q) = %+v", input, got)
		}
	}
	for _, input := range []string{"T", "Ctrl+Nope", "Ctrl+F13", "Ctrl+Ctrl+T", "Ctrl+"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) expected error", input)
		}
	}
}
