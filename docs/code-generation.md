# Code Generation in Dalec

This project uses Go code generation to maintain certain functions that need to stay in sync with struct definitions.

## Generated Files

### spec_resolve_generated.go

The `Resolve` method on the `Spec` struct is automatically generated from the field definitions in the `Spec` struct. This ensures it stays up to date as new fields are added.

**Regenerate with:**
```bash
go generate ./spec.go
```

**Or generate all:**
```bash
go generate ./...
```

### source_generated.go

Source variant validation methods are generated from the `Source` struct definition.

**Regenerate with:**
```bash
go generate ./source.go
```

## Generator Commands

### cmd/gen-resolve

Generates the `Resolve` method for the `Spec` struct by:

1. Parsing the `Spec` struct definition from `spec.go`
2. Extracting all field names and types
3. Generating appropriate field copying code
4. Including special merge logic for target-specific fields

**Usage:**
```bash
go run ./cmd/gen-resolve <output-file>
```

### cmd/gen-source-variants

Generates validation methods for source variants by parsing the `Source` struct.

**Usage:**
```bash
go run ./cmd/gen-source-variants <output-file>
```

## Maintenance

When adding new fields to the `Spec` struct:

1. Add the field to the struct definition in `spec.go`
2. Run `go generate ./spec.go` to regenerate the `Resolve` method
3. If the field requires special target-specific merge logic, update the generator in `cmd/gen-resolve/main.go`

The generated `Resolve` method will automatically include the new field in the basic copying logic. Only fields that need special merge behavior (like merging global + target-specific values) need manual updates to the generator.