package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all runtime configuration for pdbackup.
type Config struct {
	// S3 settings
	S3Endpoint         string
	S3Bucket           string
	S3AccessKey        string
	S3SecretKey        string
	S3Prefix           string
	S3Region           string
	S3DisableTLSVerify bool

	// Kopia repository settings
	KopiaPassword string
	KopiaHostname string
	KopiaUsername string

	// Backup/Restore paths
	SourceDir string
	TargetDir string

	// Daemon settings
	BackupInterval time.Duration

	// Retention policy
	RetentionKeepLatest  int
	RetentionKeepDaily   int
	RetentionKeepWeekly  int
	RetentionKeepMonthly int

	// Restore behaviour
	ForceRestore   bool
	RestorePVCMode bool
}

// fileConfig is the structure of the optional YAML config file.
// Pointer types are used for bools and ints so we can distinguish
// "explicitly set to zero/false" from "absent in the file".
type fileConfig struct {
	S3 struct {
		Endpoint         string `yaml:"endpoint"`
		Bucket           string `yaml:"bucket"`
		AccessKey        string `yaml:"access_key"`
		SecretKey        string `yaml:"secret_key"`
		Prefix           string `yaml:"prefix"`
		Region           string `yaml:"region"`
		DisableTLSVerify *bool  `yaml:"disable_tls_verify"`
	} `yaml:"s3"`

	Kopia struct {
		Password string `yaml:"password"`
		Hostname string `yaml:"hostname"`
		Username string `yaml:"username"`
	} `yaml:"kopia"`

	Backup struct {
		SourceDir string `yaml:"source_dir"`
		Interval  string `yaml:"interval"`
		Retention struct {
			KeepLatest  *int `yaml:"keep_latest"`
			KeepDaily   *int `yaml:"keep_daily"`
			KeepWeekly  *int `yaml:"keep_weekly"`
			KeepMonthly *int `yaml:"keep_monthly"`
		} `yaml:"retention"`
	} `yaml:"backup"`

	Restore struct {
		TargetDir    string `yaml:"target_dir"`
		ForceRestore *bool  `yaml:"force_restore"`
		PVCMode      *bool  `yaml:"pvc_mode"`
	} `yaml:"restore"`
}

// Load builds a Config by layering: hardcoded defaults → YAML file → env vars.
// The YAML file path defaults to /etc/pdbackup/config.yaml and can be
// overridden with PDBACKUP_CONFIG. A missing file is not an error.
func Load() (*Config, error) {
	cfg := defaults()

	filePath := os.Getenv("PDBACKUP_CONFIG")
	if filePath == "" {
		filePath = "/etc/pdbackup/config.yaml"
	}
	if err := applyFile(cfg, filePath); err != nil {
		return nil, err
	}

	if err := applyEnv(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// defaults returns a Config pre-populated with hardcoded fallback values.
func defaults() *Config {
	return &Config{
		S3Prefix:             "pdbackup",
		S3Region:             "us-east-1",
		KopiaHostname:        "pdbackup",
		KopiaUsername:        "pdbackup",
		BackupInterval:       time.Hour,
		RetentionKeepLatest:  10,
		RetentionKeepDaily:   7,
		RetentionKeepWeekly:  4,
		RetentionKeepMonthly: 3,
	}
}

// applyFile reads the YAML config file and overlays any set fields onto cfg.
func applyFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config file %s: %w", path, err)
	}

	var fc fileConfig
	if err := yaml.Unmarshal(data, &fc); err != nil {
		return fmt.Errorf("parse config file %s: %w", path, err)
	}

	setStr(&cfg.S3Endpoint, fc.S3.Endpoint)
	setStr(&cfg.S3Bucket, fc.S3.Bucket)
	setStr(&cfg.S3AccessKey, fc.S3.AccessKey)
	setStr(&cfg.S3SecretKey, fc.S3.SecretKey)
	setStr(&cfg.S3Prefix, fc.S3.Prefix)
	setStr(&cfg.S3Region, fc.S3.Region)
	setBool(&cfg.S3DisableTLSVerify, fc.S3.DisableTLSVerify)

	setStr(&cfg.KopiaPassword, fc.Kopia.Password)
	setStr(&cfg.KopiaHostname, fc.Kopia.Hostname)
	setStr(&cfg.KopiaUsername, fc.Kopia.Username)

	setStr(&cfg.SourceDir, fc.Backup.SourceDir)
	setStr(&cfg.TargetDir, fc.Restore.TargetDir)
	setBool(&cfg.ForceRestore, fc.Restore.ForceRestore)
	setBool(&cfg.RestorePVCMode, fc.Restore.PVCMode)

	setInt(&cfg.RetentionKeepLatest, fc.Backup.Retention.KeepLatest)
	setInt(&cfg.RetentionKeepDaily, fc.Backup.Retention.KeepDaily)
	setInt(&cfg.RetentionKeepWeekly, fc.Backup.Retention.KeepWeekly)
	setInt(&cfg.RetentionKeepMonthly, fc.Backup.Retention.KeepMonthly)

	if fc.Backup.Interval != "" {
		d, err := time.ParseDuration(fc.Backup.Interval)
		if err != nil {
			return fmt.Errorf("config file: invalid backup.interval %q: %w", fc.Backup.Interval, err)
		}
		cfg.BackupInterval = d
	}

	return nil
}

// applyEnv overlays environment variables onto cfg. Any non-empty env var wins.
func applyEnv(cfg *Config) error {
	setEnvStr(&cfg.S3Endpoint, "PDBACKUP_S3_ENDPOINT")
	setEnvStr(&cfg.S3Bucket, "PDBACKUP_S3_BUCKET")
	setEnvStr(&cfg.S3AccessKey, "PDBACKUP_S3_ACCESS_KEY")
	setEnvStr(&cfg.S3SecretKey, "PDBACKUP_S3_SECRET_KEY")
	setEnvStr(&cfg.S3Prefix, "PDBACKUP_S3_PREFIX")
	setEnvStr(&cfg.S3Region, "PDBACKUP_S3_REGION")
	setEnvBool(&cfg.S3DisableTLSVerify, "PDBACKUP_S3_DISABLE_TLS_VERIFY")

	setEnvStr(&cfg.KopiaPassword, "PDBACKUP_KOPIA_PASSWORD")
	setEnvStr(&cfg.KopiaHostname, "PDBACKUP_HOSTNAME")
	setEnvStr(&cfg.KopiaUsername, "PDBACKUP_USERNAME")

	setEnvStr(&cfg.SourceDir, "PDBACKUP_SOURCE_DIR")
	setEnvStr(&cfg.TargetDir, "PDBACKUP_TARGET_DIR")
	setEnvBool(&cfg.ForceRestore, "PDBACKUP_FORCE_RESTORE")
	setEnvBool(&cfg.RestorePVCMode, "PDBACKUP_RESTORE_PVC_MODE")

	setEnvInt(&cfg.RetentionKeepLatest, "PDBACKUP_RETENTION_KEEP_LATEST")
	setEnvInt(&cfg.RetentionKeepDaily, "PDBACKUP_RETENTION_KEEP_DAILY")
	setEnvInt(&cfg.RetentionKeepWeekly, "PDBACKUP_RETENTION_KEEP_WEEKLY")
	setEnvInt(&cfg.RetentionKeepMonthly, "PDBACKUP_RETENTION_KEEP_MONTHLY")

	if v := os.Getenv("PDBACKUP_BACKUP_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid PDBACKUP_BACKUP_INTERVAL %q: %w", v, err)
		}
		cfg.BackupInterval = d
	}

	return nil
}

func (c *Config) ValidateForBackup() error {
	return c.validateCommon()
}

func (c *Config) ValidateForRestore() error {
	if err := c.validateCommon(); err != nil {
		return err
	}
	if c.TargetDir == "" {
		return fmt.Errorf("target directory is required (PDBACKUP_TARGET_DIR or restore.target_dir in config file)")
	}
	return nil
}

func (c *Config) ValidateForList() error {
	if c.S3Bucket == "" {
		return fmt.Errorf("S3 bucket is required (PDBACKUP_S3_BUCKET or s3.bucket in config file)")
	}
	if c.S3AccessKey == "" {
		return fmt.Errorf("S3 access key is required (PDBACKUP_S3_ACCESS_KEY or s3.access_key in config file)")
	}
	if c.S3SecretKey == "" {
		return fmt.Errorf("S3 secret key is required (PDBACKUP_S3_SECRET_KEY or s3.secret_key in config file)")
	}
	if c.KopiaPassword == "" {
		return fmt.Errorf("Kopia password is required (PDBACKUP_KOPIA_PASSWORD or kopia.password in config file)")
	}
	return nil
}

func (c *Config) validateCommon() error {
	if c.S3Bucket == "" {
		return fmt.Errorf("S3 bucket is required (PDBACKUP_S3_BUCKET or s3.bucket in config file)")
	}
	if c.S3AccessKey == "" {
		return fmt.Errorf("S3 access key is required (PDBACKUP_S3_ACCESS_KEY or s3.access_key in config file)")
	}
	if c.S3SecretKey == "" {
		return fmt.Errorf("S3 secret key is required (PDBACKUP_S3_SECRET_KEY or s3.secret_key in config file)")
	}
	if c.KopiaPassword == "" {
		return fmt.Errorf("Kopia password is required (PDBACKUP_KOPIA_PASSWORD or kopia.password in config file)")
	}
	if c.SourceDir == "" {
		return fmt.Errorf("source directory is required (PDBACKUP_SOURCE_DIR or backup.source_dir in config file)")
	}
	return nil
}

// ── helpers ────────────────────────────────────────────────────────────────────

func setStr(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}

func setBool(dst *bool, v *bool) {
	if v != nil {
		*dst = *v
	}
}

func setInt(dst *int, v *int) {
	if v != nil {
		*dst = *v
	}
}

func setEnvStr(dst *string, key string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func setEnvBool(dst *bool, key string) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func setEnvInt(dst *int, key string) {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			*dst = i
		}
	}
}
