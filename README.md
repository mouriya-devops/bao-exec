# bao-exec

`bao-exec` is the single read point for OpenBao values in
[mouriya-devops/homelab-tf#641](https://github.com/mouriya-devops/homelab-tf/issues/641):
Komodo Periphery, GitHub Actions runners and workstation IaC entries run a
command with the fields of one or more KV v2 paths exposed as environment
variables. Values never reach argv, stdout, stderr or a log.

## Usage

```sh
bao-exec run --path <kv path> [--path <kv path>...] -- <command> [args...]
```

Each `--path` is a KV v2 secret path whose first segment is the mount, for
example `kv/mouriya-s-lab/homelab-apps/runtime`. Every field of the current
version of every path becomes an environment variable with the same name,
overriding a variable of that name already present in the environment. The
command's exit code is `bao-exec`'s exit code.

## Authentication

`BAO_EXEC_AUTH` selects the method:

| `BAO_EXEC_AUTH` | Credentials | Login |
|---|---|---|
| `token` (default) | `BAO_TOKEN`, else the file at `BAO_TOKEN_PATH`, else `~/.vault-token` | none; the token is used directly |
| `approle` | `BAO_ROLE_ID_FILE` and `BAO_SECRET_ID_FILE` | `auth/approle/login` |
| `github-actions` | `ACTIONS_ID_TOKEN_REQUEST_URL` / `ACTIONS_ID_TOKEN_REQUEST_TOKEN` (set by GitHub Actions), `BAO_EXEC_ROLE` | requests a JWT with audience `openbao`, then `auth/jwt-github/login` |

`BAO_ADDR` is a comma-separated address list. Addresses are tried in order; a
connection failure, a 5xx response or a sealed node moves on to the next one,
while a 4xx response is final because it describes the request or the caller's
credentials.

## Failure behaviour

No command starts unless every requested path was read successfully. Each
failure exits non-zero with a message naming the category and, where relevant,
the path or field name:

- `config` — required environment or credential file missing or empty;
- `auth` — login rejected or returned no token;
- `path` — path not found or not permitted;
- `field` — invalid field name, non-string value, or the same field in two paths;
- `addresses` — every address in `BAO_ADDR` unavailable.

Values and tokens are never included in an error message.

## Install

Fetch the latest release asset for the host platform and verify it against the
published `bao-exec_<version>_checksums.txt`:

```sh
gh release download -R mouriya-devops/bao-exec --pattern '*linux_amd64*' --dir /tmp/bao-exec
cd /tmp/bao-exec && shasum -a 256 -c *checksums.txt --ignore-missing
install -m 0755 bao-exec-linux-amd64 /usr/local/bin/bao-exec
```

Linux `amd64`, Linux `arm64` and Darwin `arm64` binaries are published
together with the checksum manifest.

## Build and test

```sh
go build -o bao-exec .
go test ./...
```
