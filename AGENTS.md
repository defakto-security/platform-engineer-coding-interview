# cliutils

A small Go CLI. `cliutils "host[1..2]"` expands abbreviated host strings
(`host[1..2]` → `host1 host2`). Core logic lives in `hostexpansion/`.

## Setup

```sh
mise install   # installs the Go version pinned in mise.toml
```

## Common tasks

```sh
make build   # compile to ./build/cliutils
make test    # go test ./...
make lint    # gofmt check + go vet
make fmt     # gofmt -w .
make         # lint, test, build
```

Build outputs go in `./build/` (gitignored).

## Conventions

- Format with `gofmt` before committing; `make lint` fails on unformatted files.
- Keep exported functions documented (see `hostexpansion/hosts.go`).
- Tests live alongside code in `*_test.go`.
