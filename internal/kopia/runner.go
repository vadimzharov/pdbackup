package kopia

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vadimzharov/pdbackup/internal/config"
	"github.com/vadimzharov/pdbackup/internal/k8sevents"
)

const (
	configFile = "/tmp/pdbackup-kopia.config"
	cacheDir   = "/tmp/pdbackup-kopia-cache"
	logDir     = "/tmp/pdbackup-kopia-logs"
)

type Runner struct {
	cfg     *config.Config
	events  *k8sevents.Emitter
}

type snapshotEntry struct {
	ID        string     `json:"id"`
	Source    sourceInfo `json:"source"`
	StartTime time.Time  `json:"startTime"`
}

type sourceInfo struct {
	Host     string `json:"host"`
	UserName string `json:"userName"`
	Path     string `json:"path"`
}

func New(cfg *config.Config) *Runner {
	return &Runner{
		cfg:    cfg,
		events: k8sevents.New(),
	}
}

// baseArgs returns flags that apply to every kopia invocation.
func (r *Runner) baseArgs() []string {
	return []string{
		"--config-file=" + configFile,
		"--log-level=info",
		"--log-dir=" + logDir,
	}
}

func (r *Runner) run(args []string) error {
	allArgs := append(r.baseArgs(), args...)
	slog.Info("kopia", "cmd", strings.Join(args, " "))
	cmd := exec.Command("kopia", allArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runCapture runs kopia and captures all output (used when we expect failure).
func (r *Runner) runCapture(args []string) ([]byte, error) {
	allArgs := append(r.baseArgs(), args...)
	var buf bytes.Buffer
	cmd := exec.Command("kopia", allArgs...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// runJSON runs kopia and captures only stdout for JSON parsing.
func (r *Runner) runJSON(args []string) ([]byte, error) {
	allArgs := append(r.baseArgs(), args...)
	var stdout bytes.Buffer
	cmd := exec.Command("kopia", allArgs...)
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func (r *Runner) s3Args() ([]string, error) {
	args := []string{
		"--bucket=" + r.cfg.S3Bucket,
		"--access-key=" + r.cfg.S3AccessKey,
		"--secret-access-key=" + r.cfg.S3SecretKey,
		"--prefix=" + r.cfg.S3Prefix + "/",
	}

	if r.cfg.S3Region != "" {
		args = append(args, "--region="+r.cfg.S3Region)
	}

	if r.cfg.S3Endpoint != "" {
		host, disableTLS, err := parseEndpoint(r.cfg.S3Endpoint)
		if err != nil {
			return nil, fmt.Errorf("parse S3 endpoint: %w", err)
		}
		args = append(args, "--endpoint="+host)
		if disableTLS {
			args = append(args, "--disable-tls")
		}
	}

	if r.cfg.S3DisableTLSVerify {
		args = append(args, "--disable-tls-verification")
	}

	return args, nil
}

// parseEndpoint accepts either a bare hostname or a full URL and returns the
// host portion plus whether TLS should be disabled.
func parseEndpoint(endpoint string) (host string, disableTLS bool, err error) {
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return endpoint, false, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", false, err
	}
	return u.Host, u.Scheme == "http", nil
}

// repoConnectArgs returns flags shared by connect and create.
func (r *Runner) repoConnectArgs() []string {
	return []string{
		"--password=" + r.cfg.KopiaPassword,
		"--cache-directory=" + cacheDir,
		"--no-check-for-updates",
	}
}

// overrideSource builds the kopia source spec used to tag snapshots with a
// stable identity (independent of actual container hostname).
func (r *Runner) overrideSource() string {
	return fmt.Sprintf("%s@%s:%s", r.cfg.KopiaUsername, r.cfg.KopiaHostname, r.cfg.SourceDir)
}

func (r *Runner) connect() error {
	s3args, err := r.s3Args()
	if err != nil {
		return err
	}
	args := append([]string{"repository", "connect", "s3"}, s3args...)
	args = append(args, r.repoConnectArgs()...)
	out, err := r.runCapture(args)
	if err != nil {
		slog.Debug("repository connect failed", "output", string(out))
	}
	return err
}

func (r *Runner) create() error {
	s3args, err := r.s3Args()
	if err != nil {
		return err
	}
	args := append([]string{"repository", "create", "s3"}, s3args...)
	args = append(args, r.repoConnectArgs()...)
	return r.run(args)
}

// Setup connects to an existing repository or initialises a new one on first run.
func (r *Runner) Setup() error {
	if err := r.connect(); err != nil {
		slog.Info("no existing repository found, creating new one")
		if createErr := r.create(); createErr != nil {
			return fmt.Errorf("connect: %v; create: %w", err, createErr)
		}
		slog.Info("repository created successfully")
		return nil
	}
	slog.Info("connected to existing repository")
	return nil
}

func (r *Runner) setPolicy() error {
	return r.run([]string{
		"policy", "set", "--global",
		fmt.Sprintf("--keep-latest=%d", r.cfg.RetentionKeepLatest),
		fmt.Sprintf("--keep-daily=%d", r.cfg.RetentionKeepDaily),
		fmt.Sprintf("--keep-weekly=%d", r.cfg.RetentionKeepWeekly),
		fmt.Sprintf("--keep-monthly=%d", r.cfg.RetentionKeepMonthly),
	})
}

// Backup creates a Kopia snapshot of SourceDir and applies the retention policy.
func (r *Runner) Backup() error {
	if err := r.Setup(); err != nil {
		err = fmt.Errorf("repository setup: %w", err)
		r.events.BackupFailed(r.cfg.SourceDir, err)
		return err
	}

	if err := r.setPolicy(); err != nil {
		slog.Warn("could not set retention policy", "error", err)
	}

	slog.Info("creating snapshot", "source", r.cfg.SourceDir)
	if err := r.run([]string{
		"snapshot", "create", r.cfg.SourceDir,
		"--override-source=" + r.overrideSource(),
	}); err != nil {
		err = fmt.Errorf("snapshot create: %w", err)
		r.events.BackupFailed(r.cfg.SourceDir, err)
		return err
	}

	if err := r.run([]string{"snapshot", "expire", "--all", "--delete"}); err != nil {
		slog.Warn("snapshot expire failed", "error", err)
	}

	r.events.BackupSucceeded(r.cfg.SourceDir)
	return nil
}

const pvcMarkerFile = ".pdbackup-restored-pvc"

// Restore fetches the latest snapshot and restores it to TargetDir.
//
// When FORCE_RESTORE is false (default) any inability to restore — repository
// not yet initialised, S3 unreachable, no snapshots — is treated as a warning
// and the tool exits 0 so the pod can start with an empty directory.
//
// When FORCE_RESTORE is true every such condition is a fatal error (exit 1),
// preventing the pod from starting until the restore succeeds.
//
// In PVC mode the tool first checks for a marker file. If found the restore is
// skipped entirely (data was already restored on a previous pod start).
func (r *Runner) Restore() error {
	if r.cfg.RestorePVCMode {
		marker := filepath.Join(r.cfg.TargetDir, pvcMarkerFile)
		if _, err := os.Stat(marker); err == nil {
			slog.Info("PVC mode: marker file found, skipping restore", "marker", marker)
			r.events.RestoreSkipped(r.cfg.TargetDir)
			return nil
		}
	}

	if err := r.connect(); err != nil {
		if r.cfg.ForceRestore {
			err = fmt.Errorf("FORCE_RESTORE is set and repository connect failed: %w", err)
			r.events.RestoreFailed(r.cfg.TargetDir, err)
			return err
		}
		slog.Warn("could not connect to repository, starting with empty directory", "error", err)
		return nil
	}

	snapshots, err := r.listSnapshots()
	if err != nil {
		if r.cfg.ForceRestore {
			err = fmt.Errorf("FORCE_RESTORE is set and snapshot listing failed: %w", err)
			r.events.RestoreFailed(r.cfg.TargetDir, err)
			return err
		}
		slog.Warn("could not list snapshots, starting with empty directory", "error", err)
		return nil
	}

	if len(snapshots) == 0 {
		if r.cfg.ForceRestore {
			err := fmt.Errorf("FORCE_RESTORE is set and no snapshots found for source %q", r.cfg.SourceDir)
			r.events.RestoreFailed(r.cfg.TargetDir, err)
			return err
		}
		slog.Warn("no snapshots found, starting with empty directory", "source", r.cfg.SourceDir)
		return nil
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].StartTime.After(snapshots[j].StartTime)
	})

	latest := snapshots[0]
	slog.Info("restoring snapshot",
		"id", latest.ID,
		"created", latest.StartTime.Format(time.RFC3339),
		"target", r.cfg.TargetDir,
	)

	if err := r.run([]string{"restore", latest.ID, r.cfg.TargetDir}); err != nil {
		err = fmt.Errorf("restore snapshot %s: %w", latest.ID, err)
		r.events.RestoreFailed(r.cfg.TargetDir, err)
		return err
	}

	if r.cfg.RestorePVCMode {
		marker := filepath.Join(r.cfg.TargetDir, pvcMarkerFile)
		content := fmt.Sprintf("snapshot=%s restored=%s\n", latest.ID, time.Now().UTC().Format(time.RFC3339))
		if err := os.WriteFile(marker, []byte(content), 0o644); err != nil {
			slog.Warn("PVC mode: could not write marker file", "marker", marker, "error", err)
		} else {
			slog.Info("PVC mode: marker file written", "marker", marker)
		}
	}

	r.events.RestoreSucceeded(r.cfg.TargetDir)
	return nil
}

// List prints all snapshots in the repository.
func (r *Runner) List() error {
	if err := r.connect(); err != nil {
		return fmt.Errorf("connect to repository: %w", err)
	}
	return r.run([]string{"snapshot", "list", "--all"})
}

func (r *Runner) listSnapshots() ([]snapshotEntry, error) {
	out, err := r.runJSON([]string{"snapshot", "list", "--json", "--all"})
	if err != nil {
		return nil, fmt.Errorf("kopia snapshot list: %w", err)
	}

	var all []snapshotEntry
	if err := json.Unmarshal(out, &all); err != nil {
		return nil, fmt.Errorf("parse snapshot JSON: %w", err)
	}

	// Filter to snapshots matching our configured source identity.
	var filtered []snapshotEntry
	for _, s := range all {
		if s.Source.Host == r.cfg.KopiaHostname &&
			s.Source.UserName == r.cfg.KopiaUsername &&
			(r.cfg.SourceDir == "" || s.Source.Path == r.cfg.SourceDir) {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}
