package dalec

import (
	"github.com/goccy/go-yaml"
	"github.com/pkg/errors"
)

// gomodPatchExtensionKey is the extension field key used to store gomod patch metadata
// in the spec. This allows patches generated during build to be persisted in the spec.
const gomodPatchExtensionKey = "x-dalec-gomod-patches"

// gomodPatchExtensionEntry represents a single gomod patch stored in spec extensions.
type gomodPatchExtensionEntry struct {
	Source   string `yaml:"source" json:"source"`
	FileName string `yaml:"filename" json:"filename"`
	Strip    int    `yaml:"strip" json:"strip"`
	Contents string `yaml:"contents" json:"contents"`
}

// registerGomodPatch adds a gomod patch to the spec's internal tracking map.
func (s *Spec) registerGomodPatch(patch *GomodPatch) {
	if patch == nil {
		return
	}

	if s.gomodPatches == nil {
		s.gomodPatches = make(map[string][]*GomodPatch)
	}

	s.gomodPatches[patch.SourceName] = append(s.gomodPatches[patch.SourceName], patch)
	s.gomodPatchesGenerated = true
}

// appendGomodPatchExtensionEntry serializes a gomod patch into the spec's extension data
// for persistence across spec marshal/unmarshal cycles.
func (s *Spec) appendGomodPatchExtensionEntry(patch *GomodPatch) error {
	if patch == nil || len(patch.Contents) == 0 {
		return nil
	}

	entries, err := s.gomodPatchExtensionEntries()
	if err != nil {
		return err
	}

	entries = append(entries, gomodPatchExtensionEntry{
		Source:   patch.SourceName,
		FileName: patch.FileName,
		Strip:    patch.Strip,
		Contents: string(patch.Contents),
	})

	return s.WithExtension(gomodPatchExtensionKey, entries)
}

// gomodPatchExtensionEntries retrieves all gomod patch entries from the spec's extensions.
func (s *Spec) gomodPatchExtensionEntries() ([]gomodPatchExtensionEntry, error) {
	if s.extensions == nil {
		return nil, nil
	}

	dt, ok := s.extensions[gomodPatchExtensionKey]
	if !ok || len(dt) == 0 {
		return nil, nil
	}

	var entries []gomodPatchExtensionEntry
	if err := yaml.Unmarshal(dt, &entries); err != nil {
		return nil, errors.Wrap(err, "failed to unmarshal gomod patch extension")
	}

	return entries, nil
}
