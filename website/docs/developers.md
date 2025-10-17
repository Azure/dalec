---
title: Developer Guide
---

Welcome to the Dalec developer community! This guide will help you set up your development environment and understand the development workflow.

## Prerequisites

Before you start, make sure you have the following installed:

### Required Tools

- **Go**: [Download Go](https://go.dev/dl/)
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
# Run integration tests for a specific distribution
make test-integration SUITE=Mariner2

# Run all integration tests (takes 30-60+ minutes)
make test-integration
```

### 5. Build Docker Images (Optional)

If you need to test the frontend as a Docker image:

```bash
# Build the frontend Docker image (uses docker buildx bake)
make frontend

# Build examples
make examples

# Build and test with a specific target
docker build -t go-md2man:test \
  -f docs/examples/go-md2man.yml \
  --target=azlinux3/rpm \
  --output=_output .
```

**Note**: Docker builds may fail in some environments due to TLS certificate issues with proxy.golang.org. This is an environmental limitation. For development, host-compiled binaries are usually sufficient.

## Available Make Targets

The Makefile leverages `docker-bake.hcl` for build orchestration. Run `make help` to see all available targets:

```bash
make help
```

Key targets include:

- **Development:**
  - `make generate` - Generate required source files
  - `make lint` - Run linters via docker buildx bake
  - `make lint-local` - Run custom linters locally without Docker
  - `make fmt` - Format Go code

- **Building:**
  - `make build` - Build frontend image
  - `make frontend` - Build frontend Docker image using docker buildx bake
  - `make examples` - Build example specs

- **Testing:**
  - `make test` - Run unit tests
  - `make test-integration` - Run integration tests (use `SUITE=name` for specific test)
  - `make test-bake` - Run tests via docker buildx bake

- **Documentation:**
  - `make docs-serve` - Run documentation server (requires Node.js)
  - `make docs-build` - Build documentation static site
  - `make schema` - Generate JSON schema

- **Validation:**
  - `make verify` - Run all verification steps (generate, lint, test, check-generated)
  - `make check-generated` - Verify generated files are up to date

## Testing Your Changes

### Quick Tests (Run Frequently)

```bash
# Unit tests only
make test
```

### Understanding the Test Structure

Dalec has two types of tests:

#### Unit Tests (`--test.short`)

Located throughout the codebase alongside source files (e.g., `spec_test.go`, `source_test.go`). These tests:

- Run quickly (< 1 minute total)
- Don't require Docker or network access
- Test individual functions and components in isolation
- Are marked with the `-test.short` flag
- Should always pass before committing

```bash
# Run all unit tests
go test --test.short ./...

# Or use the Makefile
make test
```

#### Integration Tests

Located in the `test/` directory. These tests:

- Require Docker with BuildKit support
- Test full end-to-end build scenarios
- Take 30-60+ minutes to complete
- Test multiple Linux distributions and Windows
- Build actual packages and containers

**Integration Test Framework:**

The integration tests use a custom framework in `test/testenv/` that:

- Manages temporary Docker build contexts
- Provides helpers for building specs and inspecting results
- Runs tests in parallel where possible
- Supports testing against different target distributions

**Test files in `test/`:**

- `linux_target_test.go` - Tests for Linux package builds
- `target_*_test.go` - Distribution-specific tests (Debian, RPM, etc.)
- `source_test.go` - Tests for source fetching and generation
- `gomod_git_auth_test.go` - Tests for Go module authentication
- `windows_test.go` - Windows container build tests
- And more...

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
# Run all integration tests (45+ minutes)
make test-integration

# Or test a specific distribution
make test-integration SUITE=Mariner2
make test-integration SUITE=Azlinux3
make test-integration SUITE=Jammy
make test-integration SUITE=Bookworm
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
2. Run `make build` to compile the frontend image
3. Test with `make test`
4. For Docker testing: `make frontend`
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
