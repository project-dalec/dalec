package dalec

import (
	"github.com/goccy/go-yaml"
	"github.com/pkg/errors"
)

const gomodPatchExtensionKey = "x-dalec-gomod-patches"

type gomodPatchExtensionEntry struct {
	Source   string `yaml:"source" json:"source"`
	FileName string `yaml:"filename" json:"filename"`
	Strip    int    `yaml:"strip" json:"strip"`
	Contents string `yaml:"contents" json:"contents"`
}

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
