package config

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/loft-sh/devpod/pkg/types"
)

var byteSizeRegExp = regexp.MustCompile(`^(\d+)(tb|gb|mb|kb)?$`)

func MergeConfiguration(config *DevContainerConfig, imageMetadataEntries []*ImageMetadata) (*MergedDevContainerConfig, error) {
	customizations := map[string][]interface{}{}
	for _, imageMetadata := range imageMetadataEntries {
		for k, v := range imageMetadata.Customizations {
			customizations[k] = append(customizations[k], v)
		}
	}

	copiedConfig := CloneDevContainerConfig(config)

	// Declaration order: base image metadata first, features in install
	// order middle, devcontainer.json last. "Last value wins." scalar fields
	// and the boolean OR need the reversed view; everything that is
	// collected or unioned must walk the entries in declaration order.
	// See https://containers.dev/implementors/spec/#merge-logic
	reversed := ReverseSlice(imageMetadataEntries)

	// merge config
	mergedConfig := &MergedDevContainerConfig{
		UpdatedConfigProperties: UpdatedConfigProperties{
			Customizations: customizations,
		},
		DevContainerConfigBase: copiedConfig.DevContainerConfigBase,
		NonComposeBase:         copiedConfig.NonComposeBase,
		ImageContainer:         copiedConfig.ImageContainer,
		ComposeContainer:       copiedConfig.ComposeContainer,
		DockerfileContainer:    copiedConfig.DockerfileContainer,
	}

	// Boolean fields: init/privileged are "true if at least one is true",
	// while overrideCommand/updateRemoteUserUID are "Last value wins.".
	mergedConfig.Init = anyTrue(imageMetadataEntries, func(entry *ImageMetadata) *bool { return entry.Init })
	mergedConfig.Privileged = anyTrue(imageMetadataEntries, func(entry *ImageMetadata) *bool { return entry.Privileged })
	mergedConfig.CapAdd = unique(unionOrNil(imageMetadataEntries, func(entry *ImageMetadata) []string { return entry.CapAdd }))
	mergedConfig.SecurityOpt = unique(unionOrNil(imageMetadataEntries, func(entry *ImageMetadata) []string { return entry.SecurityOpt }))
	mergedConfig.Entrypoints = collectOrNil(imageMetadataEntries, func(entry *ImageMetadata) string { return entry.Entrypoint })
	mergedConfig.Mounts = mergeMounts(imageMetadataEntries)
	mergedConfig.OnCreateCommands = mergeLifestyleHooks(imageMetadataEntries, func(entry *ImageMetadata) types.LifecycleHook { return entry.OnCreateCommand })
	mergedConfig.UpdateContentCommands = mergeLifestyleHooks(imageMetadataEntries, func(entry *ImageMetadata) types.LifecycleHook { return entry.UpdateContentCommand })
	mergedConfig.PostCreateCommands = mergeLifestyleHooks(imageMetadataEntries, func(entry *ImageMetadata) types.LifecycleHook { return entry.PostCreateCommand })
	mergedConfig.PostStartCommands = mergeLifestyleHooks(imageMetadataEntries, func(entry *ImageMetadata) types.LifecycleHook { return entry.PostStartCommand })
	mergedConfig.PostAttachCommands = mergeLifestyleHooks(imageMetadataEntries, func(entry *ImageMetadata) types.LifecycleHook { return entry.PostAttachCommand })
	mergedConfig.WaitFor = firstString(reversed, func(entry *ImageMetadata) string { return entry.WaitFor })
	mergedConfig.RemoteUser = firstString(reversed, func(entry *ImageMetadata) string { return entry.RemoteUser })
	mergedConfig.ContainerUser = firstString(reversed, func(entry *ImageMetadata) string { return entry.ContainerUser })
	mergedConfig.UserEnvProbe = firstString(reversed, func(entry *ImageMetadata) string { return entry.UserEnvProbe })
	mergedConfig.RemoteEnv = mergeMaps(imageMetadataEntries, func(entry *ImageMetadata) map[string]string { return entry.RemoteEnv })
	mergedConfig.ContainerEnv = mergeMaps(imageMetadataEntries, func(entry *ImageMetadata) map[string]string { return entry.ContainerEnv })
	mergedConfig.PortsAttributes = mergeMaps(imageMetadataEntries, func(entry *ImageMetadata) map[string]PortAttribute { return entry.PortsAttributes })
	mergedConfig.OverrideCommand = some(reversed, func(entry *ImageMetadata) *bool { return entry.OverrideCommand })
	mergedConfig.OtherPortsAttributes = mergeOtherPortsAttributes(reversed)
	mergedConfig.ShutdownAction = firstString(reversed, func(entry *ImageMetadata) string { return entry.ShutdownAction })
	mergedConfig.ForwardPorts = mergeForwardPorts(imageMetadataEntries)
	mergedConfig.UpdateRemoteUserUID = some(reversed, func(entry *ImageMetadata) *bool { return entry.UpdateRemoteUserUID })
	mergedConfig.HostRequirements = mergeHostRequirements(imageMetadataEntries)

	return mergedConfig, nil
}

// anyTrue reports whether any entry explicitly sets the boolean field to
// true. An explicit false contributes the value false rather than dropping
// the field, so an all-false set yields a pointer to false and a set without
// any declaration yields nil.
func anyTrue(entries []*ImageMetadata, m func(entry *ImageMetadata) *bool) *bool {
	declared := false
	result := false
	for _, entry := range entries {
		if boolPtr := m(entry); boolPtr != nil {
			declared = true
			if *boolPtr {
				result = true
			}
		}
	}
	if !declared {
		return nil
	}
	return &result
}

func mergeOtherPortsAttributes(entries []*ImageMetadata) *PortAttribute {
	for _, entry := range entries {
		if entry.OtherPortsAttributes != nil {
			return entry.OtherPortsAttributes
		}
	}
	return nil
}

func mergeMaps[K any](entries []*ImageMetadata, m func(entry *ImageMetadata) map[string]K) map[string]K {
	retMap := map[string]K{}
	for _, entry := range entries {
		entryMap := m(entry)
		for k, v := range entryMap {
			retMap[k] = v
		}
	}

	return retMap
}

func firstString(entries []*ImageMetadata, m func(entry *ImageMetadata) string) string {
	for _, entry := range entries {
		str := m(entry)
		if str != "" {
			return str
		}
	}
	return ""
}

func mergeHostRequirements(entries []*ImageMetadata) *HostRequirements {
	// Spec: "Max value wins." per dimension. Memory/storage are unit
	// suffixed strings (tb/gb/mb/kb); compare the parsed byte counts but
	// preserve the winning entry's original spelling.
	result := &HostRequirements{}
	declared := false

	var maxMemory, maxStorage uint64
	var firstGPU types.StrBool
	for _, entry := range entries {
		req := entry.HostRequirements
		if req == nil {
			continue
		}
		declared = true

		if req.CPUs > result.CPUs {
			result.CPUs = req.CPUs
		}

		if memory, ok := parseByteSize(req.Memory); ok && memory >= maxMemory {
			maxMemory = memory
			result.Memory = req.Memory
		}
		if storage, ok := parseByteSize(req.Storage); ok && storage >= maxStorage {
			maxStorage = storage
			result.Storage = req.Storage
		}

		if req.GPU != "" && firstGPU == "" {
			firstGPU = req.GPU
		}
	}

	// GPU is consumed as a StrBool flag (only the literal "true" enables
	// GPU support); an explicit "true" wins over any other declared value,
	// otherwise the first declared value is kept.
	for _, entry := range entries {
		if req := entry.HostRequirements; req != nil && req.GPU == types.StrBool("true") {
			result.GPU = req.GPU
			break
		}
	}
	if result.GPU == "" {
		result.GPU = firstGPU
	}

	if !declared {
		return nil
	}
	return result
}

// parseByteSize parses values like "4gb", "1024mb", "1tb" into bytes. A bare
// number is interpreted as bytes (matching the units documented on
// HostRequirements.Memory/Storage).
func parseByteSize(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}

	match := byteSizeRegExp.FindStringSubmatch(strings.ToLower(strings.TrimSpace(value)))
	if match == nil {
		return 0, false
	}

	number, err := strconv.ParseUint(match[1], 10, 64)
	if err != nil {
		return 0, false
	}

	multiplier := uint64(1)
	switch match[2] {
	case "kb":
		multiplier = 1 << 10
	case "mb":
		multiplier = 1 << 20
	case "gb":
		multiplier = 1 << 30
	case "tb":
		multiplier = 1 << 40
	}
	return number * multiplier, true
}

func mergeForwardPorts(entries []*ImageMetadata) types.StrIntArray {
	// Spec: union without duplicates. Numeric-only spellings are compared
	// as "localhost:<port>"; the first declared spelling of a duplicate is
	// preserved on output.
	portMap := map[string]bool{}
	var retPorts types.StrIntArray
	for _, entry := range entries {
		for _, port := range entry.ForwardPorts {
			portString := port
			if _, err := strconv.Atoi(portString); err == nil {
				portString = "localhost:" + portString
			}
			if portMap[portString] {
				continue
			}

			portMap[portString] = true
			retPorts = append(retPorts, port)
		}
	}

	return retPorts
}

func mergeMounts(entries []*ImageMetadata) []*Mount {
	// Spec: "Collected list of all mountpoints. Conflicts: Last source wins."
	// Walk declarations in order so the surviving mount keeps the position of
	// the first declaration for a target, while a later declaration of the
	// same target replaces the source.
	targetIndex := map[string]int{}
	ret := []*Mount{}

	for _, entry := range entries {
		for _, mount := range entry.Mounts {
			if index, ok := targetIndex[mount.Target]; ok {
				// Later declaration wins the conflict; output position of
				// the target stays where it was first declared.
				ret[index] = mount
				continue
			}

			targetIndex[mount.Target] = len(ret)
			ret = append(ret, mount)
		}
	}
	return ret
}

func mergeLifestyleHooks(entries []*ImageMetadata, m func(entry *ImageMetadata) types.LifecycleHook) []types.LifecycleHook {
	var out []types.LifecycleHook
	for _, entry := range entries {
		val := m(entry)
		if len(val) > 0 {
			out = append(out, m(entry))
		}
	}
	return out
}

func collectOrNil[T comparable, K any](entries []K, m func(entry K) T) []T {
	var out []T
	for _, entry := range entries {
		var defaultValue T
		val := m(entry)
		if val != defaultValue {
			out = append(out, m(entry))
		}
	}

	return out
}

func unionOrNil[T any, K any](entries []K, m func(entry K) []T) []T {
	var out []T
	for _, entry := range entries {
		vals := m(entry)
		if len(vals) > 0 {
			out = append(out, vals...)
		}
	}

	return out
}

func unique[T comparable](s []T) []T {
	inResult := make(map[T]bool)
	var result []T
	for _, str := range s {
		if _, ok := inResult[str]; !ok {
			inResult[str] = true
			result = append(result, str)
		}
	}
	return result
}

func some[T any](entries []T, m func(entry T) *bool) *bool {
	for _, entry := range entries {
		boolPtr := m(entry)
		if boolPtr != nil {
			return boolPtr
		}
	}
	return nil
}

func ReverseSlice[T comparable](s []T) []T {
	var r []T
	for i := len(s) - 1; i >= 0; i-- {
		r = append(r, s[i])
	}
	return r
}
