package normalizer

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ParseTime parses 1:12:05, 41:20, 2480 or 41:20.5 as seconds.
func ParseTime(s string) (float64, error) {
	parts := strings.Split(s, ":")
	if len(parts) > 3 || s == "" {
		return 0, fmt.Errorf("invalid time %q (use h:mm:ss, mm:ss or seconds)", s)
	}
	var total float64
	for i, part := range parts {
		last := i == len(parts)-1
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || v < 0 || (!last && v != math.Trunc(v)) || (i > 0 && v >= 60) {
			return 0, fmt.Errorf("invalid time %q (use h:mm:ss, mm:ss or seconds)", s)
		}
		total = total*60 + v
	}
	return total, nil
}

// FormatTime formats seconds as h:mm:ss, with milliseconds only when needed.
func FormatTime(s float64) string {
	h, rest := int(s)/3600, s-float64(int(s)/3600*3600)
	m := int(rest) / 60
	sec := rest - float64(m*60)
	if sec == math.Trunc(sec) {
		return fmt.Sprintf("%d:%02d:%02d", h, m, int(sec))
	}
	return fmt.Sprintf("%d:%02d:%06.3f", h, m, sec)
}
