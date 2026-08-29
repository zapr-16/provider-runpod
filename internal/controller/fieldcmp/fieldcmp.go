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

// derivedNameSuffixLen is the number of UID characters appended to a
// resource's base name. Kubernetes UIDs are UUIDs, whose first 8 characters
// are already hex digits ahead of the first hyphen, so this is a plain
// prefix of the UID string rather than a hash or re-encoding.
const derivedNameSuffixLen = 8

// DerivedName returns the deterministic name a managed resource sends to the
// RunPod API on create: base (the current name source - a spec name field if
// one exists, or metadata.name otherwise) with "-" plus the first 8 hex
// characters of the resource's Kubernetes UID appended. RunPod create calls
// are not idempotent and bill real GPUs; making the name deterministic lets
// a controller safely recover from a create whose result was never
// persisted (crash or restart between the POST and the external-name
// annotation write) by listing and matching on this exact name, instead of
// either leaking the resource or guessing whether it is safe to retry.
// uid is empty in unit tests that never set ObjectMeta.UID, in which case
// the base name is returned unchanged.
func DerivedName(base, uid string) string {
	if uid == "" {
		return base
	}
	if len(uid) > derivedNameSuffixLen {
		uid = uid[:derivedNameSuffixLen]
	}
	return base + "-" + uid
}
