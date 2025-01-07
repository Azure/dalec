package ubuntu

import (
	"github.com/Azure/dalec/frontend/linux/deb/distro"
)

const (
	BionicDefaultTargetKey  = "bionic"
	BionicAptCachePrefix    = "bionic"
	BionicWorkerContextName = "dalec-bionic-worker"

	bionicRef       = "mcr.microsoft.com/mirror/docker/library/ubuntu:bionic"
	bionicVersionID = "ubuntu18.04"
)

var (
	BionicConfig = &distro.Config{
		ImageRef:           bionicRef,
		AptCachePrefix:     BionicAptCachePrefix,
		VersionID:          bionicVersionID,
		ContextRef:         BionicWorkerContextName,
		DefaultOutputImage: bionicRef,
		BuilderPackages:    basePackages,
	}
)
