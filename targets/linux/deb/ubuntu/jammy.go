package ubuntu

import (
	"github.com/Azure/dalec/targets/linux/deb/distro"
)

const (
	JammyDefaultTargetKey  = "jammy"
	JammyAptCachePrefix    = "jammy"
	JammyWorkerContextName = "dalec-jammy-worker"

	jammyRef       = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	JammyVersionID = "ubuntu22.04"
)

var (
	JammyConfig = &distro.Config{
		ImageRef:           jammyRef,
		AptCachePrefix:     JammyAptCachePrefix,
		VersionID:          JammyVersionID,
		ContextRef:         JammyWorkerContextName,
		DefaultOutputImage: jammyRef,
		BuilderPackages:    basePackages,
	}
)
