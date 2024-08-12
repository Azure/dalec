---
title: Targets
---

This section provides an overview of the targets that Dalec supports. At this time, supported targets are `mariner2`, `azlinux3`, and `windowscross`.

Many components, such as package dependencies and base images, are specific to a distro or a subset of distros. The dalec spec allows you to move these distro specific things into a `target`.

## Available Targets

To print a list of available build targets:

```shell
GET                           DESCRIPTION
azlinux3/container (default)     Builds a container image for
azlinux3/container/depsonly      Builds a container image with only the runtime dependencies installed.
azlinux3/rpm                     Builds an rpm and src.rpm.
azlinux3/rpm/debug/buildroot     Outputs an rpm buildroot suitable for passing to rpmbuild.
azlinux3/rpm/debug/sources       Outputs all the sources specified in the spec file in the format given to rpmbuild.
azlinux3/rpm/debug/spec          Outputs the generated RPM spec file
azlinux3/worker                  Builds the base worker image responsible for building the rpm
debug/gomods                     Outputs all the gomodule dependencies for the spec
debug/resolve                    Outputs the resolved dalec spec file with build args applied.
debug/sources                    Outputs all sources from a dalec spec file.
mariner2/container (default)     Builds a container image for
mariner2/container/depsonly      Builds a container image with only the runtime dependencies installed.
mariner2/rpm                     Builds an rpm and src.rpm.
mariner2/rpm/debug/buildroot     Outputs an rpm buildroot suitable for passing to rpmbuild.
mariner2/rpm/debug/sources       Outputs all the sources specified in the spec file in the format given to rpmbuild.
mariner2/rpm/debug/spec          Outputs the generated RPM spec file
mariner2/worker                  Builds the base worker image responsible for building the rpm
windowscross/container (default) Builds binaries and installs them into a Windows base image
windowscross/worker              Builds the base worker image responsible for building the rpm
windowscross/zip                 Builds binaries combined into a zip file
```

## Dependencies

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

This works the same way in the `azlinux3` target but with the `azlinux3` base image
(not currently displayed here since it is still preview, this will be updated
once azlinux3 is GA) OR a build context named `dalec-azlinux3-worker`.
