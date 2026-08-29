package clients

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// specPath points at the vendored RunPod OpenAPI document (see
// hack/spec/openapi.json and `make spec-update`). It is resolved relative to
// this package's directory so the test runs under plain `go test` from any
// working directory, with no network access.
const specPath = "../../hack/spec/openapi.json"

// openAPISchema is the minimal subset of an OpenAPI 3 schema object needed to
// resolve a request body's top-level property names: a $ref to a named
// component, an allOf composition of sub-schemas, or an inline object with
// properties.
type openAPISchema struct {
	Ref        string                     `json:"$ref,omitempty"`
	AllOf      []openAPISchema            `json:"allOf,omitempty"`
	Properties map[string]json.RawMessage `json:"properties,omitempty"`
}

// openAPIOperation is the subset of an OpenAPI operation object needed to
// find a JSON request body schema.
type openAPIOperation struct {
	RequestBody struct {
		Content map[string]struct {
			Schema openAPISchema `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
}

// openAPISpec is the minimal subset of an OpenAPI 3 document needed by this
// test: paths (keyed by path, then by lowercase HTTP method) and the
// component schemas that $ref targets resolve into.
type openAPISpec struct {
	Paths      map[string]map[string]openAPIOperation `json:"paths"`
	Components struct {
		Schemas map[string]openAPISchema `json:"schemas"`
	} `json:"components"`
}

// loadOpenAPISpec parses the vendored spec fixture.
func loadOpenAPISpec(t *testing.T) openAPISpec {
	t.Helper()

	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatalf("read %s: %v", specPath, err)
	}

	var spec openAPISpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatalf("unmarshal %s: %v", specPath, err)
	}
	return spec
}

// requestBodyProperties resolves the top-level property names of the JSON
// request body schema for method+path, following $ref into
// components.schemas and flattening allOf composition.
func requestBodyProperties(t *testing.T, spec openAPISpec, method, path string) map[string]bool {
	t.Helper()

	pathItem, ok := spec.Paths[path]
	if !ok {
		t.Fatalf("spec has no path %q", path)
	}
	op, ok := pathItem[method]
	if !ok {
		t.Fatalf("spec path %q has no %s operation", path, method)
	}
	body, ok := op.RequestBody.Content["application/json"]
	if !ok {
		t.Fatalf("%s %s has no application/json request body", method, path)
	}

	props := map[string]bool{}
	collectSchemaProperties(t, spec, body.Schema, map[string]bool{}, props)
	return props
}

// collectSchemaProperties walks schema, following $ref (guarded by visited
// against cycles) and flattening allOf, adding every property name it finds
// into props.
func collectSchemaProperties(t *testing.T, spec openAPISpec, schema openAPISchema, visited map[string]bool, props map[string]bool) {
	t.Helper()

	if schema.Ref != "" {
		name := strings.TrimPrefix(schema.Ref, "#/components/schemas/")
		if visited[name] {
			return
		}
		visited[name] = true

		target, ok := spec.Components.Schemas[name]
		if !ok {
			t.Fatalf("$ref %q does not resolve to a component schema", schema.Ref)
		}
		collectSchemaProperties(t, spec, target, visited, props)
		return
	}

	for _, sub := range schema.AllOf {
		collectSchemaProperties(t, spec, sub, visited, props)
	}
	for name := range schema.Properties {
		props[name] = true
	}
}

// jsonFieldNames returns the JSON tag names (with any ",omitempty" and other
// options stripped) of every exported field of the given struct type.
func jsonFieldNames(t *testing.T, typ reflect.Type) map[string]bool {
	t.Helper()

	names := map[string]bool{}
	for field := range typ.Fields() {
		tag := field.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name, _, _ := strings.Cut(tag, ",")
		if name == "" {
			continue
		}
		names[name] = true
	}
	return names
}

// conformanceCase describes one create operation to check the provider's
// request struct against the vendored OpenAPI spec.
type conformanceCase struct {
	name   string
	method string
	path   string
	typ    reflect.Type

	// unsupportedSpecFields lists spec request-body properties the provider
	// intentionally does not map onto its request struct, each with a
	// one-line reason. Every spec property must appear either here or in
	// typ's JSON tags.
	unsupportedSpecFields map[string]string

	// syntheticStructFields lists request-struct JSON tags that
	// intentionally have no matching spec property, each with a one-line
	// reason. Every JSON tag must appear either here or in the spec's
	// properties (a struct tag with neither is a phantom field: something
	// the provider sends that the spec knows nothing about).
	syntheticStructFields map[string]string
}

// TestRequestStructsMatchOpenAPISpec checks, in both directions, that each
// create request struct's JSON tags line up with the corresponding OpenAPI
// request body's top-level properties. A mismatch either means a spec field
// needs mapping (or an explicit, reasoned allowlist entry) or means the
// provider is sending a field the spec does not document (a phantom field,
// which needs its own reasoned allowlist entry or a fix).
func TestRequestStructsMatchOpenAPISpec(t *testing.T) {
	spec := loadOpenAPISpec(t)

	cases := []conformanceCase{
		{
			name:   "Pod",
			method: "post",
			path:   "/pods",
			typ:    reflect.TypeFor[CreatePodRequest](),
			syntheticStructFields: map[string]string{
				// volumeEncrypted has been part of this provider's Pod API
				// surface since before this spec snapshot was vendored; the
				// vendored spec does not document it, but it is still
				// accepted by the RunPod create-pod endpoint.
				"volumeEncrypted": "accepted by the API but undocumented in the vendored spec snapshot",
			},
		},
		{
			name:   "Endpoint",
			method: "post",
			path:   "/endpoints",
			typ:    reflect.TypeFor[CreateEndpointRequest](),
		},
		{
			name:   "Template",
			method: "post",
			path:   "/templates",
			typ:    reflect.TypeFor[CreateTemplateRequest](),
			unsupportedSpecFields: map[string]string{
				"category": "GPU/AMD/CPU compute category selector; the provider infers this from the Pod/Endpoint spec instead of exposing a separate field",
				"isPublic": "the provider only ever creates private, implicit templates backing its own Pods/Endpoints, never public template listings",
				"readme":   "cosmetic markdown documentation field with no bearing on reconciliation",
			},
		},
		{
			name:   "NetworkVolume",
			method: "post",
			path:   "/networkvolumes",
			typ:    reflect.TypeFor[CreateNetworkVolumeRequest](),
		},
		{
			name:   "ContainerRegistryAuth",
			method: "post",
			path:   "/containerregistryauth",
			typ:    reflect.TypeFor[CreateContainerRegistryAuthRequest](),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			specProps := requestBodyProperties(t, spec, tc.method, tc.path)
			structTags := jsonFieldNames(t, tc.typ)

			for prop := range specProps {
				if structTags[prop] {
					continue
				}
				reason, allowlisted := tc.unsupportedSpecFields[prop]
				if !allowlisted {
					t.Errorf("spec property %q on %s %s is neither mapped on %s nor allowlisted as unsupported", prop, tc.method, tc.path, tc.typ.Name())
					continue
				}
				if reason == "" {
					t.Errorf("unsupportedSpecFields[%q] has an empty reason", prop)
				}
			}

			for tag := range structTags {
				if specProps[tag] {
					continue
				}
				reason, allowlisted := tc.syntheticStructFields[tag]
				if !allowlisted {
					t.Errorf("%s field with json tag %q has no matching property in %s %s and is not allowlisted as synthetic", tc.typ.Name(), tag, tc.method, tc.path)
					continue
				}
				if reason == "" {
					t.Errorf("syntheticStructFields[%q] has an empty reason", tag)
				}
			}

			// Catch stale allowlist entries so the allowlists stay honest as
			// the spec or the request structs change.
			for prop := range tc.unsupportedSpecFields {
				if !specProps[prop] {
					t.Errorf("unsupportedSpecFields[%q] does not name a property of %s %s's request body", prop, tc.method, tc.path)
				}
				if structTags[prop] {
					t.Errorf("unsupportedSpecFields[%q] is allowlisted as unsupported but is mapped on %s", prop, tc.typ.Name())
				}
			}
			for tag := range tc.syntheticStructFields {
				if !structTags[tag] {
					t.Errorf("syntheticStructFields[%q] does not name a json tag on %s", tag, tc.typ.Name())
				}
				if specProps[tag] {
					t.Errorf("syntheticStructFields[%q] is allowlisted as synthetic but matches a property of %s %s's request body", tag, tc.method, tc.path)
				}
			}
		})
	}
}
