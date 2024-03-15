package cmd

import (
	_ "embed"
	"os"
	"path/filepath"

	"github.com/Azure/dalec/frontend/debug"
	"github.com/Azure/dalec/frontend/mariner2"
	"github.com/moby/buildkit/client/llb"
)

const (
	DalecCurrentFrontendKey = "dalec-current-frontend"
)

func LoadFrontend() {
	mariner2.RegisterHandlers()
	debug.RegisterHandlers()
}

func CurrentFrontend() (*llb.State, error) {
	filename := filepath.Base(os.Args[0])
	base := llb.Local(DalecCurrentFrontendKey, llb.IncludePatterns([]string{filename}))

	st := llb.Scratch().File(llb.Copy(base, filename, "/dalec-redirectio"))
	return &st, nil
}
