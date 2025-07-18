---
title: Targets
---

DALEC is designed to support building artifacts for a number of different
systems.
DALEC refers to these in the [spec](spec.md) as "targets".
When executing a build with Docker these targets can be specified with the
`--target=<target>` flag.

## Available Targets

DALEC includes a number of built-in targets that you can either use in your spec.

- `azlinux3` - Azure Linux 3
- `bullseye` - Debian 11 (Bullseye) (v0.11)
- `bookworm` - Debian 12 (Bookworm) (v0.11)
- `bionic` - Ubuntu 18.04 (Bionic) (v0.11)
- `focal` - Ubuntu 20.04 (focal) (v0.11)
- `jammy` - Ubuntu 22.04 (jammy) (v0.9)
- `noble` - Ubuntu 24.04 (noble) (v0.11)
- `windowscross` - Cross compile from Ubuntu Jammy to Windows

When specifying a "target" to `docker build --target=<target>` DALEC treats
`<target>` as a route (much like an HTTP path) and each of the above mentioned
targets have subroutes you can specfiy as well, e.g. `jammy/deb` to have DALEC
build and output just the deb package. What subroutes are available depend on
the underlying target implementation.

To print a list of available build targets:

```shell
$ docker buildx build --call targets --build-arg BUILDKIT_SYNTAX=ghcr.io/azure/dalec/frontend:latest - <<< "null"
```

import TargetsCLIOut from './examples/targets.md'

<details>
<summary>DALEC targets list output</summary>
<pre><TargetsCLIOut /></pre>
</details>

:::note
The above command is passing in a "null" value as the build spec and telling
buildkit to use the latest dalec version.
This output can change depending on version or spec you provide.
:::

To check the targets available for a specific spec you can just add `--call targets`
to your normal `docker build` command:

```shell
$ docker buildx build --call targets -f ./path/to/spec .
```

If the `--target=<val>` flag is set, the list of targets will be filtered based
on `<val>`.

Likewise if the spec file contains items in the `targets` section then the list
of available targets will be filtered to just the targets in the spec.

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

## Advanced Customization

### Worker images

In some cases you may need to have additional things installed in the worker
image that are not typically available in the base image. As an example, a
package dependency may not be available in the default package repositories.

You can have Dalec output an image with the target's worker image with
`<target>/worker>` build target, e.g. `--target=azlinux3/worker`. You can then
add any customizations and feed that back in via [source polices](#source-policies)
or [named build contexts](#named-build-contexts).


### Source Policies

`docker buildx build` has experimental support for providing a
[source policy](https://docs.docker.com/build/building/variables/#experimental_buildkit_source_policy)
which updates the base image ref used to create the worker image. This method
will update any and all references to the matched image used for any part of
the build. It also requires knowing the image(s) that are used ahead of time and
creating the right set of match rules and potentially having to update this in
the future if the worker image refs in Dalec change.

A finer grained approach is to use [named build contexts](#named-build-contexts).

### Named Build Contexts

`docker buildx build` has a flag called `--build-context`
([doc](https://docs.docker.com/reference/cli/docker/buildx/build/#build-context))
which allows you to provide additional build contexts apart from the main build
context in the form of `<name>=<ref>`. See the prior linked documentation for
what can go into `<ref>`.

In the `azlinux3` target, Dalec looks for a named context called either

1. The actual base image used internally for azlinux3
  i. `--build-context mcr.microsoft.com/azurelinux/base/core:3.0=<new ref>`
2. A build context named `dalec-azlinux3-worker`
  i. `--build-context dalec-azlinux3-worker=<new ref>`

If 1 is provided, then 2 is ignored.

This works the same way in the `azlinux3`:

1. The actual base image used internally for azlinux3
  i. `--build-context mcr.microsoft.com/azurelinux/base/core:3.0=<new ref>`
2. A build context named `dalec-azlinux3-worker`
  i. `--build-context dalec-azlinux3-worker=<new ref>`

### Target Defined Artifacts

There are some situations where you may want to have multiple builds and for those different
targets they may require different binaries to exist that are not globally applicable to all
of the builds. For example, `windowscross` may require specific artifacts (binaries, docs,
config files, etc.) that are not relevant to `azlinux3`, and vice versa.

To address this you can define artifacts per target. Target-defined artifacts will override
global (spec-defined) artifacts if there is a conflict. However, if a target does not define
an artifact, it will inherit artifacts from the global spec.

Here is an example:

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

For more details on how Artifacts are structured and configured, see the [Artifacts](artifacts.md) documentation.

### Target defined package metadata

`conflicts`, `replaces`, and `provides` can be defined at the target level in addition to the [globalspec level](spec.md#additional-metadata).
This allows you to define package metadata that is specific to a target.

```yaml
targets:
  azlinux3:
    package:
      conflicts:
        - "foo"
        - "bar"
      replaces:
        - foo"
      provides:
        - "qux"
```

## Special considerations

### Windows

When using the `windowscross` target you will need to make sure that binaries use the `.exe` extension.

```yaml
build:
  steps:
    - command: |
        go build -o _output/bin/dalec_example.exe
```

You can use the built-in `TARGETOS` build-arg to determine if the build is targeting Windows or not.
Alternatively you can use the built-in `DALEC_TARGET` build-arg to determine the target being built.

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

Since `windowscross` is intended for cross-compilation, the environment has the
following env vars set by default:

- `GOOS=windows` - ensures that by default `go build` produces a Windows binary

This can be overridden in your spec by either setting them in the `env` section
or in the actual build step script, which may be necessary if you need to
build tooling or other things first.

```yaml
build:
  env:
    GOOS: linux
  steps:
    - command: |
        go build -o _output/bin/dalec_example
```

```yaml
build:
  steps:
    - command: |
        GOOS=linux go build -o _output/bin/dalec_example
```
