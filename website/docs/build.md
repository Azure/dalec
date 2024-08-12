---
title: Building with Dalec
---

In this section, we'll go over how to build packages and containers with Dalec.

To get started, you'll need to have [Docker](https://docs.docker.com/engine/install/) installed.

## Creating a Dalec Spec

The Dalec spec is a YAML file that describes the package to be built and any customizations to the output image. It includes package metadata like name, version, packager, and other things typically found in a system package. It also includes a list of build and runtime dependencies, how to build the project to be packaged, and what files are included in the package.

For more information on the Dalec spec, see the [Dalec Specification](spec.md).

## Building from source(s)

Dalec can build packages and containers from source code repositories. This is done by specifying the source code repository and the build steps in the Dalec spec. The source code is checked out, built, and the resulting artifacts are included in the package.

For more information on building from source, see [Building from source](build-source.md).

## Virtual Packages

In addition to building a traditional package that installs binaries and other files you can also create a "virtual" package, which is a package that references other packages but doesn't install any files itself. This is useful for creating a package that is just a collection of dependencies.

For more information on virtual packages, see [Virtual Packages](virtual-packages.md).
