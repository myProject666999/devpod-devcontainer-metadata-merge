package metadata

import (
	"encoding/json"
	"testing"

	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/devpod/pkg/types"
	"github.com/loft-sh/log"
	"gotest.tools/assert"
)

func mustSubstitutionContext() *config.SubstitutionContext {
	return &config.SubstitutionContext{}
}

// TestFeatureConfigToImageMetadataFieldCoverage documents exactly which
// FeatureConfig fields survive the conversion into image metadata.
//
// Spec (devcontainers/cli pickFeatureProperties, and the spec merge table):
// entrypoint, mounts, init, privileged, capAdd, securityOpt, customizations
// plus the five lifecycle hooks. containerEnv is intentionally NOT part of
// the metadata: the reference implementation emits it as Dockerfile ENV
// instructions, so the values live in the image configuration itself.
func TestFeatureConfigToImageMetadataFieldCoverage(t *testing.T) {
	feature := &config.FeatureConfig{
		ID:          "test-feature",
		Entrypoint:  "/feature/entrypoint.sh",
		CapAdd:      []string{"SYS_ADMIN"},
		Init:        boolPtr(true),
		Privileged:  boolPtr(true),
		SecurityOpt: []string{"seccomp=unconfined"},
		Mounts:      []*config.Mount{{Type: "volume", Source: "v", Target: "/mnt/v"}},
		ContainerEnv: map[string]string{
			"FEATURE_ENV": "present-at-build",
		},
		Customizations: map[string]interface{}{
			"vscode": map[string]interface{}{"extensions": []string{"a.b"}},
		},
	}

	meta := FeatureConfigToImageMetadata(feature)

	assert.Equal(t, meta.Entrypoint, "/feature/entrypoint.sh")
	assert.DeepEqual(t, meta.CapAdd, []string{"SYS_ADMIN"})
	assert.DeepEqual(t, meta.SecurityOpt, []string{"seccomp=unconfined"})
	assert.DeepEqual(t, meta.Init, boolPtr(true))
	assert.DeepEqual(t, meta.Privileged, boolPtr(true))
	assert.Equal(t, len(meta.Mounts), 1)
	assert.Equal(t, meta.Mounts[0].Target, "/mnt/v")
	assert.Equal(t, meta.Customizations["vscode"] != nil, true)

	// containerEnv is dropped from the metadata entry. This matches the
	// reference implementation because feature env vars are baked into the
	// image with ENV instructions during the build (see
	// feature/extend.go generateContainerEnvs). Reading a prebuilt image's
	// label back therefore cannot reconstruct them from the label alone.
	assert.Equal(t, len(meta.ContainerEnv), 0)
}

// TestFeatureConfigLifecycleHooksAreNotSupported pins the current schema
// gap: FeatureConfig has no lifecycle command fields, so lifecycle commands
// declared inside a devcontainer-feature.json cannot be represented even
// though the current devcontainer spec allows them there.
func TestFeatureConfigLifecycleHooksAreNotSupported(t *testing.T) {
	raw := `{
		"id": "hooks-feature",
		"onCreateCommand": "echo feature-oncreate",
		"postCreateCommand": ["echo", "feature-postcreate"],
		"postStartCommand": {"named": "echo named"},
		"postAttachCommand": "echo attach",
		"updateContentCommand": "echo update"
	}`

	feature := &config.FeatureConfig{}
	err := json.Unmarshal([]byte(raw), feature)
	assert.NilError(t, err)

	// Unknown JSON fields are silently ignored: the FeatureConfig struct has
	// nowhere to store lifecycle commands.
	meta := FeatureConfigToImageMetadata(feature)
	assert.Equal(t, len(meta.OnCreateCommand), 0)
	assert.Equal(t, len(meta.UpdateContentCommand), 0)
	assert.Equal(t, len(meta.PostCreateCommand), 0)
	assert.Equal(t, len(meta.PostStartCommand), 0)
	assert.Equal(t, len(meta.PostAttachCommand), 0)
}

// TestGetDevContainerMetadataOrdering pins the input order that every merge
// rule relies on: base image entries first, then features in the order given
// (which is the computed install order), then devcontainer.json last.
func TestGetDevContainerMetadataOrdering(t *testing.T) {
	baseImageMetadata := &config.ImageMetadataConfig{
		Raw: []*config.ImageMetadata{
			{ID: "base", Entrypoint: "base-entrypoint"},
		},
		Config: []*config.ImageMetadata{
			{ID: "base", Entrypoint: "base-entrypoint"},
		},
	}
	features := []*config.FeatureSet{
		{
			ConfigID: "feature-a",
			Config: &config.FeatureConfig{
				ID:         "feature-a",
				Entrypoint: "entrypoint-a",
			},
		},
		{
			ConfigID: "feature-b",
			Config: &config.FeatureConfig{
				ID:         "feature-b",
				Entrypoint: "entrypoint-b",
				Privileged: boolPtr(true),
			},
		},
	}
	devContainerConfig := &config.SubstitutedConfig{
		Raw: &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteUser: "json-user",
			},
		},
		Config: &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				RemoteUser: "json-user",
			},
		},
	}

	out, err := GetDevContainerMetadata(mustSubstitutionContext(), baseImageMetadata, devContainerConfig, features)
	assert.NilError(t, err)

	// Both Raw and Config tracks must carry the same 4 entries in order.
	assert.Equal(t, len(out.Raw), 4)
	assert.Equal(t, len(out.Config), 4)

	wantIDs := []string{"base", "", "", ""}
	wantEntrypoints := []string{"base-entrypoint", "entrypoint-a", "entrypoint-b", ""}
	for i := range wantIDs {
		assert.Equal(t, out.Raw[i].ID, wantIDs[i])
		assert.Equal(t, out.Raw[i].Entrypoint, wantEntrypoints[i])
		assert.Equal(t, out.Config[i].Entrypoint, wantEntrypoints[i])
	}

	// The last entry is the devcontainer.json one.
	assert.Equal(t, out.Raw[3].RemoteUser, "json-user")
	assert.Equal(t, out.Config[3].RemoteUser, "json-user")

	// Feature ordering is preserved verbatim - GetDevContainerMetadata does
	// not reorder; install ordering is the caller's responsibility (see
	// feature/extend.go computeFeatureOrder).
	assert.Equal(t, out.Raw[1].Entrypoint, "entrypoint-a")
	assert.Equal(t, out.Raw[2].Entrypoint, "entrypoint-b")
}

// TestGetDevContainerMetadataEmptyInputs covers the degenerate case where
// only a devcontainer.json exists.
func TestGetDevContainerMetadataEmptyInputs(t *testing.T) {
	devContainerConfig := &config.SubstitutedConfig{
		Raw:    &config.DevContainerConfig{},
		Config: &config.DevContainerConfig{},
	}

	out, err := GetDevContainerMetadata(mustSubstitutionContext(), &config.ImageMetadataConfig{}, devContainerConfig, nil)
	assert.NilError(t, err)
	assert.Equal(t, len(out.Raw), 1)
	assert.Equal(t, len(out.Config), 1)
}

// TestGetImageMetadataLabelParsing covers array, single-object and empty
// label shapes through both entry points.
func TestGetImageMetadataLabelParsing(t *testing.T) {
	arrayLabel := `[
		{"id": "base", "remoteUser": "base-user"},
		{"privileged": true, "capAdd": ["SYS_PTRACE"]}
	]`
	singleObjectLabel := `{"remoteUser": "single-user", "mounts": [{"type": "volume", "source": "v", "target": "/mnt"}]}`

	t.Run("array label from image details", func(t *testing.T) {
		out, err := GetImageMetadata(&config.ImageDetails{
			Config: config.ImageDetailsConfig{
				Labels: map[string]string{ImageMetadataLabel: arrayLabel},
			},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out.Raw), 2)
		assert.Equal(t, len(out.Config), 2)
		assert.Equal(t, out.Config[0].RemoteUser, "base-user")
		assert.DeepEqual(t, out.Config[1].Privileged, boolPtr(true))
		assert.DeepEqual(t, out.Config[1].CapAdd, []string{"SYS_PTRACE"})
	})

	t.Run("single object label from image details", func(t *testing.T) {
		out, err := GetImageMetadata(&config.ImageDetails{
			Config: config.ImageDetailsConfig{
				Labels: map[string]string{ImageMetadataLabel: singleObjectLabel},
			},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out.Raw), 1)
		assert.Equal(t, out.Config[0].RemoteUser, "single-user")
		assert.Equal(t, out.Config[0].Mounts[0].Target, "/mnt")
	})

	t.Run("array label from container details", func(t *testing.T) {
		out, err := GetImageMetadataFromContainer(&config.ContainerDetails{
			Config: config.ContainerDetailsConfig{
				Labels: map[string]string{ImageMetadataLabel: arrayLabel},
			},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out.Config), 2)
	})

	t.Run("single object label from container details", func(t *testing.T) {
		out, err := GetImageMetadataFromContainer(&config.ContainerDetails{
			Config: config.ContainerDetailsConfig{
				Labels: map[string]string{ImageMetadataLabel: singleObjectLabel},
			},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out.Config), 1)
		assert.Equal(t, out.Config[0].RemoteUser, "single-user")
	})

	t.Run("missing label yields empty config and no error", func(t *testing.T) {
		out, err := GetImageMetadata(&config.ImageDetails{
			Config: config.ImageDetailsConfig{Labels: map[string]string{}},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out.Raw), 0)
		assert.Equal(t, len(out.Config), 0)

		out2, err := GetImageMetadataFromContainer(nil, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out2.Raw), 0)
	})

	t.Run("empty label string yields empty config and no error", func(t *testing.T) {
		out, err := GetImageMetadata(&config.ImageDetails{
			Config: config.ImageDetailsConfig{
				Labels: map[string]string{ImageMetadataLabel: ""},
			},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)
		assert.Equal(t, len(out.Config), 0)
	})
}

// TestGetImageMetadataMalformedLabel pins the decided failure mode:
// a non-empty, unparsable label is a hard error. The base image metadata
// must not silently disappear while the build/startup proceeds. Compatibility
// trade-off: images carrying a hand-written malformed devcontainer.metadata
// label now fail instead of being treated as label-less.
func TestGetImageMetadataMalformedLabel(t *testing.T) {
	malformed := `{not valid json`

	out, err := GetImageMetadata(&config.ImageDetails{
		Config: config.ImageDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: malformed},
		},
	}, mustSubstitutionContext(), log.Default)
	assert.Assert(t, err != nil)
	assert.Equal(t, out == nil, true)

	out2, err := GetImageMetadataFromContainer(&config.ContainerDetails{
		Config: config.ContainerDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: malformed},
		},
	}, mustSubstitutionContext(), log.Default)
	assert.Assert(t, err != nil)
	assert.Equal(t, out2 == nil, true)
}

// TestGetImageMetadataNonObjectTopLevel covers a label that parses as JSON
// but is neither an object nor an array (e.g. a bare string/number): that is
// also a hard error rather than an empty, successful result.
func TestGetImageMetadataNonObjectTopLevel(t *testing.T) {
	out, err := GetImageMetadata(&config.ImageDetails{
		Config: config.ImageDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: `"just a string label"`},
		},
	}, mustSubstitutionContext(), log.Default)
	assert.Assert(t, err != nil)
	assert.Equal(t, out == nil, true)
}

// TestGetImageMetadataArrayWithNonObjectElement pins the skip policy:
// non-object elements inside an array label are skipped with a warning while
// the valid object elements survive and are still substituted.
func TestGetImageMetadataArrayWithNonObjectElement(t *testing.T) {
	mixed := `[
		{"remoteUser": "base-user"},
		"i am not an object",
		42,
		["neither", "am", "i"],
		{"capAdd": ["SYS_PTRACE"]}
	]`

	out, err := GetImageMetadata(&config.ImageDetails{
		Config: config.ImageDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: mixed},
		},
	}, mustSubstitutionContext(), log.Default)
	assert.NilError(t, err)
	assert.Equal(t, len(out.Raw), 2)
	assert.Equal(t, len(out.Config), 2)
	assert.Equal(t, out.Config[0].RemoteUser, "base-user")
	assert.DeepEqual(t, out.Config[1].CapAdd, []string{"SYS_PTRACE"})

	out2, err := GetImageMetadataFromContainer(&config.ContainerDetails{
		Config: config.ContainerDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: mixed},
		},
	}, mustSubstitutionContext(), log.Default)
	assert.NilError(t, err)
	assert.Equal(t, len(out2.Config), 2)
}

// TestGetImageMetadataArrayWithOnlyNonObjectElements ensures a label that
// contains zero valid objects is an error, not an empty success.
func TestGetImageMetadataArrayWithOnlyNonObjectElements(t *testing.T) {
	out, err := GetImageMetadata(&config.ImageDetails{
		Config: config.ImageDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: `["a", 1, true]`},
		},
	}, mustSubstitutionContext(), log.Default)
	assert.Assert(t, err != nil)
	assert.Equal(t, out == nil, true)
}

// TestGetImageMetadataSubstitution verifies variable substitution runs on
// label entries while parsing.
func TestGetImageMetadataSubstitution(t *testing.T) {
	label := `[{"remoteEnv": {"WORKSPACE": "${localWorkspaceFolder}"}}]`
	ctx := &config.SubstitutionContext{
		LocalWorkspaceFolder: "/home/me/project",
	}

	out, err := GetImageMetadata(&config.ImageDetails{
		Config: config.ImageDetailsConfig{
			Labels: map[string]string{ImageMetadataLabel: label},
		},
	}, ctx, log.Default)
	assert.NilError(t, err)
	assert.Equal(t, out.Config[0].RemoteEnv["WORKSPACE"], "/home/me/project")
	// Raw keeps the unsubstituted placeholder.
	assert.Equal(t, out.Raw[0].RemoteEnv["WORKSPACE"], "${localWorkspaceFolder}")
}

// TestContainerEnvMissingFromLabelMerge reproduces the prebuilt-image
// scenario end to end: a label-only image metadata entry that declares
// containerEnv merges fine; but a FeatureConfig whose containerEnv was baked
// into the image via ENV (and therefore absent from its metadata entry)
// cannot contribute those variables through the label path.
func TestContainerEnvMissingFromLabelMerge(t *testing.T) {
	t.Run("containerEnv present in label is merged per key", func(t *testing.T) {
		out, err := GetImageMetadata(&config.ImageDetails{
			Config: config.ImageDetailsConfig{
				Labels: map[string]string{ImageMetadataLabel: `[
					{"containerEnv": {"A": "base", "B": "base"}},
					{"containerEnv": {"B": "feature"}}
				]`},
			},
		}, mustSubstitutionContext(), log.Default)
		assert.NilError(t, err)

		merged, err := config.MergeConfiguration(&config.DevContainerConfig{}, out.Config)
		assert.NilError(t, err)
		// Spec: per variable, last value wins.
		assert.DeepEqual(t, merged.ContainerEnv, map[string]string{"A": "base", "B": "feature"})
	})

	t.Run("feature containerEnv absent from metadata entry is not recoverable", func(t *testing.T) {
		// Simulate what FeatureConfigToImageMetadata produces for a feature
		// that declares containerEnv at build time.
		meta := FeatureConfigToImageMetadata(&config.FeatureConfig{
			ID:           "env-feature",
			ContainerEnv: map[string]string{"FEATURE_ONLY": "baked-as-ENV-instruction"},
		})

		merged, err := config.MergeConfiguration(&config.DevContainerConfig{}, []*config.ImageMetadata{meta})
		assert.NilError(t, err)
		_, present := merged.ContainerEnv["FEATURE_ONLY"]
		assert.Equal(t, present, false)
	})
}

// TestGetDevContainerMetadataLifecycleOrdering ensures hooks from all three
// origins land in the metadata list in declaration order so the merge layer
// can collect them in execution order.
func TestGetDevContainerMetadataLifecycleOrdering(t *testing.T) {
	base := &config.ImageMetadataConfig{
		Raw: []*config.ImageMetadata{{
			ID: "base",
			DevContainerActions: config.DevContainerActions{
				OnCreateCommand: types.LifecycleHook{"": []string{"base-hook"}},
			},
		}},
		Config: []*config.ImageMetadata{{
			ID: "base",
			DevContainerActions: config.DevContainerActions{
				OnCreateCommand: types.LifecycleHook{"": []string{"base-hook"}},
			},
		}},
	}
	features := []*config.FeatureSet{
		{ConfigID: "f", Config: &config.FeatureConfig{ID: "f"}},
	}
	devConfig := &config.SubstitutedConfig{
		Raw: &config.DevContainerConfig{
			DevContainerActions: config.DevContainerActions{
				OnCreateCommand: types.LifecycleHook{"": []string{"json-hook"}},
			},
		},
		Config: &config.DevContainerConfig{
			DevContainerActions: config.DevContainerActions{
				OnCreateCommand: types.LifecycleHook{"": []string{"json-hook"}},
			},
		},
	}

	out, err := GetDevContainerMetadata(mustSubstitutionContext(), base, devConfig, features)
	assert.NilError(t, err)

	assert.Equal(t, out.Raw[0].OnCreateCommand[""][0], "base-hook")
	assert.Equal(t, len(out.Raw[1].OnCreateCommand), 0) // feature carries no hooks (schema gap)
	assert.Equal(t, out.Raw[2].OnCreateCommand[""][0], "json-hook")
}

func boolPtr(v bool) *bool {
	return &v
}
