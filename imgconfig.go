package dalec

func BuildImageConfig(spec *Spec, targetKey string, img *DockerImageSpec) error {
	cfg := img.Config
	if err := MergeImageConfig(&cfg, MergeSpecImage(spec, targetKey)); err != nil {
		return err
	}

	img.Config = cfg
	return nil
}

func GetBaseOutputImage(spec *Spec, target string) string {
	i := spec.Targets[target].Image
	if i == nil || i.Base == "" {
		return ""
	}
	return i.Base
}

func MergeSpecImage(spec *Spec, targetKey string) *ImageConfig {
	var cfg ImageConfig

	if spec.Image != nil {
		cfg = *spec.Image
	}

	if i := spec.Targets[targetKey].Image; i != nil {
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
