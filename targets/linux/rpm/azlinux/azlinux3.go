package azlinux

import (
	"github.com/Azure/dalec/targets/linux/rpm/distro"
)

const (
	AzLinux3TargetKey     = "azlinux3"
	tdnfCacheNameAzlinux3 = "azlinux3-tdnf-cache"

	// Azlinux3Ref is the image ref used for the base worker image
	Azlinux3Ref      = "mcr.microsoft.com/azurelinux/base/core:3.0"
	AzLinux3FullName = "Azure Linux 3"
	// Azlinux3WorkerContextName is the build context name that can be used to lookup
	Azlinux3WorkerContextName = "dalec-azlinux3-worker"
	azlinux3DistrolessRef     = "mcr.microsoft.com/azurelinux/distroless/base:3.0"
)

var Azlinux3Config = &distro.Config{
	ImageRef:   Azlinux3Ref,
	ContextRef: Azlinux3WorkerContextName,

	CacheName: tdnfCacheNameAzlinux3,
	CacheDir:  "/var/cache/tdnf",

	ReleaseVer:         "3.0",
	BuilderPackages:    builderPackages,
	BasePackages:       []string{"distroless-packages-minimal", "(prebuilt-ca-certificates or ca-certificates)"},
	RepoPlatformConfig: &defaultAzlinuxRepoPlatform,
	InstallFunc:        distro.TdnfInstall,
}
