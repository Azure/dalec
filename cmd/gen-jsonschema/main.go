package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/project-dalec/dalec"
	"github.com/atombender/go-jsonschema/pkg/schemas"
	"github.com/invopop/jsonschema"
)

func main() {
	var r jsonschema.Reflector
	if err := r.AddGoComments("github.com/project-dalec/dalec", "./"); err != nil {
		panic(err)
	}

	schema := r.Reflect(&dalec.Spec{})
	if schema.PatternProperties == nil {
		schema.PatternProperties = make(map[string]*jsonschema.Schema)
	}
	schema.PatternProperties["^x-"] = &jsonschema.Schema{}

	dt, err := json.Marshal(schema)
	if err != nil {
		panic(err)
	}

	// The above library used is good for generating the schema from the go types,
	// but it doesn't give us everything we need to make manipulations to the schema
	// since the data is not represented in go correctly.
	// So we convert the schema to JSON and then back to another go type that is more
	// suitable for manipulation.
	//
	// Specifically, the problem with the above library is that the `Type` parameter
	// is a string, but it should be a []string.
	// Both are apparently(?) valid jsonschema, but the latter is what we need to
	// fixup the schema to allow null values and other things.

	schema2, err := schemas.FromJSONReader(bytes.NewReader(dt))
	if err != nil {
		panic(err)
	}

	const (
		specKey  = "Spec"
		argsKey  = "args"
		buildKey = "build"
	)
	spec := schema2.Definitions[specKey]
	spec.Properties[argsKey].AdditionalProperties.Type = append(spec.Properties[argsKey].AdditionalProperties.Type, "integer")

	build := spec.Properties[buildKey]
	buildType := strings.TrimPrefix(build.Ref, "#/$defs/")
	build = schema2.Definitions[buildType]
	build.Properties["env"].Type = append(build.Properties["env"].Type, "integer")

	for _, v := range schema2.Definitions {
		setObjectAllowNull(v)
	}

	dt, err = json.MarshalIndent(schema2, "", "\t")
	if err != nil {
		panic(err)
	}

	if len(os.Args) > 1 {
		if err := os.MkdirAll(filepath.Dir(os.Args[1]), 0755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(os.Args[1], dt, 0644); err != nil {
			panic(err)
		}
		return
	}
	fmt.Println(string(dt))
}

func setObjectAllowNull(t *schemas.Type) {
	if t == nil {
		panic("nil type")
	}
	if t.AdditionalProperties != nil {
		setObjectAllowNull(t.AdditionalProperties)
	}

	for k, v := range t.Properties {
		if slices.Contains(t.Required, k) {
			continue
		}
		setObjectAllowNull(v)
		t.Properties[k] = v
	}

	ok := slices.ContainsFunc(t.Type, func(v string) bool {
		if v == "null" {
			// Already allows null, nothing to do.
			return false
		}
		return v == "object" || v == "string"
	})

	if !ok {
		return
	}

	t.Type = append(t.Type, "null")
}
