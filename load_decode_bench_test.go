package dalec

import (
	"fmt"
	"testing"

	"github.com/goccy/go-yaml"
)

func BenchmarkDecodeSpec(b *testing.B) {
	b.Run("simple", func(b *testing.B) {
		benchmarkDecodePaths(b, []byte(depsSpecHeader+`
sources:
  hello:
    inline:
      file:
        contents: hello
build:
  steps:
    - command: echo hello
`))
	})

	for _, count := range []int{1, 32} {
		for _, padding := range []int{1024, 64 * 1024} {
			b.Run(fmt.Sprintf("nodes=%d/padding=%d", count, padding), func(b *testing.B) {
				benchmarkDecodePaths(b, syntheticDecodeSpec(count, padding))
			})
		}
	}

	b.Run("nodes=32/padding=524288", func(b *testing.B) {
		benchmarkDecodePaths(b, syntheticDecodeSpec(32, 512*1024))
	})
}

func benchmarkDecodePaths(b *testing.B, dt []byte) {
	b.Helper()

	for _, path := range []string{"Unmarshal", "LoadSpec", "LoadSpecWithSourceMap"} {
		b.Run(path, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(dt)))

			for b.Loop() {
				var err error
				switch path {
				case "Unmarshal":
					var spec Spec
					err = yaml.Unmarshal(dt, &spec)
				case "LoadSpec":
					_, err = LoadSpec(dt)
				case "LoadSpecWithSourceMap":
					_, err = LoadSpecWithSourceMap("benchmark.yml", dt)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
