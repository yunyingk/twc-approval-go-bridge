package invoice

import (
	"encoding/json"
	"regexp"
	"strings"
)

var decimalPattern = regexp.MustCompile(`^-?(?:[0-9]+|[0-9]{1,3}(?:,[0-9]{3})+)(?:\.[0-9]+)?$`)

// Decimal preserves precision and distinguishes malformed or missing values
// from zero. A comma is accepted only as a correctly grouped thousands separator.
func Decimal(value string) (json.Number, bool) {
	value = strings.TrimSpace(value)
	if !decimalPattern.MatchString(value) {
		return "", false
	}
	value = strings.ReplaceAll(value, ",", "")
	if !json.Valid([]byte(value)) {
		return "", false
	}
	return json.Number(value), true
}
