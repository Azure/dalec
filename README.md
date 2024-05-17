# Dalec

Dalec is a project aimed at providing a declarative format for building system packages and containers from those packages.
Dalec is still under heavy development and the spec format is not currently considered stable.

Currently only support for Azure Linux (CBL-Mariner 2.0) is available but support
for other Linux distributions and operating systems is planned and is already [pluggable]().

## Usage

Dalec is currently provided as a buildkit frontend which enables you to use dalec with tools like `docker build`.
You can find the JSON schema for the dalec spec [in docs/spec.schema.json](./docs/spec.schema.json).

You will need to add a `syntax` directive at the top of your spec file to enable the dalec frontend:

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
```

### Examples:

You can look at the [test/fixtures](./test/fixtures) directory for examples of dalec specs.

Build an rpm for mariner2:

```console
$ docker build -f test/fixtures/moby-runc.yml --target mariner2/rpm --output=_output .
<output truncated>
```

With this command you should have a _output/RPMS/\<arch>, _output/RPMS/noarch, and _output/SRPMS directories with the built RPM and SRPM files.

Build a container for mariner2:

```console
$ docker build -f test/fixtures/moby-runc.yml --target mariner2/container -t test-runc-container:example .
```

This container will have the rpm along with all its dependencies installed.
The base image used for the container is determined by the target.
For mariner2 this is `mcr.microsoft.com/cbl-mariner/distroless/base:2.0`.
This can be customized in your yaml spec for the target.
Example:

```yaml
targets:
    mariner2:
        image:
            base: mcr.microsoft.com/cbl-mariner/base/core:2.0
```

Dalec will try to detect if the image is a "distroless" image by checking if the `rpm` binary is present.
If it is present then dalec assumes the image is *not* distroless and installs the rpm like normal.
When the image is distroless the rpm is installed and then the rpm database is cleaned up and
rpm manifests are created at `/var/lib/rpmmanifest`.

To print a list of available build targets:

```console
$ BUILDX_EXPERIMENTAL=1 docker build --print=targets -f test/fixtures/moby-runc.yml .
debug/gomods                 Outputs all the gomodule dependencies for the spec
debug/resolve                Outputs the resolved dalec spec file with build args applied.
debug/sources                Outputs all sources from a dalec spec file.
mariner2/container (default) Builds a container image for mariner2.
mariner2/container/depsonly  Builds a container image with only the runtime dependencies installed.
mariner2/rpm                 Builds an rpm and src.rpm for mariner2.
mariner2/rpm/debug/buildroot Outputs an rpm buildroot suitable for passing to rpmbuild.
mariner2/rpm/debug/sources   Outputs all the sources specified in the spec file in the format given to rpmbuild.
mariner2/rpm/debug/spec      Outputs the generated RPM spec file
```


## Support for other OSes and Linux distributions

Dalec can never hope to bake in support for every OS and Linux distribution.
Dalec is designed to be pluggable.
Even if the base dalec frontend does not support your distro/OS of choice, the spec format is designed to allow referencing a custom frontend.
This way you can support other distros/OSes without having to fork dalec, wait for a new release, or even change the `syntax` directive in your spec file.

Additionally, the structure of dalec is such that you can use dalec as a library to build your frontend.
In the `frontend` package you can register your build targets with `frontend.RegisterTarget` and then use `frontend.Build` as the buildkit `BuildFunc`
See `./cmd/frontend/main.go` for how this is used.

### Example

```yaml
# ...
targets:
  custom_distro:
    frontend:
        image: <frontend image>
# ...
```

## Docs

Docs are available at [https://azure.github.io/dalec/](https://azure.github.io/dalec/).


### Building the docs

You can view the docs locally by running:

```console
$ go -C website run .
```

This will, by default, make the docs available on `http://localhost:3000/dalec/`.
You can customize the port with `--port <port>`.

```console
$ go -C website run . --port 3001
```


## Contributing

This project welcomes contributions and suggestions. Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.opensource.microsoft.com.

When you submit a pull request, a CLA bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., status check, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

## Trademarks

This project may contain trademarks or logos for projects, products, or services. Authorized use of Microsoft
trademarks or logos is subject to and must follow
[Microsoft's Trademark & Brand Guidelines](https://www.microsoft.com/en-us/legal/intellectualproperty/trademarks/usage/general).
Use of Microsoft trademarks or logos in modified versions of this project must not cause confusion or imply Microsoft sponsorship.
Any use of third-party trademarks or logos are subject to those third-party's policies.
