package config

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/loft-sh/devpod/pkg/types"
	"gotest.tools/assert"
)

func boolPtr(v bool) *bool {
	return &v
}

// metadataFromJSON unmarshals a single devcontainer.metadata JSON snippet into
// an ImageMetadata, exercising the same (un)marshalling paths as production.
func metadataFromJSON(t *testing.T, snippet string) *ImageMetadata {
	t.Helper()
	entry := &ImageMetadata{}
	err := json.Unmarshal([]byte(snippet), entry)
	assert.NilError(t, err)
	return entry
}

func metadataListFromJSON(t *testing.T, label string) []*ImageMetadata {
	t.Helper()
	var entries []*ImageMetadata
	err := json.Unmarshal([]byte(label), &entries)
	assert.NilError(t, err)
	return entries
}

// TestMergeConfigurationBooleans covers the boolean fields.
//
// Spec (https://containers.dev/implementors/spec/#merge-logic):
//   - init:        "true if at least one is true, false otherwise" (OR)
//   - privileged:  "true if at least one is true, false otherwise" (OR)
//   - overrideCommand:       "Last value wins."
//   - updateRemoteUserUID:  "Last value wins."
//
// Entries are ordered by declaration: base image metadata first, then
// features in install order, then devcontainer.json last.
func TestMergeConfigurationBooleans(t *testing.T) {
	tests := []struct {
		name           string
		entries        []*ImageMetadata
		wantInit       *bool
		wantPrivileged *bool
		wantOverride   *bool
		wantUpdateUID  *bool
	}{
		{
			name: "nil stays nil when no entry declares the field",
			entries: []*ImageMetadata{
				{},
				{},
			},
			wantInit:       nil,
			wantPrivileged: nil,
			wantOverride:   nil,
			wantUpdateUID:  nil,
		},
		{
			name: "single true wins for every boolean field",
			entries: []*ImageMetadata{
				{
					NonComposeBase: NonComposeBase{
						Init:       boolPtr(true),
						Privileged: boolPtr(true),
					},
					DevContainerConfigBase: DevContainerConfigBase{
						OverrideCommand:     boolPtr(true),
						UpdateRemoteUserUID: boolPtr(true),
					},
				},
			},
			wantInit:       boolPtr(true),
			wantPrivileged: boolPtr(true),
			wantOverride:   boolPtr(true),
			wantUpdateUID:  boolPtr(true),
		},
		{
			// The motivating scenario from the audit: a feature requires
			// privileged/init while devcontainer.json explicitly sets false.
			// OR semantics mean the feature's requirement must survive.
			name: "feature true followed by devcontainer false: OR keeps true",
			entries: []*ImageMetadata{
				{
					NonComposeBase: NonComposeBase{
						Init:       boolPtr(true),
						Privileged: boolPtr(true),
					},
				},
				{
					NonComposeBase: NonComposeBase{
						Init:       boolPtr(false),
						Privileged: boolPtr(false),
					},
					DevContainerConfigBase: DevContainerConfigBase{
						OverrideCommand:     boolPtr(false),
						UpdateRemoteUserUID: boolPtr(false),
					},
				},
			},
			wantInit:       boolPtr(true),
			wantPrivileged: boolPtr(true),
			wantOverride:   boolPtr(false),
			wantUpdateUID:  boolPtr(false),
		},
		{
			name: "feature false followed by devcontainer true: OR yields true",
			entries: []*ImageMetadata{
				{NonComposeBase: NonComposeBase{Init: boolPtr(false), Privileged: boolPtr(false)}},
				{NonComposeBase: NonComposeBase{Init: boolPtr(true), Privileged: boolPtr(true)}},
			},
			wantInit:       boolPtr(true),
			wantPrivileged: boolPtr(true),
		},
		{
			name: "all false stays false (OR must not collapse an explicit false to nil)",
			entries: []*ImageMetadata{
				{NonComposeBase: NonComposeBase{Init: boolPtr(false), Privileged: boolPtr(false)}},
				{NonComposeBase: NonComposeBase{Init: boolPtr(false), Privileged: boolPtr(false)}},
			},
			wantInit:       boolPtr(false),
			wantPrivileged: boolPtr(false),
		},
		{
			name: "last value wins for overrideCommand and updateRemoteUserUID",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{OverrideCommand: boolPtr(true), UpdateRemoteUserUID: boolPtr(true)}},
				{DevContainerConfigBase: DevContainerConfigBase{OverrideCommand: boolPtr(false), UpdateRemoteUserUID: boolPtr(false)}},
				{DevContainerConfigBase: DevContainerConfigBase{OverrideCommand: boolPtr(true)}},
			},
			wantOverride:  boolPtr(true),
			wantUpdateUID: boolPtr(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := MergeConfiguration(&DevContainerConfig{}, tt.entries)
			assert.NilError(t, err)

			assert.DeepEqual(t, merged.Init, tt.wantInit)
			assert.DeepEqual(t, merged.Privileged, tt.wantPrivileged)
			assert.DeepEqual(t, merged.OverrideCommand, tt.wantOverride)
			assert.DeepEqual(t, merged.UpdateRemoteUserUID, tt.wantUpdateUID)
		})
	}
}

// TestMergeConfigurationScalarLastWins pins the "Last value wins." scalar
// fields: waitFor, remoteUser, containerUser, userEnvProbe, shutdownAction.
func TestMergeConfigurationScalarLastWins(t *testing.T) {
	entries := []*ImageMetadata{
		{
			DevContainerConfigBase: DevContainerConfigBase{
				WaitFor:        "updateContentCommand",
				RemoteUser:     "base-remote-user",
				ShutdownAction: "stopContainer",
				UserEnvProbe:   "none",
			},
			NonComposeBase: NonComposeBase{
				ContainerUser: "base-container-user",
			},
		},
		{
			DevContainerConfigBase: DevContainerConfigBase{
				WaitFor:        "postCreateCommand",
				RemoteUser:     "feature-remote-user",
				ShutdownAction: "none",
			},
			NonComposeBase: NonComposeBase{
				ContainerUser: "feature-container-user",
			},
		},
		// devcontainer.json wins
		{
			DevContainerConfigBase: DevContainerConfigBase{
				WaitFor:      "postStartCommand",
				RemoteUser:   "json-remote-user",
				UserEnvProbe: "loginInteractiveShell",
			},
			NonComposeBase: NonComposeBase{
				ContainerUser: "json-container-user",
			},
		},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)

	assert.Equal(t, merged.WaitFor, "postStartCommand")
	assert.Equal(t, merged.RemoteUser, "json-remote-user")
	assert.Equal(t, merged.ContainerUser, "json-container-user")
	assert.Equal(t, merged.UserEnvProbe, "loginInteractiveShell")
	assert.Equal(t, merged.ShutdownAction, "none")
}

// TestMergeConfigurationEmptyStringDoesNotOverride ensures an empty string in
// a later entry does not wipe an earlier declared value.
func TestMergeConfigurationEmptyStringDoesNotOverride(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{RemoteUser: "keeper"}},
		{DevContainerConfigBase: DevContainerConfigBase{RemoteUser: ""}},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)
	assert.Equal(t, merged.RemoteUser, "keeper")
}

// TestMergeConfigurationUnionCapabilities covers capAdd/securityOpt:
// union without duplicates, first-declared occurrence defines the order.
func TestMergeConfigurationUnionCapabilities(t *testing.T) {
	entries := []*ImageMetadata{
		{NonComposeBase: NonComposeBase{CapAdd: []string{"SYS_PTRACE", "NET_ADMIN"}, SecurityOpt: []string{"seccomp=base.json"}}},
		{NonComposeBase: NonComposeBase{CapAdd: []string{"SYS_PTRACE", "SYS_CHROOT"}, SecurityOpt: []string{"seccomp=feature.json", "seccomp=base.json"}}},
		{NonComposeBase: NonComposeBase{CapAdd: []string{"NET_RAW"}}},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)

	// Spec: union without duplicates. Order must follow declaration order
	// (base image -> features -> devcontainer.json), first occurrence wins.
	assert.DeepEqual(t, merged.CapAdd, []string{"SYS_PTRACE", "NET_ADMIN", "SYS_CHROOT", "NET_RAW"})
	assert.DeepEqual(t, merged.SecurityOpt, []string{"seccomp=base.json", "seccomp=feature.json"})
}

// TestMergeConfigurationForwardPorts pins the port normalization rules:
// numeric ports compare as "localhost:<port>", but the original spelling is
// preserved; duplicates keep one entry only.
func TestMergeConfigurationForwardPorts(t *testing.T) {
	tests := []struct {
		name    string
		entries []*ImageMetadata
		want    types.StrIntArray
	}{
		{
			name: "numeric then localhost spelling: first declared spelling kept",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{ForwardPorts: types.StrIntArray{"3000"}}},
				{DevContainerConfigBase: DevContainerConfigBase{ForwardPorts: types.StrIntArray{"localhost:3000"}}},
			},
			want: types.StrIntArray{"3000"},
		},
		{
			name: "localhost spelling first, numeric later",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{ForwardPorts: types.StrIntArray{"localhost:3000"}}},
				{DevContainerConfigBase: DevContainerConfigBase{ForwardPorts: types.StrIntArray{"3000"}}},
			},
			want: types.StrIntArray{"localhost:3000"},
		},
		{
			name: "unrelated ports and host mappings are all retained",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{ForwardPorts: types.StrIntArray{"3000", "db:5432"}}},
				{DevContainerConfigBase: DevContainerConfigBase{ForwardPorts: types.StrIntArray{"8080", "3000"}}},
			},
			want: types.StrIntArray{"3000", "db:5432", "8080"},
		},
		{
			name: "no ports yields nil",
			entries: []*ImageMetadata{
				{},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := MergeConfiguration(&DevContainerConfig{}, tt.entries)
			assert.NilError(t, err)
			assert.DeepEqual(t, merged.ForwardPorts, tt.want)
		})
	}
}

// TestMergeConfigurationMounts pins mount conflict semantics.
//
// Spec: "Collected list of all mountpoints. Conflicts: Last source wins."
// Mounts sharing a target are a conflict; output order must follow the
// declaration order of the surviving mounts.
func TestMergeConfigurationMounts(t *testing.T) {
	tests := []struct {
		name    string
		entries []*ImageMetadata
		want    []string // String() representation of each surviving mount
	}{
		{
			name: "same target, different types: last declared source wins",
			entries: []*ImageMetadata{
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "bind", Source: "/host/path", Target: "/data"},
				}}},
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "volume", Source: "myvol", Target: "/data"},
				}}},
			},
			want: []string{"type=volume,src=myvol,dst=/data"},
		},
		{
			name: "same target, different types, reversed declaration order",
			entries: []*ImageMetadata{
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "volume", Source: "myvol", Target: "/data"},
				}}},
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "bind", Source: "/host/path", Target: "/data"},
				}}},
			},
			want: []string{"type=bind,src=/host/path,dst=/data"},
		},
		{
			name: "distinct targets kept in declaration order and conflicting target keeps last source",
			entries: []*ImageMetadata{
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "bind", Source: "/a", Target: "/mnt/a"},
					{Type: "volume", Source: "v1", Target: "/mnt/b"},
				}}},
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "bind", Source: "/c", Target: "/mnt/c"},
					{Type: "bind", Source: "/dup", Target: "/mnt/a"},
				}}},
			},
			want: []string{
				"type=bind,src=/dup,dst=/mnt/a",
				"type=volume,src=v1,dst=/mnt/b",
				"type=bind,src=/c,dst=/mnt/c",
			},
		},
		{
			name: "repeated conflict chains to the latest source at first position",
			entries: []*ImageMetadata{
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "bind", Source: "/first", Target: "/mnt/a"},
				}}},
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "volume", Source: "second", Target: "/mnt/a"},
				}}},
				{NonComposeBase: NonComposeBase{Mounts: []*Mount{
					{Type: "bind", Source: "/third", Target: "/mnt/a"},
				}}},
			},
			want: []string{"type=bind,src=/third,dst=/mnt/a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := MergeConfiguration(&DevContainerConfig{}, tt.entries)
			assert.NilError(t, err)

			got := make([]string, 0, len(merged.Mounts))
			for _, m := range merged.Mounts {
				got = append(got, m.String())
			}
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

// TestMergeConfigurationMountsEmpty covers the empty boundaries: no mounts
// yields an empty (non-nil-safe) slice and entries without mounts are skipped.
func TestMergeConfigurationMountsEmpty(t *testing.T) {
	merged, err := MergeConfiguration(&DevContainerConfig{}, []*ImageMetadata{{}, {}})
	assert.NilError(t, err)
	assert.Equal(t, len(merged.Mounts), 0)
}

// TestMergeConfigurationLifecycleHooks pins hook collection: every entry's
// command is collected, none overwrites another, and the three supported
// spellings (string, array, object) may be mixed. Execution order follows
// declaration order: base image -> features -> devcontainer.json.
func TestMergeConfigurationLifecycleHooks(t *testing.T) {
	entries := metadataListFromJSON(t, `[
		{"onCreateCommand": "echo base-oncreate", "postStartCommand": ["echo", "base-poststart"]},
		{"onCreateCommand": ["echo", "feature-oncreate"], "postAttachCommand": {"named": "echo feature-postattach"}},
		{"onCreateCommand": {"a": "echo json-a", "b": ["echo", "json-b"]}}
	]`)

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)

	// Three entries declare onCreateCommand: all three hooks must survive,
	// in declaration order.
	assert.Equal(t, len(merged.OnCreateCommands), 3)
	assert.DeepEqual(t, merged.OnCreateCommands[0], types.LifecycleHook{"": []string{"echo base-oncreate"}})
	assert.DeepEqual(t, merged.OnCreateCommands[1], types.LifecycleHook{"": []string{"echo", "feature-oncreate"}})
	assert.DeepEqual(t, merged.OnCreateCommands[2], types.LifecycleHook{
		"a": []string{"echo json-a"},
		"b": []string{"echo", "json-b"},
	})

	assert.Equal(t, len(merged.PostStartCommands), 1)
	assert.DeepEqual(t, merged.PostStartCommands[0], types.LifecycleHook{"": []string{"echo", "base-poststart"}})

	assert.Equal(t, len(merged.PostAttachCommands), 1)
	assert.DeepEqual(t, merged.PostAttachCommands[0], types.LifecycleHook{"named": []string{"echo feature-postattach"}})

	// Hooks no entry declares stay nil.
	assert.Equal(t, len(merged.PostCreateCommands), 0)
}

// TestMergeConfigurationAllLifecycleStages ensures each of the five hook
// families is collected independently.
func TestMergeConfigurationAllLifecycleStages(t *testing.T) {
	entries := []*ImageMetadata{
		{
			DevContainerActions: DevContainerActions{
				OnCreateCommand:      types.LifecycleHook{"": []string{"on-create"}},
				UpdateContentCommand: types.LifecycleHook{"": []string{"update-content"}},
				PostCreateCommand:    types.LifecycleHook{"": []string{"post-create"}},
				PostStartCommand:     types.LifecycleHook{"": []string{"post-start"}},
				PostAttachCommand:    types.LifecycleHook{"": []string{"post-attach"}},
			},
		},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)

	assert.Equal(t, len(merged.OnCreateCommands), 1)
	assert.Equal(t, len(merged.UpdateContentCommands), 1)
	assert.Equal(t, len(merged.PostCreateCommands), 1)
	assert.Equal(t, len(merged.PostStartCommands), 1)
	assert.Equal(t, len(merged.PostAttachCommands), 1)
}

// TestMergeConfigurationEntrypoints pins entrypoint collection order.
// Spec: "Collected list of all entrypoints." in declaration order
// (base image -> features -> devcontainer.json); the start script joins
// them in that sequence so declaration order is execution order.
func TestMergeConfigurationEntrypoints(t *testing.T) {
	entries := []*ImageMetadata{
		{Entrypoint: "/base-entrypoint.sh"},
		{Entrypoint: "/feature-entrypoint.sh"},
		{Entrypoint: ""}, // empty entrypoints are not collected
		{Entrypoint: "/json-entrypoint.sh"},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)
	assert.DeepEqual(t, merged.Entrypoints, []string{"/base-entrypoint.sh", "/feature-entrypoint.sh", "/json-entrypoint.sh"})
}

// TestMergeConfigurationMapsLastWins covers remoteEnv, containerEnv and
// portAttributes: per-key last value wins while unrelated keys are retained.
func TestMergeConfigurationMapsLastWins(t *testing.T) {
	entries := []*ImageMetadata{
		{
			DevContainerConfigBase: DevContainerConfigBase{
				RemoteEnv:       map[string]string{"SHARED": "base", "ONLY_BASE": "1"},
				PortsAttributes: map[string]PortAttribute{"3000": {Label: "base-label", OnAutoForward: "notify"}},
			},
			NonComposeBase: NonComposeBase{
				ContainerEnv: map[string]string{"SHARED": "base-env", "BASE_ONLY": "x"},
			},
		},
		{
			DevContainerConfigBase: DevContainerConfigBase{
				RemoteEnv:       map[string]string{"SHARED": "feature", "FEATURE_ONLY": "2"},
				PortsAttributes: map[string]PortAttribute{"3000": {Label: "feature-label"}, "8080": {Protocol: "https"}},
			},
			NonComposeBase: NonComposeBase{
				ContainerEnv: map[string]string{"SHARED": "feature-env"},
			},
		},
		{
			// devcontainer.json has the final word per key.
			DevContainerConfigBase: DevContainerConfigBase{
				RemoteEnv: map[string]string{"SHARED": "json"},
			},
			NonComposeBase: NonComposeBase{
				ContainerEnv: map[string]string{"SHARED": "json-env", "JSON_ONLY": "y"},
			},
		},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)

	assert.DeepEqual(t, merged.RemoteEnv, map[string]string{
		"SHARED":       "json",
		"ONLY_BASE":    "1",
		"FEATURE_ONLY": "2",
	})
	assert.DeepEqual(t, merged.ContainerEnv, map[string]string{
		"SHARED":    "json-env",
		"BASE_ONLY": "x",
		"JSON_ONLY": "y",
	})
	// Per port, the whole attribute object of the last declaring entry wins;
	// other ports keep their own attribute object.
	assert.DeepEqual(t, merged.PortsAttributes, map[string]PortAttribute{
		"3000": {Label: "feature-label"},
		"8080": {Protocol: "https"},
	})
}

// TestMergeConfigurationOtherPortsAttributes pins whole-object last-wins.
func TestMergeConfigurationOtherPortsAttributes(t *testing.T) {
	entries := []*ImageMetadata{
		{DevContainerConfigBase: DevContainerConfigBase{OtherPortsAttributes: &PortAttribute{Label: "base"}}},
		{DevContainerConfigBase: DevContainerConfigBase{OtherPortsAttributes: &PortAttribute{Label: "json", Protocol: "http"}}},
	}

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)
	assert.DeepEqual(t, merged.OtherPortsAttributes, &PortAttribute{Label: "json", Protocol: "http"})
}

// TestMergeConfigurationCustomizationsCollectedPerTool pins the
// "collect per tool, no deep merge" behavior.
func TestMergeConfigurationCustomizationsCollectedPerTool(t *testing.T) {
	entries := metadataListFromJSON(t, `[
		{"customizations": {"vscode": {"extensions": ["a"]}, "devpod": {"prebuildRepository": ["reg-a"]}}},
		{"customizations": {"vscode": {"extensions": ["b"], "settings": {"editor.fontSize": 14}}}},
		{"customizations": {"other-tool": {"key": "value"}}}
	]`)

	merged, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)

	assert.Equal(t, len(merged.Customizations["vscode"]), 2)
	assert.Equal(t, len(merged.Customizations["devpod"]), 1)
	assert.Equal(t, len(merged.Customizations["other-tool"]), 1)

	// Order within a tool bucket follows declaration order.
	first, err := json.Marshal(merged.Customizations["vscode"][0])
	assert.NilError(t, err)
	assert.Equal(t, string(first), `{"extensions":["a"]}`)
}

// TestMergeConfigurationHostRequirements pins the "Max value wins." rule.
// DevPod's HostRequirements uses int CPUs and unit-suffixed strings for
// memory/storage and a StrBool GPU flag.
func TestMergeConfigurationHostRequirements(t *testing.T) {
	tests := []struct {
		name    string
		entries []*ImageMetadata
		want    *HostRequirements
	}{
		{
			name: "maximum of each dimension across entries",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{
					CPUs: 2, Memory: "2gb", Storage: "10gb", GPU: types.StrBool("false"),
				}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{
					CPUs: 8, Memory: "1gb", Storage: "50gb", GPU: types.StrBool("true"),
				}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{
					CPUs: 4, Memory: "4096mb", Storage: "20gb",
				}}},
			},
			want: &HostRequirements{
				CPUs: 8, Memory: "4096mb", Storage: "50gb", GPU: types.StrBool("true"),
			},
		},
		{
			name: "single entry is returned as-is",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{CPUs: 1, Memory: "512mb"}}},
			},
			want: &HostRequirements{CPUs: 1, Memory: "512mb"},
		},
		{
			name:    "no entry yields nil",
			entries: []*ImageMetadata{{}, {}},
			want:    nil,
		},
		{
			name: "tb unit outranks gb",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Memory: "1tb"}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Memory: "100gb"}}},
			},
			want: &HostRequirements{Memory: "1tb"},
		},
		{
			name: "equivalent sizes keep the later declared spelling",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Memory: "1024mb"}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Memory: "1gb"}}},
			},
			want: &HostRequirements{Memory: "1gb"},
		},
		{
			name: "unparsable size is ignored instead of overriding a valid value",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Memory: "2gb"}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Memory: "lots"}}},
			},
			want: &HostRequirements{Memory: "2gb"},
		},
		{
			name: "partial requirements across entries are combined dimension by dimension",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{CPUs: 4}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{Storage: "50gb"}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{GPU: types.StrBool("optional")}}},
			},
			want: &HostRequirements{CPUs: 4, Storage: "50gb", GPU: types.StrBool("optional")},
		},
		{
			name: "a true GPU requirement overrides an optional one",
			entries: []*ImageMetadata{
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{GPU: types.StrBool("true")}}},
				{DevContainerConfigBase: DevContainerConfigBase{HostRequirements: &HostRequirements{GPU: types.StrBool("optional")}}},
			},
			want: &HostRequirements{GPU: types.StrBool("true")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged, err := MergeConfiguration(&DevContainerConfig{}, tt.entries)
			assert.NilError(t, err)
			if tt.want == nil {
				assert.Equal(t, merged.HostRequirements == nil, true)
				return
			}
			assert.Assert(t, merged.HostRequirements != nil)
			assert.Equal(t, merged.HostRequirements.CPUs, tt.want.CPUs)
			assert.Equal(t, merged.HostRequirements.Memory, tt.want.Memory)
			assert.Equal(t, merged.HostRequirements.Storage, tt.want.Storage)
			assert.Equal(t, string(merged.HostRequirements.GPU), string(tt.want.GPU))
		})
	}
}

// TestMergeConfigurationDoesNotMutateInputs ensures the input config and
// metadata entries are untouched and the result shares no slice/map headers.
func TestMergeConfigurationDoesNotMutateInputs(t *testing.T) {
	config := &DevContainerConfig{
		ImageContainer: ImageContainer{Image: "registry.example/image:tag"},
		NonComposeBase: NonComposeBase{
			ContainerEnv: map[string]string{"CONFIG_KEY": "config-value"},
			CapAdd:       []string{"CONFIG_CAP"},
		},
	}
	entries := []*ImageMetadata{
		{
			NonComposeBase: NonComposeBase{
				ContainerEnv: map[string]string{"ENTRY_KEY": "entry-value"},
				CapAdd:       []string{"ENTRY_CAP"},
				Mounts:       []*Mount{{Type: "bind", Source: "/s", Target: "/t"}},
			},
		},
	}

	configJSON, err := json.Marshal(config)
	assert.NilError(t, err)
	entriesJSON, err := json.Marshal(entries)
	assert.NilError(t, err)

	merged, err := MergeConfiguration(config, entries)
	assert.NilError(t, err)

	// Inputs must be byte-identical before and after the merge.
	assert.Equal(t, mustMarshal(t, config), string(configJSON))
	assert.Equal(t, mustMarshal(t, entries), string(entriesJSON))

	// Mutating result maps/slices must not propagate back into the inputs.
	merged.ContainerEnv["MUTATED"] = "yes"
	merged.CapAdd[0] = "MUTATED_CAP"
	_, ok := entries[0].ContainerEnv["MUTATED"]
	assert.Equal(t, ok, false)
	assert.Equal(t, entries[0].CapAdd[0], "ENTRY_CAP")
	assert.Equal(t, config.ContainerEnv["CONFIG_KEY"], "config-value")

	// Mount pointers are copied through, but the mount set itself is rebuilt.
	assert.Equal(t, len(merged.Mounts), 1)
	assert.Equal(t, merged.Mounts[0].Target, "/t")
}

func mustMarshal(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	assert.NilError(t, err)
	return string(b)
}

// TestMergeConfigurationStableOutput requires repeated merges of the same
// input to produce byte-identical JSON, including map-backed fields.
func TestMergeConfigurationStableOutput(t *testing.T) {
	entries := []*ImageMetadata{
		{
			DevContainerConfigBase: DevContainerConfigBase{
				RemoteEnv:       map[string]string{"B": "1", "A": "2", "C": "3"},
				PortsAttributes: map[string]PortAttribute{"3000": {Label: "web"}, "8080": {Protocol: "https"}},
			},
			NonComposeBase: NonComposeBase{
				ContainerEnv: map[string]string{"X": "x", "Y": "y"},
			},
			DevContainerActions: DevContainerActions{
				Customizations: map[string]interface{}{"vscode": map[string]interface{}{"extensions": []string{"a"}}},
			},
		},
		{
			DevContainerConfigBase: DevContainerConfigBase{
				RemoteEnv: map[string]string{"A": "overridden", "D": "4"},
			},
		},
	}

	first, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)
	firstJSON := mustMarshal(t, first)

	for i := 0; i < 10; i++ {
		again, err := MergeConfiguration(&DevContainerConfig{}, entries)
		assert.NilError(t, err)
		assert.Equal(t, mustMarshal(t, again), firstJSON)
	}
}

// TestMergeConfigurationOrderOnlyMattersPerField shuffles entries whose
// fields are mutually unrelated: no field is declared by two entries, so the
// merged result must not depend on entry order.
func TestMergeConfigurationOrderOnlyMattersPerField(t *testing.T) {
	base := []*ImageMetadata{
		{
			NonComposeBase: NonComposeBase{
				CapAdd:       []string{"CAP_A"},
				SecurityOpt:  []string{"SEC_A"},
				ContainerEnv: map[string]string{"ENV_A": "a"},
				Mounts:       []*Mount{{Type: "bind", Source: "/a", Target: "/mnt/a"}},
			},
		},
		{
			NonComposeBase: NonComposeBase{
				CapAdd:       []string{"CAP_B"},
				SecurityOpt:  []string{"SEC_B"},
				ContainerEnv: map[string]string{"ENV_B": "b"},
				Mounts:       []*Mount{{Type: "volume", Source: "vb", Target: "/mnt/b"}},
			},
		},
		{
			NonComposeBase: NonComposeBase{
				CapAdd:       []string{"CAP_C"},
				SecurityOpt:  []string{"SEC_C"},
				ContainerEnv: map[string]string{"ENV_C": "c"},
				Mounts:       []*Mount{{Type: "bind", Source: "/c", Target: "/mnt/c"}},
			},
		},
		{
			DevContainerConfigBase: DevContainerConfigBase{
				ForwardPorts: types.StrIntArray{"3000", "4000"},
				RemoteEnv:    map[string]string{"REMOTE_A": "r"},
			},
		},
		{
			DevContainerConfigBase: DevContainerConfigBase{
				ForwardPorts: types.StrIntArray{"5000"},
				RemoteEnv:    map[string]string{"REMOTE_B": "s"},
			},
		},
	}

	reference, err := MergeConfiguration(&DevContainerConfig{}, base)
	assert.NilError(t, err)

	// Fixed-seed shuffle: deterministic within this test, random across the
	// field space because entries touch disjoint fields.
	rng := rand.New(rand.NewSource(20260629))
	for iteration := 0; iteration < 20; iteration++ {
		shuffled := make([]*ImageMetadata, len(base))
		copy(shuffled, base)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})

		merged, err := MergeConfiguration(&DevContainerConfig{}, shuffled)
		assert.NilError(t, err)

		// Order-sensitive collections (capAdd/securityOpt unions, mounts,
		// ports) are compared as sets here; map-backed fields compare
		// directly since their per-key values do not conflict.
		assert.DeepEqual(t, asSet(merged.CapAdd), asSet(reference.CapAdd))
		assert.DeepEqual(t, asSet(merged.SecurityOpt), asSet(reference.SecurityOpt))
		assert.DeepEqual(t, merged.ContainerEnv, reference.ContainerEnv)
		assert.DeepEqual(t, merged.RemoteEnv, reference.RemoteEnv)
		assert.DeepEqual(t, mountTargetSet(merged.Mounts), mountTargetSet(reference.Mounts))
		assert.DeepEqual(t, asSet(merged.ForwardPorts), asSet(reference.ForwardPorts))

		// And the whole document must remain stable across same-input reruns.
		assert.Equal(t, mustMarshal(t, merged), mustMarshal(t, mergeOrFail(t, shuffled)))
	}
}

func mergeOrFail(t *testing.T, entries []*ImageMetadata) *MergedDevContainerConfig {
	t.Helper()
	m, err := MergeConfiguration(&DevContainerConfig{}, entries)
	assert.NilError(t, err)
	return m
}

func asSet(s []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range s {
		out[v] = true
	}
	return out
}

func mountTargetSet(mounts []*Mount) map[string]bool {
	out := map[string]bool{}
	for _, m := range mounts {
		out[m.Target] = true
	}
	return out
}
