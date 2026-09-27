// Package ids derives stable identifiers, so that retries and re-runs (seed,
// migration, duplicate uploads) always produce the same IDs.
package ids

import (
	"strings"

	"github.com/google/uuid"
)

// namespace is fixed forever; changing it would change every derived ID.
var namespace = uuid.MustParse("6f0b1a3e-8d2c-5c4b-9a61-2f7e3c1d0b95")

// Grading identifies a course grading: institution + course code + period.
func Grading(institutionID, courseCode, period string) string {
	return uuid.NewSHA1(namespace, []byte("grading|"+institutionID+"|"+courseCode+"|"+normalise(period))).String()
}

// Institution derives an institution ID from its name (used for bootstrap).
func Institution(name string) string {
	return uuid.NewSHA1(namespace, []byte("institution|"+strings.ToLower(normalise(name)))).String()
}

// Derived returns a stable ID for any other natural key (e.g. migrations).
func Derived(kind string, parts ...string) string {
	return uuid.NewSHA1(namespace, []byte(kind+"|"+strings.Join(parts, "|"))).String()
}

// normalise trims and collapses internal whitespace.
func normalise(s string) string { return strings.Join(strings.Fields(s), " ") }
