# Website

This website is built using [Docusaurus](https://docusaurus.io/), a modern static website generator.

### Building the docs

You can view the docs locally by running:

```console
$ go -C website run .
```

This will, by default, make the docs available on `http://localhost:3000/dalec/`.
You can customize the port with `--port <port>`.

```console
$ go -C website run . --port 3001
```

### Installation

```
$ yarn
```

### Local Development

```
$ yarn start
```

This command starts a local development server and opens up a browser window. Most changes are reflected live without having to restart the server.

### Build

```
$ yarn build
```

This command generates static content into the `build` directory and can be served using any static contents hosting service.

### Deployment

Using SSH:

```
$ USE_SSH=true yarn deploy
```

Not using SSH:

```
$ GIT_USER=<Your GitHub username> yarn deploy
```

If you are using GitHub pages for hosting, this command is a convenient way to build the website and push to the `gh-pages` branch.
