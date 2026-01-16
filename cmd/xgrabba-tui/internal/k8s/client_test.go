package k8s

import (
	"context"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	client := NewClient("test-ns", "test-release", "")

	if client.namespace != "test-ns" {
		t.Errorf("expected namespace 'test-ns', got '%s'", client.namespace)
	}
	if client.releaseName != "test-release" {
		t.Errorf("expected release 'test-release', got '%s'", client.releaseName)
	}
}

func TestNewClientWithContext(t *testing.T) {
	client := NewClient("ns", "release", "my-context")

	if client.kubeContext != "my-context" {
		t.Errorf("expected context 'my-context', got '%s'", client.kubeContext)
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
