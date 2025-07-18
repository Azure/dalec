---
title: Targets
---

Dalec supports building artifacts for multiple systems called "targets". Targets determine which Linux distribution, package format, and build environment to use.

## Available Targets

Built-in targets you can use with `docker build --target=<target>`:

- `mariner2` - Azure Linux 2 (formerly CBL-Mariner)
- `azlinux3` - Azure Linux 3
- `bullseye` - Debian 11 (Bullseye)
- `bookworm` - Debian 12 (Bookworm)
- `bionic` - Ubuntu 18.04 (Bionic)
- `focal` - Ubuntu 20.04 (Focal)
- `jammy` - Ubuntu 22.04 (Jammy)
- `noble` - Ubuntu 24.04 (Noble)
- `windowscross` - Cross compile from Ubuntu 22.04 (Jammy) to Windows

### Target Subroutes

Each target supports subroutes for specific outputs (e.g., `jammy/deb` for just the DEB package).

List all available targets:

```shell
docker buildx build --call targets --build-arg BUILDKIT_SYNTAX=ghcr.io/azure/dalec/frontend:latest - <<< "null"
```

List targets for a specific spec:

```shell
docker buildx build --call targets -f ./path/to/spec .
```

import TargetsCLIOut from './examples/targets.md'

<details>
<summary>Example targets list output</summary>
<pre><TargetsCLIOut /></pre>
</details>

## Target-Specific Configuration

### Dependencies

Specify different dependencies for different targets:

```yaml
targets:
  azlinux3:
    dependencies:
      build:
        golang:
```

:::note
Target-level dependencies override root-level dependencies.
:::

### Custom Builder Images

Use custom builder images for extensibility:

```yaml
targets:
  azlinux3:
    frontend:
      image: docker.io/my/custom:azlinux3
```

## Dependencies

Many components, such as package dependencies and base images, are specific to
a distro or a subset of distros. The dalec spec allows you to move these distro
specific things into a `target`.

Instead of specifying a package dependency at the root of the spec, you can specify it under a target.
This allows you to include different packages for different targets.

:::note
Please note that dependencies under a target will override dependencies at the root level.
:::

```yaml
targets:
  azlinux3:
    dependencies:
      build:
        - golang
```

## Extensibility

Dalec can’t feasibly support every Linux distribution. Instead, it gives you the flexibility to specify a custom builder image for any target, directing the build process to that specified image.

This method allows for the use of a single spec file for all targets, employing one `#syntax=` directive to build the package for any specified target. It also permits the replacement of the default targets with custom builder configurations.

```yaml
targets:
  azlinux3:
    frontend:
      image: docker.io/my/custom:azlinux3
```

### Target-Specific Artifacts

Define different artifacts for different targets:

```yaml
targets:
  windowscross:
    artifacts:
      binaries:
        bin/windows-cross.exe:
          subpath: ""
          mode: 0o755
  azlinux3:
    artifacts:
      binaries:
        bin/linux-binary:
          subpath: ""
          permissions: 0o755
```

Target artifacts override global artifacts but inherit unspecified ones. See [Artifacts](artifacts.md) for more details.

### Package Metadata

Define target-specific package metadata:

```yaml
targets:
  azlinux3:
    package:
      conflicts:
        - "foo"
        - "bar"
      replaces:
        - "foo"
      provides:
        - "qux"
```

## Advanced Customization

### Custom Worker Images

When you need additional packages in the worker image, output the worker image first:

```shell
docker build --target=azlinux3/worker -t my-custom-worker .
```

Then customize it and use it via source policies or named build contexts.

### Named Build Contexts

Override worker images using `--build-context`:

```shell
# Method 1: Override the base image directly
docker build --build-context mcr.microsoft.com/azurelinux/base/core:3.0=my-custom-image .

# Method 2: Use named context
docker build --build-context dalec-azlinux3-worker=my-custom-image .
```

Target-specific context names:

- `dalec-mariner2-worker` for mariner2
- `dalec-azlinux3-worker` for azlinux3

## Windows Target Considerations

### File Extensions

Windows binaries must use the `.exe` extension:

```yaml
build:
  steps:
    - command: |
        go build -o _output/bin/dalec_example.exe
```

### Conditional Building

Use built-in build arguments for conditional logic:

```yaml
build:
  env:
    TARGETOS: ${TARGETOS}
  steps:
    - command: |
        if [ "$TARGETOS" = "windows" ]; then
          go build -o _output/bin/dalec_example.exe
        else
          go build -o _output/bin/dalec_example
        fi
```

Or use the target-specific variable:

```yaml
build:
  env:
    DALEC_TARGET: ${DALEC_TARGET}
  steps:
    - command: |
        if [ "$DALEC_TARGET" = "windowscross" ]; then
          go build -o _output/bin/dalec_example.exe
        else
          go build -o _output/bin/dalec_example
        fi
```

### Default Environment

The `windowscross` target sets `GOOS=windows` by default for cross-compilation. Override if needed:

```yaml
build:
  env:
    GOOS: linux  # Override for building tools first
  steps:
    - command: |
        go build -o _output/bin/dalec_example
```
