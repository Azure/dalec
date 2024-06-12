# Signing Packages

:::note
Available with Dalec release `v0.3.0` and later.
:::

Packages can be automatically signed using Dalec. To do this, you will
need to provide a signing frontend. There is an example in the test
code `test/signer/main.go`. Once that signing image has been built and
tagged, the following can be added to the spec to trigger the signing
operation:

```yaml
targets: # Distro specific build requirements
  mariner2:
    package_config:
      signer:
        image: "ref/to/signing:image"
        cmdline: "/signer"
```

At this time, these targets can leverage package signing:

- `windowscross/zip`
- `mariner2/rpm`
- `windowscross/container`

For container targets, only the artifacts within the container get signed.

This will send the artifacts (`.rpm`, `.deb`, or `.exe`) to the
signing frontend as the build context.

The contract between dalec and the signing image is:

1. The signing image will contain both the signing frontend, and any
additional tooling necessary to carry out the signing operation.
1. The `llb.State` corresponding the artifacts to be signed will be
provided as the build context.
1. Dalec will provide the value of `dalec.target` to the frontend as a
`FrontendOpt`. In the above example, this will be `mariner2`.
1. The response from the frontend will contain an `llb.State` that is
identical to the input `llb.State` in every way *except* that the
desired artifacts will be signed.

