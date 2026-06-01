package dalec

import (
	"fmt"
	"maps"
	"strings"
)

type SystemdConfiguration struct {
	// Units is a list of systemd units to include in the package.
	Units map[string]SystemdUnitConfig `yaml:"units,omitempty" json:"units,omitempty"`
	// Dropins is a list of systemd drop in files that should be included in the package
	Dropins map[string]SystemdDropinConfig `yaml:"dropins,omitempty" json:"dropins,omitempty"`
}

type SystemdUnitConfig struct {
	// Name is the name systemd unit should be copied under.
	// Nested paths are not supported. It is the user's responsibility
	// to name the service with the appropriate extension, i.e. .service, .timer, etc.
	Name string `yaml:"name" json:"name"`

	// Enable is used to enable the systemd unit on install
	// This determines what will be written to a systemd preset file
	Enable bool `yaml:"enable,omitempty" json:"enable,omitempty"`

	// Start can be optionally used to start the systemd unit on install
	// This determines what will be written to a systemd preset file
	// Note that depending on distribution there may be different
	// expectations as to if the package should be responsible for this
	Start bool `yaml:"start,omitempty" json:"start,omitempty"`
}

func (s SystemdUnitConfig) Artifact() *ArtifactConfig {
	return &ArtifactConfig{
		SubPath: "",
		Name:    s.Name,
	}
}

func (s SystemdUnitConfig) ResolveName(name string) string {
	return s.Artifact().ResolveName(name)
}

// Splitname resolves a unit name and then gives its unit base name.
// E.g. for  `foo.socket` this would be `foo` and `socket`.
func (s SystemdUnitConfig) SplitName(name string) (string, string) {
	name = s.ResolveName(name)
	base, other, _ := strings.Cut(name, ".")
	return base, other
}

type SystemdDropinConfig struct {
	// Name is file or dir name to use for the artifact in the package.
	// If empty, the file or dir name from the produced artifact will be used.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
	// Unit is the name of the systemd unit that the dropin files should be copied under.
	Unit string `yaml:"unit" json:"unit"` // the unit named foo.service maps to the directory foo.service.d
}

func (s SystemdDropinConfig) Artifact() *ArtifactConfig {
	return &ArtifactConfig{
		SubPath: fmt.Sprintf("%s.d", s.Unit),
		Name:    s.Name,
	}
}

func (s *SystemdConfiguration) GetUnits() map[string]SystemdUnitConfig {
	if s == nil {
		return nil
	}
	return maps.Clone(s.Units)
}

func (s *SystemdConfiguration) GetDropins() map[string]SystemdDropinConfig {
	if s == nil {
		return nil
	}
	return maps.Clone(s.Dropins)
}
