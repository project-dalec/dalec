package dalec

func BuildImageConfig(spec *Spec, targetKey string, img *DockerImageSpec) error {
	cfg := img.Config
	if err := MergeImageConfig(&cfg, MergeSpecImage(spec, targetKey)); err != nil {
		return err
	}

	img.Config = cfg
	return nil
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

		if len(i.Volumes) > 0 {
			if cfg.Volumes == nil {
				cfg.Volumes = make(map[string]struct{}, len(i.Volumes))
			}
			for k, v := range i.Volumes {
				cfg.Volumes[k] = v
			}
		}

		if len(i.Labels) > 0 {
			if cfg.Labels == nil {
				cfg.Labels = make(map[string]string, len(i.Labels))
			}
			for k, v := range i.Labels {
				cfg.Labels[k] = v
			}
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

		if i.User != "" {
			cfg.User = i.User
		}
	}

	return &cfg
}
