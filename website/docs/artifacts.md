# Artifacts

Artifacts are used to configure what actually gets installed with a package.
Anything that needs to be installed needs an entry in the artifacts section.

There are different types of artifacts which are installed to different locations
on the target system.
What location this is depends on the target OS/distro and the kind of artifact.

## Artifact Configuration

Most artifact types share a common data type so can be configured similarly.
It is shown here as a reference which is linked to in the artifact descriptions
where it is pertinent.

Configuration options shared by most artifacts:

- *subpath*(string): The provided path is joined to the typical install path,
                     e.g. `/usr/bin/<subpath>`, where the artifact will be
                     installed to.
- *permissions*(octal): file permissions to apply to the artifact.

- *user*(string): user owner to apply to the artifact. This user must exist
                  on the target system.

- *group*(string): group owner to apply to the artifact. This group must exist
                   on the target system.

Artifacts are gathered after the [build](spec.md#build-section) phase.
The paths provided to artifacts must be relative to the base directory used
during the build.

:::note
Builds are not required to define a [build section](spec.md#build-section).
In this case the build phase output is the same as the build phase input, where
the build phase input is a directory with the [sources](spec.md#sources-section)
added based on their source name.
:::


### Binaries

Binaries are binary files that may be executed.
On Linux these would typically get installed into `/usr/bin`.

Binaries are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.

Example:

```yaml
artifacts:
  binaries:
    src/my_bin:
      subpath: ""
      permissions: 0o755
```

You may use a trailing wildcard to specify multiple binaries in a directory,
though behavior may differ between different OS's/distros.

### Libexec

Libexec files are additional executable files that may be executed by one of
the main package executables. On Linux these would typically get installed into
`/usr/libexec/` or `/usr/libexec/<main-executable-name>/`.

Files under libexec are a mapping of file path to [artifact configuration](#artifact-configuration).
If `subpath` is not supplied, the artifact will be installed in `/usr/libexec`
directly. The file path is the path to a file that must be available after the
build section has finished. This path is relative to the working directory of
the build phase *before* any directory changes are made.

Example:

```yaml
name: my_package

artifacts:
  # the following config will install my_bin at /usr/libexec/my package/my_bin
  libexec:
    src/my_bin:
```

You may use a trailing wildcard to specify multiple binaries in a directory,
though behavior may differ between different OS's/distros.

### Manpages

Manpages is short for manual pages.
On Linux these are typically installed to `/usr/share/man`

Manpages are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.


```yaml
artifacts:
  manpages:
    src/man/*:
      subpath: ""
      permissions: 0o644
```

You may use a trailing wildcard to specify multiple binaries in a directory,
though behavior may differ between different OS's/distros.

### Data Dirs

Data dirs are a list of read-only, architecture-independent data files.
On Linux these typically get placed into `/usr/share`.

Data dirs are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.


```yaml
artifacts:
  data_dirs:
    build_output/my_bin:
      subpath: ""
      permissions: 0o755
```

### Directories

Directories allows you to create new directories when installing the package.
Two types of directory artifacts are supported:

1. *config*: This is a directory where configuration files typically go, e.g. `/etc/my_package`.
2. *State*: This is directory for persistent state, typically in `/var/lib` on Linux.


Unlike many other artifact types, this does not reference any file produced
by build. Instead these are created as empty directories.

Example:

```yaml
artifacts:
  createDirectories:
    state:
      mystate:
        mode: 0o755
    config:
      myconfig:
        mode: 0o755
```

### Config Files

Config files are, depending on the package manager, specially marked as configuration.
Typically these go under `/etc` on Linux.

Config files are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.


```yaml
artifacts:
  configFiles:
    src/my_config.json:
      subpath: ""
      permissions: 0o644
```

### Docs

Docs are general documentation, not manpages, for your package.
On Linux these typically go under `/usr/share/doc/<package name>`

Docs are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.


```yaml
artifacts:
  docs:
    src/doc/info.md:
      subpath: ""
      permissions: 0o644
```

You may use a trailing wildcard to specify multiple binaries in a directory,
though behavior may differ between different OS's/distros.

### Licenses

Licenses are license files to be installed with the package.

Licenses are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.


```yaml
artifacts:
  licenses:
    src/LICENSE.md:
      subpath: ""
      permissions: 0o644
```

### Systemd

Systemd artifacts are used for installing systemd unit configurations.
Two different types of systemd configurations are currently supported:

1. Unit files - including services, sockets, mounts, or any other systemd unit type.
2. Drop-ins - Adds customization to an existing systemd unit

See the systemd documentation for more details on these types.

Example:

```yaml
artifacts:
  systemd:
    units:
      src/contrib/init/my_service.service:
        enable: false
        name: ""
        start: false
    dropins:
      src/contrib/init/customize-a-thing.service:
        enable: false
        name: ""
        start: false
```

### Libs

Libs are library files to be included with your package.
On Linux these typically go under `/usr/lib/<package>`.

Libs are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file that must be available after the build
section has finished. This path is relative to the working directory of the
build phase *before* any directory changes are made.


```yaml
artifacts:
  libs:
    my_output_dir/lib.o:
        subpath: ""
        permissions: 0o644
```

You may use a trailing wildcard to specify multiple binaries in a directory,
though behavior may differ between different OS's/distros.

### Links

Links are a list of symlinks to be included with the package.
Unlike most other artifact typtes, links do not reference any specific build
artifact but rather a literal source-to-target mapping for the symlink.
Symlink ownership can also be set (user and group). Note that this sets
ownership of the symlink itself, not the target file.

User and group SHOULD be specified as names and not as UID/GID numbers.
The user/group must exist on the target system OR be created as part of the
package installation via the [users](#users) and/or [groups](#groups) section(s).

Example:

This creates a symlink at /usr/bin/go pointing to /usr/lib/golang/go with the user and group.

```yaml
artifacts:
  links:
    - source: /usr/lib/golang/go
      dest: /usr/bin/go
      user: someuser
      group: somegroup
```

### Headers

Headers are header to be included with the package. On Linux these typically go
under `/usr/include/`.

Headers are a mapping of file path to [artifact configuration](#artifact-configuration).
The file path is the path to a file or directory that must be available after
the build section has finished. This path is relative to the working directory
of the build phase *before* any directory changes are made.

```yaml
artifacts:
  headers:
    src/my_header.h:
```

or for a directory:

```yaml
artifacts:
  headers:
    src/my_headers/:
```

Note that headers are not installed within a subdirectory of `/usr/include/`
with the name of the package. They are installed directly into `/usr/include/`.
For instance, for the above examples, the headers would be installed to
`/usr/include/my_header.h` and `/usr/include/my_headers/` respectively.

### Users

Users allow you to specify a list of users to be created when installing the
package.
In most cases this will require a shell to be available on the target system.

Example:

```yaml
artifacts:
  users:
    - name: myuser
```

### Groups

Groups allow you to specify a list of groups to be created when installing the
package.
In most cases this will require a shell to be available on the target system.

Example:

```yaml
artifacts:
  groups:
    - name: mygroup
```

## Automatic Stripping

Some builds may not work with binary stripping, in which case you can disable
automatic stripping by setting `disable_strip: true`
This is a global setting that applies to all artifacts only.

If you want some binaries stripped and others not, you will need to manually
strip them in the build phase.

## Automatic Dependency Resolution

Some package tooling, such as `rpmbuild` or `debbuild` both used in core DALEC,
will attempt to automatically resolve runtime dependencies for you based on
the artifacts you have specified.
This can include detecting binaries with linked libraries or even that
a shell script is included in the artifacts.

You can disable this behavior by setting `disable_auto_requires: true`

```
artifacts:
  disable_auto_requires: true
```

You must be careful when using this as it will now be up to you to ensure
that all runtime dependencies are specified in the spec rather than
relying on the tooling to do it for you.

How this works under the hood is dependent on the build tooling.
For rpmbuild setting this to true will set `AutoReq: no` in the resulting rpm
spec file.
For debbuild, DALEC will not include `${shlibs:Depends}` in the control file,
which DALEC normally includes by default.

Note that this setting has no effect on sysext targets because the packages are
installed without any dependency resolution.
