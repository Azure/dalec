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

	specs, err := dalec.LoadSpec2(buf)
	if err != nil {
		panic(err)
	}

	allBuild, err := dalec.GetBuildTargets(specs, tgt)
	if err != nil {
		panic(err)
	}

	for _, spec := range allBuild {
		fmt.Println(spec.Name)
	}
}
