package deb

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"text/template"
	"time"

	"github.com/Azure/dalec"
	"github.com/moby/buildkit/client/llb"
)

func Changelog(spec *dalec.Spec, in llb.State, target, dir string) (llb.State, error) {
	buf := bytes.NewBuffer(nil)

	if dir == "" {
		dir = "debian"
	}

	if err := WriteChangelog(spec, target, buf); err != nil {
		return llb.Scratch(), err
	}

	return in.
			File(llb.Mkdir(dir, 0o755, llb.WithParents(true))).
			File(llb.Mkfile(filepath.Join(dir, "changelog"), 0o770, buf.Bytes())),
		nil
}

func WriteChangelog(spec *dalec.Spec, target string, w io.Writer) error {
	return changelogTmpl.Execute(w, &changelogWrapper{spec, target})
}

type changelogWrapper struct {
	*dalec.Spec
	Target string
}

var dummyChangelogEntry = dalec.ChangelogEntry{
	Date:   time.Unix(0, 0),
	Author: "Dalec Dummy Changelog <>",
	Changes: []string{
		"Dummy changelog entry",
	},
}

func (w *changelogWrapper) Change() string {
	entries := slices.Clone(w.Spec.Changelog)

	// Get the most recent changelog entry only
	// TODO: this could be better... but it's not worth the effort right now
	// Where "better" means list all changelog entries
	// Where "not worth the effort" means the changelog entry type needs updating to include extra details like the version a change was made in
	// Really the changelog model in debian does not line up well with the dalec model because it also wants distribution information as well.

	// Sort so newest entry (by change date) is on top.
	slices.SortFunc(entries, func(i, j dalec.ChangelogEntry) int {
		if i.Date.Equal(j.Date) {
			return 0
		}
		if i.Date.Before(j.Date) {
			return 1
		}
		return -1
	})

	var entry dalec.ChangelogEntry
	if len(entries) == 0 {
		entry = dummyChangelogEntry
	} else {
		entry = entries[0]
	}

	buf := &strings.Builder{}

	for _, change := range entry.Changes {
		fmt.Fprintln(buf, "  *", change)
	}

	fmt.Fprintf(buf, " -- %s  %s", entry.Author, entry.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	return buf.String()
}

var changelogTmpl = template.Must(template.New("dummy-changelog-entry").Parse(strings.TrimSpace(`
{{.Name}} ({{.Version}}-{{.Revision}}) {{.Target}}; urgency=low

{{.Change}}
`)))
