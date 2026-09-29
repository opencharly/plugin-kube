package kube

// deploy_image_resolve_test.go — R7 coverage for the post-#313 image-tag resolution:
// `resolveWorkloadImage` must treat an explicitly-tagged `image:` as the PRE-#313
// pinned-image contract (it must be present locally), and an untagged name as a
// local `:latest` resolve. Before the #313 migration this branch did not exist
// (the tag came from a removed `spec.Deploy.Version` field).

import (
	"testing"

	"github.com/opencharly/spec/spec"
)

func TestResolveWorkloadImage_TaggedImageIsPinnedContract(t *testing.T) {
	// A tagged image that is NOT present locally must fail as a PINNED image —
	// i.e. the `strings.Contains(imageRef, ":")` branch is taken and reports the
	// pinned-image error, not a resolve error. This fails without the new branch
	// (the old code had no `:`-test at all and would go straight to resolve).
	_, _, err := resolveWorkloadImage(
		&spec.Deploy{Image: "localhost/charly-does-not-exist-xyz:nope.000"},
		"workload", "podman")
	if err == nil {
		t.Fatalf("a tagged image absent locally must fail (pinned-image contract)")
	}
	if got := err.Error(); !contains(got, "pinned image") {
		t.Fatalf("want the pinned-image error, got: %v", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
