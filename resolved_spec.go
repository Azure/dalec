package dalec

// ResolvedSpec represents a spec that has been resolved for a specific target.
// This eliminates the need to pass targetKey around everywhere by pre-merging
// the target-specific configuration with global configuration.
type ResolvedSpec struct {
	// Core spec fields (unchanged from global spec)
	Name        string `json:"name"`
	Description string `json:"description"`
	Website     string `json:"website"`
	Version     string `json:"version"`
	Revision    string `json:"revision"`
	NoArch      bool   `json:"noarch,omitempty"`
	License     string `json:"license"`
	Vendor      string `json:"vendor,omitempty"`
	Packager    string `json:"packager,omitempty"`

	// Sources and build configuration (inherited from global spec)
	Sources map[string]Source `json:"sources,omitempty"`
	Patches map[string][]PatchSpec `json:"patches,omitempty"`
	Build   ArtifactBuild `json:"build,omitempty"`
	Args    map[string]string `json:"args,omitempty"`
	Tests   []*TestSpec `json:"tests,omitempty"`
	Changelog []ChangelogEntry `json:"changelog,omitempty"`

	// Resolved fields (merged from global + target specific)
	Dependencies  *PackageDependencies `json:"dependencies,omitempty"`
	PackageConfig *PackageConfig `json:"package_config,omitempty"`
	Image         *ImageConfig `json:"image,omitempty"`
	Artifacts     Artifacts `json:"artifacts,omitempty"`
	Provides      map[string]PackageConstraints `json:"provides,omitempty"`
	Replaces      map[string]PackageConstraints `json:"replaces,omitempty"`
	Conflicts     map[string]PackageConstraints `json:"conflicts,omitempty"`

	// Reference to original spec and target key for advanced use cases
	originalSpec *Spec
	targetKey    string
}

// ResolveForTarget creates a ResolvedSpec for the specified target by merging
// global and target-specific configuration.
func (s *Spec) ResolveForTarget(targetKey string) *ResolvedSpec {
	resolved := &ResolvedSpec{
		// Copy core fields from global spec
		Name:        s.Name,
		Description: s.Description,
		Website:     s.Website,
		Version:     s.Version,
		Revision:    s.Revision,
		NoArch:      s.NoArch,
		License:     s.License,
		Vendor:      s.Vendor,
		Packager:    s.Packager,
		Sources:     s.Sources,
		Patches:     s.Patches,
		Build:       s.Build,
		Args:        s.Args,
		Changelog:   s.Changelog,
		
		// Store references for advanced use cases
		originalSpec: s,
		targetKey:    targetKey,
	}

	// Merge tests (global + target-specific)
	resolved.Tests = append([]*TestSpec(nil), s.Tests...)
	if target, ok := s.Targets[targetKey]; ok && target.Tests != nil {
		resolved.Tests = append(resolved.Tests, target.Tests...)
	}

	// Resolve dependencies
	resolved.Dependencies = s.GetPackageDeps(targetKey)

	// Resolve package config
	resolved.PackageConfig = s.PackageConfig
	if target, ok := s.Targets[targetKey]; ok && target.PackageConfig != nil {
		resolved.PackageConfig = target.PackageConfig
	}

	// Resolve image config
	resolved.Image = MergeSpecImage(s, targetKey)

	// Resolve artifacts
	resolved.Artifacts = s.GetArtifacts(targetKey)

	// Resolve provides, replaces, conflicts
	resolved.Provides = s.GetProvides(targetKey)
	resolved.Replaces = s.GetReplaces(targetKey)
	resolved.Conflicts = s.GetConflicts(targetKey)

	return resolved
}

// GetRuntimeDeps returns the runtime dependencies for the resolved spec.
func (r *ResolvedSpec) GetRuntimeDeps() []string {
	if r.Dependencies == nil {
		return nil
	}
	return SortMapKeys(r.Dependencies.Runtime)
}

// GetBuildDeps returns the build dependencies for the resolved spec.
func (r *ResolvedSpec) GetBuildDeps() map[string]PackageConstraints {
	if r.Dependencies == nil {
		return nil
	}
	return r.Dependencies.Build
}

// GetTestDeps returns the test dependencies for the resolved spec.
func (r *ResolvedSpec) GetTestDeps() []string {
	if r.Dependencies == nil {
		return nil
	}
	out := make([]string, len(r.Dependencies.Test))
	copy(out, r.Dependencies.Test)
	return out
}

// GetBuildRepos returns the build repositories for the resolved spec.
func (r *ResolvedSpec) GetBuildRepos() []PackageRepositoryConfig {
	if r.Dependencies == nil {
		return nil
	}
	return r.Dependencies.GetExtraRepos("build")
}

// GetInstallRepos returns the install repositories for the resolved spec.
func (r *ResolvedSpec) GetInstallRepos() []PackageRepositoryConfig {
	if r.Dependencies == nil {
		return nil
	}
	return r.Dependencies.GetExtraRepos("install")
}

// GetTestRepos returns the test repositories for the resolved spec.
func (r *ResolvedSpec) GetTestRepos() []PackageRepositoryConfig {
	if r.Dependencies == nil {
		return nil
	}
	return r.Dependencies.GetExtraRepos("test")
}

// GetSigner returns the package signer configuration for the resolved spec.
func (r *ResolvedSpec) GetSigner() (*PackageSigner, bool) {
	if r.PackageConfig != nil && hasValidSigner(r.PackageConfig) {
		return r.PackageConfig.Signer, true
	}
	return nil, false
}

// TargetKey returns the target key this spec was resolved for.
func (r *ResolvedSpec) TargetKey() string {
	return r.targetKey
}

// OriginalSpec returns the original spec this was resolved from.
func (r *ResolvedSpec) OriginalSpec() *Spec {
	return r.originalSpec
}