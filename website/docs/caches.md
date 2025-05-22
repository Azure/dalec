# Caches

In addition to the standard step-level caching that buildkit provides, you can
also configure incremental caching that persists across builds.
This is similar to Dockerfiles `--mount=type=cache`.

You can provide multiple cache configurations:

```yaml
caches:
  - dir:
      key: my_key
      dest: /my/cache/dir
      sharing: shared
  - dir:
      key: my_other_key
      dest: /my/other/cache/dir
      sharing: private
  - gobuild:
  - bazel:
```

## Dir Cache

Dir caches are just generic directories where you choose the cache key and sharing mode.

```yaml
caches:
  - dir:
      key: my_key
      dest: /my/cache/dir
      sharing: shared
```

Supported sharing modes are:
- `shared`: The cache is shared between all builds.
- `private`: The cache is private to the build.
- `locked`: The cache is locked to the build. This is useful for caching directories that are not thread-safe.

By default, dalec will namespace these directories with a key tied to the OS and
CPU architecture which is prepended to the key you provide. This is to help
prevent common issues one would see for specific use-cases such as storing
incrmental compiler caches.
You can disable this behavior by setting the `no_auto_namespace` option to `true`.

```yaml
caches:
  - dir:
      key: my_key
      dest: /my/cache/dir
      sharing: shared
      no_auto_namespace: true
```

This will disable the automatic namespacing and use the key you provide as-is.


## Gobuild Cache

The gobuild cache is a special type of cache that is used to cache the results of
the `go build` command.
This is useful for caching the results of the build, such as the binary or
libraries.

```yaml
caches:
  - gobuild:
```

Go build caches are always in `shared` mode and are always namespaced with the OS and CPU architecture.

An optional `scope` can be provided which is added to the generated cache key.
This is intended for internal testing purposes, however may be useful for other
use-cases as well.

```yaml
caches:
  - gobuild:
      scope: my_scope
```

Finally, when go is detected in the build environment dalec will automatically
create a gobuild cache for you. This can be disabled by setting the `disabled`
option to `true` in the cache definition.

```yaml
caches:
  - gobuild:
      disabled: true
```

## Bazel Cache

The bazel cache is a special type of cache that is used to cache the results of
the `bazel build` command.

```yaml
caches:
  - bazel:
```

Bazel caches are always in `shared` mode and are always namespaced with the OS and CPU architecture.
This relies on setting the `--disk_cache` flag on `bazel build` by adding it to the *system* bazelrc file
when dalec sets up the build environment.
Dalec does not check if this is overwritten in the user or project bazelrc files.
If this conflicts with your project, you may need to manually manage the bazel cache with a [cache dir](#dir-cache).

An optional `scope` can be provided which is added to the generated cache key.
This is intended for internal testing purposes, however may be useful for other
use-cases as well.

```yaml
caches:
  - bazel:
      scope: my_scope
```
