# Contributing

Dalec welcomes contributions and suggestions!

## Terms

Most contributions require you to agree to a Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us the rights to use your contribution. For details, visit https://cla.opensource.microsoft.com.

## Code of Conduct

Dalec has adopted the CNCF Code of Conduct. Refer to our [Community Code of Conduct](CODE_OF_CONDUCT.md) for details.  For more information see the Code of Conduct FAQ or contact opencode@microsoft.com with any additional questions or comments.

## Contributing a patch

1. Submit an issue describing your proposed change to the repository in question. The repository owners will respond to your issue promptly.
2. Fork the desired repository, then develop and test your code changes.
3. Submit a pull request.

When you submit a pull request, a CLA bot will automatically determine whether you need to provide a CLA and decorate the PR appropriately (e.g., status check, comment). Simply follow the instructions provided by the bot. You will only need to do this once across all repos using our CLA.

## Development

### Code Generation

Some files in this project are automatically generated. If you modify struct definitions (especially `Spec` or `Source`), you may need to regenerate the corresponding code:

```bash
# Regenerate all generated files
go generate ./...

# Or regenerate specific files
go generate ./spec.go     # Regenerates spec_resolve_generated.go
go generate ./source.go   # Regenerates source_generated.go
```

See [docs/code-generation.md](docs/code-generation.md) for more details about the code generation system.

## Issue and pull request management

Anyone can comment on issues and submit reviews for pull requests. In order to be assigned an issue or pull request, you can leave a `/assign <your Github ID>` comment on the issue or pull request.
