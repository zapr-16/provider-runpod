// Package fieldcmp holds small comparison and copy helpers for drift
// detection that are shared across managed-resource external clients.
package fieldcmp

import (
	"fmt"
	"maps"
	"strings"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
)

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

// BuildPortTokens converts spec ports to "<port>/<protocol>" tokens, or nil
// if in is empty.
func BuildPortTokens(in []v1alpha1.Port) []string {
	if len(in) == 0 {
		return nil
	}

	out := make([]string, 0, len(in))
	for _, port := range in {
		out = append(out, NormalizePortToken(port.Number, port.Protocol))
	}
	return out
}

// PortTokensEqual compares two port-token slices as sets, since the RunPod
// API does not guarantee ordering. Both sides are normalized first.
func PortTokensEqual(want, observed []string) bool {
	return maps.Equal(tokenSet(want), tokenSet(observed))
}

func tokenSet(tokens []string) map[string]struct{} {
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[NormalizeObservedToken(token)] = struct{}{}
	}
	return set
}

// NormalizePortToken builds a "<port>/<protocol>" token from spec fields;
// the protocol is always appended, defaulting to tcp when unset.
func NormalizePortToken(number int32, protocol string) string {
	return fmt.Sprintf("%d/%s", number, NormalizeProtocol(protocol))
}

// NormalizeObservedToken parses a RunPod "<port>/<protocol>" token; a
// missing protocol segment defaults to tcp.
func NormalizeObservedToken(token string) string {
	port, protocol, _ := strings.Cut(strings.ToLower(token), "/")
	return fmt.Sprintf("%s/%s", port, NormalizeProtocol(protocol))
}

// NormalizeProtocol lowercases the protocol, defaulting empty to tcp.
func NormalizeProtocol(protocol string) string {
	if protocol == "" {
		return "tcp"
	}
	return strings.ToLower(protocol)
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
