package main

import (
	"os"

	"github.com/Azure/dalec/cmd/dalec-redirectio/redirectio"
)

func main() {
	redirectio.Main(os.Args[1:])
}
