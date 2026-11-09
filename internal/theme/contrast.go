package theme

import (
	"math"
	"strconv"
	"strings"
)

// ContrastRatio returns the WCAG relative-luminance contrast for two #RRGGBB colors.
func ContrastRatio(a, b string) float64 {
	la, oka := luminance(a)
	lb, okb := luminance(b)
	if !oka || !okb {
		return 0
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + .05) / (lb + .05)
}

func luminance(hex string) (float64, bool) {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return 0, false
	}
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, false
	}
	channel := func(x uint64) float64 {
		c := float64(x) / 255
		if c <= .04045 {
			return c / 12.92
		}
		return math.Pow((c+.055)/1.055, 2.4)
	}
	return .2126*channel((v>>16)&255) + .7152*channel((v>>8)&255) + .0722*channel(v&255), true
}
