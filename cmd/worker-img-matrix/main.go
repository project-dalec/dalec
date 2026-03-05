//go:generate go run $GOFILE ../../.github/workflows/worker-images/matrix.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/plugin"
	"github.com/project-dalec/dalec"
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
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			panic(fmt.Errorf("failed to create output directory: %w", err))
		}
		outF, err = os.Create(outPath)
		if err != nil {
			panic(err)
		}
		defer outF.Close()
	}

	filter := func(r *plugins.Registration) bool {
		return r.Type != plugins.TypeRouteProvider
	}

	ctx := context.Background()
	spec := &dalec.Spec{}
	set := plugin.NewPluginSet()

	var out []Info
	for _, reg := range plugins.Graph(filter) {
		cfg := plugin.NewContext(ctx, set, nil)

		p := reg.Init(cfg)
		if err := set.Add(p); err != nil {
			panic(fmt.Errorf("failed to add plugin %s: %w", reg.ID, err))
		}

		v, err := p.Instance()
		if err != nil {
			panic(fmt.Errorf("failed to get instance for plugin %s: %w", reg.ID, err))
		}

		provider := v.(plugins.RouteProvider)
		routes, err := provider.Routes(ctx, spec)
		if err != nil {
			panic(fmt.Errorf("failed to get routes for plugin %s: %w", reg.ID, err))
		}
		for _, route := range routes {
			_, suffix, ok := strings.Cut(route.FullPath, "/")
			if ok && suffix == "worker" {
				out = append(out, Info{Target: route.FullPath})
			}
		}
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
