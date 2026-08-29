// Package fieldcmp holds small comparison and copy helpers for drift
// detection that are shared across managed-resource external clients.
package fieldcmp

import (
	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
)

// StringSlicesEqual reports whether a and b contain the same strings in the
// same order.
func StringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// StringMapsEqual reports whether a and b have the same keys mapped to the
// same values.
func StringMapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || bv != av {
			return false
		}
	}
	return true
}

// BuildEnvMap flattens a slice of EnvVar into a name-to-value map, or nil if
// in is empty.
func BuildEnvMap(in []v1alpha1.EnvVar) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))
	for _, env := range in {
		out[env.Name] = env.Value
	}
	return out
}

// CloneStrings returns a copy of in, or nil if in is empty.
func CloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
