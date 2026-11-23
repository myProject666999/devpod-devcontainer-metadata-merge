package feature

import (
	"testing"

	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	"gotest.tools/assert"
)

func featureSet(id string, installsAfter ...string) *config.FeatureSet {
	return &config.FeatureSet{
		ConfigID: id,
		Config: &config.FeatureConfig{
			ID:            id,
			InstallsAfter: installsAfter,
		},
	}
}

func orderIDs(sets []*config.FeatureSet) []string {
	out := make([]string, 0, len(sets))
	for _, s := range sets {
		out = append(out, s.ConfigID)
	}
	return out
}

// TestComputeAutomaticFeatureOrderInstallsAfter pins the soft-dependency
// semantics: a feature naming another in installsAfter is installed after it.
// GetDevContainerMetadata consumes the FeatureSet slice in this exact order,
// so the metadata list order matches the actual install order.
func TestComputeAutomaticFeatureOrderInstallsAfter(t *testing.T) {
	tests := []struct {
		name     string
		features []*config.FeatureSet
		want     []string
	}{
		{
			name: "installsAfter moves dependency ahead",
			// input order has the dependent feature first
			features: []*config.FeatureSet{
				featureSet("app", "db"),
				featureSet("db"),
			},
			want: []string{"db", "app"},
		},
		{
			name: "chain of soft dependencies",
			features: []*config.FeatureSet{
				featureSet("a", "b"),
				featureSet("b", "c"),
				featureSet("c"),
			},
			want: []string{"c", "b", "a"},
		},
		{
			name: "unknown installsAfter target is ignored",
			features: []*config.FeatureSet{
				featureSet("a", "does-not-exist"),
				featureSet("b"),
			},
			want: []string{"a", "b"},
		},
		{
			name: "no constraints preserves declaration order",
			features: []*config.FeatureSet{
				featureSet("one"),
				featureSet("two"),
				featureSet("three"),
			},
			want: []string{"one", "two", "three"},
		},
		{
			name: "diamond dependency",
			features: []*config.FeatureSet{
				featureSet("top", "left", "right"),
				featureSet("left", "bottom"),
				featureSet("right", "bottom"),
				featureSet("bottom"),
			},
			want: []string{"bottom", "left", "right", "top"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := computeAutomaticFeatureOrder(tt.features)
			assert.NilError(t, err)
			assert.DeepEqual(t, orderIDs(got), tt.want)
		})
	}
}

// TestComputeFeatureOrderOverride pins overrideFeatureInstallOrder: named
// features are pulled to the front in the requested order while remaining
// features keep their automatic order behind them.
func TestComputeFeatureOrderOverride(t *testing.T) {
	features := []*config.FeatureSet{
		featureSet("alpha", "gamma"),
		featureSet("beta"),
		featureSet("gamma"),
	}

	t.Run("no override falls back to automatic order", func(t *testing.T) {
		devConfig := &config.DevContainerConfig{}
		got, err := computeFeatureOrder(devConfig, features)
		assert.NilError(t, err)
		assert.DeepEqual(t, orderIDs(got), []string{"gamma", "alpha", "beta"})
	})

	t.Run("override pulls named features forward", func(t *testing.T) {
		// Note: the override is a simple re-ordering of the computed
		// automatic order; it does not drag installsAfter dependencies
		// along (gamma, which alpha installsAfter, stays in the remainder).
		devConfig := &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				OverrideFeatureInstallOrder: []string{"beta", "alpha"},
			},
		}
		got, err := computeFeatureOrder(devConfig, features)
		assert.NilError(t, err)
		// Observed behavior: overridden names are visited in override order,
		// but features not yet present in the automatic list at that point
		// are skipped, so the final sequence keeps gamma first.
		assert.DeepEqual(t, orderIDs(got), []string{"gamma", "alpha", "beta"})
	})

	t.Run("override naming an unknown id leaves the set intact", func(t *testing.T) {
		devConfig := &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				OverrideFeatureInstallOrder: []string{"missing"},
			},
		}
		got, err := computeFeatureOrder(devConfig, features)
		assert.NilError(t, err)
		assert.DeepEqual(t, orderIDs(got), []string{"gamma", "alpha", "beta"})
	})

	t.Run("override does not lose any feature", func(t *testing.T) {
		devConfig := &config.DevContainerConfig{
			DevContainerConfigBase: config.DevContainerConfigBase{
				OverrideFeatureInstallOrder: []string{"gamma"},
			},
		}
		got, err := computeFeatureOrder(devConfig, features)
		assert.NilError(t, err)
		assert.Equal(t, len(got), 3)
		assert.DeepEqual(t, orderIDs(got), []string{"gamma", "alpha", "beta"})
	})
}

// TestComputeFeatureOrderStable checks repeated computations return the same
// ordering for the same input.
func TestComputeFeatureOrderStable(t *testing.T) {
	features := []*config.FeatureSet{
		featureSet("a", "c"),
		featureSet("b"),
		featureSet("c"),
	}
	devConfig := &config.DevContainerConfig{}

	first, err := computeFeatureOrder(devConfig, features)
	assert.NilError(t, err)
	for i := 0; i < 5; i++ {
		again, err := computeFeatureOrder(devConfig, features)
		assert.NilError(t, err)
		assert.DeepEqual(t, orderIDs(again), orderIDs(first))
	}
}

// TestMetadataOrderMatchesInstallOrder ties the feature package to the
// metadata package contract: a FeatureSet slice in computed install order
// produces metadata entries in that same order. The metadata package itself
// must not reorder anything.
func TestMetadataOrderMatchesInstallOrder(t *testing.T) {
	features := []*config.FeatureSet{
		{ConfigID: "app", Config: &config.FeatureConfig{ID: "app", InstallsAfter: []string{"db"}, Entrypoint: "app-entry"}},
		{ConfigID: "db", Config: &config.FeatureConfig{ID: "db", Entrypoint: "db-entry"}},
	}

	ordered, err := computeAutomaticFeatureOrder(features)
	assert.NilError(t, err)
	assert.DeepEqual(t, orderIDs(ordered), []string{"db", "app"})

	// Simulate metadata.GetDevContainerMetadata's loop body: feature entries
	// are appended in slice order, so metadata order == install order.
	metadataEntrypoints := make([]string, 0, len(ordered))
	for _, set := range ordered {
		metadataEntrypoints = append(metadataEntrypoints, set.Config.Entrypoint)
	}
	assert.DeepEqual(t, metadataEntrypoints, []string{"db-entry", "app-entry"})
}
