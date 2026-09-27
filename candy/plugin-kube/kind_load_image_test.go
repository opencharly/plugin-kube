package kube

import (
	"archive/tar"
	"io"
	"os"
	"os/exec"
	"testing"
)

// TestKindSaveImageArchive is the R7 witness for the `podman save -o -` defect: the
// pre-fix kindLoadImage piped `podman save -o - <image>` into `kind load image-archive`,
// but `-` is not stdout on the supported podman versions (measured on podman 6.1.2: it
// writes a FILE named `-` and leaves stdout EMPTY), so the kind node's
// `ctr images import` failed with `ctr: unrecognized image format`. This drives the
// EXTRACTED path `kindSaveImageArchive` (the exact call `kindLoadImage` makes) and
// asserts the destination is non-empty and a docker-archive (`manifest.json`) — so
// reintroducing `-o -` leaves `dest` empty and FAILS this test.
//
// LIVE OR SKIP (R7a): gated on podman + a real local image via KIND_LOAD_TEST_IMAGE;
// absent → the test SKIPS visibly, never a silent pass.
func TestKindSaveImageArchive(t *testing.T) {
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

	// The changed path: the exact save kindLoadImage invokes.
	if err := kindSaveImageArchive("podman", img, f.Name()); err != nil {
		t.Fatalf("kindSaveImageArchive(%s): %v", img, err)
	}

	st, err := os.Stat(f.Name())
	if err != nil {
		t.Fatalf("stat archive: %v", err)
	}
	if st.Size() == 0 {
		t.Fatalf("kindSaveImageArchive left an EMPTY archive for %s — the pre-fix `-o -` shape", img)
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
