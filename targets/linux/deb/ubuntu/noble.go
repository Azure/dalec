package ubuntu

import (
	"github.com/Azure/dalec/targets/linux/deb/distro"
)

const (
	NobleDefaultTargetKey  = "noble"
	NobleAptCachePrefix    = "noble"
	NobleWorkerContextName = "dalec-noble-worker"

	nobleRef       = "docker.io/library/ubuntu:noble"
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
