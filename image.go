package dalec

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/platforms"
	"github.com/google/shlex"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type DockerImageSpec = dockerspec.DockerOCIImage
type DockerImageConfig = dockerspec.DockerOCIImageConfig

// ImageConfig is the configuration for the output image.
// When the target output is a container image, this is used to configure the image.
type ImageConfig struct {
	// Entrypoint sets the image's "entrypoint" field.
	// This is used to control the default command to run when the image is run.
	Entrypoint string `yaml:"entrypoint,omitempty" json:"entrypoint,omitempty"`
	// Cmd sets the image's "cmd" field.
	// When entrypoint is set, this is used as the default arguments to the entrypoint.
	// When entrypoint is not set, this is used as the default command to run.
	Cmd string `yaml:"cmd,omitempty" json:"cmd,omitempty"`
	// Env is the list of environment variables to set in the image.
	Env []string `yaml:"env,omitempty" json:"env,omitempty"`
	// Labels is the list of labels to set in the image metadata.
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	// Volumes is the list of volumes for the image.
	// Volumes instruct the runtime to bypass the any copy-on-write filesystems and mount the volume directly to the container.
	Volumes map[string]struct{} `yaml:"volumes,omitempty" json:"volumes,omitempty"`
	// WorkingDir is the working directory to set in the image.
	// This sets the directory the container will start in.
	WorkingDir string `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
	// StopSignal is the signal to send to the container to stop it.
	// This is used to stop the container gracefully.
	StopSignal string `yaml:"stop_signal,omitempty" json:"stop_signal,omitempty" jsonschema:"example=SIGTERM"`
	// Base is the base image to use for the output image.
	// This only affects the output image, not the intermediate build image.
	Base string `yaml:"base,omitempty" json:"base,omitempty"`

	// Post is the post install configuration for the image.
	// This allows making additional modifications to the container rootfs after the package(s) are installed.
	//
	// Use this to perform actions that would otherwise require additional tooling inside the container that is not relavent to
	// the resulting container and makes a post-install script as part of the package unnecessary.
	Post *PostInstall `yaml:"post,omitempty" json:"post,omitempty"`

	// User is the that the image should run as.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
}

type imageBuilderConfig struct {
	platform *ocispecs.Platform
}

type ConfigOpt func(*imageBuilderConfig)

func WithPlatform(p ocispecs.Platform) ConfigOpt {
	return func(c *imageBuilderConfig) {
		c.platform = &p
	}
}

func BuildImageConfig(ctx context.Context, client gwclient.Client, spec *Spec, targetKey string, dflt string, opts ...ConfigOpt) (*DockerImageSpec, error) {
	dc, err := dockerui.NewClient(client)
	if err != nil {
		return nil, err
	}

	builderCfg := imageBuilderConfig{}
	for _, optFunc := range opts {
		optFunc(&builderCfg)
	}

	baseImgRef := getBaseOutputImage(spec, targetKey, dflt)
	platform := platforms.DefaultSpec()
	if builderCfg.platform != nil {
		platform = *builderCfg.platform
	}

	_, _, dt, err := client.ResolveImageConfig(ctx, baseImgRef, sourceresolver.Opt{
		Platform: &platform,
		ImageOpt: &sourceresolver.ResolveImageOpt{
			ResolveMode: dc.ImageResolveMode.String(),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("error resolving image config: %w", err)
	}

	var img DockerImageSpec
	if err := json.Unmarshal(dt, &img); err != nil {
		return nil, fmt.Errorf("error unmarshalling image config: %w", err)
	}

	cfg := img.Config
	if err := MergeImageConfig(&cfg, mergeSpecImage(spec, targetKey)); err != nil {
		return nil, err
	}

	img.Config = cfg
	return &img, nil
}

// MergeImageConfig copies the fields from the source [ImageConfig] into the destination [image.Image].
// If a field is not set in the source, it is not modified in the destination.
// Envs from [ImageConfig] are merged into the destination [image.Image] and take precedence.
func MergeImageConfig(dst *DockerImageConfig, src *ImageConfig) error {
	if src == nil {
		return nil
	}

	if src.Entrypoint != "" {
		split, err := shlex.Split(src.Entrypoint)
		if err != nil {
			return errors.Wrap(err, "error splitting entrypoint into args")
		}
		dst.Entrypoint = split
		// Reset cmd as this may be totally invalid now
		// This is the same behavior as the Dockerfile frontend
		dst.Cmd = nil
	}
	if src.Cmd != "" {
		split, err := shlex.Split(src.Cmd)
		if err != nil {
			return errors.Wrap(err, "error splitting cmd into args")
		}
		dst.Cmd = split
	}

	if len(src.Env) > 0 {
		// Env is append only
		// If the env var already exists, replace it
		envIdx := make(map[string]int)
		for i, env := range dst.Env {
			envIdx[env] = i
		}

		for _, env := range src.Env {
			if idx, ok := envIdx[env]; ok {
				dst.Env[idx] = env
			} else {
				dst.Env = append(dst.Env, env)
			}
		}
	}

	if src.WorkingDir != "" {
		dst.WorkingDir = src.WorkingDir
	}
	if src.StopSignal != "" {
		dst.StopSignal = src.StopSignal
	}

	if src.User != "" {
		dst.User = src.User
	}

	return nil
}

func getBaseOutputImage(spec *Spec, target, defaultBase string) string {
	baseRef := defaultBase
	if spec.Targets[target].Image != nil && spec.Targets[target].Image.Base != "" {
		baseRef = spec.Targets[target].Image.Base
	}
	return baseRef
}

func mergeSpecImage(spec *Spec, target string) *ImageConfig {
	var cfg ImageConfig

	if spec.Image != nil {
		cfg = *spec.Image
	}

	if i := spec.Targets[target].Image; i != nil {
		if i.Entrypoint != "" {
			cfg.Entrypoint = i.Entrypoint
		}

		if i.Cmd != "" {
			cfg.Cmd = i.Cmd
		}

		cfg.Env = append(cfg.Env, i.Env...)

		for k, v := range i.Volumes {
			cfg.Volumes[k] = v
		}

		for k, v := range i.Labels {
			cfg.Labels[k] = v
		}

		if i.WorkingDir != "" {
			cfg.WorkingDir = i.WorkingDir
		}

		if i.StopSignal != "" {
			cfg.StopSignal = i.StopSignal
		}

		if i.Base != "" {
			cfg.Base = i.Base
		}
	}

	return &cfg
}
