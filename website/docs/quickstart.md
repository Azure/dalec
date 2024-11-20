---
title: Quickstart
---

In this section, we'll go over how to build packages and containers from source with Dalec. Note that, in this context, "source" refers to the source code of the package being built. Before continuing, it first useful to go over some preliminary background.

## Some Preliminaries

### But what actually *is* Dalec?

Dalec is what is known as a *frontend* for [Docker Buildkit](https://docs.docker.com/build/buildkit/frontend/) . If you've ever used a `Dockerfile` before (under newer versions of docker) you have interacted with a buildkit frontend. A frontend is a little like a compiler in that it translates higher level syntax into specific build instructions which the buildkit engine knows how to execute. Dalec, then, provides a specialized spec format for specifying particular artifacts -- in this case packages and containers -- and then translates that spec into build instructions to be run in buildkit. This is why Dalec has no dependencies other than Docker -- it actually becomes a component loaded during the docker build. 

### Targets 

First, a word on **targets**: A target refers to a specific output of a dalec build. Dalec can produce different types of outputs, such as RPMs, DEBs, and container images. 

### Stages of a Dalec Build

A Dalec build happens in up to three main stages, some of which are independent of each other:
1. **Package Build** - This is where the sources are checked out and built using the build steps defined in the spec file. The output of this phase is an actual package, such as an RPM or DEB. These steps execute in the build environment, which is a worker container image with the necessary build dependencies installed.
2. **Package Test** (optional) - This is where the package is installed in a clean environment and tested to ensure it was built correctly -- for example, to ensure that package artifacts are installed in the proper locations and have the correct permissions.
3. **Create Output Image** (optional) - This stage is independent of the first two; you may build a container image with Dalec *without* first building a package or running any tests; see [Container Only Builds](container-only-builds.md). However, for many use cases, a package will have been built in stage (1) and it will be installed in a base image for the resulting **output container image** to be created. There may be additional runtime dependencies specified in the spec file that are installed at this stage, and additional configuration of the image itself is also allowed. 


## Creating a Package and Container from Source

Now, without further ado, let's get started.

To do our build, we need a few things:

1. A list of sources to pull from
2. A build script to build the sources
3. A list of artifacts to include in the package

In this example, we'll build the `go-md2man` package and container from the [`go-md2man`](https://github.com/cpuguy83/go-md2man) repo using `v2.0.3` tag in the repo.

First, let's start with the constructing a [Dalec spec](spec.md) file.

We define the metadata for the package in the spec. This includes the name, packager, vendor, license, website, and description of the package.

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: go-md2man
version: 2.0.3
revision: "1"
license: MIT
description: A tool to convert markdown into man pages (roff).
packager: Dalec Example
vendor: Dalec Example
website: https://github.com/cpuguy83/go-md2man
```

:::tip
In metadata section, `packager`, `vendor` and `website` are optional fields.
:::

In the next section of the spec, we define the [sources](sources.md) that we will be pulling from. In this case, we are pulling from a git repository.

One thing to note, in many build systems you will not have access to the Internet while building the package, and indeed that is the case with the `mariner2` target. As such, this build will fail because `go build` will try to download the go modules. For this reason, we added a `generate` section to the source to run `go mod download` in a docker image with the `src` source mounted and then extract the go modules from the resulting filesystem.

```yaml
sources:
  src:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: "v2.0.3"
    generate:
      - gomod: {}
```

In the next section, we define the dependencies that are needed to build the package. In this case, we need the `golang` dependency at the build time, and `openssl-libs` at runtime. Build dependencies are dependencies that are needed to build the package, while runtime dependencies are dependencies that are needed to run the package, i.e., they will be installed alongside the package when it is installed on a system. Runtime dependencies are not required for this specific example, but they are included for illustrative purposes.

```yaml
dependencies:
  build:
    golang:
  runtime:
    # as stated above, included for illustrative purposes
    openssl-libs:
```

Now, let's define the build steps. In this case, we are building the `go-md2man` binary.

```yaml
build:
  # the env section allows us to define environment variables 
  # that will be set during the build process
  env:
    CGO_ENABLED: "0"
  steps:
    - command: |
        cd src
        go build -o go-md2man .
```

Next, we define the artifacts that we want to include in the package. In this case, we are including the `go-md2man` binary.

```yaml
artifacts:
  binaries:
    src/go-md2man:
```

The Image section defines the entrypoint and command for the image. In this case, we are setting the entrypoint to `go-md2man` and the command to `--help`.

```yaml
image:
  entrypoint: go-md2man
  cmd: --help
```

Finally, we can add a test case to the spec file which helps ensure the package is assembled as expected. The following test will make sure `/usr/bin/go-md2man` is installed and has the expected permissions. These tests are automatically executed when building the container image.

```yaml
tests:
  - name: Check file permissions
    files:
      # The generated package will install go-md2man to /usr/bin because it was listed explicitly as a "binary" artifact
      /usr/bin/go-md2man:
        permissions: 0755
```

Now, let's put it all together in a single file:

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: go-md2man
version: 2.0.3
revision: "1"
packager: Dalec Example
vendor: Dalec Example
license: MIT
description: A tool to convert markdown into man pages (roff).
website: https://github.com/cpuguy83/go-md2man

sources:
  src:
    generate:
      - gomod: {}
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: "v2.0.3"

dependencies:
  build:
    golang:

build:
  env:
    CGO_ENABLED: "0"
  steps:
    - command: |
        cd src
        go build -o go-md2man .

artifacts:
  binaries:
    src/go-md2man:

image:
  entrypoint: go-md2man
  cmd: --help

tests:
  - name: Check bin
    files:
      /usr/bin/go-md2man:
        permissions: 0755
```

:::note
The full example can be found at [docs/examples/go-md2man.yml](https://github.com/Azure/dalec/blob/main/docs/examples/go-md2man.yml)
:::

Now that we have a spec file, we can build the package and container using `docker`.

## Building using Docker

In this section, we'll go over how to actually *perform* a build with Dalec once the spec file as been written. Other applicable Docker commands (such as `--push` and others) will also apply to Dalec.

:::note
`mariner2` target here is an example. You can find more information about available targets in the [targets](targets.md) section.
:::

:::tip
These steps are independent of each other. You don't have to build an RPM first to build a container.
:::

### Building just an RPM package

To build an RPM package only, we can use the following command:

```shell
docker build -t go-md2man:2.0.3 -f docs/examples/go-md2man.yml --target=mariner2/rpm --output=_output .
```

This will create `RPM` and `SRPM` directories in the `_output` directory with the built RPM and SRPM packages respectively.

### Building a Container with the Package Installed

To build a container, we can use the following command:

```shell
docker build -t go-md2man:2.0.3 -f docs/examples/go-md2man.yml --target=mariner2 .
```

This will produce a container image named `go-md2man:2.0.3`.