package layersplit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkBuildPlan plans a large root filesystem where each package depends
// on the previous one, so the base set is a long dependency chain.
func BenchmarkBuildPlan(b *testing.B) {
	root := b.TempDir()
	in := Inputs{
		Packages: map[string]bool{"app": true},
		App:      map[string]bool{"app": true},
		Base:     map[string]bool{"pkg000": true},
	}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		b.Fatal(err)
	}
	for i := range 400 {
		pkg := fmt.Sprintf("pkg%03d", i)
		in.Packages[pkg] = true
		if i > 0 {
			in.Deps.addRequire(fmt.Sprintf("pkg%03d", i-1), []string{pkg})
		}
		dir := filepath.Join(root, "usr", "lib", pkg)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		for j := range 50 {
			name := fmt.Sprintf("f%02d", j)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
				b.Fatal(err)
			}
			// Owned through the /lib symlink, like many real packages.
			in.Files = append(in.Files, OwnedFile{Package: pkg, Path: "/lib/" + pkg + "/" + name})
		}
	}

	b.ResetTimer()
	for range b.N {
		if _, err := BuildPlan(root, in); err != nil {
			b.Fatal(err)
		}
	}
}
