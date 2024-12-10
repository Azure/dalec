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
