# pdbackup — Claude Code context

## What this project is

`pdbackup` is a Go CLI tool for backing up and restoring Kubernetes pod filesystem data to S3-compatible object storage. It wraps the `kopia` CLI binary as a subprocess — it does not use kopia's Go library.

The tool runs as:
- an `initContainer` (using the `restore` command) to seed a volume before the main container starts
- a sidecar container (using the `daemon` command) to periodically snapshot the volume to S3

## Repository layout

```
main.go                        entry point, delegates to cmd.Execute()
cmd/
  root.go                      cobra root command, registers all subcommands
  backup.go                    one-shot backup command
  restore.go                   one-shot restore command (initContainer use)
  daemon.go                    periodic backup loop (sidecar use)
  list.go                      list snapshots in the repository
internal/
  k8sevents/emitter.go         Kubernetes Event emitter (nil-safe, no-op outside cluster)
internal/
  config/config.go             configuration loading (defaults → YAML → env vars)
  kopia/runner.go              kopia CLI wrapper — all kopia subprocess calls live here
Dockerfile                     multi-stage build; downloads kopia binary at build time
```

## Build and run

```bash
# Build
go build -o pdbackup .

# Run (kopia must be on PATH)
./pdbackup backup
./pdbackup restore
./pdbackup daemon
./pdbackup list

# Container image
docker build -t pdbackup:latest .
docker build --build-arg KOPIA_VERSION=0.18.2 -t pdbackup:latest .
```

`go vet ./...` must pass before committing.

## Dependencies

- `github.com/spf13/cobra` — CLI subcommand framework
- `gopkg.in/yaml.v3` — YAML config file parsing
- `k8s.io/client-go` + `k8s.io/api` + `k8s.io/apimachinery` — Kubernetes Events API
- `kopia` binary (v0.18.2 tested) — invoked as a subprocess; included in the Docker image

## Configuration system (`internal/config/config.go`)

Loading priority: **hardcoded default → YAML file → environment variable** (env wins).

The YAML file path defaults to `/etc/pdbackup/config.yaml` and can be overridden with `PDBACKUP_CONFIG`. A missing file is silently ignored.

`fileConfig` uses pointer types (`*bool`, `*int`) for fields where the zero value (`false`, `0`) is a meaningful setting, so "field absent in YAML" can be distinguished from "field explicitly set to zero/false".

`applyFile` applies only non-zero/non-nil YAML values on top of defaults.
`applyEnv` applies only non-empty env var values on top of whatever is already set.

## Kopia integration (`internal/kopia/runner.go`)

### Key design decisions

**subprocess, not library** — kopia's Go packages are internal and not a stable public API. All kopia operations are `exec.Command("kopia", ...)` calls.

**`--override-source`** — every `snapshot create` call passes `--override-source=username@hostname:path`. This tags snapshots with a fixed identity (from `KopiaUsername` / `KopiaHostname` config) rather than the actual OS hostname and username. This is critical for cross-pod restore: the initContainer in a new pod (with a different OS hostname) can find snapshots created by a sidecar in a previous pod.

**`listSnapshots` filtering** — filters the `snapshot list --json --all` output by `source.Host == KopiaHostname && source.UserName == KopiaUsername && source.Path == SourceDir`. This matches the identity set via `--override-source`.

**`snapshot list --all` without a path arg** — the `List()` command intentionally does not pass `SourceDir` as a positional argument to `kopia snapshot list`. Passing a path argument makes kopia filter by the *current* OS hostname/user, not the override identity, so no matching snapshots would be shown.

**connect-or-create** — `Setup()` first tries `repository connect`. On failure it silently retries with `repository create` to initialise a new repository on first backup. The connect failure output is captured (not printed) to avoid confusing log noise on first run.

**config/cache paths** — `/tmp/pdbackup-kopia.config` and `/tmp/pdbackup-kopia-cache`. These live in `/tmp` so no persistent storage is required. They persist for the lifetime of the container, which is sufficient.

## Kubernetes Events (`internal/k8sevents/emitter.go`)

`k8sevents.New()` attempts `rest.InClusterConfig()`. If that fails (running outside a cluster) it returns `nil`. All methods on a nil `*Emitter` are no-ops, so no guard clauses are needed at call sites.

When inside a cluster the emitter requires `POD_NAME`, `POD_NAMESPACE`, and `POD_UID` env vars (set via the downward API). If missing it returns nil and logs a warning.

Events are attached to the pod itself (`involvedObject = Pod`). The pod's ServiceAccount must have `create` on `events` in its namespace (narrow RBAC Role).

Events emitted: `BackupSucceeded`, `BackupFailed`, `RestoreSucceeded`, `RestoreFailed`, `RestoreSkipped`.

The emitter is held by `Runner` and called at the end of `Backup()` and `Restore()` — success and all failure paths.

### Kopia CLI flags (v0.18.2)

- `--cache-directory` and `--no-check-for-updates` are per-command flags on `repository connect/create`, **not** global flags.
- There is no `--override-hostname` / `--override-username` on `repository connect/create` in v0.18.2 — use `--override-source` on `snapshot create` instead.
- `snapshot list` has no `--host` / `--user` filter flags — use `--all` and filter the JSON output in Go.
- `kopia restore <snapshot-id> <target-path>` is the correct restore syntax (not `kopia snapshot restore`).

## Testing against the local S3

A Rook/Ceph S3 instance is available for integration testing:

```bash
export PDBACKUP_S3_ENDPOINT="https://your-s3-endpoint.example.com"
export PDBACKUP_S3_BUCKET="your-bucket-name"
export PDBACKUP_S3_ACCESS_KEY="your-access-key"
export PDBACKUP_S3_SECRET_KEY="your-secret-key"
export PDBACKUP_S3_PREFIX="my-test-prefix"
export PDBACKUP_KOPIA_PASSWORD="your-repository-password"
export PDBACKUP_SOURCE_DIR="/tmp/test-src"
export PDBACKUP_TARGET_DIR="/tmp/test-dst"
```

`kopia` must be on `PATH`. To install locally:

```bash
curl -fsSL https://github.com/kopia/kopia/releases/download/v0.18.2/kopia-0.18.2-linux-x64.tar.gz | tar xz
sudo mv kopia-0.18.2-linux-x64/kopia /usr/local/bin/kopia
```
