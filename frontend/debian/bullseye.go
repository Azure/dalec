package debian

import (
	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/deb/distro"
)

const (
	BullseyeDefaultTargetKey  = "bullseye"
	BullseyeAptCachePrefix    = "bullseye"
	BullseyeWorkerContextName = "dalec-bullseye-worker"

	bullseyeRef       = "mcr.microsoft.com/mirror/docker/library/debian:bullseye"
	bullseyeVersionID = "debian11"
)

var (
	BullseyeConfig = &distro.Config{
		ImageRef:           bullseyeRef,
		AptCachePrefix:     BullseyeAptCachePrefix,
		VersionID:          bullseyeVersionID,
		ContextRef:         BullseyeWorkerContextName,
		DefaultOutputImage: bullseyeRef,
		BuilderPackages:    basePackages,

		// Ubuntu typically has backports repos already in it but Debian does not.
		// Without this the go modules test will fail since there is no viable
		// version of go except with the backports repository added.
		ExtraRepos: []dalec.PackageRepositoryConfig{
			{
				Envs: []string{"build", "test", "install"},
				Config: map[string]dalec.Source{
					"backports.list": {
						Inline: &dalec.SourceInline{
							File: &dalec.SourceInlineFile{
								Contents: "deb http://deb.debian.org/debian bullseye-backports main",
							},
						},
					},
				},
			},
		},
	}
)
