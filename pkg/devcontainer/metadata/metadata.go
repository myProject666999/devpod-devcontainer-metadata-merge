package metadata

import (
	"encoding/json"
	"fmt"

	"github.com/loft-sh/devpod/pkg/devcontainer/config"
	"github.com/loft-sh/log"
)

const ImageMetadataLabel = "devcontainer.metadata"

func GetDevContainerMetadata(substitutionContext *config.SubstitutionContext, baseImageMetadata *config.ImageMetadataConfig, devContainerConfig *config.SubstitutedConfig, featuresConfig []*config.FeatureSet) (*config.ImageMetadataConfig, error) {
	// features
	featuresRaw := []*config.ImageMetadata{}
	for _, featureSet := range featuresConfig {
		featuresRaw = append(featuresRaw, FeatureConfigToImageMetadata(featureSet.Config))
	}

	retImageMetadataConfig := &config.ImageMetadataConfig{}
	retImageMetadataConfig.Raw = append(retImageMetadataConfig.Raw, baseImageMetadata.Raw...)
	retImageMetadataConfig.Raw = append(retImageMetadataConfig.Raw, featuresRaw...)
	retImageMetadataConfig.Raw = append(retImageMetadataConfig.Raw, DevContainerConfigToImageMetadata(devContainerConfig.Raw))

	retImageMetadataConfig.Config = append(retImageMetadataConfig.Config, baseImageMetadata.Config...)
	for _, featureRaw := range featuresRaw {
		featureConfig := &config.ImageMetadata{}
		err := config.Substitute(substitutionContext, featureRaw, featureConfig)
		if err != nil {
			return nil, err
		}

		retImageMetadataConfig.Config = append(retImageMetadataConfig.Config, featureConfig)
	}
	retImageMetadataConfig.Config = append(retImageMetadataConfig.Config, DevContainerConfigToImageMetadata(devContainerConfig.Config))
	return retImageMetadataConfig, nil
}

func FeatureConfigToImageMetadata(feature *config.FeatureConfig) *config.ImageMetadata {
	return &config.ImageMetadata{
		Entrypoint: feature.Entrypoint,
		DevContainerActions: config.DevContainerActions{
			Customizations: feature.Customizations,
		},
		NonComposeBase: config.NonComposeBase{
			Mounts:      feature.Mounts,
			Init:        feature.Init,
			Privileged:  feature.Privileged,
			CapAdd:      feature.CapAdd,
			SecurityOpt: feature.SecurityOpt,
		},
	}
}

func DevContainerConfigToImageMetadata(devConfig *config.DevContainerConfig) *config.ImageMetadata {
	return &config.ImageMetadata{
		DevContainerConfigBase: config.DevContainerConfigBase{
			ForwardPorts:         devConfig.ForwardPorts,
			PortsAttributes:      devConfig.PortsAttributes,
			OtherPortsAttributes: devConfig.OtherPortsAttributes,
			UpdateRemoteUserUID:  devConfig.UpdateRemoteUserUID,
			RemoteEnv:            devConfig.RemoteEnv,
			RemoteUser:           devConfig.RemoteUser,
			ShutdownAction:       devConfig.ShutdownAction,
			WaitFor:              devConfig.WaitFor,
			UserEnvProbe:         devConfig.UserEnvProbe,
			HostRequirements:     devConfig.HostRequirements,
			OverrideCommand:      devConfig.OverrideCommand,
		},
		DevContainerActions: config.DevContainerActions{
			OnCreateCommand:      devConfig.OnCreateCommand,
			UpdateContentCommand: devConfig.UpdateContentCommand,
			PostCreateCommand:    devConfig.PostCreateCommand,
			PostStartCommand:     devConfig.PostStartCommand,
			PostAttachCommand:    devConfig.PostAttachCommand,
			Customizations:       devConfig.Customizations,
		},
		NonComposeBase: config.NonComposeBase{
			ContainerEnv:  devConfig.ContainerEnv,
			ContainerUser: devConfig.ContainerUser,
			Mounts:        devConfig.Mounts,
			Init:          devConfig.Init,
			Privileged:    devConfig.Privileged,
			CapAdd:        devConfig.CapAdd,
			SecurityOpt:   devConfig.SecurityOpt,
		},
	}
}

func GetImageMetadataFromContainer(containerDetails *config.ContainerDetails, substituteContext *config.SubstitutionContext, log log.Logger) (*config.ImageMetadataConfig, error) {
	if containerDetails == nil || containerDetails.Config.Labels == nil || containerDetails.Config.Labels[ImageMetadataLabel] == "" {
		return &config.ImageMetadataConfig{}, nil
	}

	raw, err := parseImageMetadataLabel(containerDetails.Config.Labels[ImageMetadataLabel], log)
	if err != nil {
		return nil, err
	}

	imageMetadataConfig := &config.ImageMetadataConfig{Raw: raw}
	err = substituteConfig(imageMetadataConfig, substituteContext)
	if err != nil {
		return nil, err
	}

	return imageMetadataConfig, nil
}

func GetImageMetadata(imageDetails *config.ImageDetails, substituteContext *config.SubstitutionContext, log log.Logger) (*config.ImageMetadataConfig, error) {
	if imageDetails.Config.Labels == nil || imageDetails.Config.Labels[ImageMetadataLabel] == "" {
		return &config.ImageMetadataConfig{}, nil
	}

	raw, err := parseImageMetadataLabel(imageDetails.Config.Labels[ImageMetadataLabel], log)
	if err != nil {
		return nil, err
	}

	imageMetadataConfig := &config.ImageMetadataConfig{Raw: raw}
	err = substituteConfig(imageMetadataConfig, substituteContext)
	if err != nil {
		return nil, err
	}

	return imageMetadataConfig, nil
}

// parseImageMetadataLabel decodes a devcontainer.metadata label. The label
// may hold either an array of metadata snippets or a single object (the
// shapes documented in the devcontainer spec).
//
// An array may mix valid objects with scalar/array elements: the invalid
// elements are skipped with a warning while the valid objects are kept. A
// label that is present but cannot be parsed into either shape, or that
// contains no valid object at all, is a hard error so callers cannot
// silently build a container whose base image metadata has vanished.
func parseImageMetadataLabel(label string, log log.Logger) ([]*config.ImageMetadata, error) {
	var generic interface{}
	if err := json.Unmarshal([]byte(label), &generic); err != nil {
		return nil, fmt.Errorf("parse %s label: %w", ImageMetadataLabel, err)
	}

	switch value := generic.(type) {
	case []interface{}:
		raw := []*config.ImageMetadata{}
		for i, element := range value {
			if _, ok := element.(map[string]interface{}); !ok {
				log.Warnf("Skipping element %d in %s label: expected a metadata object, got %T", i, ImageMetadataLabel, element)
				continue
			}

			entry := &config.ImageMetadata{}
			elementBytes, err := json.Marshal(element)
			if err != nil {
				return nil, fmt.Errorf("re-encode %s label element %d: %w", ImageMetadataLabel, i, err)
			}
			if err := json.Unmarshal(elementBytes, entry); err != nil {
				return nil, fmt.Errorf("parse %s label element %d: %w", ImageMetadataLabel, i, err)
			}
			raw = append(raw, entry)
		}

		if len(raw) == 0 {
			return nil, fmt.Errorf("%s label array contains no valid metadata object", ImageMetadataLabel)
		}
		return raw, nil

	case map[string]interface{}:
		single := &config.ImageMetadata{}
		if err := json.Unmarshal([]byte(label), single); err != nil {
			return nil, fmt.Errorf("parse %s label object: %w", ImageMetadataLabel, err)
		}
		return []*config.ImageMetadata{single}, nil

	default:
		return nil, fmt.Errorf("%s label must be an object or an array of objects, got %T", ImageMetadataLabel, generic)
	}
}

func substituteConfig(imageConfig *config.ImageMetadataConfig, substituteContext *config.SubstitutionContext) error {
	imageConfig.Config = []*config.ImageMetadata{}
	for _, raw := range imageConfig.Raw {
		imageMetadata := &config.ImageMetadata{}
		err := config.Substitute(substituteContext, raw, imageMetadata)
		if err != nil {
			return err
		}

		imageConfig.Config = append(imageConfig.Config, imageMetadata)
	}

	return nil
}
