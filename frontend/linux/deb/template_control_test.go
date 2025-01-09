package deb

import (
	"reflect"
	"testing"

	"github.com/Azure/dalec"
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
				"packageA": {Version: []string{">= 1.0", "< 2.0"}},
			},
			want: []string{"packageA (< 2.0) | packageA (>= 1.0)"},
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
				"packageA": {Version: []string{">= 1.0", "< 2.0"}, Arch: []string{"amd64", "arm64"}},
			},
			want: []string{"packageA (< 2.0) [amd64 arm64] | packageA (>= 1.0) [amd64 arm64]"},
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
			if got := appendConstraints(tt.deps); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("appendConstraints() = %v, want %v", got, tt.want)
			}
		})
	}
}
