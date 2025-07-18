# Caches

Configure persistent caching across builds to improve build performance. Similar to Docker's `--mount=type=cache`.

## Cache Types

### Directory Cache

Generic directory caching with configurable keys and sharing modes:

```yaml
caches:
  - dir:
      key: my_cache
      dest: /path/to/cache
      sharing: shared  # shared, private, or locked
```

**Sharing modes:**
- `shared` - Cache shared between all builds
- `private` - Cache private to each build
- `locked` - Cache locked for thread-unsafe operations

**Auto-namespacing:** Dalec automatically namespaces cache keys by OS/architecture. Disable with:

```yaml
caches:
  - dir:
      key: my_cache
      dest: /path/to/cache
      no_auto_namespace: true
```


### Go Build Cache

Specialized cache for Go build artifacts. Always shared and auto-namespaced by OS/architecture:

```yaml
caches:
  - gobuild:
```

**Optional scope** for additional cache key differentiation:

```yaml
caches:
  - gobuild:
      scope: my_scope
```

**Auto-detection:** Dalec automatically creates a gobuild cache when Go is detected. Disable with:

```yaml
caches:
  - gobuild:
      disabled: true
```

### Bazel Cache

Specialized cache for Bazel build artifacts. Always shared and auto-namespaced by OS/architecture:

```yaml
caches:
  - bazel:
```

**How it works:** Sets `--disk_cache` flag in the system bazelrc file. If this conflicts with your project, use a [directory cache](#directory-cache) instead.

**Optional scope:**

```yaml
caches:
  - bazel:
      scope: my_scope
```

## Remote Cache Support

Bazel supports remote caching through socket forwarding. Dalec looks for a `bazel-default` socket ID provided by the buildkit client.

### Docker Buildx Setup

Requires buildx 0.25.0+ with socket forwarding:

```bash
docker buildx build --ssh socket=<path>,raw=true
```

### Custom Client Integration

Use the `sessionutil/socketprovider` package for custom buildkit clients:

- **`SocketProxy`** - Implements socket forwarding interface
- **`Mux`** - Routes socket requests to different backends

These implement the `session.Attachable` interface for the `SolveOpt.Session` field.

**Example remote cache servers:**

- [bazel-remote](https://github.com/buchgr/bazel-remote)
- Any [Bazel remote caching protocol](https://bazel.build/remote/caching) implementation
