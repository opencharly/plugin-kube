package kube

// deploy_image_resolve_test.go — R7 coverage for the post-#313 image-tag resolution.
//
// A bare `strings.Contains(ref, ":")` cannot distinguish a TAG from a registry
// host:port (`localhost:5099/box`) or a namespace separator (`ns:name`); imageRefTag
// parses the tag as the text after the last `:` that lies after the last `/`.

import (
	"strings"
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestImageRefTag(t *testing.T) {
	cases := []struct{ ref, want string }{
		{"nginx", ""},                   // bare name, no tag
		{"box:latest", "latest"},        // simple tag
		{"localhost:5099/box", ""},      // registry host:port, NOT a tag
		{"localhost:5099/box:v1", "v1"}, // host:port + tag
		{"localhost/charly-box:2026.04", "2026.04"},
		{"ghcr.io/opencharly/versa:next", "next"},
		{"ns:name", "name"}, // namespaced ref: `name` is the tag position
	}
	for _, c := range cases {
		if got := imageRefTag(c.ref); got != c.want {
			t.Errorf("imageRefTag(%q) = %q, want %q", c.ref, got, c.want)
		}
	}
}

// TestResolveWorkloadImage_TaggedImageIsPinnedContract: a tagged image that is NOT
// present locally must fail as a PINNED image (not a resolve error). This FAILS
// without the tagged-image branch (the pre-fix code stripped the tag via LeafName
// and took the resolve path).
func TestResolveWorkloadImage_TaggedImageIsPinnedContract(t *testing.T) {
	_, _, err := resolveWorkloadImage(
		&spec.Deploy{Image: "localhost/charly-does-not-exist-xyz:nope.000"},
		"workload", "podman")
	if err == nil {
		t.Fatalf("a tagged image absent locally must fail (pinned-image contract)")
	}
	if got := err.Error(); !strings.Contains(got, "pinned image") {
		t.Fatalf("want the pinned-image error, got: %v", got)
	}
}
