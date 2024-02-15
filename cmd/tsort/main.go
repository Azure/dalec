package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/Azure/dalec"
)

func main() {
	if len(os.Args) < 3 {
		panic("need args: filename, target")
	}
	filename := os.Args[1]
	tgt := os.Args[2]
	b, err := os.ReadFile(filename)
	if err != nil {
		panic(err)
	}
	buf := bytes.NewBuffer(b)

	allSpecs, err := dalec.LoadSpecs(buf)
	if err != nil {
		panic(err)
	}

	sorted, err := dalec.TopSort(allSpecs, tgt)
	if err != nil {
		panic(err)
	}

	for _, spec := range sorted {
		fmt.Println(spec.Name)
	}
}
