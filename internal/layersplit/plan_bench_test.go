package layersplit

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkBuildPlan plans a large root filesystem with more packages than
// the layer budget, so packages are folded into the misc group.
func BenchmarkBuildPlan(b *testing.B) {
	root := b.TempDir()
	in := Inputs{Packages: map[string]string{}, App: map[string]bool{"app": true}}
	if err := os.Symlink("usr/lib", filepath.Join(root, "lib")); err != nil {
		b.Fatal(err)
	}
	for i := range 400 {
		pkg := fmt.Sprintf("pkg%03d", i)
		in.Packages[pkg] = pkg
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
	in.Packages["app"] = "app"

	b.ResetTimer()
	for range b.N {
		if _, err := BuildPlan(root, in, Options{}); err != nil {
			b.Fatal(err)
		}
	}
}
