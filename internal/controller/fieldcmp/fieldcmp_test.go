package fieldcmp

import (
	"reflect"
	"testing"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
)

func TestBuildEnvMap(t *testing.T) {
	cases := map[string]struct {
		in   []v1alpha1.EnvVar
		want map[string]string
	}{
		"Empty": {in: nil, want: nil},
		"Single": {
			in:   []v1alpha1.EnvVar{{Name: "FOO", Value: "bar"}},
			want: map[string]string{"FOO": "bar"},
		},
		"Multiple": {
			in: []v1alpha1.EnvVar{
				{Name: "FOO", Value: "bar"},
				{Name: "BAZ", Value: "qux"},
			},
			want: map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := BuildEnvMap(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildEnvMap(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildPortTokens(t *testing.T) {
	cases := map[string]struct {
		in   []v1alpha1.Port
		want []string
	}{
		"Empty":           {in: nil, want: nil},
		"DefaultsToTCP":   {in: []v1alpha1.Port{{Number: 22}}, want: []string{"22/tcp"}},
		"LowercasesProto": {in: []v1alpha1.Port{{Number: 8000, Protocol: "HTTP"}, {Number: 22, Protocol: "tcp"}}, want: []string{"8000/http", "22/tcp"}},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := BuildPortTokens(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("BuildPortTokens(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestPortTokensEqual(t *testing.T) {
	cases := map[string]struct {
		want, observed []string
		equal          bool
	}{
		"BothNil":          {equal: true},
		"OrderInsensitive": {want: []string{"22/tcp", "8000/http"}, observed: []string{"8000/http", "22/tcp"}, equal: true},
		"CaseAndDefault":   {want: []string{"22/tcp", "8000/http"}, observed: []string{"22", "8000/HTTP"}, equal: true},
		"DifferentLen":     {want: []string{"22/tcp"}, observed: []string{"22/tcp", "8000/http"}, equal: false},
		"DifferentToken":   {want: []string{"22/tcp"}, observed: []string{"22/udp"}, equal: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := PortTokensEqual(tc.want, tc.observed); got != tc.equal {
				t.Fatalf("PortTokensEqual(%v, %v) = %v, want %v", tc.want, tc.observed, got, tc.equal)
			}
		})
	}
}

func TestNormalizePortToken(t *testing.T) {
	if got := NormalizePortToken(8000, ""); got != "8000/tcp" {
		t.Fatalf("NormalizePortToken default protocol = %q, want 8000/tcp", got)
	}
	if got := NormalizePortToken(8000, "HTTP"); got != "8000/http" {
		t.Fatalf("NormalizePortToken uppercase protocol = %q, want 8000/http", got)
	}
}

func TestNormalizeObservedToken(t *testing.T) {
	cases := map[string]string{
		"22":        "22/tcp",
		"22/":       "22/tcp",
		"22/TCP":    "22/tcp",
		"8000/http": "8000/http",
	}

	for in, want := range cases {
		if got := NormalizeObservedToken(in); got != want {
			t.Fatalf("NormalizeObservedToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDerivedName(t *testing.T) {
	cases := map[string]struct {
		base string
		uid  string
		want string
	}{
		"EmptyUIDReturnsBaseUnchanged": {
			base: "vllm-small",
			uid:  "",
			want: "vllm-small",
		},
		"StandardUUIDUsesFirst8Chars": {
			base: "vllm-small",
			uid:  "550e8400-e29b-41d4-a716-446655440000",
			want: "vllm-small-550e8400",
		},
		"ShortUIDUsedInFull": {
			base: "vllm-small",
			uid:  "ab12",
			want: "vllm-small-ab12",
		},
		"ExactlyEightCharUID": {
			base: "vllm-small",
			uid:  "aabbccdd",
			want: "vllm-small-aabbccdd",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := DerivedName(tc.base, tc.uid)
			if got != tc.want {
				t.Fatalf("DerivedName(%q, %q) = %q, want %q", tc.base, tc.uid, got, tc.want)
			}
		})
	}
}
