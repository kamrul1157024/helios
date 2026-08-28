package tui

import (
	"math"
	"strconv"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// Every scheme that paints its own card, checked against both the resting and
// the selected card. The schemes were originally hand-picked against one
// background and reused across all of them, which is how `forest` ended up
// drawing a green "active" dot on a green card at 2.89:1.

// xterm256 is the standard 256-colour palette: sixteen system colours, a
// 6×6×6 cube, then a 24-step grey ramp.
func xterm256() [256][3]float64 {
	var p [256][3]float64
	system := [16][3]float64{
		{0, 0, 0}, {128, 0, 0}, {0, 128, 0}, {128, 128, 0},
		{0, 0, 128}, {128, 0, 128}, {0, 128, 128}, {192, 192, 192},
		{128, 128, 128}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0},
		{0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255},
	}
	copy(p[:16], system[:])
	levels := [6]float64{0, 95, 135, 175, 215, 255}
	i := 16
	for _, r := range levels {
		for _, g := range levels {
			for _, b := range levels {
				p[i] = [3]float64{r, g, b}
				i++
			}
		}
	}
	for step := range 24 {
		v := float64(8 + step*10)
		p[i] = [3]float64{v, v, v}
		i++
	}
	return p
}

var palette = xterm256()

func luminance(t *testing.T, c lipgloss.Color) float64 {
	t.Helper()
	index, err := strconv.Atoi(string(c))
	if err != nil || index < 0 || index > 255 {
		t.Fatalf("colour %q is not an xterm index", string(c))
	}
	rgb := palette[index]
	channel := func(v float64) float64 {
		s := v / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(rgb[0]) + 0.7152*channel(rgb[1]) + 0.0722*channel(rgb[2])
}

func contrast(t *testing.T, a, b lipgloss.Color) float64 {
	t.Helper()
	la, lb := luminance(t, a), luminance(t, b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

func TestSchemes_KeepTheirTextLegibleOnBothCardStates(t *testing.T) {
	for name, c := range schemes {
		if !c.hasBG {
			// The card is the terminal's own background, which the app does
			// not choose and therefore cannot measure against.
			continue
		}
		roles := []struct {
			label  string
			colour lipgloss.Color
			floor  float64
		}{
			{"onSurface", c.onSurface, 4.5},
			{"onSurfaceDim", c.onSurfaceDim, 4.5},
			{"onSurfaceMuted", c.onSurfaceMuted, 3},
			{"outline", c.outline, 3},
			{"primary", c.primary, 3},
			{"statusActive", c.statusActive, 3},
			{"statusWaiting", c.statusWaiting, 3},
			{"statusCompacting", c.statusCompacting, 3},
			{"statusError", c.statusError, 3},
			{"statusIdle", c.statusIdle, 3},
			{"statusTerminated", c.statusTerminated, 3},
			// A rule, not a label: visible without competing with the text.
			{"separator", c.separator, 1.8},
		}
		for _, card := range []struct {
			label  string
			colour lipgloss.Color
		}{{"surface", c.surface}, {"surfaceSelected", c.surfaceSelected}} {
			for _, role := range roles {
				got := contrast(t, role.colour, card.colour)
				if got < role.floor {
					t.Errorf("%s: %s on %s is %.2f, want at least %.2f",
						name, role.label, card.label, got, role.floor)
				}
			}
		}
	}
}
