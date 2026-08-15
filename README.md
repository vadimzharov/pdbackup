# Pod Data Backup - pdbackup

A lightweight Kubernetes-native tool for backing up and restoring pod filesystem data to S3-compatible object storage using [Kopia](https://kopia.io/).

## How it works

**pdbackup** runs as a container alongside your application pods:

- **Restore** — run as an `initContainer` to seed a shared volume with the latest snapshot before the main container starts.
- **Backup** — run as a sidecar container to periodically snapshot the volume to S3 while the application is running.

Kopia handles deduplication, encryption, compression, and retention automatically. The tool wraps the `kopia` CLI so no Kopia knowledge is required.

## Commands

| Command | Description |
|---|---|
| `pdbackup backup` | Take a one-shot snapshot and upload it to S3 |
| `pdbackup restore` | Restore the latest snapshot from S3 (designed for `initContainer`) |
| `pdbackup daemon` | Run `backup` on a configurable interval (designed for sidecar) |
| `pdbackup list` | List all snapshots stored in the repository |

## Restore modes

### emptyDir mode (default)

The default behaviour: restore the latest snapshot to `target_dir` on every pod start. This is the right choice for `emptyDir` volumes because their contents are lost whenever the pod is rescheduled.

### PVC mode

Enable with `PDBACKUP_RESTORE_PVC_MODE=true` (or `restore.pvc_mode: true`).

When a pod uses a **PersistentVolumeClaim** the data already on the volume survives pod restarts — restoring on every start would overwrite any changes the application has made since the first restore. PVC mode solves this with a marker file:

1. On restore the `initContainer` checks for a file named `.pdbackup-restored-pvc` inside `target_dir`.
2. **File absent** → restore the latest snapshot, then write the marker file containing the snapshot ID and timestamp.
3. **File present** → skip the restore entirely and exit successfully.

The marker file persists on the PVC, so data is restored exactly once: the first time the PVC is mounted (when the file doesn't exist yet). All subsequent pod restarts find the marker and skip the restore, leaving the application's live data untouched.

## Configuration

Configuration is loaded in priority order: **environment variable** > **YAML config file** > **built-in default**.

### YAML config file

By default pdbackup looks for `/etc/pdbackup/config.yaml`. Override the path with `PDBACKUP_CONFIG`.

```yaml
s3:
  endpoint: https://s3.example.com   # omit for AWS S3
  bucket: my-backup-bucket
  access_key: AKIAIOSFODNN7EXAMPLE
  secret_key: wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY
  prefix: my-app                     # repository root inside the bucket
  region: us-east-1
  disable_tls_verify: false          # set true for self-signed certs

kopia:
  password: a-strong-repository-password
  hostname: my-app                   # stable tag applied to every snapshot
  username: pdbackup                 # stable tag applied to every snapshot

backup:
  source_dir: /data                  # directory to back up
  interval: 1h                       # daemon tick (any Go duration: 30m, 6h …)
  retention:
    keep_latest: 10
    keep_daily: 7
    keep_weekly: 4
    keep_monthly: 3

restore:
  target_dir: /data                  # where to restore files
  force_restore: false               # true = exit 1 if restore is not possible
  pvc_mode: false                    # true = skip restore if marker file exists
```

A missing config file is silently ignored — you can configure everything with environment variables instead.

### Environment variables

Every YAML key has a corresponding environment variable. When both are set the environment variable wins.

| Environment variable | YAML equivalent | Default |
|---|---|---|
| `PDBACKUP_CONFIG` | — | `/etc/pdbackup/config.yaml` |
| `PDBACKUP_S3_ENDPOINT` | `s3.endpoint` | _(AWS S3)_ |
| `PDBACKUP_S3_BUCKET` | `s3.bucket` | **required** |
| `PDBACKUP_S3_ACCESS_KEY` | `s3.access_key` | **required** |
| `PDBACKUP_S3_SECRET_KEY` | `s3.secret_key` | **required** |
| `PDBACKUP_S3_PREFIX` | `s3.prefix` | `pdbackup` |
| `PDBACKUP_S3_REGION` | `s3.region` | `us-east-1` |
| `PDBACKUP_S3_DISABLE_TLS_VERIFY` | `s3.disable_tls_verify` | `false` |
| `PDBACKUP_KOPIA_PASSWORD` | `kopia.password` | **required** |
| `PDBACKUP_HOSTNAME` | `kopia.hostname` | `pdbackup` |
| `PDBACKUP_USERNAME` | `kopia.username` | `pdbackup` |
| `PDBACKUP_SOURCE_DIR` | `backup.source_dir` | **required** |
| `PDBACKUP_BACKUP_INTERVAL` | `backup.interval` | `1h` |
| `PDBACKUP_RETENTION_KEEP_LATEST` | `backup.retention.keep_latest` | `10` |
| `PDBACKUP_RETENTION_KEEP_DAILY` | `backup.retention.keep_daily` | `7` |
| `PDBACKUP_RETENTION_KEEP_WEEKLY` | `backup.retention.keep_weekly` | `4` |
| `PDBACKUP_RETENTION_KEEP_MONTHLY` | `backup.retention.keep_monthly` | `3` |
| `PDBACKUP_TARGET_DIR` | `restore.target_dir` | **required for restore** |
| `PDBACKUP_FORCE_RESTORE` | `restore.force_restore` | `false` |
| `PDBACKUP_RESTORE_PVC_MODE` | `restore.pvc_mode` | `false` |
| `PDBACKUP_DEBUG` | — | `false` |

> **Note on `PDBACKUP_HOSTNAME` / `kopia.hostname`**
> Kopia tags every snapshot with the hostname of the machine that created it. Because Kubernetes pod names change between restarts, pdbackup overrides this with a fixed value so that an `initContainer` launched in a new pod can always find the snapshots created by a previous pod's sidecar.
> Set this to something stable and unique per application (e.g. the Deployment name).

## Kubernetes deployment

### Kubernetes Events

When running inside a cluster, pdbackup emits Kubernetes Events to the pod so you can monitor backup and restore activity with `kubectl get events` or `kubectl describe pod`.

**Events emitted:**

| Reason | Type | Emitted when |
|---|---|---|
| `BackupSucceeded` | Normal | snapshot uploaded successfully |
| `BackupFailed` | Warning | backup failed for any reason |
| `RestoreSucceeded` | Normal | data restored successfully |
| `RestoreFailed` | Warning | restore failed (e.g. FORCE_RESTORE + no data) |
| `RestoreSkipped` | Normal | PVC mode: marker file found, restore skipped |

**Required RBAC** — the pod's ServiceAccount needs permission to create events:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pdbackup-events
  namespace: my-app-namespace
rules:
  - apiGroups: [""]
    resources: ["events"]
    verbs: ["create"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: pdbackup-events
  namespace: my-app-namespace
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: pdbackup-events
subjects:
  - kind: ServiceAccount
    name: default          # replace with your pod's ServiceAccount
    namespace: my-app-namespace
```

**Required downward API env vars** — add these to every pdbackup container (both the initContainer and the sidecar) so the emitter knows which pod to attach events to:

```yaml
env:
  - name: POD_NAME
    valueFrom:
      fieldRef:
        fieldPath: metadata.name
  - name: POD_NAMESPACE
    valueFrom:
      fieldRef:
        fieldPath: metadata.namespace
  - name: POD_UID
    valueFrom:
      fieldRef:
        fieldPath: metadata.uid
```

If these vars are absent, or if pdbackup is run outside a cluster (e.g. for local testing), event emission is silently skipped — no error occurs.

### Storing credentials

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: pdbackup-secret
stringData:
  PDBACKUP_S3_ACCESS_KEY: "AKIAIOSFODNN7EXAMPLE"
  PDBACKUP_S3_SECRET_KEY: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
  PDBACKUP_KOPIA_PASSWORD: "a-strong-repository-password"
```

### Storing non-secret configuration

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: pdbackup-config
data:
  config.yaml: |
    s3:
      endpoint: https://s3.example.com
      bucket: my-backup-bucket
      prefix: my-app
      region: us-east-1
    kopia:
      hostname: my-app
      username: pdbackup
    backup:
      source_dir: /data
      interval: 1h
      retention:
        keep_latest: 10
        keep_daily: 7
        keep_weekly: 4
        keep_monthly: 3
    restore:
      target_dir: /data
```

### Deployment with emptyDir (default mode)

Data is restored on every pod start. Use this when `source_dir` is backed by an `emptyDir` volume.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      volumes:
        - name: data
          emptyDir: {}
        - name: pdbackup-config
          configMap:
            name: pdbackup-config

      # Restore the latest snapshot before the main container starts.
      initContainers:
        - name: restore
          image: ghcr.io/vadimzharov/pdbackup:latest
          args: ["restore"]
          envFrom:
            - secretRef:
                name: pdbackup-secret
          env:
            - name: PDBACKUP_CONFIG
              value: /etc/pdbackup/config.yaml
          volumeMounts:
            - name: data
              mountPath: /data
            - name: pdbackup-config
              mountPath: /etc/pdbackup

      containers:
        # Main application
        - name: my-app
          image: my-app:latest
          volumeMounts:
            - name: data
              mountPath: /data

        # Sidecar: periodically back up /data to S3.
        - name: backup
          image: ghcr.io/vadimzharov/pdbackup:latest
          args: ["daemon"]
          envFrom:
            - secretRef:
                name: pdbackup-secret
          env:
            - name: PDBACKUP_CONFIG
              value: /etc/pdbackup/config.yaml
          volumeMounts:
            - name: data
              mountPath: /data
            - name: pdbackup-config
              mountPath: /etc/pdbackup
```

### Deployment with PVC (PVC mode)

Data is restored only once — the first time the PVC is mounted. Subsequent pod restarts find the marker file and skip the restore.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
spec:
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      volumes:
        - name: data
          persistentVolumeClaim:
            claimName: my-app-data
        - name: pdbackup-config
          configMap:
            name: pdbackup-config

      initContainers:
        - name: restore
          image: ghcr.io/vadimzharov/pdbackup:latest
          args: ["restore"]
          envFrom:
            - secretRef:
                name: pdbackup-secret
          env:
            - name: PDBACKUP_CONFIG
              value: /etc/pdbackup/config.yaml
            - name: PDBACKUP_RESTORE_PVC_MODE
              value: "true"
          volumeMounts:
            - name: data
              mountPath: /data
            - name: pdbackup-config
              mountPath: /etc/pdbackup

      containers:
        - name: my-app
          image: my-app:latest
          volumeMounts:
            - name: data
              mountPath: /data

        - name: backup
          image: ghcr.io/vadimzharov/pdbackup:latest
          args: ["daemon"]
          envFrom:
            - secretRef:
                name: pdbackup-secret
          env:
            - name: PDBACKUP_CONFIG
              value: /etc/pdbackup/config.yaml
          volumeMounts:
            - name: data
              mountPath: /data
            - name: pdbackup-config
              mountPath: /etc/pdbackup
```

### StatefulSet example

For StatefulSets with predictable pod names you can use the pod name directly as the hostname, ensuring each replica keeps its own independent backup history.

```yaml
      initContainers:
        - name: restore
          image: ghcr.io/vadimzharov/pdbackup:latest
          args: ["restore"]
          envFrom:
            - secretRef:
                name: pdbackup-secret
          env:
            - name: PDBACKUP_CONFIG
              value: /etc/pdbackup/config.yaml
            - name: PDBACKUP_HOSTNAME
              valueFrom:
                fieldRef:
                  fieldPath: metadata.name   # e.g. "my-app-0"
```

## Building

```bash
# Build binary
go build -o pdbackup .

# Build container image (requires Docker or compatible runtime)
docker build -t pdbackup:latest .
```

The Dockerfile downloads the `kopia` binary automatically during the build. To pin a specific Kopia version:

```bash
docker build --build-arg KOPIA_VERSION=0.18.2 -t pdbackup:latest .
```

## Listing and inspecting snapshots

```bash
# List all snapshots in the repository
pdbackup list

# Or run directly with kopia for advanced inspection
kopia --config-file=/tmp/pdbackup-kopia.config snapshot list --all
kopia --config-file=/tmp/pdbackup-kopia.config snapshot show <snapshot-id>
```

## First-run behaviour

- **Backup (`backup` / `daemon`):** if no repository exists at the configured S3 location, pdbackup initialises a new one automatically.
- **Restore (`restore`):** by default, if data cannot be restored for any reason — no repository yet, S3 unreachable, no snapshots — pdbackup exits 0 with a warning and leaves the target directory empty, allowing the pod to start fresh. Set `PDBACKUP_FORCE_RESTORE=true` (or `restore.force_restore: true`) to turn every such condition into a fatal error (exit 1). This prevents the pod from starting until a successful restore is confirmed, which is essential when the application must not run without its data.
