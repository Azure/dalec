---
title: Virtual Packages
---

In this section, we'll go over how to build virtual packages with Dalec. Virtual packages are packages that don't install any files themselves but instead reference other packages as dependencies. They are useful for creating a package that is just a collection of dependencies.

In this example, we'll build a virtual package that just installs other packages as dependencies.

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: my-package
version: 1.0.0
revision: "1"
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package that, when installed, triggers other packages to be installed
website: http://contoso.com

dependencies:
  runtime:
    my-package-foo:
    my-package-bar:
```

You can build it with:

```shell
docker build -t my-package-image:1.0.0 --target=mariner2 -f my-package.yml .
```

:::tip
You could also pass the dalec spec file via stdin `docker build -t my-package-image:1.0.0 -< my-package.yml`*
See [docker's documentation](https://docs.docker.com/engine/reference/commandline/build/) for more details on how you can pass the spec file to docker.
:::

This will produce a container image named `my-package-image:1.0.0` that has the `my-package` virtual package installed along with its runtime dependencies. By default, the produced container image is a [`scratch`](https://hub.docker.com/_/scratch/) container image that only contains the package and its dependencies. You can customize the base image to use for the produced container. Below is an example that uses the Azure Linux `core` image as the base image which includes a shell and other tools.

```yaml
# syntax=ghcr.io/azure/dalec/frontend:latest
name: my-package
version: 1.0.0
revision: "1"
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

You can also set other image settings like entrypoint/cmd, environment variables, working directory, labels, and more. Below is an example that sets the entrypoint to `/bin/sh -c`.

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
    my-package-foo:
    my-package-bar:

image:
    entrypoint: /bin/sh -c
```

Note how this is at the top level of the spec and not under a build target. This means that it applies to all targets, but can also be customized per target by adding it under a target.

```yaml
targets:
  mariner2:
    image:
      entrypoint: /bin/sh -c
```
