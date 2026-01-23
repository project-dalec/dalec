package main

import (
	"github.com/project-dalec/dalec/linters"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(linters.NewYamlJSONTagsAnalyzer())
}
