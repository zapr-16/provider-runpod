package fieldcmp

import (
	"testing"

	v1alpha1 "github.com/zapr-16/provider-runpod/apis/v1alpha1"
)

func TestStringSlicesEqual(t *testing.T) {
	cases := map[string]struct {
		a, b []string
		want bool
	}{
		"BothNil":        {a: nil, b: nil, want: true},
		"BothEmpty":      {a: []string{}, b: []string{}, want: true},
		"Equal":          {a: []string{"a", "b"}, b: []string{"a", "b"}, want: true},
		"DifferentLen":   {a: []string{"a"}, b: []string{"a", "b"}, want: false},
		"DifferentOrder": {a: []string{"a", "b"}, b: []string{"b", "a"}, want: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := StringSlicesEqual(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("StringSlicesEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestStringMapsEqual(t *testing.T) {
	cases := map[string]struct {
		a, b map[string]string
		want bool
	}{
		"BothNil":        {a: nil, b: nil, want: true},
		"BothEmpty":      {a: map[string]string{}, b: map[string]string{}, want: true},
		"Equal":          {a: map[string]string{"k": "v"}, b: map[string]string{"k": "v"}, want: true},
		"DifferentLen":   {a: map[string]string{"k": "v"}, b: map[string]string{}, want: false},
		"MissingKey":     {a: map[string]string{"k": "v"}, b: map[string]string{"other": "v"}, want: false},
		"DifferentValue": {a: map[string]string{"k": "v"}, b: map[string]string{"k": "other"}, want: false},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := StringMapsEqual(tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("StringMapsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

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
			if !StringMapsEqual(got, tc.want) {
				t.Fatalf("BuildEnvMap(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestCloneStrings(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want []string
	}{
		"Nil":   {in: nil, want: nil},
		"Empty": {in: []string{}, want: nil},
		"NonEmpty": {
			in:   []string{"a", "b"},
			want: []string{"a", "b"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := CloneStrings(tc.in)
			if !StringSlicesEqual(got, tc.want) {
				t.Fatalf("CloneStrings(%v) = %v, want %v", tc.in, got, tc.want)
			}

			if len(tc.in) > 0 {
				got[0] = "mutated"
				if tc.in[0] == "mutated" {
					t.Fatalf("CloneStrings did not copy the input slice")
				}
			}
		})
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
