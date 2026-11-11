package kube

// methods_observe_test.go — the D-VERB-1 control on the kube verb's method surface.
//
// D-VERB-1 (DESIGN.md §23): "A verb observes and never changes state: a state change is a field
// (§14.4), and the one step that changes state is a `run:` step (D-VERB-5)." The `kube:` verb used
// to ship two MUTATION methods — `apply` (dynamic Create, then Update on AlreadyExists) and
// `delete` (dynamic Delete) — so a `check:` step was a second, unchecked deployment pathway beside
// the deploy lowering. opencharly/plugin-kube#32 removed them; these tests are the control that
// keeps them removed, at both boundaries the verb has:
//
//  1. DISPATCH — the method switch no longer knows the words, so an op carrying one fails as an
//     UNKNOWN method before any cluster connection is resolved (the nil *clusterConn here is
//     deliberate: it proves the refusal happens before the connection, not after it).
//  2. AUTHORED INPUT — the plugin's OWN served schema (schemaFS, the same bytes NewMeta hands the
//     host) admits all eleven observation methods and refuses the two mutation words plus the
//     `manifest` field that existed only to carry their YAML. The host validates every authored
//     step's plugin_input with exactly this shape — `def.Unify(input).Validate(cue.Concrete(true))`
//     in charly's validateAuthoredPluginInput — so this is the authored-input boundary itself, not
//     a text match over it.

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"

	"github.com/opencharly/plugin-kube/candy/plugin-kube/params"
)

// observationMethods is every method the verb still carries after the mutation removal — the
// control set for the schema arm, spelled once so a re-added method has to be argued for here.
var observationMethods = []string{
	"nodes", "wait-nodes", "pods", "wait-ready", "ingress", "ingressclass",
	"storageclass", "service", "lb-external-ip", "addons", "raw",
}

func TestDispatch_RefusesRemovedMutationMethods(t *testing.T) {
	for _, method := range []string{"apply", "delete"} {
		if mods, ok := requiredModifiers[method]; ok {
			t.Errorf("requiredModifiers still carries the mutation method %q (requires %v): D-VERB-1 says a verb observes", method, mods)
		}
		_, err := dispatch(context.Background(), nil, nil, &params.KubeInput{Method: method})
		if err == nil {
			t.Errorf("dispatch(%q) returned no error: the method must be unknown, never runnable", method)
			continue
		}
		if !strings.Contains(err.Error(), "unknown kube method") {
			t.Errorf("dispatch(%q) error = %v, want an unknown-kube-method refusal so a mutation never reaches the cluster", method, err)
		}
	}
}

func TestKubeInput_ObservationMethodsOnly(t *testing.T) {
	src, err := fs.ReadFile(schemaFS, "schema/kube.cue")
	if err != nil {
		t.Fatalf("reading the served schema: %v", err)
	}
	ctx := cuecontext.New()
	schema := ctx.CompileBytes(src)
	if err := schema.Err(); err != nil {
		t.Fatalf("the served schema does not compile: %v", err)
	}
	def := schema.LookupPath(cue.ParsePath("#KubeInput"))
	if err := def.Err(); err != nil {
		t.Fatalf("the served schema has no #KubeInput: %v", err)
	}

	// Positive control: every observation method validates against the authored-input def.
	for _, method := range observationMethods {
		doc := ctx.CompileString(`{method: "` + method + `"}`)
		if err := def.Unify(doc).Validate(cue.Concrete(true)); err != nil {
			t.Errorf("observation method %q is refused by #KubeInput: %v", method, err)
		}
	}

	// The two mutation methods, and the manifest field that carried them, are refused.
	for _, tc := range []struct{ name, doc string }{
		{"method apply", `{method: "apply"}`},
		{"method delete", `{method: "delete"}`},
		{"manifest field", `{method: "nodes", manifest: "/tmp/smoke.yaml"}`},
	} {
		doc := ctx.CompileString(tc.doc)
		if err := def.Unify(doc).Validate(cue.Concrete(true)); err == nil {
			t.Errorf("#KubeInput accepts %s (%s): a mutation method and its manifest field must not be authorable (D-VERB-1)", tc.name, tc.doc)
		}
	}
}
