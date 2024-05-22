package rpm

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"gotest.tools/v3/assert"
)

func TestTemplateSources(t *testing.T) {
	t.Run("no sources", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{}}
		s, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if s.String() != "" {
			t.Fatalf("unexpected sources: %s", s.String())
		}
	})

	// Each source entry is prefixed by comments documenting how the source was generated
	// This gets the source documentation and turns it into the expected comment string
	srcDoc := func(name string, src dalec.Source) string {
		rdr, err := src.Doc(name)
		if err != nil {
			return ""
		}
		buf := bytes.NewBuffer(nil)
		scanner := bufio.NewScanner(rdr)
		for scanner.Scan() {
			buf.WriteString("# ")
			buf.WriteString(scanner.Text())
			buf.WriteString("\n")
		}
		return buf.String()
	}

	t.Run("one source file", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{},
					},
				},
			},
		}}

		out, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedDoc := srcDoc("src1", w.Spec.Sources["src1"])

		s := out.String()
		if !strings.HasPrefix(s, expectedDoc) {
			t.Errorf("Expected doc:\n%q\n\n, got:\n%q\n", expectedDoc, s)
		}

		// File sources are not (currently) compressed, so the source is the file itself
		expected := "Source0: src1\n"
		actual := s[len(expectedDoc):] // trim off the doc from the output
		if actual != expected {
			t.Fatalf("unexpected sources: expected %q, got: %q", expected, actual)
		}
	})

	t.Run("one source dir", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
				},
			},
		}}

		out, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		expectedDoc := srcDoc("src1", w.Spec.Sources["src1"])

		s := out.String()
		if !strings.HasPrefix(s, expectedDoc) {
			t.Errorf("Expected doc:\n%q\n\n, got:\n%q\n", expectedDoc, s)
		}

		expected := "Source0: src1.tar.gz\n"
		actual := s[len(expectedDoc):] // trim off the doc from the output
		if actual != expected {
			t.Fatalf("unexpected sources: expected %q, got: %q", expected, actual)
		}

		t.Run("with gomod", func(t *testing.T) {
			src := w.Spec.Sources["src1"]
			src.Generate = []*dalec.SourceGenerator{
				{Gomod: &dalec.GeneratorGomod{}},
			}
			w.Spec.Sources["src1"] = src

			out2, err := w.Sources()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			s2 := out2.String()
			if !strings.HasPrefix(s2, s) {
				t.Fatalf("expected output to start with %q, got %q", s, out2.String())
			}

			s2 = strings.TrimPrefix(out2.String(), s)
			expected := "Source1: " + gomodsName + ".tar.gz\n"
			if s2 != expected {
				t.Fatalf("unexpected sources: expected %q, got: %q", expected, s2)
			}
		})
	})

	t.Run("multiple sources", func(t *testing.T) {
		w := &specWrapper{Spec: &dalec.Spec{
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{},
					},
				},
				"src4": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
					Generate: []*dalec.SourceGenerator{
						{Gomod: &dalec.GeneratorGomod{}},
					},
				},
				"src5": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{},
					},
					Generate: []*dalec.SourceGenerator{
						{Gomod: &dalec.GeneratorGomod{}},
					},
				},
			},
		}}

		out, err := w.Sources()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		s := out.String()

		// Note: order (in the produced output) should be deterministic here regardless of map ordering (especially since maps are randomized).
		ordered := dalec.SortMapKeys(w.Spec.Sources)
		for i, name := range ordered {
			src := w.Spec.Sources[name]
			expectedDoc := srcDoc(name, src)

			if !strings.HasPrefix(s, expectedDoc) {
				t.Errorf("%s: Expected doc:\n%q\n\n, got:\n%q\n", name, expectedDoc, s)
			}

			s = s[len(expectedDoc):] // trim off the doc from the output
			suffix := "\n"
			if dalec.SourceIsDir(src) {
				suffix = ".tar.gz\n"
			}

			expected := "Source" + strconv.Itoa(i) + ": " + name + suffix
			if s[:len(expected)] != expected {
				t.Fatalf("%s: unexpected sources: expected %q, got: %q", name, expected, s[:len(expected)])
			}

			// Trim off the rest of the bits we've checked for the next loop iteration
			s = s[len(expected):]
		}

		// Now we should have one more entry for gomods.
		// Note there are 2 gomod sources but they should be combined into one entry.

		expected := "Source5: " + gomodsName + ".tar.gz\n"
		if s != expected {
			t.Fatalf("gomod: unexpected sources: expected %q, got: %q", expected, s)
		}
		s = s[len(expected):]
		if s != "" {
			t.Fatalf("unexpected trailing sources: %q", s)
		}
	})
}

func TestTemplate_Artifacts(t *testing.T) {

	w := &specWrapper{Spec: &dalec.Spec{
		Artifacts: dalec.Artifacts{
			SystemdUnits: map[string]dalec.SystemdUnitConfig{
				"test.service": {},
			},
		},
	}}

	got := w.PostUn().String()
	want := `%postun
%systemd_postun test.service
`
	assert.Equal(t, want, got)
}
