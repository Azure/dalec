# Spec.Resolve() Method - Simple and Clean

This document shows the new `spec.Resolve(targetKey)` approach that was requested as an alternative to the `ResolvedSpec` approach.

## The Problem

Before, we had to pass `targetKey` everywhere:

```go
func processPackage(spec *dalec.Spec, targetKey string) {
    // Each call does expensive target lookup + merge
    deps := spec.GetRuntimeDeps(targetKey)     
    buildDeps := spec.GetBuildDeps(targetKey)  
    signer, ok := spec.GetSigner(targetKey)    
    
    // targetKey must be threaded through every function
    doSomethingWithDeps(spec, targetKey, deps, buildDeps)
}
```

## The Solution

Now with `spec.Resolve(targetKey)`, we get a clean, simple solution:

```go
func processPackageResolved(spec *dalec.Spec, targetKey string) {
    // Single resolution step - returns a regular *Spec with all target config merged
    resolved := spec.Resolve(targetKey)
    
    // Direct access - no targetKey needed, all existing methods work
    deps := resolved.GetRuntimeDeps(targetKey)        
    buildDeps := resolved.GetBuildDeps(targetKey)     
    signer, ok := resolved.GetSigner(targetKey)       
    
    // Clean API - resolved spec works everywhere a spec works
    doSomethingWithDepsResolved(resolved, deps, buildDeps)
}
```

## Key Benefits

- **Simple**: Just one method `Resolve(targetKey)` that returns `*Spec`
- **No new types**: Uses existing `Spec` type, all existing methods work
- **Performance**: Eliminates repeated target lookups and dependency merging
- **Clean API**: No need to pass `targetKey` parameter everywhere
- **Backward compatible**: All existing code continues to work

## Example Usage

```go
// Load spec
spec, err := dalec.LoadSpec(ctx, client)
if err != nil {
    return err
}

// Resolve for specific target - one time operation
targetKey := "ubuntu-22.04"
resolved := spec.Resolve(targetKey)

// Now use resolved spec - all methods work, but with merged config
deps := resolved.GetRuntimeDeps(targetKey)
artifacts := resolved.GetArtifacts(targetKey)
image := resolved.Image  // Already contains target-specific settings

// Pass resolved spec to functions - they work exactly like regular specs
result := buildWithSpec(ctx, resolved, targetKey)
```

This approach is much simpler and cleaner than creating a separate `ResolvedSpec` type.