package kube

import (
	"archive/tar"
	"io"
	"os"
	"os/exec"
	"testing"
)

// TestKindSaveProducesDockerArchive is the R7 witness for the `podman save -o -`
// defect in kindLoadImage: the pre-fix code piped `podman save -o - <image>` into
// `kind load image-archive`, but podman's `-o -` does NOT write the archive to stdout
// on the supported versions (measured on podman 6.1.2: an EMPTY stdout), so the kind
// node's `ctr images import` failed with `ctr: unrecognized image format`. This proves
// the fixed invocation `podman save -o <file> <image>` emits a docker-archive tar
// (`manifest.json` present, non-empty).
//
// LIVE OR SKIP (R7a): gated on podman + a real local image via KIND_LOAD_TEST_IMAGE;
// absent → the test SKIPS visibly, never a silent pass.
func TestKindSaveProducesDockerArchive(t *testing.T) {
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman not present — live or skip")
	}
	img := os.Getenv("KIND_LOAD_TEST_IMAGE")
	if img == "" {
		t.Skip("KIND_LOAD_TEST_IMAGE unset — live or skip")
	}

	f, err := os.CreateTemp("", "kind-save-*.tar")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer os.Remove(f.Name())
	if err := f.Close(); err != nil {
		t.Fatalf("close temp: %v", err)
	}

	// The fixed invocation: a REAL output path, never `-`.
	if out, err := exec.Command("podman", "save", "-o", f.Name(), img).CombinedOutput(); err != nil {
		t.Fatalf("podman save -o <file> %s: %v\n%s", img, err, out)
	}

	st, err := os.Stat(f.Name())
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("podman save -o <file> produced an EMPTY archive for %s — the pre-fix `-o -` shape", img)
	}

	fh, err := os.Open(f.Name())
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer fh.Close()
	tr := tar.NewReader(fh)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		if hdr.Name == "manifest.json" {
			return // docker-archive confirmed — the format `ctr images import` accepts
		}
	}
	t.Fatalf("podman save -o <file> archive for %s has no manifest.json (not a docker-archive)", img)
}
