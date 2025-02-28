package almalinux

import (
	"github.com/Azure/dalec/targets/linux/rpm/distro"
)

const (
	v9TargetKey    = "almalinux9"
	dnfCacheNameV9 = "almalinux9-dnf-cache"

	// v9Ref is the image ref used for the base worker image
	v9Ref      = "mcr.microsoft.com/mirror/docker/library/almalinux:9"
	v9FullName = "AlmaLinux 9"
	// v9WorkerContextName is the build context name that can be used to lookup
	v9WorkerContextName = "dalec-almalinux9-worker"
)

var ConfigV9 = &distro.Config{
	ImageRef:   v9Ref,
	ContextRef: v9WorkerContextName,

	CacheName: dnfCacheNameV9,
	CacheDir:  "/var/cache/dnf",

	ReleaseVer:         "9",
	BuilderPackages:    builderPackages,
	BasePackages:       []string{"almalinux-release", "tzdata"},
	RepoPlatformConfig: &defaultPlatformConfig,
	InstallFunc:        distro.DnfInstall,
}
