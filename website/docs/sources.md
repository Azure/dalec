# Sources

A "source" in Dalec is an abstraction for fetching dependencies of a build.
Usually this is source code but technically it could be anything.
The source abstraction enables fetching build sources over various protocols.

Some sources are considered as inherently file-based sources, like HTTP URLs or local directories.
Other sources are considered as inherently directory-based sources, like git repositories.
Depending on the source type, the behavior of certain things may be different, depending on the target implementation (e.g. mariner2, jammy, windows, etc).
Sources are injected into the root path of the build environment using the name of the source.

Ideally the content of a source is platform agnostic (e.g. no platform specific binaries).
These sources are used to create source packages for the target platform, such as an srpm or a debian dsc.
However, some source types may allow you to mount another source type in or are wrappers for other source types, like docker images or build sources respectively.
Only the output of a top-level source is included in the build environment.
These wrapper types (docker image, build) are useful for more advanced use-cases where you need to generate content or utilize other tooling in order to create the source.

## Top-level source configuration

For all source types, you can specify the following top-level configuration:

- `path`: The path to extract from the source type
- `includes`: A list of glob patterns to include from the source
- `excludes`: A list of glob patterns to exclude from the source
- `generate`: See [Generators](#Generators)

The below example uses a [`context`](#build-context) source type.
The root of the source is the `path/in/source` directory.
The source will include all `.txt` files within `path/in/source` except for `secret.txt`.

```yaml
sources:
  someSource:
    path: path/in/source
    includes:
      - "*.txt"
    excludes:
      - "secret.txt"
    context: {}
```

## Source Types

### Git

Git sources fetch a git repository at a specific commit.
You can use either an SSH style git URL or an HTTPS style git URL.

For SSH style git URLs, if the client (such as the docker CLI) has provided
access to an SSH agent, that agent will be used to authenticate with the git
server.

```yaml
sources:
  someSource1:
    git:
      # This uses an SSH style git URL.
      url: git@github.com:myOrg/myRepo.git
      commit: 1234567890abcdef
  someSource2:
    git:
      # This uses an HTTPS style git URL.
      url: https://github.com/myOrg/myRepo.git
      commit: 1234567890abcdef
      keepGitDir: true # [Optional] Keep the .git directory when fetching the git source. Default: false
```

By default, Dalec will discard the `.git` directory when fetching a git source.
You can override this behavior by setting `keepGitDir: true` in the git configuration.

Git repositories are considered to be "directory" sources.


### HTTP

HTTP sources fetch a file from an HTTP URL.
HTTP content is not verified by digest today, but it is in the roadmap.

```yaml
sources:
  someSource1:
    http:
      # No Digest verification
      url: https://example.com/someFile.txt
```

The HTTP source type is considered to be a "file" source.

### Build context

Clients provide a build context to Dalec.
As an example, here is how the Docker client provides a build context to Dalec:

```shell
$ docker build <some args> .
```

In this case the `.`, or current directory, is the build context.
Dalec is able to use the build context as a source:

```yaml
sources:
  someSource:
    context: {}
```

Note the empty brackets.
This is an unfortunate syntax requirement to not have `context` considered as a nil value.
This is the equivelent of the following:

```yaml
sources:
  someSource:
    context:
      name: "context"
```

Where `name: "context"`, not to be confused with the source type `context`, is named by convention by the docker CLI.
Additionally contexts can be passed in from the docker cli: `docker build --build-context <name>=<path>`.
The `<name>` would be the name to use in your yaml to access it.

This could also be written as below, since the `name: context` is the default and is the main build context passed in by the client:

```yaml
sources:
  someSource:
    context: {}
```

The `context` source type is considered to be a "directory" source.

### Inline

Inline sources are sources that are defined inline in the Dalec configuration.
You can only specify one of `file` or `dir` in an inline source.
Directories cannot be nested in inline sources.
Filenames must not contain a path separator (`/`).

```yaml
sources:
  someInlineFile:
    inline:
      # This is the content of the source.
      file:
        uid: 0
        gid: 0
        permissions: 0644
        contents: |
          some content
          some more content

  someInlineDir:
    inline:
      dir:
        uid: 0
        gid: 0
        permissions: 0755
        files:
          # This is the content of the source.
          file1:
            contents: |
              some content
              some more content
            permissions: 0644
            uid: 0
            gid: 0
          file2:
            contents: |
              some content
              some more content
            permissions: 0644
            uid: 0
            gid: 0
```

Inline sources with `file` are considered to be "file" sources.
Inline sources with `dir` are considered to be "directory" sources.

### Docker Image

Docker image sources fetch a docker image from a registry.
The output of this source is a directory containing the contents of the image.

```yaml
sources:
  someDockerImage:
    image:
      ref: docker.io/library/alpine:3.14
```

You can also run commands in the image before fetching the contents.
This is especially useful for generating content.

```yaml
sources:
  someDockerImage:
    image:
      ref: docker.io/library/alpine:3.14
      cmd:
        dir: / # Default path that command steps are executed in
        cache_dirs: null # Map of cache mounts. Default value: `null`
          /foo: {
            mode: shared # The other options are `locked` or `private`
            key: myCacheKey
            include_distro_key: false # Add the target key from the target being built into the cache key
            include_arch_key: false # add the architecture of the image to run the command in into the cache key
          }

        steps:
          - command: echo ${FOO} ${BAR}
            env: # Environment variables to set for the step
              FOO: foo
              BAR: bar
```

You can mount any other source type into the image as well.
Here's an example mounting an inline source, modifying it, and extracting the result:

```yaml
sources:
  someDockerImage:
    path: /bar # Extract `/bar` fromt he result of running the command in the docker image below
    image:
      ref: docker.io/library/alpine:3.14
      cmd:
        mounts: # Mount other sources into each command step
          - dest: /foo
            spec:
              inline:
                file:
                  uid: 0
                  gid: 0
                  permissions: 0644
                  contents: |
                    some content
                    some more content
        steps:
          - command: echo add some extra stuff >> /foo; mkdir /bar; cp /foo /bar

```

You can use the docker image source to produce any kind of content for your build.

Docker image sources are considered to be "directory" sources.

### Build

Build sources allow you to build a dockerfile and use the resulting image as a source.
It takes as input another source which must include the dockerfile to build.
The default dockerfile path is `Dockerfile` just like a normal docker build.

```yaml
sources:
  someBuild:
    build:
      source: # Specfy another source to use as the build context of this build operation
        git:
          url: https://github.com/Azure/dalec.git
          commit: v0.1.0
```

The above example will fetch the git repo and build the dockerfile at the root of the repo.

Here's another example using an inline source as the build source:

```yaml
  someBuild:
    path: /hello.txt
    build:
      dockerfile_path: other.Dockerfile # [Optional] Change dockerfile path. Default value: "Dockerfile"
      source:
        inline:
          dir:
            uid: 0
            gid: 0
            permissions: 0755
            files:
              Dockerfile:
                contents: |
                  FROM alpine:3.14 AS base
                  RUN echo "hello world" > /hello.txt

                  FROM scratch
                  COPY --from=base /hello.txt /hello.txt
```

You can also specify a `target` which is the name of the build stage to execute.
Build args can be specified as well as `args` which is a map of key value pairs.

Build sources are considered to be "directory" sources.

## Generators

Generators are used to generate a source from another source.
Currently the only generator supported is `gomod`.

### Gomod

The `gomod` generator manages a single go module cache for all sources that
specify it in the spec. It is expected that the build dependencies include a
go toolchain suitable for fetching go module dependencies.

Adding a gomod generator to 1 or more sources causes the following to occur automatically:

1. Fetch all go module dependencies for *all* sources in the spec that specify the generator
2. Keeps a single go module cache directory for all go module deps.
3. Adds the go module cache directory a source which gets included in source packages like a normal source.
4. Adds the `GOMODCACHE` environment variable to the build environment.

```yaml
sources:
  md2man:
    git:
        url: https://github.com/cpuguy83/go-md2man.git
        commit: v2.1.0
    generate:
        subpath: "" # path inside the source to use as the root for the generator
        gomod: {} # Generates a go module cache to cache dependencies
```

`gomod` currently does not have any options but may in the future.


## Patches

Dalec supports applying patches to sources. Patches must be specified in the
sources section just like any other type of source.
To apply a source as a patch to another source there is a patches section that
is a mapping of the source name you want to apply a patch to, to an ordered list
of sources that are the patch to apply.

```yaml
sources:
  md2man:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
        commit: v2.0.3
    generate:
      gomod: {} # Generates a go module cache to cache dependencies
  md2man-patch:
    http:
      url: https://github.com/cpuguy83/go-md2man/commit/fd6bc094ed445b6954a67df55e75d7db95fa8879.patch

patches:
  m2dman: # Name of the source we want to patch.
      # Each entry is a patch spec and points to a source listed in the `sources` section
    - source: md2man-patch # The name of the source that contains the patch
      path: "" # Path inside the patch source where the patch file is located.
    # Add more patches to the list (After adding them to the sources section) if needed
```

Each patch in the list of patch sources MUST be pointing to a file. When the
patch source is a file-based source, such as `http`, the `path` parameter in the
patch spec must not be set. When the patch is a directory-based source, such as
`context`, the `path` parameter in the patch spec must be set AND referencing a file
in the source.
See the source type definitions for if a source type is directory or file based.

Here is another example using a directory-backed source for patches. In the
example we'll also sow using multiple patch files from the same source.

```yaml
sources:
  md2man:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: v2.0.3
  localPatches:
    context: {}


patches:
  md2man:
    - source: localPatches
      path: patches/some0.patch
    - source: localPatches
      path: patches/some2.patch
```

Note: If you want to optimize the above example you can use the `includes`
feature on the context source so that only the needed files are fetched during
the build.

```yaml
sources:
  md2man:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: v2.0.3
  localPatches:
    context: {}
    includes:
      - patches/some0.patch
      - patches/some1.patch

patches:
  md2man:
    - source: localPatches
      path: patches/some0.patch
    - source: localPatches
      path: patches/some2.patch
```

Here is another example where we have a directory-based source with a subpath
defined on the source. Note that even if the subpath is pointing to a file, it
is still considered a directory-based source and still requires specifying a path
in the patch spec.

```yaml
sources:
  md2man:
    git:
      url: https://github.com/cpuguy83/go-md2man.git
      commit: v2.0.3
  localPatches:
    context: {}
    path: patches/some0.patch

patches:
  md2man:
    - source: localPatches
      path: some0.patch
```

## Advanced Source Configurations

You can see more advanced configurations in our [test fixtures](https://github.com/Azure/dalec/tree/main/test/fixtures).
These are in here to test lots of different edge cases and are only mentioned to provide examples of what might be possible
when these simple configurations are not enough.
The examples in that directory are not exhaustive and are not guaranteed to work in all cases or with all inputs and are
there strictly for testing purposes.

