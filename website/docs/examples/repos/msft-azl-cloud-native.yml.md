### Azure Linux 3 yum/dnf repository

This only makes sense to run on Azure Linux, which already has the neccessary
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
