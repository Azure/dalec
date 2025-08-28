package deb

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/stretchr/testify/require"
)

func TestAppendConstraints(t *testing.T) {
	tests := []struct {
		name string
		deps map[string]dalec.PackageConstraints
		want []string
	}{
		{
			name: "nil dependencies",
			deps: nil,
			want: nil,
		},
		{
			name: "empty dependencies",
			deps: map[string]dalec.PackageConstraints{},
			want: []string{},
		},
		{
			name: "single dependency without constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {},
			},
			want: []string{"packageA"},
		},
		{
			name: "single dependency with version constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {Version: []string{">= 1.0", "<< 2.0"}},
			},
			want: []string{"packageA (<< 2.0) | packageA (>= 1.0)"},
		},
		{
			name: "single dependency with architecture constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {Arch: []string{"amd64", "arm64"}},
			},
			want: []string{"packageA [amd64 arm64]"},
		},
		{
			name: "single dependency with version and architecture constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageA": {Version: []string{">= 1.0", "<< 2.0"}, Arch: []string{"amd64", "arm64"}},
			},
			want: []string{"packageA (<< 2.0) [amd64 arm64] | packageA (>= 1.0) [amd64 arm64]"},
		},
		{
			name: "multiple dependencies with constraints",
			deps: map[string]dalec.PackageConstraints{
				"packageB": {Version: []string{"= 1.0"}},
				"packageA": {Arch: []string{"amd64"}},
			},
			want: []string{"packageA [amd64]", "packageB (= 1.0)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AppendConstraints(tt.deps); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("AppendConstraints() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestControlWrapper_ReplacesConflictsProvides(t *testing.T) {
	t.Run("target specific", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			Targets: map[string]dalec.Target{
				"target1": {
					Replaces: map[string]dalec.PackageConstraints{
						"pkg-a": {Version: []string{">> 1.0.0"}},
					},
					Conflicts: map[string]dalec.PackageConstraints{
						"pkg-b": {Version: []string{"<< 2.0.0"}},
					},
					Provides: map[string]dalec.PackageConstraints{
						"pkg-c": {},
					},
				},
				"target2": {
					Replaces: map[string]dalec.PackageConstraints{
						"pkg-d": {Version: []string{"= 3.0.0"}},
					},
					Conflicts: map[string]dalec.PackageConstraints{
						"pkg-e": {Arch: []string{"amd64", "arm64"}},
					},
					Provides: map[string]dalec.PackageConstraints{
						"pkg-f": {Version: []string{">= 4.0.0"}},
					},
				},
			},
		}

		// Test target1
		wrapper1 := &controlWrapper{spec, "target1"}

		// Test Replaces
		replaces := wrapper1.Replaces().String()
		require.Contains(t, replaces, "Replaces: pkg-a (>> 1.0.0)")

		// Test Conflicts
		conflicts := wrapper1.Conflicts().String()
		require.Contains(t, conflicts, "Conflicts: pkg-b (<< 2.0.0)")

		// Test Provides
		provides := wrapper1.Provides().String()
		require.Contains(t, provides, "Provides: pkg-c")

		// Test target2
		wrapper2 := &controlWrapper{spec, "target2"}

		// Test Replaces
		replaces = wrapper2.Replaces().String()
		require.Contains(t, replaces, "Replaces: pkg-d (= 3.0.0)")

		// Test Conflicts
		conflicts = wrapper2.Conflicts().String()
		require.Contains(t, conflicts, "Conflicts: pkg-e [amd64 arm64]")

		// Test Provides
		provides = wrapper2.Provides().String()
		require.Contains(t, provides, "Provides: pkg-f (>= 4.0.0)")
	})

	t.Run("non-target specific", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			Replaces: map[string]dalec.PackageConstraints{
				"pkg-g": {Version: []string{">> 1.0.0"}},
			},
			Conflicts: map[string]dalec.PackageConstraints{
				"pkg-h": {Version: []string{"<< 2.0.0"}},
			},
			Provides: map[string]dalec.PackageConstraints{
				"pkg-i": {Version: []string{">= 3.0.0"}, Arch: []string{"amd64"}},
			},
		}

		// Test with any target name
		wrapper := &controlWrapper{spec, "any-target"}

		// Test Replaces
		replaces := wrapper.Replaces().String()
		require.Contains(t, replaces, "Replaces: pkg-g (>> 1.0.0)")

		// Test Conflicts
		conflicts := wrapper.Conflicts().String()
		require.Contains(t, conflicts, "Conflicts: pkg-h (<< 2.0.0)")

		// Test Provides
		provides := wrapper.Provides().String()
		require.Contains(t, provides, "Provides: pkg-i (>= 3.0.0) [amd64]")
	})

	t.Run("empty values", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			// No Replaces, Conflicts, or Provides defined
		}

		wrapper := &controlWrapper{spec, "target1"}

		// Test empty values
		require.Equal(t, "", wrapper.Replaces().String())
		require.Equal(t, "", wrapper.Conflicts().String())
		require.Equal(t, "", wrapper.Provides().String())
	})

	t.Run("multiline format", func(t *testing.T) {
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			Replaces: map[string]dalec.PackageConstraints{
				"pkg-a": {Version: []string{">> 1.0.0"}},
				"pkg-b": {Version: []string{"<< 2.0.0"}},
				"pkg-c": {Version: []string{">= 3.0.0"}},
			},
		}

		wrapper := &controlWrapper{spec, "any-target"}
		replaces := wrapper.Replaces().String()

		// Test multiline formatting
		lines := strings.Split(strings.TrimSpace(replaces), "\n")
		require.Equal(t, 3, len(lines))
		require.Contains(t, lines[0], "Replaces: pkg-a (>> 1.0.0),")
		require.Contains(t, lines[1], "         pkg-b (<< 2.0.0),")
		require.Contains(t, lines[2], "         pkg-c (>= 3.0.0)")
	})

	t.Run("target precedence", func(t *testing.T) {
		// Create spec with both root-level and target-specific values
		spec := &dalec.Spec{
			Name:    "test-pkg",
			Version: "1.0.0",
			// Root-level definitions
			Replaces: map[string]dalec.PackageConstraints{
				"root-pkg-r": {Version: []string{">= 1.0.0"}},
				"common-pkg": {Version: []string{">= 2.0.0"}}, // Will be overridden in target1
			},
			Conflicts: map[string]dalec.PackageConstraints{
				"root-pkg-c": {Version: []string{"<= 3.0.0"}},
				"common-pkg": {Version: []string{"<= 4.0.0"}}, // Will be overridden in target1
			},
			Provides: map[string]dalec.PackageConstraints{
				"root-pkg-p": {Version: []string{"= 5.0.0"}},
				"common-pkg": {Version: []string{"= 6.0.0"}}, // Will be overridden in target1
			},
			Targets: map[string]dalec.Target{
				// target1 overrides values
				"target1": {
					Replaces: map[string]dalec.PackageConstraints{
						"target-pkg-r": {Version: []string{">= 1.1.0"}},
						"common-pkg":   {Version: []string{">= 2.1.0"}}, // Overrides root
					},
					Conflicts: map[string]dalec.PackageConstraints{
						"target-pkg-c": {Version: []string{"<= 3.1.0"}},
						"common-pkg":   {Version: []string{"<= 4.1.0"}}, // Overrides root
					},
					Provides: map[string]dalec.PackageConstraints{
						"target-pkg-p": {Version: []string{"= 5.1.0"}},
						"common-pkg":   {Version: []string{"= 6.1.0"}}, // Overrides root
					},
					Artifacts: &dalec.Artifacts{
						DisableAutoRequires: true,
					},
				},
				// target2 has explicit empty maps to override the root values
				"target2": {
					Replaces:  map[string]dalec.PackageConstraints{},
					Conflicts: map[string]dalec.PackageConstraints{},
					Provides:  map[string]dalec.PackageConstraints{},
				},
			},
		}

		// Test target1 (should see target-specific values taking precedence)
		wrapper1 := &controlWrapper{spec, "target1"}

		// Test Replaces - should contain target-specific values and not root values for common-pkg
		replaces := wrapper1.Replaces().String()
		require.Contains(t, replaces, "common-pkg (>= 2.1.0)")
		require.Contains(t, replaces, "target-pkg-r (>= 1.1.0)")
		require.NotContains(t, replaces, "root-pkg-r")
		require.NotContains(t, replaces, "(>= 2.0.0)") // common-pkg old version

		// Test Conflicts - should contain target-specific values and not root values for common-pkg
		conflicts := wrapper1.Conflicts().String()
		require.Contains(t, conflicts, "common-pkg (<= 4.1.0)")
		require.Contains(t, conflicts, "target-pkg-c (<= 3.1.0)")
		require.NotContains(t, conflicts, "root-pkg-c")
		require.NotContains(t, conflicts, "(<= 4.0.0)") // common-pkg old version

		// Test Provides - should contain target-specific values and not root values for common-pkg
		provides := wrapper1.Provides().String()
		require.Contains(t, provides, "common-pkg (= 6.1.0)")
		require.Contains(t, provides, "target-pkg-p (= 5.1.0)")
		require.NotContains(t, provides, "root-pkg-p")
		require.NotContains(t, provides, "(= 6.0.0)") // common-pkg old version

		deps := wrapper1.AllRuntimeDeps()
		require.NotContains(t, deps.String(), "${shlibs:Depends}")

		// Test with non-existent target to get root values
		// Current implementation only falls back to root if target doesn't exist
		wrapperNonExistent := &controlWrapper{spec, "non-existent-target"}

		// Test Replaces - should contain root values
		replaces = wrapperNonExistent.Replaces().String()
		require.Contains(t, replaces, "common-pkg (>= 2.0.0)")
		require.Contains(t, replaces, "root-pkg-r (>= 1.0.0)")

		// Test Conflicts - should contain root values
		conflicts = wrapperNonExistent.Conflicts().String()
		require.Contains(t, conflicts, "common-pkg (<= 4.0.0)")
		require.Contains(t, conflicts, "root-pkg-c (<= 3.0.0)")

		// Test Provides - should contain root values
		provides = wrapperNonExistent.Provides().String()
		require.Contains(t, provides, "common-pkg (= 6.0.0)")
		require.Contains(t, provides, "root-pkg-p (= 5.0.0)")

		// Test target2 - should return empty values because the maps are explicitly empty
		wrapper2 := &controlWrapper{spec, "target2"}
		require.Equal(t, "", wrapper2.Replaces().String())
		require.Equal(t, "", wrapper2.Conflicts().String())
		require.Equal(t, "", wrapper2.Provides().String())

		deps = wrapper2.AllRuntimeDeps()
		require.Contains(t, deps.String(), "${shlibs:Depends}")
	})
}
