package normalizer

import (
	"fmt"
	"math"
)

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
