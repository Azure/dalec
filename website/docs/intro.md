---
title: Introduction
slug: /
---

Dalec is a project aimed at providing a declarative format for building system packages and containers from those packages.

Our goal is to provide a secure way to build packages and containers, with a focus on supply chain security.

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

👉 To get started with building packages and containers, please see [Quickstart](quickstart.md)!
