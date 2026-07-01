# watchur

`watchur` is a small recursive file watcher that runs a command when matching files change.

It is intended for local development loops where you want one command to be re-run as files move under a directory tree.

## Behavior

`watchur` watches directories recursively and debounces bursts of filesystem events into a single command execution.

It only considers modifications observed after the program starts. Files that were already modified before `watchur` begins are treated as baseline state and do not trigger a replay just because they already differ from some external tool like Git.

By default, `watchur` also performs one initial run at startup. Use `--no-initial-run` if you only want post-start filesystem activity to trigger the command.

## Install

Use the install script:

```bash
curl -sSL https://raw.githubusercontent.com/phillarmonic/watchur/master/install.sh | bash
```

Install a specific version:

```bash
curl -sSL https://raw.githubusercontent.com/phillarmonic/watchur/master/install.sh | bash -s vX.Y.Z
```

Install with Go:

```bash
go install github.com/phillarmonic/watchur/cmd/watchur@latest
```

Install a specific tagged version with Go:

```bash
go install github.com/phillarmonic/watchur/cmd/watchur@vX.Y.Z
```

The installer downloads the release binary for your platform and installs it to `$HOME/.local/bin` by default on Unix systems or `$HOME/bin` on Windows-compatible shells.

Check the installed version:

```bash
watchur --version
```

## Usage

```bash
watchur --run "<command>" [flags]
```

Example:

```bash
watchur --dir . --extensions "*.go" --run "go test ./..."
```

Release binaries are published as plain executables named like `watchur-linux-amd64` and `watchur-darwin-arm64`.

## Flags

```text
-debounce int
    debounce window in milliseconds (default 250)
-dir string
    directory to watch recursively (default ".")
-except string
    comma-separated paths or globs to exclude
-extensions string
    comma-separated glob patterns to include
-no-initial-run
    do not run the command once at startup
-run string
    command to run on changes
-v
    verbose logging
-version
    print version and build date
```

## Examples

Run Go tests whenever Go files change:

```bash
watchur --extensions "*.go" --run "go test ./..."
```

Rebuild a binary when source or template files change:

```bash
watchur \
  --dir . \
  --extensions "*.go,*.tmpl" \
  --except ".git/,tmp/,dist/" \
  --run "go build ./cmd/watchur"
```

Watch a frontend project without an initial run:

```bash
watchur \
  --dir web \
  --extensions "*.ts,*.tsx,*.css" \
  --no-initial-run \
  --run "npm test"
```

## Matching Rules

`--extensions` accepts comma-separated glob patterns such as `*.go,*.tmpl`.

Matching is checked against both the relative path and the file basename for convenience.

`--except` accepts:

- File globs such as `*.tmp`
- Explicit relative paths such as `config/local.yaml`
- Directory prefixes when the value ends with `/`, such as `.git/` or `node_modules/`

## Development

For local development, install from source in this repo:

```bash
go install ./cmd/watchur
```

If you want the project-defined build metadata and commands, use `drun`:

```bash
xdrun test
xdrun lint
xdrun vet
xdrun sec
xdrun ci
```
