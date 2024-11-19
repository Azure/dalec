package ubuntu

import (
	"github.com/Azure/dalec/frontend/deb/distro"
)

const (
	JammyDefaultTargetKey  = "jammy"
	JammyAptCachePrefix    = "jammy"
	JammyWorkerContextName = "dalec-jammy-worker"

	jammyRef       = "mcr.microsoft.com/mirror/docker/library/ubuntu:jammy"
	jammyVersionID = "ubuntu22.04"
)

var (
	JammyConfig = &distro.Config{
		ImageRef:           jammyRef,
		AptCachePrefix:     JammyAptCachePrefix,
		VersionID:          jammyVersionID,
		ContextRef:         JammyWorkerContextName,
		DefaultOutputImage: jammyRef,
		BuilderPackages:    basePackages,
	}
)
