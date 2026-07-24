# Website

The Dalec documentation site is built with [Hugo](https://gohugo.io/) and the
[Hextra](https://github.com/imfing/hextra) theme.

## Theme dependency

Hugo uses Go modules as a package manager for themes, even though those themes
do not need to contain Go code. `website/go.mod` pins the Hextra version and
`website/go.sum` verifies the downloaded module. Hextra ships its templates,
CSS, JavaScript, FlexSearch implementation, and other assets inside that single
module, so it does not add transitive Go dependencies.

The website is a separate Go module because the Go command cannot see imports
declared in `hugo.yaml`. Keeping that module separate prevents routine
root-level `go mod tidy` commands from removing the Hextra requirement. Use
Hugo's module command when cleaning its metadata:

```console
$ cd website
$ hugo mod tidy
```

Do not run `go mod tidy` from `website`; the Go command cannot see imports from
`hugo.yaml` and may remove the Hextra requirement. Running `go mod tidy` from
the repository root only operates on Dalec's main module.

## Local preview

The repository's Go preview command builds the site with the pinned image in
`website/Dockerfile` and serves it through BuildKit at
`http://localhost:3000/dalec/`. Using the production base path locally catches
path handling problems before deployment:

```console
$ go run ./cmd/website
```

Use `--addr` to select another address:

```console
$ go run ./cmd/website --addr 127.0.0.1:3001
```

For live reload, install the Hugo Extended version pinned in
`website/Dockerfile` or later and run:

```console
$ hugo server --source website
```

## Production build

```console
$ hugo --source website --gc --minify
```

The generated site is written to `website/public`. The deploy workflow publishes
that directory to GitHub Pages. CI builds with `website/Dockerfile`, so
Dependabot updates the Hugo version used by both CI and the BuildKit preview.

## Netlify deploy previews

The root-level `netlify.toml` builds from `website`. GitHub Pages remains the
production site, using the base URL in `hugo.yaml`. Netlify supplies its
deployment URL to Hugo only for branch deploys and pull request previews, which
keeps their links and canonical URLs inside each preview.

The file overrides only the matching base directory, build command, and publish
directory from the Netlify dashboard. Other site settings remain unchanged.
Clear the obsolete package directory and functions directory in the dashboard;
the site does not use either setting. The old Yarn build command and
`website/build` publish directory can also be cleared because the repository
configuration now owns those values.
