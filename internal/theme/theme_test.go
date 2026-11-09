package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// 5.4 — Apply switches the concrete color vars, and presets differ from the
// default. Restores the default afterward so other tests see the normal palette.
func TestApplyPresetsAndOverrides(t *testing.T) {
	t.Cleanup(func() { Apply(Default()) })

	Apply(Default())
	dark := Text
	Apply(Light())
	if Text == dark {
		t.Fatal("light preset should change the Text color")
	}
	if BgDark != lipgloss.Color(Light().BgDark) {
		t.Fatalf("BgDark = %v, want %v", BgDark, Light().BgDark)
	}

	// An override on top of a preset takes effect (and rebuilds derived styles).
	p := Default()
	p.Primary = "#123456"
	p.Heading1 = "#123456"
	Apply(p)
	if Primary != lipgloss.Color("#123456") {
		t.Fatalf("Primary override not applied: %v", Primary)
	}
	if EditorH1.GetForeground() != lipgloss.Color("#123456") {
		t.Fatalf("EditorH1 not rebuilt from override: %v", EditorH1.GetForeground())
	}
}

func TestPresetSelectionContrast(t *testing.T) {
	for _, p := range []Palette{Default(), Light()} {
		if got := ContrastRatio(p.OnPrimary, p.Primary); got < 4.5 {
			t.Fatalf("primary contrast %.2f", got)
		}
		if got := ContrastRatio(p.OnAccent, p.Accent); got < 4.5 {
			t.Fatalf("accent contrast %.2f", got)
		}
	}
}

func TestPresetSelection(t *testing.T) {
	if Preset("light").BgDark != Light().BgDark {
		t.Fatal(`Preset("light") should return the Light palette`)
	}
	if Preset("dark").BgDark != Default().BgDark || Preset("").BgDark != Default().BgDark {
		t.Fatal("unknown/empty preset should fall back to Default")
	}
}
