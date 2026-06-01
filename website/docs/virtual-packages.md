---
title: Virtual Packages
---

Virtual packages are packages that don't install files themselves but reference other packages as dependencies. They're useful for creating meta-packages that group related dependencies together.

## Basic Virtual Package

Here's a simple virtual package that installs two dependencies:

```yaml
# syntax=ghcr.io/project-dalec/dalec/frontend:latest
name: my-package
version: 1.0.0
revision: "1"
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package that installs related dependencies
website: http://contoso.com

dependencies:
  runtime:
    my-package-foo:
    my-package-bar:
```

Build the package:

```shell
docker build -t my-package-image:1.0.0 --target=azlinux3 -f my-package.yml .
```

This creates a [`scratch`](https://hub.docker.com/_/scratch/) container with only the virtual package and its dependencies installed.

:::tip
You can also pass the spec via stdin: `docker build -t my-package-image:1.0.0 -< my-package.yml`
:::

## Customizing the Base Image

Use a different base image instead of scratch:

```yaml
# syntax=ghcr.io/project-dalec/dalec/frontend:latest
name: my-package
version: 1.0.0
revision: "1"
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package with a custom base image
website: http://contoso.com

dependencies:
  runtime:
    my-package-foo:
    my-package-bar:

targets:
  azlinux3:
    image:
      base: mcr.microsoft.com/azurelinux/base/core:3.0
```

## Image Configuration

Set image properties like entrypoint, environment variables, and working directory:

```yaml
# syntax=ghcr.io/project-dalec/dalec/frontend:latest
name: my-package
version: 1.0.0
revision: "1"
packager: Contoso
vendor: Contoso
license: MIT
description: A virtual package with custom image settings
website: http://contoso.com

dependencies:
  runtime:
    my-package-foo:
    my-package-bar:

image:
  entrypoint: /bin/sh
  cmd: ["-c"]
```

### Target-Specific Configuration

Override image settings for specific targets:

```yaml
targets:
  azlinux3:
    image:
      entrypoint: /bin/bash
      base: mcr.microsoft.com/azurelinux/base/core:3.0
```

This allows different configurations per build target while maintaining common defaults.
