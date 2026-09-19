package bot

import (
	"strconv"
	"strings"
)

// FormatToman renders an integer amount with thousand separators, e.g. 45000 -> "45,000".
func FormatToman(n int) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.Itoa(n)

	var groups []string
	for len(s) > 3 {
		groups = append([]string{s[len(s)-3:]}, groups...)
		s = s[:len(s)-3]
	}
	groups = append([]string{s}, groups...)

	out := strings.Join(groups, ",")
	if neg {
		out = "-" + out
	}
	return out
}

// FormatWeight renders a kilogram amount without a trailing ".0" for whole numbers.
func FormatWeight(kg float64) string {
	if kg == float64(int64(kg)) {
		return strconv.FormatInt(int64(kg), 10)
	}
	return strconv.FormatFloat(kg, 'f', 1, 64)
}
