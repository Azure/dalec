---
title: Extra Repository Configs
---

When you need packages from repositories not available in the default worker environment, use `extra_repos` in the [dependencies section](spec.md#dependencies-section) to add custom repositories.

## Repository Configuration

Each repository configuration has these components:

### Keys

GPG keys required to verify repository packages:

```yaml
extra_repos:
  - keys:
      mykey:
        http:
          url: "https://example.com/repo-key.gpg"
          permissions: 0644
```

:::tip
Name key files with correct extensions: `.asc` for ASCII-armored keys, `.gpg` for binary keys. Some package managers like `apt` require proper extensions.
:::

### Config

Repository configuration files (distribution-specific format):

```yaml
extra_repos:
  - config:
      myrepo:
        inline:
          file:
            contents: |
              deb [signed-by=/usr/share/keyrings/mykey.gpg] https://example.com/repo stable main
            permissions: 0644
```

### Data

Additional files needed by repositories (advanced use case):

```yaml
extra_repos:
  - data:
      - dest: "/opt/local-repo"
        spec:
          context:
            name: "my-local-repo"
```

### Environments

Specify when repositories are available:

- `build` - Before installing build dependencies
- `test` - Before installing test dependencies
- `install` - Before installing the final package in containers

```yaml
extra_repos:
  - envs: ["build", "install"]
    # ... rest of config
```

## Examples

All configurations are distribution-specific. Here are common patterns:

import MsftUbuntuRepo from './examples/repos/msft-ubuntu.yml.md'

<details>
<summary>Microsoft Ubuntu repository (APT)</summary>
<MsftUbuntuRepo />
</details>

import MsftAzlCNRepo from './examples/repos/msft-azl-cloud-native.yml.md'

<details>
<summary>Microsoft Azure Linux cloud-native repository (DNF)</summary>
<MsftAzlCNRepo />
</details>
