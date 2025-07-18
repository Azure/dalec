# Dalec

## What is Dalec?

Dalec is a [Docker Buildkit frontend](https://docs.docker.com/build/buildkit/frontend/) that translates declarative YAML specifications into build instructions. Think of it as a specialized compiler that takes your package configuration and produces packages and containers across multiple Linux distributions and Windows.

## Why Use Dalec?

- **Unified Build Process**: Write once, build for multiple targets (Debian, Ubuntu, Azure Linux, Rocky Linux, etc.)
- **Supply Chain Security**: Built-in support for SBOMs, provenance attestations, and package signing
- **Docker-Native**: Leverages Docker Buildkit's caching, parallelization, and security features
- **Cross-Platform**: Build packages for different architectures and operating systems

## Features

- 🐳 No additional tools are needed except for [Docker](https://docs.docker.com/engine/install/)!
- 🚀 Easy to use declarative configuration
- 📦 Build packages and/or containers for a number of different [targets](https://azure.github.io/dalec/targets)
  - DEB-based: Debian, and Ubuntu
  - RPM-based: Azure Linux, Rocky Linux, and Alma Linux
  - Windows containers (cross compilation only)
- 🔌 Pluggable support for other operating systems
- 🤏 Minimal image size, resulting in less vulnerabilities and smaller attack surface
- 🪟 Support for Windows containers
- ✍️ Support for signed packages
- 🔐 Ensure supply chain security with build time SBOMs, and Provenance attestations

👉 To get started, please see [Dalec documentation](https://azure.github.io/dalec/)!

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

### Badges

[![OpenSSF Best Practices](https://www.bestpractices.dev/projects/10703/badge)](https://www.bestpractices.dev/projects/10703)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/Azure/dalec/badge)](https://scorecard.dev/viewer/?uri=github.com/Azure/dalec)
