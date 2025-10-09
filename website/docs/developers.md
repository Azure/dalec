---
title: Developer Guide
---

Welcome to the Dalec developer community! This guide will help you set up your development environment and understand the development workflow.

## Prerequisites

Before you start, make sure you have the following installed:

### Required Tools

- **Go 1.23 or later**: [Download Go](https://go.dev/dl/)
  - Verify installation: `go version`
- **Docker with BuildKit support**: [Install Docker](https://docs.docker.com/engine/install/)
  - Verify installation: `docker version` and `docker buildx version`
- **Git**: For version control
  - Verify installation: `git --version`
- **Make**: For running development tasks (usually pre-installed on Linux/macOS)
  - Verify installation: `make --version`

### Optional Tools

- **Node.js 18+**: For building and running the documentation site locally
  - Verify installation: `node --version`
  - [Download Node.js](https://nodejs.org/)
- **golangci-lint**: For additional linting (the Makefile can run linting in Docker if not installed)
  - [Install golangci-lint](https://golangci-lint.run/usage/install/)

## Setting Up Your Development Environment

### Fork and Clone the Repository

```bash
# Fork the repository on GitHub, then clone your fork
git clone https://github.com/<your-username>/dalec.git
cd dalec

# Add the upstream repository
git remote add upstream https://github.com/Azure/dalec.git
```

### Verify Your Setup

```bash
# Run a quick verification
make verify
```

This will:

- Generate required source files
- Run the custom linters
- Run unit tests
- Validate that generated files are up to date

## Development Workflow

We recommend the following iterative development workflow:

### 1. Create a Feature Branch

```bash
git checkout -b feature/your-feature-name
```

### 2. Make Your Changes

Edit the code using your favorite editor. Dalec is structured as follows:

- `cmd/` - CLI tools and binaries
  - `frontend/` - Main BuildKit frontend
  - `gen-jsonschema/` - JSON schema generator
  - `lint/` - Custom linters
- `targets/` - Target-specific build implementations (Linux RPM/DEB, Windows)
- `frontend/` - Core BuildKit frontend logic
- `test/` - Integration tests
- `website/docs/` - Documentation

### 3. Development Loop

While developing, use this tight feedback loop:

```bash
# 1. Generate any required code (if you modified generators)
make generate

# 2. Run custom linters to catch issues early
make lint

# 3. Run unit tests
make test

# 4. Build the frontend binary to verify compilation
make build
```

**Pro Tip**: You can run all verification steps at once:

```bash
make verify
```

### 4. Test Your Changes

After you're satisfied with your changes, run more comprehensive tests:

```bash
# Run all unit tests with verbose output
make test-verbose

# Run integration tests for a specific distribution
make test-integration SUITE=Mariner2

# Run all integration tests
make test-integration-all
```

### 5. Build Docker Images (Optional)

If you need to test the frontend as a Docker image:

```bash
# Build the frontend Docker image
make docker-frontend

# Build and test with a specific target
docker build -t go-md2man:test \
  -f docs/examples/go-md2man.yml \
  --target=azlinux3/rpm \
  --output=_output .
```

**Note**: Docker builds may fail in some environments due to TLS certificate issues with proxy.golang.org. This is an environmental limitation. For development, host-compiled binaries (via `make build`) are usually sufficient.

## Testing Your Changes

### Quick Tests (Run Frequently)

```bash
# Unit tests only
make test

# With verbose output
make test-verbose
```

### Validation Before Committing

**Always run before creating a pull request:**

```bash
make verify
```

This ensures:

- ✅ All unit tests pass
- ✅ Code passes custom linters
- ✅ Generated files are up to date
- ✅ No formatting issues

### Integration Tests (Run for Significant Changes)

Integration tests are comprehensive and time-consuming. Run them for significant changes or when modifying target-specific code:

```bash
# Test a specific distribution
make test-integration SUITE=Mariner2
make test-integration SUITE=Azlinux3
make test-integration SUITE=Jammy
make test-integration SUITE=Bookworm

# Run all integration tests
make test-integration-all
```

**Available test suites**: Mariner2, Azlinux3, Bookworm, Bullseye, Bionic, Focal, Jammy, Noble, Windows, Almalinux8, Almalinux9, Rockylinux8, Rockylinux9

### Testing Specific Components

```bash
# Test a specific package
go test -v ./cmd/frontend

# Test with a filter
go test -v -run TestSpecLoad ./...

# Test with race detection
go test -race ./...
```

## Using the Makefile

The Makefile provides convenient shortcuts for common development tasks.

### Essential Commands

| Command         | Description                        |
| --------------- | ---------------------------------- |
| `make help`     | Show all available commands        |
| `make generate` | Generate required source files     |
| `make lint`     | Run custom linters                 |
| `make lint-all` | Run golangci-lint + custom linters |
| `make test`     | Run unit tests                     |
| `make build`    | Build all binaries                 |
| `make verify`   | Run all verification steps         |
| `make clean`    | Clean build artifacts              |

### Build Commands

| Command                 | Description                                   |
| ----------------------- | --------------------------------------------- |
| `make build`            | Build all binaries                            |
| `make build-frontend`   | Build frontend binary only                    |
| `make build-redirectio` | Build dalec-redirectio only                   |
| `make install`          | Install binaries to `$GOBIN` or `$GOPATH/bin` |

### Docker Commands

| Command                | Description                 |
| ---------------------- | --------------------------- |
| `make docker-frontend` | Build frontend Docker image |
| `make docker-lint`     | Run linting in Docker       |

### Test Commands

| Command                              | Description                              |
| ------------------------------------ | ---------------------------------------- |
| `make test`                          | Run unit tests                           |
| `make test-verbose`                  | Run unit tests with verbose output       |
| `make test-short`                    | Run short unit tests only                |
| `make test-integration SUITE=<name>` | Run integration tests for specific suite |
| `make test-integration-all`          | Run all integration tests                |

### Documentation Commands

| Command           | Description                                          |
| ----------------- | ---------------------------------------------------- |
| `make docs-serve` | Serve documentation locally at http://localhost:3000 |
| `make docs-build` | Build documentation for production                   |
| `make schema`     | Generate JSON schema                                 |

## Common Development Tasks

### Adding a New Source Type

1. Add your source implementation to the appropriate file (e.g., `source_*.go`)
2. Run `make generate` to update generated code
3. Run `make lint` to check for issues
4. Add tests and run `make test`
5. Update documentation in `website/docs/`

### Adding a New Target

1. Create a new target directory under `targets/`
2. Implement the target interface
3. Register the target in `targets/register.go`
4. Add integration tests in `test/target_<name>_test.go`
5. Run `make verify` and `make test-integration SUITE=<YourTarget>`
6. Update documentation

### Modifying the Frontend

1. Make changes in `cmd/frontend/` or `frontend/`
2. Run `make build-frontend` to compile
3. Test with `make test`
4. For Docker testing: `make docker-frontend`
5. Run integration tests if modifying core logic

### Working on Documentation

```bash
# Install Node.js dependencies (first time only)
cd website
npm install
cd ..

# Start the documentation server
make docs-serve

# Open http://localhost:3000 in your browser
# Edit files in website/docs/ and see live changes
```

## Best Practices

### Before Committing

Always run:

```bash
make verify
```

### Code Style

- Follow standard Go conventions
- Run `gofmt` (included in `make lint-all`)
- Add comments for exported functions and types
- Write tests for new functionality

### Commit Messages

- Use clear, descriptive commit messages
- Reference issue numbers when applicable
- Follow conventional commits format when possible:
  - `feat:` for new features
  - `fix:` for bug fixes
  - `docs:` for documentation changes
  - `test:` for test additions/changes
  - `refactor:` for code refactoring

### Pull Requests

1. Ensure `make verify` passes
2. Add tests for new functionality
3. Update documentation if needed
4. Keep PRs focused on a single change
5. Respond to review feedback promptly

## Getting Help

- **Documentation**: [https://azure.github.io/dalec/](https://azure.github.io/dalec/)
- **Issues**: [https://github.com/Azure/dalec/issues](https://github.com/Azure/dalec/issues)
- **Discussions**: [https://github.com/Azure/dalec/discussions](https://github.com/Azure/dalec/discussions)

## Next Steps

- Read the [main CONTRIBUTING.md](https://github.com/Azure/dalec/blob/main/CONTRIBUTING.md) for CLA and contribution guidelines
- Explore the documentation to understand Dalec's features
- Look at [example specs](https://github.com/Azure/dalec/tree/main/docs/examples) to understand the declarative format
- Check out [good first issues](https://github.com/Azure/dalec/labels/good%20first%20issue)
