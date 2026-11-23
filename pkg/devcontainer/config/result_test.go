package config

import (
	"encoding/json"
	"testing"

	"github.com/loft-sh/devpod/pkg/types"
	"gotest.tools/assert"
)

// TestGetDevPodCustomizationsOnlyReadsUserConfig pins where DevPod-specific
// customizations are read from after a merge.
//
// MergeConfiguration collects customizations per tool into
// map[string][]interface{} (one bucket entry per metadata snippet).
// GetDevPodCustomizations, however, only accepts the *DevContainerConfig
// shape (map[string]interface{}): it sees solely what the user's
// devcontainer.json declared. DevPod customizations contributed by image
// labels or features land in MergedDevContainerConfig.Customizations["devpod"]
// and are invisible to this helper.
func TestGetDevPodCustomizationsOnlyReadsUserConfig(t *testing.T) {
	t.Run("devpod customization from devcontainer.json is returned", func(t *testing.T) {
		userConfig := &DevContainerConfig{
			DevContainerActions: DevContainerActions{
				Customizations: map[string]interface{}{
					"devpod": map[string]interface{}{
						"prebuildRepository": []string{"registry.example/prebuild"},
					},
				},
			},
		}

		got := GetDevPodCustomizations(userConfig)
		assert.DeepEqual(t, got.PrebuildRepository, types.StrArray{"registry.example/prebuild"})
	})

	t.Run("missing devpod customization yields empty struct, not nil", func(t *testing.T) {
		got := GetDevPodCustomizations(&DevContainerConfig{})
		assert.Assert(t, got != nil)
		assert.Equal(t, len(got.PrebuildRepository), 0)
	})

	t.Run("unparsable devpod value yields empty struct", func(t *testing.T) {
		userConfig := &DevContainerConfig{
			DevContainerActions: DevContainerActions{
				Customizations: map[string]interface{}{
					// a string cannot convert into DevPodCustomizations
					"devpod": "not-an-object",
				},
			},
		}

		got := GetDevPodCustomizations(userConfig)
		assert.Equal(t, len(got.PrebuildRepository), 0)
	})

	t.Run("merged multi-entry devpod bucket has no consumer here", func(t *testing.T) {
		// This is the shape MergeConfiguration produces: several snippets
		// each carrying a devpod customization. GetDevPodCustomizations
		// cannot consume it (different type), and nothing in the merge
		// result deep-merges the entries together, so only the user
		// config's single object is ever read elsewhere.
		entries := []*ImageMetadata{
			{DevContainerActions: DevContainerActions{Customizations: map[string]interface{}{
				"devpod": map[string]interface{}{"prebuildRepository": []string{"from-label"}},
			}}},
			{DevContainerActions: DevContainerActions{Customizations: map[string]interface{}{
				"devpod": map[string]interface{}{"prebuildRepository": []string{"from-json"}},
			}}},
		}

		merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
		assert.NilError(t, err)

		bucket, ok := merged.Customizations["devpod"]
		assert.Equal(t, ok, true)
		assert.Equal(t, len(bucket), 2)
		// Both entries are retained; neither overrides nor deep-merges the other.
		first, err := json.Marshal(bucket[0])
		assert.NilError(t, err)
		assert.Equal(t, string(first), `{"prebuildRepository":["from-label"]}`)
		second, err := json.Marshal(bucket[1])
		assert.NilError(t, err)
		assert.Equal(t, string(second), `{"prebuildRepository":["from-json"]}`)
	})
}
