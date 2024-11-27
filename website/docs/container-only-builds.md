---
title: Container-only builds
---


It is possible to use Dalec when you wish to build a minimal image from scratch or based on one of Dalec's supported distros (see [Targets](targets.md) for a list of these) with only certain packages installed. To do this, simply define a Dalec spec with only runtime dependencies specified. The resulting image will contain only the specified packages and their dependencies.

```yaml
name: my-minimal-image
version: 0.1.0
description: A minimal distroless image with only curl and shell access
revision: 1

dependencies:
    runtime:
        curl:
        bash:

image:
    entrypoint: /bin/bash

```

Then, to build:
`docker buildx build -f my-minimal-image.yml --target=mariner2 -t my-minimal-image:0.1.0 .`

This will produce a minimal image from `scratch` with `curl`, `bash`, and just a few other essential packages such as `prebuilt-ca-certificates` and `tzdata`. 

How does this work? Dalec will create a [Virtual Package](virtual-packages.md) which has only the specified runtime dependencies and install this in the target base image. This is where the `--target=mariner2` flag comes in. Even though the resulting image is from scratch, it will have the specified packages installed from mariner2 repos.

:::note
Dalec needs to use the [buildx cli](https://github.com/docker/buildx#manual-download) in order to interact with a buildkit builder. In newer versions of docker, `docker build` is an alias for `docker buildx build`, and so the `docker buildx` command can be used interchangeably with `docker build`. However, if unsure or using an old version of docker, use `docker buildx` to ensure compatibility.
:::