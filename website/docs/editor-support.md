---
title: Editor Support
---

We provide a [JSON schema file](https://github.com/project-dalec/dalec/blob/main/docs/spec.schema.json) to integrate with your editor.
This will help validate your yaml files and provide intellisense for the spec.

Alternatively, the schema can be fetched from the dalec frontend starting with v0.20:

### With an existing spec

```bash
docker buildx build --call dalec.schema -< path/to/spec
```

This loads the version of dalec set in the "syntax" directive of the spec file.

### Without a spec

```bash
# Provide an empty spec to stdin and set the buildkit frontend parser with the `BUILDKIT_SYNTAX` build arg.
docker buildx build --call dalec.schema --build-arg BUILDKIT_SYNTAX=ghcr.io/project-dalec/frontend:latest -<<<'{}'
```

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

1. Schema is locally available and enable it for yml files under multiple directories.

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

1. Directly reference schema from GitHub repository URL.

```json
{
    "yaml.schemas": {
        "https://raw.githubusercontent.com/project-dalec/dalec/<version>/docs/spec.schema.json" : "test/fixtures/*.yml"
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

## Vim

For vim there are 2 required vim plugins to add to your vimrc, though you may
find your own equivalents. The example below uses vim-plug to manage the plugins.
The first two listed below are required, the second two are recommended.

```
Plug 'prabirshrestha/vim-lsp'
Plug 'mattn/vim-lsp-settings'
Plug 'prabirshrestha/asyncomplete.vim'
Plug 'prabirshrestha/asyncomplete-lsp.vim'
```

The `mattn/vim-lsp-settings` is a generic plugin for installing and managing
LSP servers on your system.
See the [github](https://github.com/mattn/vim-lsp-settings) repo for more details
as well as links to the other mentioned plugins.

After the plugins are installed, while editing a yaml file you need to run the
following vim command to install the correct LSP, this only needs to be done one
time:

```
:LspInstallServer
```

Finally, in your project dir you can add a file `.vim-lsp-settings/settings.json`
where we'll put the yaml LSP config for the particular project. Here is an example
similar to the vscode example above:

```json
{
	"yaml-language-server": {
		"schemas": [
			{
				"fileMatch": ["test/fixtures/*.yml"],
				"url": "https://raw.githubusercontent.com/project-dalec/dalec/<version>/docs/spec.schema.json"
			}
		],
		"completion": true,
		"validate": true
	}
}
```

In the `"fileMatch"` section you can add the file patterns to associate the schema
with.
In this case,  `<version>`  should be replaced with the specific Dalec frontend
version  (i.e., `v0.6.1`) that is referenced in any files which match the
`fileMatch` pattern. To pick up whatever the latest schema is, released or not,
use `main`.

:::note
One or more of the above plugins depend on node.js and is known to work with
node >= 16.20 but may work with earlier releases.
:::
