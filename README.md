# cliutils

A small command-line tool for expanding abbreviated host strings into full
hostname lists.

```
cliutils hosts "host[1..2]"
host1
host2
```

## Prerequisites

- `git`, `make`, and `curl` (preinstalled on most dev machines; on Debian/Ubuntu:
  `sudo apt-get install -y git make curl`)
- Go 1.26 — installed via [mise](https://mise.jdx.dev) below, or bring your own.

## Setup

This project pins its Go version with mise. Install mise if you don't have it:

```sh
curl https://mise.run | sh
```

Activate it in your shell so the pinned `go` lands on your PATH, then reload:

```sh
echo 'eval "$(mise activate bash)"' >> ~/.bashrc   # or ~/.zshrc, with "zsh"
exec $SHELL
```

Install the pinned Go toolchain from `mise.toml`:

```sh
mise install
```

(Prefer not to use mise? Install Go 1.26 yourself and skip these steps.)

## Verify your setup

From the repo root, this should pass with no errors:

```sh
make
```

You're ready when it prints `ok` for the tests and produces `./build/cliutils`.

## Build & run

```sh
make build
./build/cliutils hosts "web[1..3]"
```

## Development

```sh
make test    # run tests
make lint    # gofmt check + go vet
make fmt     # format sources
make         # lint, test, build
```

The core expansion logic lives in [`hostexpansion/`](hostexpansion/).
