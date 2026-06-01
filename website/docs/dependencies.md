---
title: Dependencies
---

`PackageDependencies` specifies dependency information for a particular package.  This includes dependencies for runtime, which will be installed along with the package by the package manager, as well as any dependencies needed to build and/or test the resulting package. Configuration for any additional package repositories which are required may also be added here.

### Fields

- **Build**: The list of packages required to build the package.
    ```yaml
    build:
      package_name:
          version: [">=1.0.0", "<2.0.0"]
          arch: ["amd64", "arm64"]
    ```

- **Runtime**: The list of packages required to install/run the package.
    ```yaml
    runtime:
      package_name:
          version: [">=1.0.0", "<2.0.0"]
          arch: ["amd64", "arm64"]
    ```

- **Recommends**: The list of packages recommended to install with the generated package.
    ```yaml
    recommends:
      package_name:
          version: [">=1.0.0", "<2.0.0"]
          arch: ["amd64", "arm64"]
    ```

- **Sysext**: The list of packages to include in the generated system extension. No dependency resolution is performed when generating system extensions, so all required dependencies must be explicitly listed here.
    ```yaml
    sysext:
      package_name:
          version: [">=1.0.0", "<2.0.0"]
          arch: ["amd64", "arm64"]
    ```

:::note
Each of the above fields is a list of [PackageConstraints](https://pkg.go.dev/github.com/project-dalec/dalec#PackageConstraints).
:::

- **Test**: Lists and extra packages required for running tests. These are only installed for tests which have steps that require running a command in the built container. See [TestSpec](https://pkg.go.dev/github.com/project-dalec/dalec#TestSpec) for more information
    ```yaml
    test:
      - package_name_1
      - package_name_2
    ```

- **ExtraRepos**: Used to inject extra package repositories that may be used to satisfy package dependencies in various stages. 
    ```yaml
    extra_repos:
      - keys:
          mykey:
            http:
              URL: "https://example.com/mykey.gpg"
              permissions: 0644
        config: 
          myrepo:
            http:
              url: "https://example.com/myrepo.list"
        data: 
          - dest: "/path/to/dest"
            spec: source
        envs: ["build", "test", "install"] 
    ```
    See [repositories](repositories.md) for more details on repository configs.

### Example
```yaml
dependencies:
  build:
    gcc:
      version: [">=9.0.0"]
      arch: ["amd64"]
  runtime:
    libc6:
      version: [">=2.29"]
  recommends:
    curl:
      version: [">=7.68.0"]
  sysext:
    zstd:
      version: [">=1.5.0"]
  test:
    - bats
  extra_repos:
    - keys:
        mykey: 
          http:
            url: "https://example.com/mykey.gpg"
            permissions: 0644
      config:
        myrepo:
          http:
            url: "https://example.com/myrepo.list"
      # this assumes that a build context named `my-local-repo` containing a local repository will be passed 
      # to the Dalec build.
      # /opt/repo can now be referenced as a local repository in a repository config imported to dalec
      data:
        - dest: "/opt/repo"
          spec:
            context:
              name: "my-local-repo"
      envs: ["build", "install"]
```