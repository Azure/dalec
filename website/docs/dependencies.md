---
title: Dependencies
---

Dependencies specify which packages are needed at different stages of the build and runtime process.

## Dependency Types

### Build Dependencies

Packages required to build your package:

```yaml
dependencies:
  build:
    gcc:
      version: [">=9.0.0"]
      arch: ["amd64"]
    golang:  # Simple form without constraints
```

### Runtime Dependencies

Packages required when your package is installed and running:

```yaml
dependencies:
  runtime:
    libc6:
      version: [">=2.29"]
    libssl3:  # Simple form
```

### Recommended Packages

Packages that enhance functionality but aren't required:

```yaml
dependencies:
  recommends:
    curl:
      version: [">=7.68.0"]
    wget:
```

### Test Dependencies

Packages needed only for running tests:

```yaml
dependencies:
  test:
    - bats
    - pytest
    - nodejs
```

## Package Constraints

You can specify version and architecture constraints for dependencies:

```yaml
dependencies:
  build:
    gcc:
      version: [">=9.0.0", "<12.0.0"]  # Version range
      arch: ["amd64", "arm64"]         # Specific architectures
  runtime:
    simple-package:  # No constraints - any version/arch
```

**Version operators:** `>=`, `>`, `<=`, `<`, `=`, `!=`

## Extra Repositories

Add custom package repositories to satisfy dependencies:

```yaml
dependencies:
  extra_repos:
    - keys:
        mykey:
          http:
            url: "https://example.com/mykey.gpg"
            permissions: 0644
      config:
        myrepo:
          http:
            url: "https://example.com/myrepo.list"
      data:
        - dest: "/opt/repo"
          spec:
            context:
              name: "my-local-repo"
      envs: ["build", "test", "install"]  # When to use this repo
```

See [Repositories](repositories.md) for detailed repository configuration.

## Complete Example

```yaml
dependencies:
  build:
    gcc:
      version: [">=9.0.0"]
      arch: ["amd64"]
    golang:
    make:

  runtime:
    libc6:
      version: [">=2.29"]
    openssl:

  recommends:
    curl:
      version: [">=7.68.0"]

  test:
    - bats
    - python3

  extra_repos:
    - keys:
        mykey:
          http:
            url: "https://example.com/mykey.gpg"
            permissions: 0644
      config:
        myrepo:
          http:
            url: "https://example.com/myrepo.list"
      data:
        - dest: "/opt/repo"
          spec:
            context:
              name: "my-local-repo"
      envs: ["build", "install"]
```

## Target-Specific Dependencies

Dependencies can be overridden per target. See [Targets](targets.md) for more information:

```yaml
dependencies:
  build:
    - gcc  # Global build dependency

targets:
  mariner2:
    dependencies:
      build:
        - clang  # Override for mariner2 target only
```
