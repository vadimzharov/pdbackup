// Package k8sevents emits Kubernetes Events for backup and restore outcomes.
// When pdbackup is not running inside a cluster, or the required downward-API
// env vars are absent, all methods are silent no-ops — no error is returned.
package k8sevents

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const component = "pdbackup"

// Emitter sends Kubernetes Events on behalf of the running pod.
// A nil Emitter is safe — every method is a no-op.
type Emitter struct {
	client kubernetes.Interface
	pod    corev1.ObjectReference
}

// New returns an Emitter when running inside a Kubernetes cluster with the
// POD_NAME and POD_NAMESPACE environment variables set (downward API).
// Returns nil without error when not in a cluster or env vars are missing.
func New() *Emitter {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Not running inside a cluster — event emission disabled silently.
		return nil
	}

	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		slog.Warn("k8s events: could not create client, events disabled", "error", err)
		return nil
	}

	podName := os.Getenv("POD_NAME")
	podNamespace := os.Getenv("POD_NAMESPACE")
	podUID := os.Getenv("POD_UID")

	if podName == "" || podNamespace == "" {
		slog.Warn("k8s events: POD_NAME / POD_NAMESPACE not set via downward API, events disabled")
		return nil
	}

	slog.Info("k8s events: emitter ready", "pod", podName, "namespace", podNamespace)

	return &Emitter{
		client: client,
		pod: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      podName,
			Namespace: podNamespace,
			UID:       types.UID(podUID),
		},
	}
}

// BackupSucceeded emits a Normal event indicating a successful backup.
func (e *Emitter) BackupSucceeded(source string) {
	e.emit(corev1.EventTypeNormal, "BackupSucceeded",
		fmt.Sprintf("backup of %q completed successfully", source))
}

// BackupFailed emits a Warning event indicating a failed backup.
func (e *Emitter) BackupFailed(source string, err error) {
	e.emit(corev1.EventTypeWarning, "BackupFailed",
		fmt.Sprintf("backup of %q failed: %v", source, err))
}

// RestoreSucceeded emits a Normal event indicating a successful restore.
func (e *Emitter) RestoreSucceeded(target string) {
	e.emit(corev1.EventTypeNormal, "RestoreSucceeded",
		fmt.Sprintf("restore to %q completed successfully", target))
}

// RestoreSkipped emits a Normal event when PVC mode detects the marker file.
func (e *Emitter) RestoreSkipped(target string) {
	e.emit(corev1.EventTypeNormal, "RestoreSkipped",
		fmt.Sprintf("PVC mode: marker file found, restore of %q skipped", target))
}

// RestoreFailed emits a Warning event indicating a failed restore.
func (e *Emitter) RestoreFailed(target string, err error) {
	e.emit(corev1.EventTypeWarning, "RestoreFailed",
		fmt.Sprintf("restore to %q failed: %v", target, err))
}

func (e *Emitter) emit(eventType, reason, message string) {
	if e == nil {
		return
	}

	now := metav1.Now()
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			// Name must be unique within the namespace.
			Name:      fmt.Sprintf("%s.%x", e.pod.Name, rand.Int63()),
			Namespace: e.pod.Namespace,
		},
		InvolvedObject: e.pod,
		Reason:         reason,
		Message:        message,
		Type:           eventType,
		Source: corev1.EventSource{
			Component: component,
			Host:      e.pod.Name,
		},
		FirstTimestamp: now,
		LastTimestamp:  now,
		Count:          1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err := e.client.CoreV1().Events(e.pod.Namespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		slog.Warn("k8s events: failed to emit event", "reason", reason, "error", err)
	} else {
		slog.Debug("k8s events: emitted", "type", eventType, "reason", reason)
	}
}
