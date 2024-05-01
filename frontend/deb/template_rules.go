package deb

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
)

func Rules(spec *dalec.Spec, in llb.State, dir string) (llb.State, error) {
	buf := bytes.NewBuffer(nil)

	if dir == "" {
		dir = "debian"
	}

	if err := WriteRules(spec, buf); err != nil {
		return llb.Scratch(), err
	}

	return in.
			File(llb.Mkdir(dir, 0o755, llb.WithParents(true))).
			File(llb.Mkfile(filepath.Join(dir, "rules"), 0o770, buf.Bytes())),
		nil
}

func WriteRules(spec *dalec.Spec, w io.Writer) error {
	return rulesTmpl.Execute(w, &rulesWrapper{spec})
}

type rulesWrapper struct {
	*dalec.Spec
}

func (w *rulesWrapper) Envs() fmt.Stringer {
	b := &strings.Builder{}

	for k, v := range w.Spec.Build.Env {
		fmt.Fprintf(b, "export %s := %s\n", k, v)
	}

	if w.Spec.HasGomods() {
		fmt.Fprintf(b, "export %s := $(PWD)/%s\n", "GOMODCACHE", gomodsName)
	}

	return b
}

func (w *rulesWrapper) OverridePerms() fmt.Stringer {
	b := &strings.Builder{}

	if w.Artifacts.Directories == nil {
		return b
	}

	var fixPerms bool
	for _, cfg := range w.Artifacts.Directories.Config {
		if cfg.Mode != 0 {
			fixPerms = true
			break
		}
	}

	if !fixPerms {
		for _, cfg := range w.Artifacts.Directories.State {
			if cfg.Mode != 0 {
				fixPerms = true
				break
			}
		}
	}

	if fixPerms {
		b.WriteString("execute_after_dh_fixperms:\n")
		b.WriteString("\tdebian/dalec/fix_perms.sh\n\n")
	}

	return b
}

var (
	//go:embed templates/debian_rules.tmpl
	rulesTmplContent []byte

	rulesTmpl = template.Must(template.New("rules").Parse(string(rulesTmplContent)))
)
