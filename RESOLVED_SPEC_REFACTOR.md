# ResolvedSpec Refactor - Eliminating targetKey Parameter Passing

## Overview

This refactor addresses issue #486 by introducing the `ResolvedSpec` approach to eliminate the need for passing `targetKey` parameters throughout the codebase. Instead of repeatedly looking up and merging target-specific configuration, we now resolve the complete configuration once and provide direct access methods.

## Problem Addressed

### Before: Repeated Target Lookups
```go
func processPackage(spec *dalec.Spec, targetKey string) {
    // Each call performs target lookup and merge
    runtimeDeps := spec.GetRuntimeDeps(targetKey)   // O(n) lookup + merge
    buildDeps := spec.GetBuildDeps(targetKey)       // O(n) lookup + merge  
    testDeps := spec.GetTestDeps(targetKey)         // O(n) lookup + merge
    signer, ok := spec.GetSigner(targetKey)         // O(n) lookup + merge
    
    // targetKey must be passed to every function
    doSomethingWithDeps(spec, targetKey, runtimeDeps)
}
```

### After: Single Resolution
```go
func processPackageResolved(resolved *dalec.ResolvedSpec) {
    // Direct access to pre-resolved configuration  
    runtimeDeps := resolved.GetRuntimeDeps()        // O(1) direct access
    buildDeps := resolved.GetBuildDeps()            // O(1) direct access
    testDeps := resolved.GetTestDeps()              // O(1) direct access  
    signer, ok := resolved.GetSigner()              // O(1) direct access
    
    // No targetKey needed
    doSomethingWithDepsResolved(resolved, runtimeDeps)
}

// Usage:
resolved := spec.ResolveForTarget(targetKey)  // One-time resolution
processPackageResolved(resolved)
```

## Key Components

### 1. ResolvedSpec Struct
- Contains fully merged configuration for a specific target
- Eliminates need for runtime target lookups
- Provides clean API without targetKey parameters

### 2. Resolution Process
- `spec.ResolveForTarget(targetKey)` creates a ResolvedSpec
- Merges global spec with target-specific overrides
- Handles all configuration types: dependencies, artifacts, images, etc.

### 3. New Function Variants
- `BuildWithResolvedSpec()` - Uses ResolvedPlatformBuildFunc  
- `BuildImageConfigResolved()` - Image config without targetKey
- `MaybeSignResolved()` - Package signing without targetKey
- `HasGolangResolved()`, `HasNpmResolved()` - Helper functions

## Benefits

### Performance
- **Eliminates Repeated Lookups**: Old approach did O(n) target lookup on every method call
- **Caches Merged Configuration**: Resolution happens once, access is O(1)
- **Reduces Function Call Overhead**: No need to pass targetKey through call chains

### Code Quality  
- **Cleaner APIs**: Functions no longer need targetKey parameter
- **Reduced Parameter Passing**: Eliminates long parameter lists with targetKey
- **Better Encapsulation**: Configuration is self-contained in ResolvedSpec
- **Type Safety**: ResolvedSpec provides compile-time guarantees about resolved state

### Maintainability
- **Single Source of Truth**: All resolved config in one place
- **Easier Testing**: ResolvedSpec can be easily mocked/stubbed
- **Clear Separation**: Resolution logic separate from business logic
- **Backward Compatible**: Old functions still work during transition

## Examples

### Frontend Handler (Before/After)

**Before:**
```go
frontend.BuildWithPlatform(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, spec *dalec.Spec, targetKey string) (gwclient.Reference, *dalec.DockerImageSpec, error) {
    deps := spec.GetRuntimeDeps(targetKey)
    buildDeps := spec.GetBuildDeps(targetKey) 
    imgConfig := dalec.DockerImageSpec{}
    dalec.BuildImageConfig(spec, targetKey, &imgConfig)
    // ... rest of function needs targetKey everywhere
})
```

**After:**
```go  
frontend.BuildWithResolvedSpec(ctx, client, func(ctx context.Context, client gwclient.Client, platform *ocispecs.Platform, resolved *dalec.ResolvedSpec) (gwclient.Reference, *dalec.DockerImageSpec, error) {
    deps := resolved.GetRuntimeDeps()     // No targetKey needed
    buildDeps := resolved.GetBuildDeps()  // No targetKey needed
    imgConfig := dalec.DockerImageSpec{}
    dalec.BuildImageConfigResolved(resolved, &imgConfig)  // No targetKey needed
    // ... rest of function is cleaner
})
```

## Migration Strategy

1. **Non-Breaking Introduction**: New ResolvedSpec functions added alongside existing ones
2. **Gradual Adoption**: Teams can migrate individual functions/handlers over time  
3. **Backward Compatibility**: All existing code continues to work unchanged
4. **Performance Incentive**: New approach provides immediate performance benefits
5. **Future Cleanup**: Eventually deprecated functions can be removed

## Testing

Comprehensive test coverage ensures:
- Proper merging of global and target-specific configuration
- Correct fallback behavior for non-existent targets  
- All ResolvedSpec methods work correctly
- Backward compatibility maintained
- Performance characteristics as expected

## Files Modified

- `resolved_spec.go` - Core ResolvedSpec implementation
- `resolved_spec_test.go` - Comprehensive test coverage
- `frontend/build.go` - ResolvedPlatformBuildFunc infrastructure
- `frontend/request.go` - MaybeSignResolved function
- `imgconfig.go` - BuildImageConfigResolved function  
- `helpers.go` - HasGolangResolved, HasNpmResolved functions
- `deps.go` - Fixed GetExtraRepos bug
- `targets/linux/rpm/distro/container.go` - Example migration
- `frontend/debug/handle_resolve.go` - Practical demonstration

## Conclusion

This refactor successfully eliminates the need for passing `targetKey` parameters throughout the codebase while maintaining full backward compatibility. The ResolvedSpec approach provides better performance, cleaner APIs, and improved maintainability while making the codebase easier to understand and test.