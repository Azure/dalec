package ubuntu

import (
	"github.com/Azure/dalec/frontend/linux/deb/distro"
)

const (
	NobleDefaultTargetKey  = "noble"
	NobleAptCachePrefix    = "noble"
	NobleWorkerContextName = "dalec-noble-worker"

	nobleRef       = "mcr.microsoft.com/mirror/docker/library/ubuntu:noble"
	nobleVersionID = "ubuntu24.04"
)

var (
	NobleConfig = &distro.Config{
		ImageRef:           nobleRef,
		AptCachePrefix:     NobleAptCachePrefix,
		VersionID:          nobleVersionID,
		ContextRef:         NobleWorkerContextName,
		DefaultOutputImage: nobleRef,
		BuilderPackages:    basePackages,
	}
)
