package main

import (
	"github.com/Azure/dalec/linters"
	"golang.org/x/tools/go/analysis/singlechecker"
)

func main() {
	singlechecker.Main(linters.YamlJSONTagsMatch)
}
