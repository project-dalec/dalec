package dalec

import (
	"maps"
	"net/netip"
	"net/url"
	"strings"

	"github.com/moby/buildkit/util/gitutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	legacyImageSourceLabel   = "org.label-schema.vcs-url"
	legacyImageRevisionLabel = "org.label-schema.vcs-ref"
)

type imageConfigOptions struct {
	inferSourceLabel bool
}

type ImageConfigOpt func(*imageConfigOptions)

// WithImageSourceLabel enables upstream source inference and replaces inherited
// source/revision labels with the component's provenance.
func WithImageSourceLabel() ImageConfigOpt {
	return func(opts *imageConfigOptions) {
		opts.inferSourceLabel = true
	}
}

func BuildImageConfig(spec *Spec, targetKey string, img *DockerImageSpec, opts ...ImageConfigOpt) error {
	var options imageConfigOptions
	for _, opt := range opts {
		opt(&options)
	}

	cfg := img.Config
	cfg.Labels = maps.Clone(cfg.Labels)
	specCfg := MergeSpecImage(spec, targetKey)
	if options.inferSourceLabel {
		// A base revision must not be paired with the component's source.
		delete(cfg.Labels, ocispecs.AnnotationSource)
		delete(cfg.Labels, ocispecs.AnnotationRevision)
		delete(cfg.Labels, legacyImageSourceLabel)
		delete(cfg.Labels, legacyImageRevisionLabel)

		_, sourceSet := specCfg.Labels[ocispecs.AnnotationSource]
		_, legacySourceSet := specCfg.Labels[legacyImageSourceLabel]
		if !sourceSet && !legacySourceSet {
			if source := imageSourceURL(spec); source != "" {
				if cfg.Labels == nil {
					cfg.Labels = make(map[string]string)
				}
				cfg.Labels[ocispecs.AnnotationSource] = source
			}
		}
	}

	if err := MergeImageConfig(&cfg, specCfg); err != nil {
		return err
	}

	img.Config = cfg
	return nil
}

func MergeSpecImage(spec *Spec, targetKey string) *ImageConfig {
	var cfg ImageConfig

	if spec.Image != nil {
		cfg = *spec.Image
		cfg.Labels = maps.Clone(cfg.Labels)
	}

	if i := spec.Targets[targetKey].Image; i != nil {
		if i.MinimizationProfile != "" {
			cfg.MinimizationProfile = i.MinimizationProfile
		}

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

// imageSourceURL only considers top-level Git sources, not repositories used by
// generators, build images, or nested build contexts.
func imageSourceURL(spec *Spec) string {
	var git *SourceGit
	for _, src := range spec.Sources {
		if src.Git == nil {
			continue
		}
		if git != nil {
			return ""
		}
		git = src.Git
	}
	if git == nil {
		return ""
	}
	return gitSourceWebURL(git.URL)
}

func gitSourceWebURL(raw string) string {
	remote, err := gitutil.ParseURL(raw)
	if err != nil {
		return ""
	}
	if remote.Scheme != "http" && remote.Scheme != "https" {
		return ""
	}

	u := url.URL{Scheme: remote.Scheme, Host: remote.Host, Path: remote.Path}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return ""
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		ip = ip.Unmap()
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			return ""
		}
	} else if isNumericIPv4Host(host) {
		// Consumers may resolve shortened, integer, octal, or hexadecimal
		// IPv4 spellings that ParseAddr rejects. Do not infer from these.
		return ""
	}

	u.Path = strings.TrimSuffix(u.Path, "/")
	u.Path = "/" + strings.TrimPrefix(u.Path, "/")
	if u.Path == "/" || strings.ContainsAny(u.Path, "\\$") {
		return ""
	}
	for part := range strings.SplitSeq(strings.TrimPrefix(u.Path, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return ""
		}
	}

	// Constructing a new URL deliberately excludes credentials, query
	// parameters and BuildKit's ref/subdirectory fragment.
	return u.String()
}

func isNumericIPv4Host(host string) bool {
	for part := range strings.SplitSeq(host, ".") {
		if part == "" {
			return false
		}
		digits := "0123456789"
		if strings.HasPrefix(part, "0x") {
			part = strings.TrimPrefix(part, "0x")
			digits += "abcdef"
		}
		if strings.Trim(part, digits) != "" {
			return false
		}
	}
	return true
}
