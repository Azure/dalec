package dalec

import (
	"testing"
)

func TestResolveForTarget(t *testing.T) {
	// Create a test spec with global and target-specific settings
	spec := &Spec{
		Name:        "test-package",
		Description: "A test package",
		Version:     "1.0.0",
		Revision:    "1",
		License:     "MIT",
		Dependencies: &PackageDependencies{
			Runtime: map[string]PackageConstraints{
				"libc": {Version: []string{">= 2.0"}},
			},
			Build: map[string]PackageConstraints{
				"gcc": {Version: []string{">= 9.0"}},
			},
			Test: []string{"test-runner"},
		},
		Provides: map[string]PackageConstraints{
			"global-provide": {Version: []string{"1.0"}},
		},
		Targets: map[string]Target{
			"test-target": {
				Dependencies: &PackageDependencies{
					Runtime: map[string]PackageConstraints{
						"target-lib": {Version: []string{">= 1.0"}},
					},
					Build: map[string]PackageConstraints{
						"target-compiler": {Version: []string{">= 10.0"}},
					},
				},
				Provides: map[string]PackageConstraints{
					"target-provide": {Version: []string{"2.0"}},
				},
				Tests: []*TestSpec{
					{Name: "target-test"},
				},
			},
		},
		Tests: []*TestSpec{
			{Name: "global-test"},
		},
	}

	t.Run("ResolveWithTargetSpecific", func(t *testing.T) {
		resolved := spec.ResolveForTarget("test-target")

		// Check that core fields are copied
		if resolved.Name != "test-package" {
			t.Errorf("Expected name %q, got %q", "test-package", resolved.Name)
		}
		if resolved.Version != "1.0.0" {
			t.Errorf("Expected version %q, got %q", "1.0.0", resolved.Version)
		}

		// Check that dependencies are merged
		deps := resolved.GetBuildDeps()
		if deps["target-compiler"].Version[0] != ">= 10.0" {
			t.Errorf("Expected target-specific build dep, got %v", deps["target-compiler"])
		}
		
		runtimeDeps := resolved.GetRuntimeDeps()
		found := false
		for _, dep := range runtimeDeps {
			if dep == "target-lib" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected target-lib in runtime deps, got %v", runtimeDeps)
		}

		// Check that provides is resolved
		if resolved.Provides["target-provide"].Version[0] != "2.0" {
			t.Errorf("Expected target-specific provide, got %v", resolved.Provides["target-provide"])
		}

		// Check that tests are merged
		if len(resolved.Tests) != 2 {
			t.Errorf("Expected 2 tests (global + target), got %d", len(resolved.Tests))
		}

		// Check target key
		if resolved.TargetKey() != "test-target" {
			t.Errorf("Expected target key %q, got %q", "test-target", resolved.TargetKey())
		}
	})

	t.Run("ResolveWithNonExistentTarget", func(t *testing.T) {
		resolved := spec.ResolveForTarget("non-existent")

		// Should use global settings only
		runtimeDeps := resolved.GetRuntimeDeps()
		if len(runtimeDeps) != 1 || runtimeDeps[0] != "libc" {
			t.Errorf("Expected only global runtime deps, got %v", runtimeDeps)
		}

		// Should only have global tests
		if len(resolved.Tests) != 1 {
			t.Errorf("Expected 1 test (global only), got %d", len(resolved.Tests))
		}

		// Check target key
		if resolved.TargetKey() != "non-existent" {
			t.Errorf("Expected target key %q, got %q", "non-existent", resolved.TargetKey())
		}
	})
}

func TestResolvedSpecMethods(t *testing.T) {
	spec := &Spec{
		Dependencies: &PackageDependencies{
			Runtime: map[string]PackageConstraints{
				"lib1": {Version: []string{">= 1.0"}},
				"lib2": {Version: []string{">= 2.0"}},
			},
			Build: map[string]PackageConstraints{
				"build1": {Version: []string{">= 1.0"}},
			},
			Test: []string{"test1", "test2"},
			ExtraRepos: []PackageRepositoryConfig{
				{
					Envs: []string{"build", "install"},
				},
			},
		},
		PackageConfig: &PackageConfig{
			Signer: &PackageSigner{
				Frontend: &Frontend{
					Image: "test-signer:latest",
				},
			},
		},
	}

	resolved := spec.ResolveForTarget("any-target")

	t.Run("GetRuntimeDeps", func(t *testing.T) {
		deps := resolved.GetRuntimeDeps()
		if len(deps) != 2 {
			t.Errorf("Expected 2 runtime deps, got %d", len(deps))
		}
	})

	t.Run("GetBuildDeps", func(t *testing.T) {
		deps := resolved.GetBuildDeps()
		if len(deps) != 1 {
			t.Errorf("Expected 1 build dep, got %d", len(deps))
		}
		if deps["build1"].Version[0] != ">= 1.0" {
			t.Errorf("Expected build1 version, got %v", deps["build1"])
		}
	})

	t.Run("GetTestDeps", func(t *testing.T) {
		deps := resolved.GetTestDeps()
		if len(deps) != 2 {
			t.Errorf("Expected 2 test deps, got %d", len(deps))
		}
	})

	t.Run("GetBuildRepos", func(t *testing.T) {
		repos := resolved.GetBuildRepos()
		if len(repos) != 1 {
			t.Errorf("Expected 1 build repo, got %d", len(repos))
		}
	})

	t.Run("GetSigner", func(t *testing.T) {
		signer, ok := resolved.GetSigner()
		if !ok {
			t.Error("Expected signer to be available")
		}
		if signer == nil {
			t.Error("Expected non-nil signer")
		}
	})
}