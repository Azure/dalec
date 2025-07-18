---
title: Sources
---

Sources define how Dalec fetches build dependencies like source code, patches, and files. Sources support various protocols and can be either file-based (HTTP URLs, local files) or directory-based (Git repositories, build contexts).

## Overview

Sources are injected into the build environment using their configured name. The content should ideally be platform-agnostic to create portable source packages across different targets.

## Common Configuration

All source types support these top-level options:

```yaml
sources:
  my-source:
    path: path/in/source      # Extract from this subdirectory
    includes:                 # Include only these patterns
      - "*.txt"
    excludes:                 # Exclude these patterns
      - "secret.txt"
    generate:                 # See Generators section
      - gomod: {}
    # Source type configuration here
    context: {}
```

## Source Types

### Git Repositories

Fetch code from Git repositories at specific commits:

```yaml
sources:
  my-repo:
    git:
      url: https://github.com/myOrg/myRepo.git
      commit: 1234567890abcdef
      keepGitDir: true  # Optional: keep .git directory
```

**Authentication** (uses build secrets):

- SSH: Default SSH agent or custom secret
- Token: `GIT_AUTH_TOKEN` secret
- Header: `GIT_AUTH_HEADER` secret

```yaml
sources:
  private-repo:
    git:
      url: git@github.com:myOrg/privateRepo.git
      commit: abcdef1234567890
      auth:
        token: MY_CUSTOM_TOKEN    # Custom secret name
        ssh: my-ssh-key          # Custom SSH secret
```

### HTTP Files

Download files from HTTP URLs:

```yaml
sources:
  remote-file:
    http:
      url: https://example.com/file.tar.gz
      digest: sha256:1234567890abcdef  # Optional verification
      permissions: 0644                # Optional file permissions
```

### Build Context

Use the build context provided by the client:

```yaml
sources:
  local-files:
    context: {}  # Uses main build context

  named-context:
    context:
      name: "my-context"  # Uses named context from --build-context
```

Example usage:

```shell
docker build --build-context my-context=./path/to/files .
```

### Inline Content

Define content directly in the spec:

```yaml
sources:
  # Single file
  config-file:
    inline:
      file:
        uid: 0
        gid: 0
        permissions: 0644
        contents: |
          app.name=myapp
          app.version=1.0.0

  # Directory with multiple files
  scripts:
    inline:
      dir:
        uid: 0
        gid: 0
        permissions: 0755
        files:
          install.sh:
            contents: |
              #!/bin/bash
              echo "Installing application..."
            permissions: 0755
          config.json:
            contents: |
              {"debug": false}
            permissions: 0644
```

### Docker Images

Use Docker images as sources, optionally running commands to generate content:

```yaml
sources:
  # Basic image source
  alpine-base:
    image:
      ref: docker.io/library/alpine:3.14

  # Generate content by running commands
  generated-content:
    path: /output
    image:
      ref: docker.io/library/alpine:3.14
      cmd:
        dir: /workspace
        steps:
          - command: |
              mkdir -p /output
              echo "Generated at $(date)" > /output/timestamp.txt
            env:
              TZ: UTC
```

### Build Sources

Build a Dockerfile and use the result as a source:

```yaml
sources:
  custom-build:
    build:
      dockerfile_path: custom.Dockerfile  # Optional, defaults to "Dockerfile"
      target: production                  # Optional build stage
      args:                              # Optional build args
        VERSION: "1.0.0"
      source:
        git:
          url: https://github.com/example/app.git
          commit: v1.0.0
```

## Generators

Generators automatically fetch and cache dependencies for specific ecosystems:

### Go Modules (`gomod`)

Automatically manages Go module dependencies:

```yaml
sources:
  my-go-app:
    git:
      url: https://github.com/example/go-app.git
      commit: v1.0.0
    generate:
      - gomod: {}  # Fetches all Go dependencies
```

**Multi-module support:**

```yaml
sources:
  go-monorepo:
    context: {}
    generate:
      - gomod:
          paths:
            - backend
            - frontend
```

**Private modules:**

```yaml
sources:
  private-go-app:
    git:
      url: https://github.com/private/repo.git
      commit: main
    generate:
      - gomod:
          auth:
            private.com:
              token: PRIVATE_TOKEN
            gitlab.example.com:
              header: GITLAB_AUTH_HEADER
```

### Cargo Dependencies (`cargohome`)

Manages Rust Cargo dependencies:

```yaml
sources:
  rust-app:
    git:
      url: https://github.com/example/rust-app.git
      commit: v1.0.0
    generate:
      - cargohome: {}
```

### Python Packages (`pip`)

Manages Python pip dependencies:

```yaml
sources:
  python-app:
    git:
      url: https://github.com/example/python-app.git
      commit: v1.0.0
    generate:
      - pip:
          requirements_file: requirements.txt  # Default
          index_url: https://pypi.org/simple/
          extra_index_urls:
            - https://custom-pypi.example.com/simple/
```

### Node Modules (`nodemod`)

Manages Node.js dependencies:

```yaml
sources:
  node-app:
    git:
      url: https://github.com/example/node-app.git
      commit: v1.0.0
    generate:
      - nodemod: {}
```

:::note
The `nodemod` generator may include platform-specific binaries, making source packages less portable across architectures.
:::

## Patches

Apply patches to sources by referencing other sources as patch files:

```yaml
sources:
  # Main source to patch
  my-app:
    git:
      url: https://github.com/example/app.git
      commit: v1.0.0
    generate:
      - gomod: {}

  # Patch files
  security-fix:
    http:
      url: https://github.com/example/app/commit/abc123.patch

  local-patches:
    context: {}
    includes:
      - "patches/*.patch"

patches:
  my-app:  # Source to patch
    - source: security-fix     # HTTP patch (file source)
    - source: local-patches    # Directory source requires path
      path: patches/feature.patch
    - source: local-patches
      path: patches/bugfix.patch
```

**Patch Requirements:**
- File-based sources (HTTP): Don't specify `path`
- Directory-based sources (Git, context): Must specify `path` to the patch file
- Patches are applied in the order listed

## Advanced Examples

For more complex configurations and edge cases, see our [test fixtures](https://github.com/Azure/dalec/tree/main/test/fixtures). These examples demonstrate advanced source combinations and are used for testing various scenarios.
