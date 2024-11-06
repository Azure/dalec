### Ubuntu example with Microsoft Ubuntu 22.04 APT repository

```yaml
dependencies:
  build:
    # This is not available in the main Ubuntu repos
    # It will be supplied by the repository we are adding here.
    msft-golang:
  extra_repos:
    - keys:
        msft.gpg: # Note: This must currently use a `.gpg` suffix or apt will not be happy
          http:
            url: https://packages.microsoft.com/keys/microsoft.asc
            digest: sha256:2cfd20a306b2fa5e25522d78f2ef50a1f429d35fd30bd983e2ebffc2b80944fa
      config:
        microsoft-prod.list:
          inline:
            file:
              # Note the `signed-by` path is always going to be `/usr/share/keyrings/<source key name>` for Ubuntu, in this case our source key name is `msft.gpg`
              contents: deb [arch=amd64,arm64,armhf signed-by=/usr/share/keyrings/msft.gpg] https://packages.microsoft.com/ubuntu/22.04/prod jammy main
      envs:
        # The repository will only be available when installing build dependencies
        - build
```
