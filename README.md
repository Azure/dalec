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

Dalec currently requires support for Buildkit's `DiffOp` and `MergeOp`.
These are currently not available with the default installation of docker.
To use this with docker you must
[enable the containerd image store](https://docs.docker.com/storage/containerd/#enable-containerd-image-store-on-docker-engine)
in the docker daemon config.

Alternatively you can use `docker buildx` to create a builder, which should have support for these features.

```console
$ docker buildx create --use
```

### Exmples:

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
debug/resolve                Outputs the resolved dalec spec file with build args applied.
mariner2                     Alias for target mariner2/container
mariner2/container (default) Builds a container with the RPM installed.
mariner2/rpm                 Builds an rpm and src.rpm for mariner2.
mariner2/rpm/buildroot       Outputs an rpm buildroot suitable for passing to rpmbuild.
mariner2/rpm/sources         Outputs all the sources specified in the spec file.
mariner2/rpm/spec            Outputs the generated RPM spec file
mariner2/toolkitroot         Outputs configs suitable for passing to the mariner2 build toolkit.
```

### Azure Linux (CBL-Mariner) Note

In order to comply with the Azure Linux team's requirements for building
packages, dalec uses the
[toolkit](https://github.com/microsoft/CBL-Mariner/tree/2.0/toolkit) provided by
the Azure Linux team to build packages rather than a raw rpmbuild.
The toolkit image is approximately 3GB in size and is automatically downloaded from
ghcr.io/azure/dalec/mariner2/toolchain:latest when needed.
If you need to supply a custom toolkit image you can use `--build-context
mariner2-toolkit=docker-image://<image-name>` to specify a custom toolkit image.
The docker cli also supports other formats than just `docker-image` for the
build context, see the [docker
documentation](https://docs.docker.com/engine/reference/commandline/buildx_build/#build-context)
for more information.
Alternatively you can use the experimental support for buildkit source policies:

```console
$ cat policy.json
{
    "rules": [
        {
            "action": "CONVERT",
            "selector": {
                "identifier": "docker-image://ghcr.io/azure/dalec/mariner2/toolchain:*"
            }
            "updates": {
                "identifier": "<ref>"
            }
        }
    ]
}
$ EXPERIMENTAL_BUILDKIT_SOURCE_POLICY=policy.json docker build -f test/fixtures/moby-runc.yml --target mariner2/rpm --output=_output .
```

In this example `<ref>` is the reference to the toolkit image you want to use which would follow the same format as described in the `build-context` example above.


Please note, the default toolkit image is optimized for dalec's use case and assumes that the worker_chroot.tar.gz is pre-built.

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


## Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a
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
