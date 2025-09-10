package dalec

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSpecResolve(t *testing.T) {
	spec := &Spec{
		Name:        "test-package",
		Description: "Test package description",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "MIT",
		Dependencies: &PackageDependencies{
			Runtime: map[string]PackageConstraints{
				"global-runtime": {},
			},
			Build: map[string]PackageConstraints{
				"global-build": {},
			},
		},
		Targets: map[string]Target{
			"ubuntu": {
				Dependencies: &PackageDependencies{
					Runtime: map[string]PackageConstraints{
						"ubuntu-runtime": {},
					},
					Build: map[string]PackageConstraints{
						"ubuntu-build": {},
					},
				},
			},
		},
	}

	t.Run("resolve for target", func(t *testing.T) {
		resolved := spec.Resolve("ubuntu")

		// Basic fields should be copied
		require.Equal(t, spec.Name, resolved.Name)
		require.Equal(t, spec.Description, resolved.Description)
		require.Equal(t, spec.Version, resolved.Version)
		require.Equal(t, spec.Revision, resolved.Revision)
		require.Equal(t, spec.License, resolved.License)

		// Dependencies should be resolved (target overrides global, not merged)
		require.NotNil(t, resolved.Dependencies)
		require.Contains(t, resolved.Dependencies.Runtime, "ubuntu-runtime")
		require.Contains(t, resolved.Dependencies.Build, "ubuntu-build") 
		// Since target has runtime deps, global runtime deps are not included (that's the current behavior)
		require.NotContains(t, resolved.Dependencies.Runtime, "global-runtime")
		// Since target has build deps, global build deps are not included
		require.NotContains(t, resolved.Dependencies.Build, "global-build")

		// Targets should be cleared since this is resolved for a specific target
		require.Nil(t, resolved.Targets)
	})

	t.Run("resolve for non-existent target", func(t *testing.T) {
		resolved := spec.Resolve("non-existent")

		// Basic fields should be copied
		require.Equal(t, spec.Name, resolved.Name)
		require.Equal(t, spec.Description, resolved.Description)

		// Should only have global dependencies since target doesn't exist
		require.NotNil(t, resolved.Dependencies)
		require.Contains(t, resolved.Dependencies.Runtime, "global-runtime")
		require.Contains(t, resolved.Dependencies.Build, "global-build")

		// Targets should be cleared
		require.Nil(t, resolved.Targets)
	})

	t.Run("original spec unchanged", func(t *testing.T) {
		originalTargets := len(spec.Targets)
		resolved := spec.Resolve("ubuntu")

		// Original spec should be unchanged
		require.Equal(t, originalTargets, len(spec.Targets))
		require.NotNil(t, spec.Targets["ubuntu"])

		// But resolved spec should have no targets
		require.Nil(t, resolved.Targets)
	})
}

// TestResolveVsOriginalMethods demonstrates that the resolved spec provides
// the same results as the original targetKey-based methods, but more efficiently.
func TestResolveVsOriginalMethods(t *testing.T) {
	spec := &Spec{
		Name:    "test-pkg",
		Version: "1.0",
		Dependencies: &PackageDependencies{
			Runtime: map[string]PackageConstraints{
				"global-dep": {},
			},
		},
		Targets: map[string]Target{
			"ubuntu": {
				Dependencies: &PackageDependencies{
					Runtime: map[string]PackageConstraints{
						"ubuntu-dep": {},
					},
				},
			},
		},
	}

	targetKey := "ubuntu"
	resolved := spec.Resolve(targetKey)

	// Runtime dependencies should be the same
	originalRuntimeDeps := spec.GetRuntimeDeps(targetKey)
	resolvedRuntimeDeps := resolved.GetRuntimeDeps(targetKey) // Should work the same
	require.ElementsMatch(t, originalRuntimeDeps, resolvedRuntimeDeps)

	// Build dependencies should be the same
	originalBuildDeps := spec.GetBuildDeps(targetKey)
	resolvedBuildDeps := resolved.GetBuildDeps(targetKey)
	require.Equal(t, originalBuildDeps, resolvedBuildDeps)
}