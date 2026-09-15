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

func BuildImageConfig(spec *Spec, targetKey string, img *DockerImageSpec) error {
	cfg := img.Config
	cfg.Labels = maps.Clone(cfg.Labels)
	// Base-image provenance does not describe the packaged component. In
	// particular, a base revision must not be paired with an upstream source.
	delete(cfg.Labels, ocispecs.AnnotationSource)
	delete(cfg.Labels, ocispecs.AnnotationRevision)
	delete(cfg.Labels, legacyImageSourceLabel)
	delete(cfg.Labels, legacyImageRevisionLabel)

	specCfg := MergeSpecImage(spec, targetKey)
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
	}

	switch u.Scheme {
	case "http", "https":
	case "ssh", "git":
		// Arbitrary SSH/Git servers need not have a web endpoint, and their
		// paths may be private filesystem paths rather than repository names.
		switch host {
		case "github.com", "gitlab.com", "bitbucket.org":
		default:
			return ""
		}
		if port := u.Port(); port != "" && !(u.Scheme == "ssh" && port == "22") && !(u.Scheme == "git" && port == "9418") {
			return ""
		}
		u.Scheme = "https"
		u.Host = host
	default:
		return ""
	}

	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), ".git")
	u.Path = "/" + strings.TrimPrefix(u.Path, "/")
	if u.Path == "/" || strings.ContainsAny(u.Path, "\\$") {
		return ""
	}
	for part := range strings.SplitSeq(strings.TrimPrefix(u.Path, "/"), "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, "~") {
			return ""
		}
	}

	// Constructing a new URL deliberately excludes credentials, query
	// parameters and BuildKit's ref/subdirectory fragment.
	return u.String()
}
