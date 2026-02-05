# PackageFiles Support for Make Install Workflows

This document describes the new `package_files` feature that allows projects using `make install` or similar installation commands to specify files for package creation without manually categorizing them into specific artifact types.

## Overview

Previously, dalec required explicitly categorizing artifacts into specific types (binaries, manpages, config files, etc.). This worked well for projects that manually copy specific files, but was cumbersome for projects using `make install` or similar standard installation mechanisms.

The new `package_files` feature allows you to specify the files that should be included in packages using package-specific file listing formats, while letting `make install` handle the actual file placement.

## Usage

### Basic Example

```yaml
name: my-app
description: Example using make install
version: 1.0.0
revision: 1
license: MIT

sources:
  src:
    git:
      url: https://github.com/example/my-app.git
      commit: main

build:
  steps:
    - command: |
        cd src
        make all
        make install DESTDIR=%{buildroot}

artifacts:
  package_files:
    rpm: |
      %{_bindir}/myapp
      %{_includedir}/myapp.h
      %{_mandir}/man1/myapp.1*
```

### Multiple Package Formats

You can specify file listings for different package formats:

```yaml
artifacts:
  package_files:
    rpm: |
      %{_bindir}/myapp
      %{_includedir}/myapp.h
      %{_mandir}/man1/myapp.1*
    deb: |
      usr/bin/myapp
      usr/include/myapp.h
      usr/share/man/man1/myapp.1*
```

### With Traditional Artifacts

You can mix `package_files` with traditional artifact specifications. When `package_files` is present for a package format, it takes precedence over traditional artifact categorization for that format:

```yaml
artifacts:
  # These will be used for package formats that don't have custom file listings
  binaries:
    src/myapp: {}
  
  # This will override the binaries specification for RPM packages
  package_files:
    rpm: |
      %{_bindir}/myapp
      %{_mandir}/man1/myapp.1*
```

### Integration with Make Install

The typical pattern is:

1. Use `make install DESTDIR=%{buildroot}` in your build steps to install files
2. Specify which installed files should be included in the package using `package_files`

```yaml
build:
  steps:
    - command: |
        cd src
        ./configure --prefix=/usr
        make all
        make install DESTDIR=%{buildroot}

artifacts:
  package_files:
    rpm: |
      %{_bindir}/*
      %{_libdir}/*.so.*
      %{_mandir}/man1/*
      %{_datadir}/my-app/*
```

## Package Format Specifics

### RPM

For RPM packages, use standard RPM macros and file specifications as they would appear in the `%files` section:

- `%{_bindir}/` - `/usr/bin/`
- `%{_libdir}/` - `/usr/lib64/` or `/usr/lib/`
- `%{_includedir}/` - `/usr/include/`
- `%{_mandir}/` - `/usr/share/man/`
- `%{_datadir}/` - `/usr/share/`
- `%{_sysconfdir}/` - `/etc/`

You can use glob patterns (`*`) and RPM file attributes as needed.

### DEB (Future)

DEB support follows similar patterns but uses DEB package paths (without leading `/`):

```yaml
package_files:
  deb: |
    usr/bin/myapp
    usr/lib/myapp.so.1
    usr/share/man/man1/myapp.1
```

## Benefits

1. **Simpler Configuration**: No need to manually map every file to an artifact category
2. **Standard Workflows**: Works naturally with `make install`, autotools, cmake, etc.
3. **Package Format Control**: Fine-grained control over what gets included in each package format
4. **Backward Compatible**: Existing specs continue to work unchanged

## Limitations

- Symlinks still need to be specified in the traditional `links` section
- User and group creation still uses the traditional `users` and `groups` sections
- Package-specific file listings take complete precedence - you cannot mix them with traditional artifact categorization for the same package format

## Migration

To migrate from traditional artifact specification:

1. Keep your existing build steps that use `make install`
2. Replace the `artifacts` section categories with a `package_files` entry
3. List the files using the package format's native file specification syntax

### Before

```yaml
artifacts:
  binaries:
    src/myapp: {}
  manpages:
    src/man/myapp.1: {}
  headers:
    src/myapp.h: {}
```

### After

```yaml
artifacts:
  package_files:
    rpm: |
      %{_bindir}/myapp
      %{_mandir}/man1/myapp.1*
      %{_includedir}/myapp.h
```