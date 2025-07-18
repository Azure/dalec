# Signing Packages

Dalec can automatically sign packages using a custom signing frontend. Configure signing through the spec or build arguments.

## Quick Start

Add signing configuration to your spec:

```yaml
name: my-package
targets:
  azlinux3:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
```

**Supported targets:**
- `mariner2/rpm`
- `azlinux3/rpm`
- `windowscross/zip`
- `windowscross/container`

For container targets, only artifacts within the container are signed.

## Configuration Methods

### 1. In Spec (Recommended)

Target-specific configuration:

```yaml
targets:
  azlinux3:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
```

Global configuration (all targets):

```yaml
package_config:
  signer:
    image: "ref/to/signing:image"
    cmdline: "/signer"
```

### 2. Build Arguments

Use build arguments for external configuration:

- `DALEC_SIGNING_CONFIG_PATH` - Path to signing config file
- `DALEC_SIGNING_CONFIG_CONTEXT_NAME` - Named context containing config
- `DALEC_SKIP_SIGNING=1` - Disable signing

**Precedence order:**

1. `DALEC_SKIP_SIGNING` (highest)
2. Build context configs
3. Spec configuration (lowest)

## Build-Time Customization

### Secrets

Pass secrets from the Docker CLI to the signer:

```bash
docker buildx build --secret id=mysecret,src=./secret.txt
```

The signer must be configured to consume these secrets. No spec changes required.

### Named Contexts

Provide additional data to the signer:

```bash
docker buildx build --build-context signing-config=./config-dir
```

Multiple named contexts are supported. The signer accesses these by name.

### Build Arguments

Pass key-value pairs to the signer:

```yaml
args:
  SIGNING_KEY_ID: ""
  CERT_PATH: "default_cert.pem"

targets:
  azlinux3:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
        args:
          KEY_ID: "${SIGNING_KEY_ID}"
          CERTIFICATE: "${CERT_PATH}"
```

## Signer Contract

The signing frontend must follow this contract:

1. **Image contains:** Signing frontend and required tooling
2. **Input:** Artifacts to sign provided as build context
3. **Target info:** Dalec provides target name (e.g., `azlinux3`) as `FrontendOpt`
4. **Output:** Identical build context with signed artifacts
