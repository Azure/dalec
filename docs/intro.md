# Intro

Dalec is a tool for producing container images by first building packages
targeting the linux distribution used by the container image.
The final output image is a "distroless" container image with the package and all its dependencies installed.

Additionally other outputs can be produced such as source and binary packages, buildroots, and more.

## Spec

The dalec spec is a yaml file that describes the package to be built and any
customizations to the output image. It includes package metadata like name,
version, packager, and other things typically found in a system package. It
also includes a list of build and runtime dependencies, how to build the project
to be packaged, and what files are included in the package.

In addition to building a traditional package that installs binaries and other
files you can also create a "virtual" package, which is a package that
references other packages but doesn't install any files itself. This is useful
for creating a package that is just a collection of dependencies.

### Example

In this examnple wee'll build a virtual package that just installs other packages as dependencies.

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: my-package
version: 1.0.0
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package that, when installed, triggers other packages to be installed
website: http://contoso.com

dependencies:
    runtime:
        - my-package-foo
        - my-package-bar
```

You can build it with:

```bash
$ docker build -t my-package-image:1.0.0 --target=mariner2 -f my-package.yml .
```

*Note*: The `syntax` line tells docker the parser to use so it can understand the dalec spec format.

*Note: You could also pass the dalec spec file via stdin `docker build -t my-package-image:1.0.0 -< my-package.yml`*
*Note: See [docker's documentation](https://docs.docker.com/engine/reference/commandline/build/) for more details on how you can pass the spec file to docker.*

This will produce a container image named `my-package-image:1.0.0` that has the
`my-package` virtual package installed along with its runtime dependencies. The
produced container image is a "distroless" container image that only contains
the package and its dependencies. You can customize the base image to use for
the prooduced container. Below is an example that uses the mariner "core" image
as the base image which includes a shell and other tools.

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: my-package
version: 1.0.0
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package that, when installed, triggers other packages to be installed
website: http://contoso.com

dependencies:
    runtime:
        - my-package-foo
        - my-package-bar

targets:
    mariner2:
        image:
            base: mcr.microsoft.com/cbl-mariner/base/core:2.0
```

You can also set other image settings like entrypoint/cmd, environment
variables, working directory, labels, and more.
For now, the best place to find what all is available to set is to look at the
[code](../spec.go).

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: my-package
version: 1.0.0
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package that, when installed, triggers other packages to be installed
website: http://contoso.com

dependencies:
    runtime:
        - my-package-foo
        - my-package-bar

image:
    entrypoint: /bin/sh -c
```

Note how this is at the top level of the spec and not under a build target.
This means that it applies to all targets, but can also be customized per target by adding it under a target.

```yaml
targets:
    mariner2:
        image:
            entrypoint: /bin/sh -c
```

### Building from source(s)

Virtual packages are helpful but we need to build packages from source too.
To do this we'll need a few things:

1. A list of sources to pull from
2. A build script to build the sources
3. A list of artifacts to include in the package

Here we'll pull from a github repo.
It will use the `go-md2man` repo and build the `go-md2man` from the v2.0.3 tag in the repo.

*Note*: See the full example from [examples/go-md2man.yml](examples/go-md2man-1.yml)

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: go-md2man
version: 2.0.3
packager: Dalec Example
vendor: Dalec Example
license: MIT
description: A tool to convert markdown into man pages (roff).
website: https://github.com/cpuguy83/go-md2man

sources:
  src:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: "v2.0.3"

dependencies:
  build:
    golang:

build:
  env:
    GOROOT: /usr/lib/golang # Note: This is needed due to a bug in the golang package for mariner
    CGO_ENABLED: "0"
  steps:
    - command: |
        export GOMODCACHE="$(pwd)/gomods"
        cd src
        go build -o go-md2man .

artifacts:
  binaries:
    src/go-md2man:

image:
  entrypoint: go-md2man
  cmd: --help
```

In the `sources` section there is a single source called `src` that references
the github repo at tag v2.0.3. The name `src` is arbitrary, however this is
where the source will be checked out to in the build phase. You can add
multiple sources, and in the build phase they will be checked out to the name
you give them.

One thing to note, in many build systems you will not have access to the
internet while building the package, and indeed that is the case with the
`mariner2` target.
As such, this build will fail because `go build` will try to download the go modules.

What is actually happening with `sources` is the source is fetched and stored
such that it can be packed up into a "source package". What a source package
entails is dependent on the system. For rpm based systems this is an `srpm` or
`.src.rpm`, on debian based systems this is a `.dsc`. These packages contain
everything needed to build the package (aside from dependencies on other
packages).  Source packages can be published to a package repository and then
another system can download the source package and build it.

In the case of the above example, we need to include the go modules in the
list of sources.  We'll accomplish this by add a source which will run `go mod
download` in a docker image with the `src` source mounted and then extract the
go modules from the resulting filesystem.

*Note*: See the full example from [examples/go-md2man.yml](examples/go-md2man-2.yml)

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: go-md2man
version: 2.0.3
packager: Dalec Example
vendor: Dalec Example
license: MIT
description: A tool to convert markdown into man pages (roff).
website: https://github.com/cpuguy83/go-md2man

sources:
  src:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: "v2.0.3"
  gomods: # This is required when the build environment does not allow network access. This downloads all the go modules.
    path: /build/gomodcache # This is the path we will be extracing after running the command below.
    image:
      ref: mcr.microsoft.com/oss/go/microsoft/golang:1.21
      cmd:
        dir: /build/src
        mounts:
          # Mount a source (inline, under `spec`), so our command has access to it.
          - dest: /build/src
            spec:
              git:
                url: https://github.com/cpuguy83/go-md2man.git
                commit: "v2.0.3"
        steps:
          - command: go mod download
            env:
              # This variable controls where the go modules are downloaded to.
              GOMODCACHE: /build/gomodcache

dependencies:
  build:
    golang:

build:
  env:
    GOROOT: /usr/lib/golang # Note: This is needed due to a bug in the golang package for mariner
    CGO_ENABLED: "0"
  steps:
    - command: |
        export GOMODCACHE="$(pwd)/gomods"
        cd src
        go build -o go-md2man .

artifacts:
  binaries:
    src/go-md2man:

image:
  entrypoint: go-md2man
  cmd: --help
```

Finally, we can add a test case to the spec file which helps ensure the package is assembled as expected.
The following test will make sure `/usr/bin/go-md2man` is installed and has the expected permissions.
These tests are automatically executed when building the container image.
This can get added to the spec file like so:

```yaml
tests:
  - name: Check bin
    files:
      /usr/bin/go-md2man:
        permissions: 0755
```

### Targets

So far we've only really built a spec file asusming a single target distro (mariner2).
However many things, such as package dependencies and base images are specific to a distro or a subset of distros (e.g. Debian and Ubuntu).
The dalec spec allows you to move these distro specific things into a `target`.

Instead of specifying a package dependency at the root of the spec, you can specify it under a target.
This allows you to include different packages for different targets.

```yaml
targets:
    mariner2:
        dependencies:
            build:
                - golang

```

Dalec can never hope to support every distro, so it allows you to specify a custom builder image for a target that the build will be forwarded to.
This lets you keep the same spec file for all targets and use one `# syntax=` directive to build the package for any target.
It also allows you to replace the built-in targets with your own custom builder.

```yaml
targets:
    mariner2:
        frontend:
            image: docker.io/my/custom:mariner2
```

## Additional Reading

* Details on editor support in [editor-support.md](editor-support.md)
* More in-depth documentation for testing can be found in [testing.md](testing.md).
