package kube

import (
	"testing"

	"github.com/opencharly/plugin-kube/candy/plugin-kube/params"
	"github.com/opencharly/spec/spec"
)

// kube_verb_cluster_resolve_test.go — the `kube:` VERB's half of the cluster resolution: what this
// plugin does with what the sdk resolved. The resolution ARMS (nil executor, failing project-dir
// seam, failing entity resolve, resolved-but-empty) live with the mechanism, in the sdk that owns
// it (opencharly/sdk#376, loaderkit/resolve_cluster_via_executor_test.go); keeping a second copy of
// them here is the duplication this change deletes. What stays is the WIRING, which is this
// plugin's: both halves of the resolved template must reach clusterConn's inputs, and the loudness
// opencharly/plugin-kube#15 depends on is preserved by the verb returning the resolver's error
// rather than degrading to the ambient current-context.

// TestApplyResolvedCluster pins the wiring, case by case.
func TestApplyResolvedCluster(t *testing.T) {
	cases := []struct {
		name                    string
		view                    *spec.ResolvedKubernetes
		in                      params.KubeInput
		wantCtx, wantKubeconfig string
	}{
		{
			name:           "both halves ride through",
			view:           &spec.ResolvedKubernetes{Kubeconfig: "/home/op/.kube/quant-dev.conf", KubeconfigContext: "kubernetes-admin@quant-dev"},
			wantCtx:        "kubernetes-admin@quant-dev",
			wantKubeconfig: "/home/op/.kube/quant-dev.conf",
		},
		{
			name:           "a context-only template leaves the file to the ambient config",
			view:           &spec.ResolvedKubernetes{KubeconfigContext: "k3s-check"},
			wantCtx:        "k3s-check",
			wantKubeconfig: "",
		},
		{
			name:           "a file-only template is honoured too (the context stays for fallback)",
			view:           &spec.ResolvedKubernetes{Kubeconfig: "/etc/rancher/k3s/k3s.yaml"},
			wantCtx:        "",
			wantKubeconfig: "/etc/rancher/k3s/k3s.yaml",
		},
		{
			name:           "an authored kubeconfig WINS over the template's",
			view:           &spec.ResolvedKubernetes{Kubeconfig: "/template.conf", KubeconfigContext: "template-ctx"},
			in:             params.KubeInput{Kubeconfig: "/authored.conf"},
			wantCtx:        "template-ctx",
			wantKubeconfig: "/authored.conf",
		},
		{
			name:           "a legitimate miss changes nothing (the caller keeps its fallback)",
			view:           nil,
			in:             params.KubeInput{Kubeconfig: "/authored.conf", KubeContext: "authored-ctx"},
			wantCtx:        "authored-ctx",
			wantKubeconfig: "/authored.conf",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			applyResolvedCluster(&in, tc.view)
			if in.KubeContext != tc.wantCtx {
				t.Errorf("KubeContext = %q, want %q", in.KubeContext, tc.wantCtx)
			}
			if in.Kubeconfig != tc.wantKubeconfig {
				t.Errorf("Kubeconfig = %q, want %q", in.Kubeconfig, tc.wantKubeconfig)
			}
		})
	}
}

// TestKubectlClusterArgs pins the ONE argv builder both halves go through, for every cluster
// substrate and for the apply and the delete alike.
func TestKubectlClusterArgs(t *testing.T) {
	cases := []struct {
		name            string
		kubeconfig, ctx string
		want            []string
	}{
		{"file and context", "/etc/kubernetes/admin.conf", "kubernetes-admin@quant-dev",
			[]string{"--kubeconfig", "/etc/kubernetes/admin.conf", "--context", "kubernetes-admin@quant-dev"}},
		{"context only (a kindcluster's own kubeconfig)", "", "kind-check",
			[]string{"--context", "kind-check"}},
		{"file only (the context falls back to the kubeconfig's current-context)", "/home/op/.kube/quant-dev.conf", "",
			[]string{"--kubeconfig", "/home/op/.kube/quant-dev.conf"}},
		{"neither (the fully ambient default)", "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := kubectlClusterArgs(tc.kubeconfig, tc.ctx)
			if len(got) != len(tc.want) {
				t.Fatalf("kubectlClusterArgs = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("kubectlClusterArgs = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestKubectlClusterPrefix is the same builder for the RECORDED teardown script, where the values
// are shell strings and must be quoted: a kubeconfig path with a space would otherwise split.
func TestKubectlClusterPrefix(t *testing.T) {
	if got, want := kubectlClusterPrefix("/home/op/my kube/config", "kubernetes-admin@quant-dev"),
		"--kubeconfig '/home/op/my kube/config' --context 'kubernetes-admin@quant-dev' "; got != want {
		t.Errorf("kubectlClusterPrefix = %q, want %q", got, want)
	}
	if got := kubectlClusterPrefix("", ""); got != "" {
		t.Errorf("an unset pair must produce an empty prefix, got %q", got)
	}
}
