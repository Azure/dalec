---
title: Dalec Specification
---

Dalec YAML specification is a declarative format for building system packages and containers from those packages.

This section provides a high level overview of the Dalec YAML specification. For more detailed information, please see the applicable sections.

:::note
All Dalec spec YAMLs must start with `# syntax=ghcr.io/azure/dalec/frontend:latest`.
:::

Dalec spec YAMLs are composed of the following sections:
- [Args section](#args-section)
- [Metadata section](#metadata-section)
- [Targets section](#targets-section)
  - [Image section](#image-section)
  - [Package Config section](#package-config-section)
- [Sources section](#sources-section)
- [Dependencies section](#dependencies-section)
- [Build section](#build-section)
- [Artifacts section](#artifacts-section)
- [Tests section](#tests-section)

## Args section

Args section is an optional section that is used to define the arguments that can be passed to the spec. These arguments can be used in the spec to define the version, commit, or any other fields.

```yaml
args:
  VERSION: 1.0.0
  COMMIT: 55019c83b0fd51ef4ced8c29eec2c4847f896e74
  REVISION: 1
```

There are a few built-in arguments which, if present, Dalec will substitute values for. They are listed as examples in the following block:

```yaml
args:
  TARGETOS:
  TARGETARCH:
  TARGETPLATFORM:
  TARGETVARIANT:
```

For example, upon invoking `docker build` with `--platform=linux/amd64`, we would have `TARGETOS=linux`, `TARGETARCH=amd64`, `TARGETPLATFORM=linux/amd64`.

:::note
No default value should be included for these build args. These args are opt-in. If you haven't listed them in the args section as shown above, Dalec will **not** substitute values for them.
:::

## Metadata section

Metadata section is used to define the metadata of the spec. This metadata includes the name, packager, vendor, license, website, and description of the spec.

```yaml
name: My-Package
license: Apache-2.0
description: This is a sample package
version: ${VERSION}
revision: ${REVISION}
packager: Dalec Authors
vendor: Dalec Authors
website: https://github.com/foo/bar
```

- `name`: The name of the package.
- `license`: The license of the package.
- `description`: The description of the package.
- `version`: The version of the package.
- `revision`: The revision of the package.
- `packager`: The packager of the package. This is an optional field.
- `vendor`: The vendor of the package. This is an optional field.
- `website`: The website of the package. This is an optional field.

:::tip
Any field at the top-level that begins with `x-` will be ignored by Dalec. This allows for custom fields to be added to the spec. For example, `x-foo: bar`. Any other field that is not recognized by Dalec will result in a validation error.
:::

## Targets section

Targets section is used to define configuration per target. Each target can have its own configuration.

```yaml
targets:
  mariner2:
    image:
      base: mcr.microsoft.com/cbl-mariner/distroless/minimal:2.0
      post:
        symlinks:
          /usr/bin/my-binary:
            path: /my-binary
    package_config:
      signer:
        image: azcutools.azurecr.io/azcu-dalec/signer:latest
  azlinux3:
    # same fields as above
  windowscross:
    # same fields as above
```

Valid targets are `mariner2`, `azlinux3`, `windowscross`.

For more information, please see [Targets](targets.md).

### Image section

Image section is used to define the base image and post processing for the image.

- `base`: The base image for the target.
- `post`: The post processing for the image, such as symlinks.

### Package Config section

Package Config section is used to define the package configuration for the target.

- `signer`: The signer configuration for the package. This is used to sign the package. For more information, please see [Signing Packages](signing.md).

## Sources section

Sources section is used to define the sources for the spec. These sources can be used to define the source code, patches, or any other files needed for the spec.

```yaml
sources:
  foo:
    git:
      url: https://github.com/foo/bar.git
      commit: ${COMMIT}
      keepGitDir: true
    generate:
    - gomod: {}
  foo-patch:
    http:
      url: https://example.com/foo.patch
  foo-inline:
    inline:
      - name: my-script
        content: |
          #!/bin/sh
          echo "Hello, World!"
  foo-context:
    context: {}
```

For more information, please see [Sources](sources.md).

## Dependencies section

The dependencies section is used to define the dependencies for the spec. These dependencies can be used to define the build dependencies, runtime dependencies, or any other dependencies needed for the package.

:::tip
Dependencies can be defined at the root level or under a target. If defined under a target, the dependencies will only be used for that target. Dependencies under a target will override dependencies at the root level. For more information, please see [Targets](targets.md).
:::

```yaml
dependencies:
  build:
    - golang
    - gcc
  runtime:
    - libfoo
    - libbar
```

## Build section

Build section is used to define the build steps for the spec. These build steps can be used to define the build commands, environment variables, or any other build configuration needed for the package.

```yaml
build:
  env:
    TAG: v${VERSION}
    GOPROXY: direct
    CGO_ENABLED: "0"
    GOOS: ${TARGETOS}
  steps:
    - command: |
        go build -ldflags "-s -w -X github.com/foo/bar/version.Version=${TAG}" -o /out/my-binary ./cmd/my-binary
```

- `env`: The environment variables for the build.
- `steps`: The build steps for the package.

:::tip
TARGETOS is a built-in argument that Dalec will substitute with the target OS value. For more information, please see [Args section](#args-section).
:::

## Artifacts section

Artifacts section is used to define the artifacts for the spec. These artifacts can be used to define the output of the build, such as the package or container image.

```yaml
artifacts:
  binaries:
     foo/my-binary: {}
  manpages:
    src/man/man8/*:
      subpath: man8
```

## Tests section

Tests section is used to define the tests for the spec. These tests can be used to define the test cases, steps, or any other tests needed for the package.

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

For more information, please see [Testing](testing.md).
