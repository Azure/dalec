package linters

import (
	"go/ast"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var YamlJSONTagsMatch = &analysis.Analyzer{
	Name: "yaml_json_names_match",
	Doc:  "check that struct tags for json and yaml use the same name",
	Run:  structTagLinter{}.Run,
}

type structTagLinter struct{}

func (l structTagLinter) Run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.TypeSpec:
				if structType, ok := x.Type.(*ast.StructType); ok {
					l.checkStructTags(structType, pass)
				}
			}
			return true
		})
	}
	return nil, nil
}

func (structTagLinter) checkStructTags(structType *ast.StructType, pass *analysis.Pass) {
	for _, field := range structType.Fields.List {
		if field.Tag != nil {
			tag := field.Tag.Value

			v := getYamlJSONNames(tag)

			var checkTags bool
			if v[0] != "" || v[1] != "" {
				checkTags = true
			}

			if checkTags && v[0] != v[1] {
				pass.Reportf(field.Pos(), "mismatch in struct tags: json=%s, yaml=%s", v[0], v[1])
			}
		}
	}
}

func getYamlJSONNames(tag string) [2]string {
	const (
		yaml = "yaml"
		json = "json"
	)

	tag = strings.Trim(tag, "`")

	var out [2]string
	for _, tag := range strings.Fields(tag) {
		key, tag, _ := strings.Cut(tag, ":")

		value := strings.Trim(tag, `"`)

		switch key {
		case json:
			t, _, _ := strings.Cut(value, ",")
			out[0] = t
		case yaml:
			t, _, _ := strings.Cut(value, ",")
			out[1] = t
		}

		if out[0] != "" && out[1] != "" {
			break
		}
	}

	return out
}
