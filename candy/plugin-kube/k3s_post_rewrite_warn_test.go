package kube

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencharly/sdk"
	"github.com/opencharly/spec/spec"
)

// captureSlog redirects the default logger into a buffer for the duration of fn and returns
// everything written. The rewrite's whole defect (opencharly/plugin-kube#22) is a MESSAGE that
// was never emitted, so a message is what the test has to observe.
func captureSlog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)
	fn()
	return buf.String()
}

func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kubeconfig.yaml")
	body := "apiVersion: v1\nclusters:\n- cluster:\n    server: " + server + "\n  name: c\n" +
		"contexts:\n- context:\n    cluster: c\n  name: default\ncurrent-context: default\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return p
}

// TestRewriteK3sServerToForward_NoForwardMatches_SaysSo is the regression test for
// opencharly/plugin-kube#22. The deploy declares one forwarded guest port (6443), the retrieved
// kubeconfig names a DIFFERENT guest-local port, so the rewrite changes nothing — and that used
// to be a bare `return nil`. The operator then saw a host-side `kube:` probe refused on the
// guest-local port with nothing anywhere stating that the kubeconfig had been left unrewritten.
//
// It fails without the change: a bare return nil writes nothing to any logger.
func TestRewriteK3sServerToForward_NoForwardMatches_SaysSo(t *testing.T) {
	stubVmEntityForForwards(t, &spec.ResolvedVm{Network: &spec.VmNetwork{PortForwards: []string{"auto:6443"}}})
	stubVmStateForForwards(t, &spec.VmDeployState{PortForwards: map[string]int{"6443": 34325}})
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})

	path := writeKubeconfig(t, "https://127.0.0.1:7777")
	before, _ := os.ReadFile(path)

	out := captureSlog(t, func() {
		if err := rewriteK3sServerToForward(context.Background(), exec, path, "k3s-vm", "rca-deploy4"); err != nil {
			t.Fatalf("rewriteK3sServerToForward: %v", err)
		}
	})

	if !strings.Contains(out, "left on its guest-local port") {
		t.Fatalf("want a warning naming the unrewritten kubeconfig, got %q", out)
	}
	if !strings.Contains(out, "6443") {
		t.Fatalf("want the warning to name the forwarded guest port it did hold, got %q", out)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatalf("a non-matching rewrite must leave the kubeconfig alone; it changed it")
	}
}

// TestRewriteK3sServerToForward_MatchRewritesAndStaysSilent is the negative case, and it is not
// optional: a warning that fires on correct configuration is the same defect one level up. When
// the kubeconfig names the forwarded guest port, the rewrite must do its job and say nothing.
func TestRewriteK3sServerToForward_MatchRewritesAndStaysSilent(t *testing.T) {
	stubVmEntityForForwards(t, &spec.ResolvedVm{Network: &spec.VmNetwork{PortForwards: []string{"auto:6443"}}})
	stubVmStateForForwards(t, &spec.VmDeployState{PortForwards: map[string]int{"6443": 34325}})
	exec := sdk.NewInProcExecutor(&fakeExecutorServiceClient{})

	path := writeKubeconfig(t, "https://127.0.0.1:6443")

	out := captureSlog(t, func() {
		if err := rewriteK3sServerToForward(context.Background(), exec, path, "k3s-vm", "rca-deploy4"); err != nil {
			t.Fatalf("rewriteK3sServerToForward: %v", err)
		}
	})

	if out != "" {
		t.Fatalf("a rewrite that acted must stay silent, got %q", out)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), "34325") {
		t.Fatalf("want the server port rewritten to the forwarded host port, got %s", data)
	}
}
