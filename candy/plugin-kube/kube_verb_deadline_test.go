package kube

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/opencharly/plugin-kube/candy/plugin-kube/params"
	"github.com/opencharly/spec/spec"
)

// recordingResource is a dynamic client that only records the context it was
// handed. Embedding the interfaces unfulfilled keeps it to the two methods the
// `pods` path actually calls.
type recordingResource struct {
	dynamic.NamespaceableResourceInterface
	seen *[]context.Context
}

func (r recordingResource) Namespace(string) dynamic.ResourceInterface { return r }

func (r recordingResource) List(ctx context.Context, _ metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	*r.seen = append(*r.seen, ctx)
	return &unstructured.UnstructuredList{}, nil
}

type recordingClient struct {
	dynamic.Interface
	seen *[]context.Context
	gvr  *schema.GroupVersionResource
}

func (c recordingClient) Resource(gvr schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	*c.gvr = gvr
	return recordingResource{seen: c.seen}
}

// A kube op runs inside the host's RPC call and inherits its caller's context. The
// verb MUST hand that context to its client: with the previous context.TODO() a hung
// dial outlived the host's deadline, kept the plugin blocked on an uncancellable call,
// and surfaced only as `verb "kube": rpc error: code = DeadlineExceeded` with no phase
// named — the failure that reddens a bed's probe (opencharly/plugin-kube#24).
//
// This test FAILS against context.TODO(): the recorded context then carries no
// deadline at all.
func TestVerbHandsTheCallersDeadlineToItsClient(t *testing.T) {
	for _, tc := range []struct {
		name     string
		caller   time.Duration // 0 => no caller deadline
		step     string        // the step's `timeout:`
		wantMax  time.Duration
		wantWhat string
	}{
		{name: "step timeout bounds the call", step: "30s", wantMax: 30 * time.Second, wantWhat: "the step's timeout"},
		{name: "default bounds the call when the step names none", wantMax: defaultOpTimeout, wantWhat: "the default"},
		{name: "the caller's tighter deadline wins", caller: 5 * time.Second, step: "600s", wantMax: 5 * time.Second, wantWhat: "the caller's deadline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seen []context.Context
			var gvr schema.GroupVersionResource
			in := params.KubeInput{Method: "pods", Namespace: "default"}
			conn := connFromInput(&in)
			conn.clientFunc = func() (dynamic.Interface, error) {
				return recordingClient{seen: &seen, gvr: &gvr}, nil
			}
			ctx := context.Background()
			if tc.caller > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.caller)
				defer cancel()
			}
			op := &spec.Op{Timeout: tc.step}

			if _, err := dispatch(ctx, conn, op, &in); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if len(seen) == 0 {
				t.Fatal("the client was never called: the test proves nothing")
			}
			if gvr.Resource != "pods" {
				t.Fatalf("the pods method probed %q, not pods", gvr.Resource)
			}
			for _, got := range seen {
				dl, ok := got.Deadline()
				if !ok {
					t.Fatalf("the verb handed its client a context with NO deadline: an uncancellable call outlives the host, which is the defect this change removes")
				}
				if until := time.Until(dl); until > tc.wantMax+2*time.Second {
					t.Fatalf("call deadline %s is later than %s (%s): the op is not bounded by %s", until, tc.wantMax, dl, tc.wantWhat)
				}
			}
		})
	}
}

// The per-method defaults live in ONE table, and a step's own `timeout:` still overrides
// them. Asserted directly, because the alternative — driving a waiting method through the
// dispatcher — would spin to its full budget inside a unit test.
//
// This FAILS if any default drifts: a one-shot probe that waits on ServiceLB gets a
// shorter budget than a method that walks the k3s addon stack, and neither may silently
// become the generic one.
func TestOpTimeoutIsOneTableWithPerMethodDefaults(t *testing.T) {
	for _, tc := range []struct {
		method string
		step   string
		want   time.Duration
	}{
		{method: "pods", want: 120 * time.Second},
		{method: "addons", want: 180 * time.Second},
		{method: "lb-external-ip", want: 60 * time.Second},
		{method: "wait-nodes", want: 120 * time.Second},
		{method: "pods", step: "45s", want: 45 * time.Second},
		{method: "addons", step: "600s", want: 600 * time.Second},
	} {
		if got := opTimeout(tc.method, &spec.Op{Timeout: tc.step}); got != tc.want {
			t.Errorf("opTimeout(%q, timeout=%q) = %s, want %s", tc.method, tc.step, got, tc.want)
		}
	}
	if got := opTimeout("pods", nil); got != defaultOpTimeout {
		t.Errorf("opTimeout with a nil op = %s, want the generic default %s", got, defaultOpTimeout)
	}
}
