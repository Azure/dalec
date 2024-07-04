---
title: Targets
---

This section provides an overview of the targets that Dalec supports. At this time, supported targets are `mariner2`, `azlinux3`, and `windowscross`.

Many components, such as package dependencies and base images, are specific to a distro or a subset of distros. The dalec spec allows you to move these distro specific things into a `target`.

## Available Targets

To print a list of available build targets:

```shell
docker build --print=targets -f test/fixtures/moby-runc.yml .
TARGET                           DESCRIPTION
azlinux3/container (default)     Builds a container image for
azlinux3/container/depsonly      Builds a container image with only the runtime dependencies installed.
azlinux3/rpm                     Builds an rpm and src.rpm.
azlinux3/rpm/debug/buildroot     Outputs an rpm buildroot suitable for passing to rpmbuild.
azlinux3/rpm/debug/sources       Outputs all the sources specified in the spec file in the format given to rpmbuild.
azlinux3/rpm/debug/spec          Outputs the generated RPM spec file
debug/gomods                     Outputs all the gomodule dependencies for the spec
debug/resolve                    Outputs the resolved dalec spec file with build args applied.
debug/sources                    Outputs all sources from a dalec spec file.
mariner2/container (default)     Builds a container image for
mariner2/container/depsonly      Builds a container image with only the runtime dependencies installed.
mariner2/rpm                     Builds an rpm and src.rpm.
mariner2/rpm/debug/buildroot     Outputs an rpm buildroot suitable for passing to rpmbuild.
mariner2/rpm/debug/sources       Outputs all the sources specified in the spec file in the format given to rpmbuild.
mariner2/rpm/debug/spec          Outputs the generated RPM spec file
windowscross/container (default) Builds binaries and installs them into a Windows base image
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
