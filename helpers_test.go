package dalec

import (
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestMergeDependencies(t *testing.T) {
	tests := []struct {
		name     string
		base     *PackageDependencies
		target   *PackageDependencies
		expected *PackageDependencies
	}{
		{
			name:     "both nil",
			base:     nil,
			target:   nil,
			expected: nil,
		},
		{
			name: "base nil",
			base: nil,
			target: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
			},
			expected: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
			},
		},
		{
			name: "target nil",
			base: &PackageDependencies{
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
			target: nil,
			expected: &PackageDependencies{
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
		},
		{
			name: "merge dependencies",
			base: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg1": {},
				},
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
			},
			target: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg3": {},
				},
				Test: []string{"test1"},
			},
			expected: &PackageDependencies{
				Build: map[string]PackageConstraints{
					"pkg3": {},
				},
				Runtime: map[string]PackageConstraints{
					"pkg2": {},
				},
				Test: []string{"test1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeDependencies(tt.base, tt.target)
			assert.Check(t, cmp.DeepEqual(tt.expected, result))
		})
	}
}
