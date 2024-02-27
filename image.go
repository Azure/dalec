package dalec

import (
	"github.com/google/shlex"
	"github.com/moby/buildkit/exporter/containerimage/image"
	"github.com/pkg/errors"
)

// MergeImageConfig copies the fields from the source [ImageConfig] into the destination [image.Image].
// If a field is not set in the source, it is not modified in the destination.
// Envs from [ImageConfig] are merged into the destination [image.Image] and take precedence.
func MergeImageConfig(dst *image.Image, src *ImageConfig) error {
	if src == nil {
		return nil
	}

	if src.Entrypoint != "" {
		split, err := shlex.Split(src.Entrypoint)
		if err != nil {
			return errors.Wrap(err, "error splitting entrypoint into args")
		}
		dst.Config.Entrypoint = split
		// Reset cmd as this may be totally invalid now
		// This is the same behavior as the Dockerfile frontend
		dst.Config.Cmd = nil
	}
	if src.Cmd != "" {
		split, err := shlex.Split(src.Cmd)
		if err != nil {
			return errors.Wrap(err, "error splitting cmd into args")
		}
		dst.Config.Cmd = split
	}

	if len(src.Env) > 0 {
		// Env is append only
		// If the env var already exists, replace it
		envIdx := make(map[string]int)
		for i, env := range dst.Config.Env {
			envIdx[env] = i
		}

		for _, env := range src.Env {
			if idx, ok := envIdx[env]; ok {
				dst.Config.Env[idx] = env
			} else {
				dst.Config.Env = append(dst.Config.Env, env)
			}
		}
	}

	if src.WorkingDir != "" {
		dst.Config.WorkingDir = src.WorkingDir
	}
	if src.StopSignal != "" {
		dst.Config.StopSignal = src.StopSignal
	}

	if src.User != "" {
		dst.Config.User = src.User
	}

	return nil
}
