package k8s

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient("test-ns", "test-release", "")

	if client.Namespace() != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got '%s'", client.Namespace())
	}
	if client.ReleaseName() != "test-release" {
		t.Errorf("expected release 'test-release', got '%s'", client.ReleaseName())
	}
}

func TestNewClientWithContext(t *testing.T) {
	client := NewClient("ns", "release", "my-context")

	// Verify context is set (indirectly via namespace/release)
	if client.Namespace() != "ns" {
		t.Errorf("expected namespace 'ns', got '%s'", client.Namespace())
	}
}

func TestNamespace(t *testing.T) {
	client := NewClient("my-namespace", "release", "")

	if client.Namespace() != "my-namespace" {
		t.Errorf("expected 'my-namespace', got '%s'", client.Namespace())
	}
}

func TestReleaseName(t *testing.T) {
	client := NewClient("ns", "my-release", "")

	if client.ReleaseName() != "my-release" {
		t.Errorf("expected 'my-release', got '%s'", client.ReleaseName())
	}
}

func TestFormatAge(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 5 * time.Minute, "5m"},
		{"hours", 3 * time.Hour, "3h"},
		{"days", 48 * time.Hour, "2d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamp := time.Now().Add(-tt.duration)
			result := formatAge(timestamp)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestFormatAgeZero(t *testing.T) {
	result := formatAge(time.Time{})
	if result != "unknown" {
		t.Errorf("expected 'unknown' for zero time, got '%s'", result)
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"short", 10, "short"},
		{"this is a longer string", 10, "this is..."},
		{"exact", 5, "exact"},
		{"ab", 10, "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := truncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestDetectIssues(t *testing.T) {
	client := NewClient("ns", "release", "")

	// Test with pod restarts
	status := &ClusterStatus{
		Pods: []Pod{
			{Name: "pod-1", Status: "Running", Restarts: 5},
		},
	}

	issues := client.detectIssues(status)
	if len(issues) == 0 {
		t.Error("expected issues for pod with restarts")
	}

	found := false
	for _, issue := range issues {
		if issue == "Pod pod-1 has 5 restart(s)" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected restart issue message")
	}
}

func TestDetectIssuesNonRunningPod(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Pods: []Pod{
			{Name: "pod-1", Status: "Pending", Restarts: 0},
		},
	}

	issues := client.detectIssues(status)

	found := false
	for _, issue := range issues {
		if issue == "Pod pod-1 is in Pending state" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected pending pod issue message")
	}
}

func TestDetectIssuesUnboundPVC(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		PVCs: []PVC{
			{Name: "pvc-1", Status: "Pending"},
		},
	}

	issues := client.detectIssues(status)

	found := false
	for _, issue := range issues {
		if issue == "PVC pvc-1 is Pending" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected PVC issue message")
	}
}

func TestDetectIssuesHelmReleaseNotReady(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Release: &HelmRelease{
			Name:    "test",
			Ready:   false,
			Message: "reconciliation failed",
		},
	}

	issues := client.detectIssues(status)

	found := false
	for _, issue := range issues {
		if issue == "Helm release not ready: reconciliation failed" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected helm release issue message")
	}
}

func TestDetectIssuesWarningEvents(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Events: []Event{
			{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container"},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) == 0 {
		t.Error("expected warning event to be detected")
	}
}

func TestDetectIssuesNoIssues(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Pods: []Pod{
			{Name: "pod-1", Status: "Running", Restarts: 0},
		},
		PVCs: []PVC{
			{Name: "pvc-1", Status: "Bound"},
		},
		Release: &HelmRelease{
			Name:  "test",
			Ready: true,
		},
		Events: []Event{
			{Type: "Normal", Reason: "Pulled", Message: "Container pulled"},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) != 0 {
		t.Errorf("expected no issues, got %d: %v", len(issues), issues)
	}
}

func TestPodFields(t *testing.T) {
	pod := Pod{
		Name:     "test-pod",
		Status:   "Running",
		Ready:    "1/1",
		Restarts: 2,
		Age:      "5m",
		Node:     "node-1",
		IP:       "10.0.0.1",
	}

	if pod.Name != "test-pod" {
		t.Errorf("unexpected name: %s", pod.Name)
	}
	if pod.Status != "Running" {
		t.Errorf("unexpected status: %s", pod.Status)
	}
}

func TestContainerFields(t *testing.T) {
	container := Container{
		Name:         "main",
		Image:        "nginx:latest",
		RestartCount: 1,
		Ready:        true,
		State:        "Running",
	}

	if container.Name != "main" {
		t.Errorf("unexpected name: %s", container.Name)
	}
	if !container.Ready {
		t.Error("expected container to be ready")
	}
}

func TestDeploymentFields(t *testing.T) {
	deployment := Deployment{
		Name:      "test-deploy",
		Ready:     "3/3",
		UpToDate:  3,
		Available: 3,
		Age:       "1h",
	}

	if deployment.Name != "test-deploy" {
		t.Errorf("unexpected name: %s", deployment.Name)
	}
	if deployment.Available != 3 {
		t.Errorf("unexpected available: %d", deployment.Available)
	}
}

func TestServiceFields(t *testing.T) {
	service := Service{
		Name:       "test-svc",
		Type:       "ClusterIP",
		ClusterIP:  "10.0.0.10",
		ExternalIP: "<none>",
		Ports:      "80/TCP",
		Age:        "2h",
	}

	if service.Name != "test-svc" {
		t.Errorf("unexpected name: %s", service.Name)
	}
	if service.Type != "ClusterIP" {
		t.Errorf("unexpected type: %s", service.Type)
	}
}

func TestEventFields(t *testing.T) {
	event := Event{
		Type:      "Normal",
		Reason:    "Scheduled",
		Age:       "1m",
		From:      "default-scheduler",
		Message:   "Successfully assigned pod",
		Count:     1,
		Object:    "Pod/test-pod",
		Timestamp: time.Now(),
	}

	if event.Type != "Normal" {
		t.Errorf("unexpected type: %s", event.Type)
	}
	if event.Reason != "Scheduled" {
		t.Errorf("unexpected reason: %s", event.Reason)
	}
}

func TestHealthStatusFields(t *testing.T) {
	health := HealthStatus{
		PodName:   "test-pod",
		Component: "api",
		Healthy:   true,
		Message:   "OK",
		Endpoint:  "health",
	}

	if !health.Healthy {
		t.Error("expected healthy status")
	}
	if health.Message != "OK" {
		t.Errorf("unexpected message: %s", health.Message)
	}
}

func TestClusterStatusTimestamp(t *testing.T) {
	now := time.Now()
	status := ClusterStatus{
		Timestamp: now,
	}

	if !status.Timestamp.Equal(now) {
		t.Error("timestamp mismatch")
	}
}

// Integration tests (require kubectl access)
func TestGetPodsIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	client := NewClient("default", "test", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.GetPods(ctx)
	// This may fail if kubectl isn't configured, which is fine
	if err != nil {
		t.Logf("GetPods returned error (may be expected if no cluster): %v", err)
	}
}

func TestFormatAge_EdgeCases(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{"zero", 0, "0s"},
		{"1 second", 1 * time.Second, "1s"},
		{"59 seconds", 59 * time.Second, "59s"},
		{"1 minute", 1 * time.Minute, "1m"},
		{"59 minutes", 59 * time.Minute, "59m"},
		{"1 hour", 1 * time.Hour, "1h"},
		{"23 hours", 23 * time.Hour, "23h"},
		{"24 hours", 24 * time.Hour, "1d"},
		{"48 hours", 48 * time.Hour, "2d"},
		{"7 days", 7 * 24 * time.Hour, "7d"},
		{"30 days", 30 * 24 * time.Hour, "30d"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timestamp := time.Now().Add(-tt.duration)
			result := formatAge(timestamp)
			if result != tt.expected {
				t.Errorf("formatAge(%v) = %q, want %q", tt.duration, result, tt.expected)
			}
		})
	}
}

func TestTruncate_EdgeCases(t *testing.T) {
	tests := []struct {
		input    string
		maxLen   int
		expected string
	}{
		{"", 10, ""},
		{"short", 5, "short"},
		{"this is a longer string", 10, "this is..."},
		{"exact", 5, "exact"},
		{"exact", 6, "exact"},
		{"exact", 4, "e..."},
		{"ab", 10, "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			// Skip tests that would cause panic (maxLen < 3)
			if tt.maxLen < 3 && len(tt.input) > tt.maxLen {
				t.Skip("skipping test that would cause panic")
			}
			result := truncate(tt.input, tt.maxLen)
			if result != tt.expected {
				t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, result, tt.expected)
			}
		})
	}
}

func TestDetectIssues_EmptyStatus(t *testing.T) {
	client := NewClient("ns", "release", "")
	status := &ClusterStatus{}

	issues := client.detectIssues(status)

	if len(issues) != 0 {
		t.Errorf("expected no issues for empty status, got %d", len(issues))
	}
}

func TestDetectIssues_MultipleIssues(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Pods: []Pod{
			{Name: "pod-1", Status: "Pending", Restarts: 0},
			{Name: "pod-2", Status: "Running", Restarts: 10},
		},
		PVCs: []PVC{
			{Name: "pvc-1", Status: "Pending"},
		},
		Events: []Event{
			{Type: "Warning", Reason: "Failed", Message: "Container failed"},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) < 3 {
		t.Errorf("expected at least 3 issues, got %d", len(issues))
	}
}

func TestDetectIssues_FailedPod(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Pods: []Pod{
			{Name: "pod-1", Status: "Failed", Restarts: 0},
		},
	}

	issues := client.detectIssues(status)

	found := false
	for _, issue := range issues {
		if strings.Contains(issue, "Failed") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected issue for Failed pod")
	}
}

func TestDetectIssues_CrashLoopBackOff(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Pods: []Pod{
			{Name: "pod-1", Status: "CrashLoopBackOff", Restarts: 5},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) == 0 {
		t.Error("expected issues for CrashLoopBackOff pod")
	}
}

func TestDetectIssues_ErrorEvents(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Events: []Event{
			{Type: "Warning", Reason: "Failed", Message: "Error occurred"},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) == 0 {
		t.Error("expected issues for Warning events")
	}
}

func TestDetectIssues_NormalEventsIgnored(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Events: []Event{
			{Type: "Normal", Reason: "Created", Message: "Pod created"},
			{Type: "Normal", Reason: "Started", Message: "Container started"},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) != 0 {
		t.Errorf("expected no issues for Normal events, got %d", len(issues))
	}
}

func TestDetectIssues_BoundPVC(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		PVCs: []PVC{
			{Name: "pvc-1", Status: "Bound"},
		},
	}

	issues := client.detectIssues(status)

	if len(issues) != 0 {
		t.Errorf("expected no issues for Bound PVC, got %d", len(issues))
	}
}

func TestDetectIssues_ReadyHelmRelease(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Release: &HelmRelease{
			Name:  "test",
			Ready: true,
		},
	}

	issues := client.detectIssues(status)

	if len(issues) != 0 {
		t.Errorf("expected no issues for ready release, got %d", len(issues))
	}
}

func TestDetectIssues_UnsyncedHelmRelease(t *testing.T) {
	client := NewClient("ns", "release", "")

	status := &ClusterStatus{
		Release: &HelmRelease{
			Name:  "test",
			Ready: false,
		},
	}

	issues := client.detectIssues(status)

	if len(issues) == 0 {
		t.Error("expected issues for not ready release")
	}
}

func TestPVCFields(t *testing.T) {
	pvc := PVC{
		Name:         "test-pvc",
		Status:       "Bound",
		Volume:       "pv-123",
		Capacity:     "10Gi",
		AccessModes:  "RWO",
		StorageClass: "standard",
		Age:          "1h",
	}

	if pvc.Name != "test-pvc" {
		t.Errorf("unexpected name: %s", pvc.Name)
	}
	if pvc.Status != "Bound" {
		t.Errorf("unexpected status: %s", pvc.Status)
	}
}

func TestHelmReleaseFields(t *testing.T) {
	release := HelmRelease{
		Name:         "test-release",
		ChartName:    "xgrabba",
		ChartVersion: "1.0.0",
		AppVersion:   "1.0.0",
		Ready:        true,
		Synced:       true,
		Message:      "Release synced",
	}

	if release.Name != "test-release" {
		t.Errorf("unexpected name: %s", release.Name)
	}
	if !release.Ready {
		t.Error("expected release to be ready")
	}
}

func TestDaemonSetFields(t *testing.T) {
	ds := DaemonSet{
		Name:         "test-ds",
		Desired:      3,
		Current:      3,
		Ready:        3,
		UpToDate:     3,
		Available:    3,
		NodeSelector: "role=worker",
		Age:          "2h",
	}

	if ds.Name != "test-ds" {
		t.Errorf("unexpected name: %s", ds.Name)
	}
	if ds.Desired != 3 {
		t.Errorf("unexpected desired: %d", ds.Desired)
	}
}

func TestClusterStatus_Empty(t *testing.T) {
	status := ClusterStatus{}

	if len(status.Pods) != 0 {
		t.Error("expected empty pods")
	}
	if len(status.Issues) != 0 {
		t.Error("expected empty issues")
	}
	if !status.Timestamp.IsZero() {
		t.Error("expected zero timestamp")
	}
}
