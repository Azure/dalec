# Artifacts

Artifacts define what gets installed with your package. Each file, binary, or configuration must be declared in the artifacts section.

## Common Configuration

Most artifacts share these configuration options:

- **`subpath`** - Subdirectory within the standard install path (e.g., `/usr/bin/<subpath>`)
- **`permissions`** - File permissions in octal format (e.g., `0o755`, `0o644`)

File paths are relative to the build working directory before any directory changes.


## File Artifacts

### Binaries

Executable files installed to `/usr/bin` (Linux):

```yaml
artifacts:
  binaries:
    src/my_bin:
      subpath: ""
      permissions: 0o755
```

### Libexec

Helper executables for main binaries, installed to `/usr/libexec/<package-name>`:

```yaml
artifacts:
  libexec:
    src/helper_bin:
      # Installs to /usr/libexec/my_package/helper_bin
```

### Libraries

Library files installed to `/usr/lib/<package>`:

```yaml
artifacts:
  libs:
    my_output_dir/lib.o:
      permissions: 0o644
```

### Headers

Header files installed to `/usr/include`:

```yaml
artifacts:
  headers:
    src/my_header.h:      # Single file
    src/my_headers/:      # Directory
```

## Documentation

### Manpages

Manual pages installed to `/usr/share/man`:

```yaml
artifacts:
  manpages:
    src/man/*:
      permissions: 0o644
```

### Docs

General documentation installed to `/usr/share/doc/<package-name>`:

```yaml
artifacts:
  docs:
    src/doc/info.md:
      permissions: 0o644
```

### Licenses

License files:

```yaml
artifacts:
  licenses:
    src/LICENSE.md:
      permissions: 0o644
```

## Configuration

### Config Files

Configuration files (marked specially by package managers), typically in `/etc`:

```yaml
artifacts:
  configFiles:
    src/my_config.json:
      subpath: ""
      permissions: 0o644
```

### Data Dirs

Read-only data files installed to `/usr/share`:

```yaml
artifacts:
  data_dirs:
    build_output/data:
      permissions: 0o755
```

### Directories

Create empty directories during package installation:

```yaml
artifacts:
  createDirectories:
    state:     # /var/lib directories
      mystate:
        mode: 0o755
    config:    # /etc directories
      myconfig:
        mode: 0o755
```

## System Integration

### Links

Create symlinks:

```yaml
artifacts:
  links:
    - source: /usr/lib/golang/go
      dest: /usr/bin/go
      user: someuser
      group: somegroup
```

### Systemd

Install systemd unit files and drop-ins:

```yaml
artifacts:
  systemd:
    units:
      src/contrib/init/my_service.service:
        enable: false
        name: ""
    dropins:
      src/contrib/init/customize.service:
        enable: false
        name: ""
```

### Users & Groups

Create users and groups during installation:

```yaml
artifacts:
  users:
    - name: myuser
  groups:
    - name: mygroup
```

## Advanced Configuration

### Wildcards

Use trailing wildcards to include multiple files from a directory:

```yaml
artifacts:
  binaries:
    src/bin/*:      # All files in src/bin/
      permissions: 0o755
```

:::note
Wildcard behavior may vary between operating systems and distributions.
:::

### Automatic Binary Stripping

Disable automatic binary stripping if it interferes with your build:

```yaml
artifacts:
  disable_strip: true
```

For selective stripping, manually strip binaries in the build phase.

### Automatic Dependency Resolution

Package managers automatically detect runtime dependencies from artifacts (linked libraries, shell scripts, etc.). Disable this behavior if you want full control:

```yaml
artifacts:
  disable_auto_requires: true
```

:::warning
When disabled, you must manually specify all runtime dependencies in your spec file.
:::
