---
title: Dalec Specification
---

The Dalec YAML specification is a declarative format for building system packages and containers.

:::note
All Dalec spec files must start with `# syntax=ghcr.io/azure/dalec/frontend:latest`.
:::

## Specification Overview

A Dalec spec consists of these main sections:

- **[Args](#args-section)** - Build arguments and variables
- **[Metadata](#metadata-section)** - Package information (name, version, license, etc.)
- **[Sources](#sources-section)** - Source code, patches, and files
- **[Dependencies](#dependencies-section)** - Build and runtime dependencies
- **[Build](#build-section)** - Build commands and environment
- **[Artifacts](#artifacts-section)** - Output files and binaries
- **[Image](#image-section)** - Container configuration
- **[Tests](#tests-section)** - Package validation tests
- **[Targets](#targets-section)** - Target-specific overrides
- **[Changelog](#changelog-section)** - Package change history

## Args Section

Define build arguments that can be passed to the spec:

```yaml
args:
  VERSION: 1.0.0
  COMMIT: 55019c83b0fd51ef4ced8c29eec2c4847f896e74
  REVISION: 1
```

### Built-in Arguments

Dalec provides these built-in arguments (opt-in by listing them):

```yaml
args:
  TARGETOS:      # Target OS (linux, windows, etc.)
  TARGETARCH:    # Target architecture (amd64, arm64, etc.)
  TARGETPLATFORM: # Combined platform (linux/amd64, etc.)
  TARGETVARIANT: # Platform variant
  DALEC_TARGET:  # Target name (mariner2, azlinux3, etc.)
```

:::note
Don't provide default values for built-in arguments. They're automatically set based on the build platform or target.
:::

## Metadata Section

Package metadata and information:

```yaml
name: My-Package
version: ${VERSION}
revision: ${REVISION}
license: Apache-2.0
description: This is a sample package
packager: Dalec Authors    # Optional
vendor: Dalec Authors      # Optional
website: https://github.com/foo/bar  # Optional
```

### Required Fields

- `name` - Package name
- `version` - Package version
- `revision` - Package revision
- `license` - Package license
- `description` - Package description

### Package Relationships

Control how your package interacts with others:

```yaml
# Packages that conflict with this one
conflicts:
  foo:
  bar:
    version:
      - ">=1.0.0"
      - "<2.0.0"

# Virtual packages this package provides
provides:
  foo:
  bar:
    version:
      - "= 1.0.0"

# Packages this one replaces
replaces:
  foo:
  bar:
    version:
      - "< 1.0.0"
```

:::tip
Fields starting with `x-` are ignored by Dalec, allowing custom metadata (e.g., `x-foo: bar`).
:::

## Sources Section

Define source code, patches, and files needed for the build:

```yaml
sources:
  # Git repository
  foo:
    git:
      url: https://github.com/foo/bar.git
      commit: ${COMMIT}
      keepGitDir: true
    generate:
      - gomod: {}  # Pre-download Go modules

  # HTTP file
  foo-patch:
    http:
      url: https://example.com/foo.patch

  # Inline content
  foo-inline:
    inline:
      - name: my-script
        content: |
          #!/bin/sh
          echo "Hello, World!"

  # Build context
  foo-context:
    context: {}
```

See [Sources](sources.md) for detailed configuration options.

## Dependencies Section

Specify packages needed at different stages:

```yaml
dependencies:
  build:           # Packages needed to build
    - golang
    - gcc
  runtime:         # Packages needed to run
    - libfoo
    - libbar
  recommends:      # Recommended packages
    - libcafe
  test:            # Packages needed for testing
    - kind
  extra_repos:     # Additional repositories
    - libdecaf
```

:::tip
Dependencies can be defined globally or per-target. Target dependencies override global ones. See [Dependencies](dependencies.md) and [Targets](targets.md).
:::

## Build Section

Configure the build environment and steps:

```yaml
build:
  env:
    TAG: v${VERSION}
    GOPROXY: direct
    CGO_ENABLED: "0"
    GOOS: ${TARGETOS}
  network_mode: sandbox    # Enable internet access
  caches:
    - gobuild:            # Go build cache
    - dir:               # Custom directory cache
        key: my_key
        dest: /my/cache/dir
  steps:
    - command: |
        go build -ldflags "-s -w -X github.com/foo/bar/version.Version=${TAG}" \
          -o /out/my-binary ./cmd/my-binary
```

- `env` - Environment variables for the build
- `steps` - Build commands to execute
- `network_mode` - Network access (`none`, `sandbox`, or empty for default)
- `caches` - Build caches that persist between builds

See [Caches](caches.md) for cache configuration details.

## Artifacts Section

Define what files to include in the package:

```yaml
artifacts:
  binaries:
    foo/my-binary: {}      # Binary executable
  manpages:
    src/man/man8/*:       # Man pages
      subpath: man8
  config:
    config/app.conf:      # Configuration files
      subpath: etc/myapp
```

See [Artifacts](artifacts.md) for all available artifact types and options.

## Image Section

Configure the output container image:

```yaml
image:
  base: mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0
  entrypoint: /my-binary
  cmd: ["--help"]
  post:
    symlinks:
      /usr/bin/my-binary:
        paths:
          - /my-binary
```

See [Images](image.md) for complete image configuration options.

## Tests Section

Define validation tests for your package:

```yaml
tests:
  - name: check permissions
    files:
      /usr/bin/my-binary:
        permissions: 0755

  - name: version reporting
    steps:
      - command: my-binary --version
        stdout:
          starts_with: "my-binary version ${VERSION}"
          contains:
            - "libseccomp: "
        stderr:
          empty: true
```

See [Testing](testing.md) for all available test types and assertions.

## Targets Section

Override configuration for specific build targets:

```yaml
targets:
  mariner2:
    image:
      base: mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0
    package_config:
      signer:
        image: azcutools.azurecr.io/azcu-dalec/signer:latest
  azlinux3:
    dependencies:
      build:
        - golang
  windowscross:
    artifacts:
      binaries:
        bin/my-app.exe: {}
```

See [Targets](targets.md) for complete target configuration options.

## Changelog Section

Document package changes:

```yaml
changelog:
  - author: John Doe <john.doe@example.com>
    date: 2025-04-24
    changes:
      - "Update build dependency on libfoo to version 2.0"
      - "Update upstream source to version 1.1.0"
```

The changelog is stored in package metadata and viewable through package manager tools.
