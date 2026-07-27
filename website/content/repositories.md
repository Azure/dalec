---
title: Extra Repository Configs
weight: 17
---

When you need to reference packages from repositories not in the worker's
repository config you can use the `extra_repos` field in the [dependencies
section](spec.md#dependencies-section) to inject extra repositories at
the right time so those dependencies can be satisfied.

The `extra_repos` field takes a list of repository configs with the following
structure:

- **`keys`**
  A map of keys required to enable the configured repositories. Each key in
  this map is associated with a specific source and must be imported to allow
  the repositories to function as expected. The content of this is a
  [source](sources.md) just like in the sources section. 

- **`config`**
  A collection of repository configurations to add to the environment. The
  structure and format of these configurations are specific to the distribution
  being used (e.g., apt or yum configurations). The content of this is a
  [source](sources.md) just like in the sources section.

- **`data`**
  A list of additional data required to support the repository configurations.
  This may include files or other resources essential for certain
  configurations, particularly file-backed repositories that need data not
  already available in the environment. The content of this is a mount which
  takes a `dest` field for where the content should be mounted in the build env
  and a `spec` field, the content of which is a [source](sources.md) just like
  in the sources section. Usage of `data` is considered an advanced case and,
  aside from creating local repositories, is likely not necessary for most
  uses.

- **`envs`**
  Specifies the environments in which the repositories should be made
  available. Possible values are:
  - **`build`** - Adds repositories before installing build dependencies.
  - **`test`** - Adds repositories before installing test dependencies.
  - **`install`** - Adds repositories before installing the final package in a
    container build target.

These configurations are highly distribution specific.

{{< callout type="default" >}}
Be careful to name the key files properly depending on whether they are ascii armored (`*.asc`) or binary (`*.gpg`). 
Some package managers such as `apt` do not handle keys properly if they are not named with the correct extension.
{{< /callout >}}

### Examples:

{{% details title="Microsoft Ubuntu repository (APT)" closed="true" %}}

### Ubuntu example with Microsoft Ubuntu 22.04 APT repository

```yaml
dependencies:
  build:
    # This is not available in the main Ubuntu repos
    # It will be supplied by the repository we are adding here.
    msft-golang:
  extra_repos:
    - keys:
       # Note: The name for the key must use the proper `.gpg` (binary) or `.asc` (ascii)
       # extension, or apt will not be able to import the key properly
        msft.asc:
          http:
            url: https://packages.microsoft.com/keys/microsoft.asc
            digest: sha256:2cfd20a306b2fa5e25522d78f2ef50a1f429d35fd30bd983e2ebffc2b80944fa
            permissions: 0o644
      config:
        microsoft-prod.list:
          inline:
            file:
              # Note the `signed-by` path is always going to be `/usr/share/keyrings/<source key name>` for Ubuntu, in this case our source key name is `msft.asc`
              contents: deb [arch=amd64,arm64,armhf signed-by=/usr/share/keyrings/msft.asc] https://packages.microsoft.com/ubuntu/22.04/prod jammy main
      envs:
        # The repository will only be available when installing build dependencies
        - build
```

{{% /details %}}

{{% details title="Microsoft Azure Linux cloud-native repository (DNF)" closed="true" %}}

### Azure Linux 3 yum/dnf repository

This only makes sense to run on Azure Linux, which already has the necessary
keys installed, so this only needs to have the actual repo config file.

```yaml
dependencies:
  runtime:
    # This is only in the AZL cloud-native repo
    gatekeeper-manager: {}
  extra_repos:
    - config:
        azl-cloud-native.repo:
          inline:
            file:
              contents: |
                [azurelinux-cloud-native]
                name=Azure Linux Cloud Native $releasever $basearch
                baseurl=https://packages.microsoft.com/azurelinux/$releasever/prod/cloud-native/$basearch
                gpgkey=file:///etc/pki/rpm-gpg/MICROSOFT-RPM-GPG-KEY
                gpgcheck=1
                repo_gpgcheck=1
                enabled=1
                skip_if_unavailable=True
                sslverify=1
      envs:
        # The repository will only be available when installing into the final container
        - install
```

{{% /details %}}


{{< callout type="info" >}}
For yum repos backed by the local filesystem, you may want to set `metadata_expire=0` in the repo config to avoid caching issues.
{{< /callout >}}
