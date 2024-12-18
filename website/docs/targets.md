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

- `mariner2` - Azure Linux 2 (formerly CBL-Mariner)
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
  mariner2:
    dependencies:
      build:
        - golang
```


## Extensibility

Dalec can’t feasibly support every Linux distribution. Instead, it gives you the flexibility to specify a custom builder image for any target, directing the build process to that specified image.

This method allows for the use of a single spec file for all targets, employing one `#syntax=` directive to build the package for any specified target. It also permits the replacement of the default targets with custom builder configurations.

```yaml
targets:
  mariner2:
    frontend:
      image: docker.io/my/custom:mariner2
```

## Advanced Customization

### Worker images

In some cases you may need to have additional things installed in the worker
image that are not typically available in the base image. As an example, a
package dependency may not be available in the default package repositories.

You can have Dalec output an image with the target's worker image with
`<target>/worker>` build target, e.g. `--target=mariner2/worker`. You can then
add any customizations and feed that back in via [source polices](#source-policies)
or [named build contexts](#named-build-contexts).


#### Source Policies

`docker buildx build` has experimental support for providing a
[source policy](https://docs.docker.com/build/building/variables/#experimental_buildkit_source_policy)
which updates the base image ref used to create the worker image. This method
will update any and all references to the matched image used for any part of
the build. It also requires knowing the image(s) that are used ahead of time and
creating the right set of match rules and potentially having to update this in
the future if the worker image refs in Dalec change.

A finer grained approach is to use [named build contexts](#named-build-contexts).

#### Named Build Contexts

`docker buildx build` has a flag called `--build-context`
([doc](https://docs.docker.com/reference/cli/docker/buildx/build/#build-context))
which allows you to provide additional build contexts apart from the main build
context in the form of `<name>=<ref>`. See the prior linked documentation for
what can go into `<ref>`.

In the `mariner2` target, Dalec looks for a named context called either

1. The actual base image used internally for mariner2
  i. `--build-context mcr.microsoft.com/cbl-mariner/base/core:2.0=<new ref>`
2. A build context named `dalec-mariner2-worker`
  i. `--build-context dalec-mariner2-worker=<new ref>`

If 1 is provided, then 2 is ignored.

This works the same way in the `azlinux3`:

1. The actual base image used internally for azlinux3
  i. `--build-context mcr.microsoft.com/azurelinux/base/core:3.0=<new ref>`
2. A build context named `dalec-mariner2-worker`
  i. `--build-context dalec-azlinux3-worker=<new ref>`

#### Windows

For Windows containers, typically the container image OS needs to match
the Windows host OS.

You can use DALEC to create a single multi-platform image with the different
Windows versions you want to use.
Normally you would specify a single base image in the DALEC spec's image config,
however this is not sufficient to accomplish this task.

With DALEC you can pass in a build-arg `DALEC_WINDOWSCROSS_BASES_PATH` the value
of which should be the path to a file containing json with the following
structure to the `windowscross/container` build target:


```json
{
    "refs": [
        "mcr.microsoft.com/windows/nanoserver:1809",
        "mcr.microsoft.com/windows/nanoserver:ltsc2022"
    ]
}
```

:::note
Values in the "refs" field can be any Windows image.

You can provide any number of images here, however each image must have a
different value for the `os.version` field in the image config's platform.
If there are images with the same platform values the build will fail.
:::

You can also provide this file in a named build context, but you must still
specifiy the above mentioned build-arg so that DALEC knows how to find the file
in that named context.
You can tell DALEC to use a named context by providing the name in a build-arg
`DALEC_WINDOWSCROSS_BASES_CONTEXT`

