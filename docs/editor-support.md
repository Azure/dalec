# Editor Support

There is a [json schema file](spec.schema.json) which can be used in coms ecases to integrate with your editor.
This will help validate your yaml files and provide intellisense for the spec.

## VSCode

For VSCode you'll need to use a YAML plugin that supports JSON schemas, such as [YAML Support by Red Hat](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml).
Follow the plugins instructions to add the schema to your workspace.
Here is an example vscode config for my workspace:

```json
{
    "yaml.schemas": {
       "docs/spec.schema.json": "test/fixtures/*.yml" 
    }
}
```

You may find with this extension that null-able yaml objects will show as errors in the editor unless you specify the empty value. An example:

```
args:
    FOO:
```

In this example the json schema says that `FOO` should be a string but we've left it null which is perfectly valid yaml and will unmarshal to an empty string.
The yaml plugin will complain that it is an incorrect type. To fix this you can specify the empty string as the value:

```
args:
    FOO: ""
```
