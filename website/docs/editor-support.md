---
title: Editor Support
---

There is a [json schema file](https://github.com/Azure/dalec/blob/main/docs/spec.schema.json) which can be used to integrate with your editor.
This will help validate your yaml files and provide intellisense for the spec.

## VSCode

For VSCode you'll need to use a YAML plugin that supports JSON schemas, such as [YAML Support by Red Hat](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml).
Follow the plugins instructions to add the schema to your workspace.
Here are some examples of vscode workspace configs `settings.json` enabling the JSON schema:

1. Schema is locally available and enable it for yml files under a single directory.
```json
{
    "yaml.schemas": {
       "docs/spec.schema.json": "test/fixtures/*.yml"
    }
}
```
2. Schema is locally available and enable it for yml files under multiple directories.
```json
{
    "yaml.schemas": {
       "docs/spec.schema.json": [
            "test/fixtures/*.yml",
            "docs/**/*.yml"
        ]
    }
}
```
3. Directly reference schema from GitHub repository URL.
```json
{
    "yaml.schemas": {
        "https://raw.githubusercontent.com/Azure/dalec/<version>/docs/spec.schema.json" : "test/fixtures/*.yml"
    }
}
```

You may find with this extension that null-able yaml objects will show as errors in the editor unless you specify the empty value. An example:

```yaml
args:
    FOO:
```

In this example the json schema says that `FOO` should be a string but we've left it null which is perfectly valid yaml and will unmarshal to an empty string.
The yaml plugin will complain that it is an incorrect type. To fix this you can specify the empty string as the value:

```yaml
args:
    FOO: ""
```
