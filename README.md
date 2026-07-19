# a-lancxo

Application indexer and launcher daemon for ADE. Replaces the former **ade-exe-ctld** binary.

Indexes executables from PATH and `.desktop` files, exposes a Unix-socket CMDLIST API for clients such as **xopen**.

## Status

**In active development and not suitable for use by end users!**

## Binaries

| Command | Role |
|---------|------|
| **a-lancxo** | Main daemon (application index + launch) |
| **ade-exe-cli** | CLI client for testing |

## Socket

Default: `/tmp/ade-{UID}/indexd` (`ADE_INDEXD_SOCK`).

## Build

```bash
make build
make test
make install   # installs a-lancxo
```
