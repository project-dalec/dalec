package deb

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"gotest.tools/v3/assert"
)

func TestGenerateReadmeDebian(t *testing.T) {
	t.Run("no sources", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:        "test-package",
			Description: "Test package description",
		}
		
		result := generateReadmeDebian(spec)
		
		// Should return nil when there are no sources
		assert.Assert(t, result == nil)
	})

	t.Run("single inline source", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:        "test-package",
			Description: "Test package description",
			Sources: map[string]dalec.Source{
				"config.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "test config content",
						},
					},
				},
			},
		}
		
		result := generateReadmeDebian(spec)
		content := string(result)
		
		assert.Assert(t, strings.Contains(content, "test-package for Debian"))
		assert.Assert(t, strings.Contains(content, "Test package description"))
		assert.Assert(t, strings.Contains(content, "Source Provenance"))
		assert.Assert(t, strings.Contains(content, "Source: config.txt"))
		assert.Assert(t, strings.Contains(content, "Generated from an inline source"))
	})

	t.Run("multiple sources", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:        "multi-source-package",
			Description: "Package with multiple sources",
			Sources: map[string]dalec.Source{
				"config1.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "config1 content",
						},
					},
				},
				"config2.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "config2 content",
						},
					},
				},
			},
		}
		
		result := generateReadmeDebian(spec)
		content := string(result)
		
		assert.Assert(t, strings.Contains(content, "multi-source-package for Debian"))
		assert.Assert(t, strings.Contains(content, "Package with multiple sources"))
		assert.Assert(t, strings.Contains(content, "Source Provenance"))
		assert.Assert(t, strings.Contains(content, "Source: config1.txt"))
		assert.Assert(t, strings.Contains(content, "Source: config2.txt"))
		
		// Verify sources are ordered (sorted by name)
		lines := strings.Split(content, "\n")
		config1Index := -1
		config2Index := -1
		for i, line := range lines {
			if strings.Contains(line, "Source: config1.txt") {
				config1Index = i
			}
			if strings.Contains(line, "Source: config2.txt") {
				config2Index = i
			}
		}
		assert.Assert(t, config1Index >= 0 && config2Index >= 0)
		assert.Assert(t, config1Index < config2Index, "Sources should be sorted alphabetically")
	})

	t.Run("source documentation is indented", func(t *testing.T) {
		spec := &dalec.Spec{
			Name: "test-package",
			Sources: map[string]dalec.Source{
				"test-file": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "test content",
						},
					},
				},
			},
		}
		
		result := generateReadmeDebian(spec)
		
		// Parse the result to verify indentation
		scanner := bufio.NewScanner(bytes.NewReader(result))
		foundSourceLine := false
		foundIndentedDoc := false
		
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Source: test-file") {
				foundSourceLine = true
				continue
			}
			if foundSourceLine && strings.HasPrefix(line, "  ") && strings.Contains(line, "Generated from an inline source") {
				foundIndentedDoc = true
				break
			}
		}
		
		assert.Assert(t, foundSourceLine, "Should find source line")
		assert.Assert(t, foundIndentedDoc, "Should find indented documentation")
	})
}