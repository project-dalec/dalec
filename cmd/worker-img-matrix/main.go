//go:generate go run $GOFILE ../../.github/workflows/worker-images/matrix.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/project-dalec/dalec/internal/plugins"
	_ "github.com/project-dalec/dalec/targets/plugin"
)

type Matrix struct {
	Include []Info `json:"include" yaml:"include"`
}

type Info struct {
	Target string `json:"target" yaml:"target"`
}

func main() {
	flag.Parse()
	if flag.NArg() > 1 {
		fmt.Println("Usage: worker-img-matrix [file]")
	}

	outF := os.Stdout
	if outPath := flag.Arg(0); outPath != "" {
		var err error
		if err := os.MkdirAll(path.Dir(outPath), 0755); err != nil {
			panic(fmt.Errorf("failed to create output directory: %w", err))
		}
		outF, err = os.Create(outPath)
		if err != nil {
			panic(err)
		}
		defer outF.Close()
	}

	filter := func(r *plugins.Registration) bool {
		return r.Type != plugins.TypeBuildTarget
	}

	var out []Info
	for _, r := range plugins.Graph(filter) {
		var i Info
		i.Target = path.Join(r.ID, "worker")
		out = append(out, i)
	}

	m := Matrix{
		Include: out,
	}
	dt, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	if _, err := fmt.Fprintln(outF, string(dt)); err != nil {
		panic(fmt.Errorf("failed to write output: %w", err))
	}
}
