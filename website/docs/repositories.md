---
title: Extra Repository Configs
---

When you need to reference packages from repositories not in the worker's
repository config you can use the `extra_repos` field in the [dependencies
section](spec#dependencies-section) to inject extra repositories at
the right time so those dependencies can be satisfied.

The `extra_repos` field takes a list of repository configs with the following
structure:

- **`keys`**
  A map of keys required to enable the configured repositories. Each key in
  this map is associated with a specific source and must be imported to allow
  the repositories to function as expected. The content of this is a
  [source](sources.md) just like in the sources section. 

- **`config`**
  A collection of repository configurations to add to the environment. The
  structure and format of these configurations are specific to the distribution
  being used (e.g., apt or yum configurations). The content of this is a
  [source](sources.md) just like in the sources section.

- **`data`**
  A list of additional data required to support the repository configurations.
  This may include files or other resources essential for certain
  configurations, particularly file-backed repositories that need data not
  already available in the environment. The content of this is a mount which
  takes a `dest` field for where the content should be mounted in the build env
  and a `spec` field, the content of which is a [source](sources.md) just like
  in the sources section. Usage of `data` is considered an advanced case and,
  aside from creating local repositories, is likely not necessary for most
  uses.

- **`envs`**
  Specifies the environments in which the repositories should be made
  available. Possible values are:
  - **`build`** - Adds repositories before installing build dependencies.
  - **`test`** - Adds repositories before installing test dependencies.
  - **`install`** - Adds repositories before installing the final package in a
    container build target.

These configurations are highly distribution specific.

:::tip
Be careful to name the key files properly depending on whether they are ascii armored (`*.asc`) or binary (`*.gpg`). 
Some package managers such as `apt` do not handle keys properly if they are not named with the correct extension.
:::

### Examples:

import MsftUbuntuRepo from './examples/repos/msft-ubuntu.yml.md'

<details>
<summary>Microsoft Ubuntu repository (APT)</summary>
<MsftUbuntuRepo />
</details>

import MsftAzlCNRepo from './examples/repos/msft-azl-cloud-native.yml.md'

<details>
<summary>Microsoft Azure Linux cloud-native repository (DNF)</summary>
<MsftAzlCNRepo />
</details>


:::note
For yum repos backed by the local filesystem, you may want to set `metadata_expire=0` in the repo config to avoid caching issues.
:::
