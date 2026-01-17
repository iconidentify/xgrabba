// Package k8s provides Kubernetes client operations for the XGrabba TUI.
package k8s

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Client provides Kubernetes operations via kubectl.
type Client struct {
	namespace   string
	releaseName string
	kubeContext string
}

// NewClient creates a new Kubernetes client.
func NewClient(namespace, releaseName, kubeContext string) *Client {
	return &Client{
		namespace:   namespace,
		releaseName: releaseName,
		kubeContext: kubeContext,
	}
}

// Pod represents a Kubernetes pod.
type Pod struct {
	Name       string
	Status     string
	Ready      string
	Restarts   int
	Age        string
	Node       string
	IP         string
	Containers []Container
}

// Container represents a container in a pod.
type Container struct {
	Name         string
	Image        string
	RestartCount int
	Ready        bool
	State        string
}

// Deployment represents a Kubernetes deployment.
type Deployment struct {
	Name      string
	Ready     string
	UpToDate  int
	Available int
	Age       string
}

// DaemonSet represents a Kubernetes daemonset.
type DaemonSet struct {
	Name         string
	Desired      int
	Current      int
	Ready        int
	UpToDate     int
	Available    int
	NodeSelector string
	Age          string
}

// Service represents a Kubernetes service.
type Service struct {
	Name       string
	Type       string
	ClusterIP  string
	ExternalIP string
	Ports      string
	Age        string
}

// PVC represents a persistent volume claim.
type PVC struct {
	Name         string
	Status       string
	Volume       string
	Capacity     string
	AccessModes  string
	StorageClass string
	Age          string
}

// Event represents a Kubernetes event.
type Event struct {
	Type      string
	Reason    string
	Age       string
	From      string
	Message   string
	Count     int
	Object    string
	Timestamp time.Time
}

// HelmRelease represents Crossplane Helm release status.
type HelmRelease struct {
	Name         string
	ChartName    string
	ChartVersion string
	AppVersion   string
	Ready        bool
	Synced       bool
	Message      string
}

// HealthStatus represents health check results.
type HealthStatus struct {
	PodName   string
	Component string
	Healthy   bool
	Message   string
	Endpoint  string
}

// ClusterStatus holds complete cluster status.
type ClusterStatus struct {
	Timestamp    time.Time
	Release      *HelmRelease
	Pods         []Pod
	Deployments  []Deployment
	DaemonSets   []DaemonSet
	Services     []Service
	PVCs         []PVC
	Events       []Event
	HealthChecks []HealthStatus
	Issues       []string
}

// CrossplanePackage represents a Crossplane package resource (Provider/Configuration).
type CrossplanePackage struct {
	Name        string
	Package     string
	Revision    string
	Healthy     bool
	Installed   bool
	Message     string
	Age         string
	PackageType string
}

// CrossplaneComposition represents a Crossplane Composition.
type CrossplaneComposition struct {
	Name          string
	CompositeKind string
	Age           string
}

// CrossplaneXRD represents a CompositeResourceDefinition.
type CrossplaneXRD struct {
	Name      string
	Kind      string
	ClaimKind string
	Age       string
}

// CrossplaneStatus holds Crossplane-specific status data.
type CrossplaneStatus struct {
	Providers      []CrossplanePackage
	Configurations []CrossplanePackage
	Compositions   []CrossplaneComposition
	XRDs           []CrossplaneXRD
	Issues         []string
}

// kubectl executes a kubectl command and returns output.
func (c *Client) kubectl(ctx context.Context, args ...string) ([]byte, error) {
	cmdArgs := args
	if c.kubeContext != "" {
		cmdArgs = append([]string{"--context", c.kubeContext}, args...)
	}
	cmdArgs = append([]string{"-n", c.namespace}, cmdArgs...)

	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	return cmd.CombinedOutput()
}

// kubectlNoNS executes kubectl without namespace flag.
func (c *Client) kubectlNoNS(ctx context.Context, args ...string) ([]byte, error) {
	cmdArgs := args
	if c.kubeContext != "" {
		cmdArgs = append([]string{"--context", c.kubeContext}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	return cmd.CombinedOutput()
}

// GetStatus retrieves complete cluster status.
func (c *Client) GetStatus(ctx context.Context) (*ClusterStatus, error) {
	status := &ClusterStatus{
		Timestamp: time.Now(),
	}

	// Get Helm release
	release, err := c.GetHelmRelease(ctx)
	if err == nil {
		status.Release = release
	}

	// Get pods
	pods, err := c.GetPods(ctx)
	if err == nil {
		status.Pods = pods
	}

	// Get deployments
	deployments, err := c.GetDeployments(ctx)
	if err == nil {
		status.Deployments = deployments
	}

	// Get daemonsets
	daemonsets, err := c.GetDaemonSets(ctx)
	if err == nil {
		status.DaemonSets = daemonsets
	}

	// Get services
	services, err := c.GetServices(ctx)
	if err == nil {
		status.Services = services
	}

	// Get PVCs
	pvcs, err := c.GetPVCs(ctx)
	if err == nil {
		status.PVCs = pvcs
	}

	// Get events
	events, err := c.GetEvents(ctx, 15)
	if err == nil {
		status.Events = events
	}

	// Check for issues
	status.Issues = c.detectIssues(status)

	return status, nil
}

// GetPods returns all pods in the namespace.
func (c *Client) GetPods(ctx context.Context) ([]Pod, error) {
	out, err := c.kubectl(ctx, "get", "pods", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				NodeName   string `json:"nodeName"`
				Containers []struct {
					Name  string `json:"name"`
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				PodIP             string `json:"podIP"`
				ContainerStatuses []struct {
					Name         string `json:"name"`
					Ready        bool   `json:"ready"`
					RestartCount int    `json:"restartCount"`
					State        struct {
						Running    *struct{}                `json:"running"`
						Waiting    *struct{ Reason string } `json:"waiting"`
						Terminated *struct{ Reason string } `json:"terminated"`
					} `json:"state"`
				} `json:"containerStatuses"`
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse pods: %w", err)
	}

	var pods []Pod
	for _, item := range result.Items {
		pod := Pod{
			Name:   item.Metadata.Name,
			Status: item.Status.Phase,
			Node:   item.Spec.NodeName,
			IP:     item.Status.PodIP,
			Age:    formatAge(item.Metadata.CreationTimestamp),
		}

		// Calculate ready count
		readyCount := 0
		totalCount := len(item.Spec.Containers)
		for _, cs := range item.Status.ContainerStatuses {
			pod.Restarts += cs.RestartCount
			if cs.Ready {
				readyCount++
			}

			state := "Unknown"
			if cs.State.Running != nil {
				state = "Running"
			} else if cs.State.Waiting != nil {
				state = cs.State.Waiting.Reason
			} else if cs.State.Terminated != nil {
				state = cs.State.Terminated.Reason
			}

			// Find image
			var image string
			for _, c := range item.Spec.Containers {
				if c.Name == cs.Name {
					image = c.Image
					break
				}
			}

			pod.Containers = append(pod.Containers, Container{
				Name:         cs.Name,
				Image:        image,
				RestartCount: cs.RestartCount,
				Ready:        cs.Ready,
				State:        state,
			})
		}
		pod.Ready = fmt.Sprintf("%d/%d", readyCount, totalCount)

		// Check if pod is actually ready
		for _, cond := range item.Status.Conditions {
			if cond.Type == "Ready" && cond.Status != "True" {
				pod.Status = "NotReady"
			}
		}

		pods = append(pods, pod)
	}

	return pods, nil
}

// GetDeployments returns all deployments in the namespace.
func (c *Client) GetDeployments(ctx context.Context) ([]Deployment, error) {
	out, err := c.kubectl(ctx, "get", "deployments", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get deployments: %w", err)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Status struct {
				ReadyReplicas     int `json:"readyReplicas"`
				Replicas          int `json:"replicas"`
				UpdatedReplicas   int `json:"updatedReplicas"`
				AvailableReplicas int `json:"availableReplicas"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse deployments: %w", err)
	}

	var deployments []Deployment
	for _, item := range result.Items {
		deployments = append(deployments, Deployment{
			Name:      item.Metadata.Name,
			Ready:     fmt.Sprintf("%d/%d", item.Status.ReadyReplicas, item.Status.Replicas),
			UpToDate:  item.Status.UpdatedReplicas,
			Available: item.Status.AvailableReplicas,
			Age:       formatAge(item.Metadata.CreationTimestamp),
		})
	}

	return deployments, nil
}

// GetDaemonSets returns all daemonsets in the namespace.
func (c *Client) GetDaemonSets(ctx context.Context) ([]DaemonSet, error) {
	out, err := c.kubectl(ctx, "get", "daemonsets", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get daemonsets: %w", err)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Selector struct {
					MatchLabels map[string]string `json:"matchLabels"`
				} `json:"selector"`
			} `json:"spec"`
			Status struct {
				DesiredNumberScheduled int `json:"desiredNumberScheduled"`
				CurrentNumberScheduled int `json:"currentNumberScheduled"`
				NumberReady            int `json:"numberReady"`
				UpdatedNumberScheduled int `json:"updatedNumberScheduled"`
				NumberAvailable        int `json:"numberAvailable"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse daemonsets: %w", err)
	}

	var daemonsets []DaemonSet
	for _, item := range result.Items {
		daemonsets = append(daemonsets, DaemonSet{
			Name:      item.Metadata.Name,
			Desired:   item.Status.DesiredNumberScheduled,
			Current:   item.Status.CurrentNumberScheduled,
			Ready:     item.Status.NumberReady,
			UpToDate:  item.Status.UpdatedNumberScheduled,
			Available: item.Status.NumberAvailable,
			Age:       formatAge(item.Metadata.CreationTimestamp),
		})
	}

	return daemonsets, nil
}

// GetServices returns all services in the namespace.
func (c *Client) GetServices(ctx context.Context) ([]Service, error) {
	out, err := c.kubectl(ctx, "get", "services", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get services: %w", err)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Type       string   `json:"type"`
				ClusterIP  string   `json:"clusterIP"`
				ClusterIPs []string `json:"clusterIPs"`
				Ports      []struct {
					Port       int         `json:"port"`
					TargetPort interface{} `json:"targetPort"`
					Protocol   string      `json:"protocol"`
					Name       string      `json:"name"`
				} `json:"ports"`
			} `json:"spec"`
			Status struct {
				LoadBalancer struct {
					Ingress []struct {
						IP       string `json:"ip"`
						Hostname string `json:"hostname"`
					} `json:"ingress"`
				} `json:"loadBalancer"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse services: %w", err)
	}

	var services []Service
	for _, item := range result.Items {
		svc := Service{
			Name:      item.Metadata.Name,
			Type:      item.Spec.Type,
			ClusterIP: item.Spec.ClusterIP,
			Age:       formatAge(item.Metadata.CreationTimestamp),
		}

		// Format ports
		var ports []string
		for _, p := range item.Spec.Ports {
			ports = append(ports, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
		svc.Ports = strings.Join(ports, ",")

		// External IP
		if len(item.Status.LoadBalancer.Ingress) > 0 {
			ing := item.Status.LoadBalancer.Ingress[0]
			if ing.IP != "" {
				svc.ExternalIP = ing.IP
			} else {
				svc.ExternalIP = ing.Hostname
			}
		} else {
			svc.ExternalIP = "<none>"
		}

		services = append(services, svc)
	}

	return services, nil
}

// GetPVCs returns all PVCs in the namespace.
func (c *Client) GetPVCs(ctx context.Context) ([]PVC, error) {
	out, err := c.kubectl(ctx, "get", "pvc", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("failed to get pvcs: %w", err)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				AccessModes      []string `json:"accessModes"`
				StorageClassName string   `json:"storageClassName"`
				VolumeName       string   `json:"volumeName"`
			} `json:"spec"`
			Status struct {
				Phase    string `json:"phase"`
				Capacity struct {
					Storage string `json:"storage"`
				} `json:"capacity"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse pvcs: %w", err)
	}

	var pvcs []PVC
	for _, item := range result.Items {
		pvcs = append(pvcs, PVC{
			Name:         item.Metadata.Name,
			Status:       item.Status.Phase,
			Volume:       item.Spec.VolumeName,
			Capacity:     item.Status.Capacity.Storage,
			AccessModes:  strings.Join(item.Spec.AccessModes, ","),
			StorageClass: item.Spec.StorageClassName,
			Age:          formatAge(item.Metadata.CreationTimestamp),
		})
	}

	return pvcs, nil
}

// GetEvents returns recent events in the namespace.
func (c *Client) GetEvents(ctx context.Context, limit int) ([]Event, error) {
	out, err := c.kubectl(ctx, "get", "events", "-o", "json", "--sort-by=.lastTimestamp")
	if err != nil {
		return nil, fmt.Errorf("failed to get events: %w", err)
	}

	var result struct {
		Items []struct {
			Type    string `json:"type"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
			Count   int    `json:"count"`
			Source  struct {
				Component string `json:"component"`
			} `json:"source"`
			InvolvedObject struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
			} `json:"involvedObject"`
			LastTimestamp time.Time `json:"lastTimestamp"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse events: %w", err)
	}

	var events []Event
	for _, item := range result.Items {
		events = append(events, Event{
			Type:      item.Type,
			Reason:    item.Reason,
			Message:   item.Message,
			Count:     item.Count,
			From:      item.Source.Component,
			Object:    fmt.Sprintf("%s/%s", item.InvolvedObject.Kind, item.InvolvedObject.Name),
			Timestamp: item.LastTimestamp,
			Age:       formatAge(item.LastTimestamp),
		})
	}

	// Sort by timestamp descending and limit
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	if len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

// GetHelmRelease returns Crossplane Helm release status.
func (c *Client) GetHelmRelease(ctx context.Context) (*HelmRelease, error) {
	out, err := c.kubectl(ctx, "get", "release.helm.crossplane.io", c.releaseName, "-o", "json")
	if err != nil {
		// Extract the actual error message from kubectl output
		errStr := strings.TrimSpace(string(out))
		if errStr == "" {
			errStr = err.Error()
		}
		
		// Check if it's a "not found" error
		if strings.Contains(errStr, "NotFound") || strings.Contains(errStr, "not found") || strings.Contains(errStr, "does not exist") {
			return nil, fmt.Errorf("release '%s' not found in namespace '%s' (not using Crossplane?): %s", c.releaseName, c.namespace, errStr)
		}
		
		// Return the actual kubectl error message
		return nil, fmt.Errorf("failed to get release '%s' in namespace '%s': %s", c.releaseName, c.namespace, errStr)
	}

	var result struct {
		Spec struct {
			ForProvider struct {
				Chart struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"chart"`
			} `json:"forProvider"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type    string `json:"type"`
				Status  string `json:"status"`
				Message string `json:"message"`
			} `json:"conditions"`
			AtProvider struct {
				AppVersion string `json:"appVersion"`
			} `json:"atProvider"`
		} `json:"status"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("failed to parse release: %w", err)
	}

	release := &HelmRelease{
		Name:         c.releaseName,
		ChartName:    result.Spec.ForProvider.Chart.Name,
		ChartVersion: result.Spec.ForProvider.Chart.Version,
		AppVersion:   result.Status.AtProvider.AppVersion,
	}

	for _, cond := range result.Status.Conditions {
		switch cond.Type {
		case "Ready":
			release.Ready = cond.Status == "True"
			if !release.Ready {
				release.Message = cond.Message
			}
		case "Synced":
			release.Synced = cond.Status == "True"
		}
	}

	return release, nil
}

// GetLogs streams logs from a pod.
func (c *Client) GetLogs(ctx context.Context, podName string, follow bool, tailLines int) (io.ReadCloser, error) {
	args := []string{"logs", podName}
	if follow {
		args = append(args, "-f")
	}
	if tailLines > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tailLines))
	}

	cmdArgs := args
	if c.kubeContext != "" {
		cmdArgs = append([]string{"--context", c.kubeContext}, args...)
	}
	cmdArgs = append([]string{"-n", c.namespace}, cmdArgs...)

	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &logReader{
		reader: stdout,
		cmd:    cmd,
	}, nil
}

// GetLogsByLabel streams logs from pods matching a label.
func (c *Client) GetLogsByLabel(ctx context.Context, label string, follow bool, tailLines int) (io.ReadCloser, error) {
	args := []string{"logs", "-l", label}
	if follow {
		args = append(args, "-f")
	}
	if tailLines > 0 {
		args = append(args, "--tail", fmt.Sprintf("%d", tailLines))
	}

	cmdArgs := args
	if c.kubeContext != "" {
		cmdArgs = append([]string{"--context", c.kubeContext}, args...)
	}
	cmdArgs = append([]string{"-n", c.namespace}, cmdArgs...)

	cmd := exec.CommandContext(ctx, "kubectl", cmdArgs...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return &logReader{
		reader: stdout,
		cmd:    cmd,
	}, nil
}

// logReader wraps a pipe reader with command cleanup.
type logReader struct {
	reader io.ReadCloser
	cmd    *exec.Cmd
}

func (l *logReader) Read(p []byte) (n int, err error) {
	return l.reader.Read(p)
}

func (l *logReader) Close() error {
	l.reader.Close()
	return l.cmd.Process.Kill()
}

// Exec executes a command in a pod.
func (c *Client) Exec(ctx context.Context, podName string, command []string) (string, error) {
	args := append([]string{"exec", podName, "--"}, command...)
	out, err := c.kubectl(ctx, args...)
	return string(out), err
}

// ExecInteractive starts an interactive shell in a pod.
func (c *Client) ExecInteractive(ctx context.Context, podName string, command []string) *exec.Cmd {
	args := []string{"exec", "-it", podName, "--"}
	args = append(args, command...)
	cmdArgs := args
	if c.kubeContext != "" {
		cmdArgs = append([]string{"--context", c.kubeContext}, cmdArgs...)
	}
	cmdArgs = append([]string{"-n", c.namespace}, cmdArgs...)

	return exec.CommandContext(ctx, "kubectl", cmdArgs...)
}

// PortForward starts port forwarding to a pod.
func (c *Client) PortForward(ctx context.Context, podName string, localPort, remotePort int) (*exec.Cmd, error) {
	args := []string{"-n", c.namespace, "port-forward", podName, fmt.Sprintf("%d:%d", localPort, remotePort)}
	if c.kubeContext != "" {
		args = append([]string{"--context", c.kubeContext}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

// CheckHealth performs health check on a pod.
func (c *Client) CheckHealth(ctx context.Context, podName string, port int, endpoint string) (*HealthStatus, error) {
	cmd := fmt.Sprintf("wget -q -O- http://localhost:%d/%s 2>/dev/null || curl -sf http://localhost:%d/%s 2>/dev/null || echo 'FAILED'", port, endpoint, port, endpoint)
	out, err := c.Exec(ctx, podName, []string{"sh", "-c", cmd})
	if err != nil {
		return &HealthStatus{
			PodName:  podName,
			Endpoint: endpoint,
			Healthy:  false,
			Message:  err.Error(),
		}, nil
	}

	out = strings.TrimSpace(out)
	healthy := out != "" && out != "FAILED"

	return &HealthStatus{
		PodName:  podName,
		Endpoint: endpoint,
		Healthy:  healthy,
		Message:  out,
	}, nil
}

// DescribePod returns detailed pod information using kubectl describe.
func (c *Client) DescribePod(ctx context.Context, podName string) (string, error) {
	out, err := c.kubectl(ctx, "describe", "pod", podName)
	return string(out), err
}

// GetPodNames returns pod names matching a label selector.
func (c *Client) GetPodNames(ctx context.Context, labelSelector string) ([]string, error) {
	out, err := c.kubectl(ctx, "get", "pods", "-l", labelSelector, "-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		return nil, err
	}

	names := strings.Fields(string(out))
	return names, nil
}

// GetLatestChartVersion retrieves the latest chart version from the Helm repo.
func (c *Client) GetLatestChartVersion(ctx context.Context, repoURL string) (string, error) {
	cmd := exec.CommandContext(ctx, "helm", "show", "chart", repoURL)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get chart info: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "version:")), nil
		}
	}

	return "", fmt.Errorf("version not found in chart")
}

// TriggerUpgrade triggers a Helm upgrade via Crossplane.
func (c *Client) TriggerUpgrade(ctx context.Context) error {
	// Remove pinned chart version to use latest
	_, _ = c.kubectl(ctx, "patch", "release.helm.crossplane.io", c.releaseName, "--type=json", "-p=[{\"op\": \"remove\", \"path\": \"/spec/forProvider/chart/version\"}]")

	// Remove hardcoded image tag
	_, _ = c.kubectl(ctx, "patch", "release.helm.crossplane.io", c.releaseName, "--type=json", "-p=[{\"op\": \"remove\", \"path\": \"/spec/forProvider/values/image/tag\"}]")

	return nil
}

// WaitForRollout waits for a deployment rollout to complete.
func (c *Client) WaitForRollout(ctx context.Context, deploymentName string, timeout time.Duration) error {
	args := []string{"rollout", "status", "deployment/" + deploymentName, "--timeout", timeout.String()}
	_, err := c.kubectl(ctx, args...)
	return err
}

// WaitForDaemonSetRollout waits for a daemonset rollout to complete.
func (c *Client) WaitForDaemonSetRollout(ctx context.Context, dsName string, timeout time.Duration) error {
	args := []string{"rollout", "status", "daemonset/" + dsName, "--timeout", timeout.String()}
	_, err := c.kubectl(ctx, args...)
	return err
}

// GetCrossplaneStatus returns Crossplane package/composition status.
func (c *Client) GetCrossplaneStatus(ctx context.Context) (*CrossplaneStatus, error) {
	status := &CrossplaneStatus{}

	providers, err := c.getCrossplanePackages(ctx, "providers.pkg.crossplane.io", "Provider")
	if err == nil {
		status.Providers = providers
	}

	configurations, err := c.getCrossplanePackages(ctx, "configurations.pkg.crossplane.io", "Configuration")
	if err == nil {
		status.Configurations = configurations
	}

	compositions, err := c.getCrossplaneCompositions(ctx)
	if err == nil {
		status.Compositions = compositions
	}

	xrds, err := c.getCrossplaneXRDs(ctx)
	if err == nil {
		status.XRDs = xrds
	}

	status.Issues = detectCrossplaneIssues(status)
	return status, nil
}

func (c *Client) getCrossplanePackages(ctx context.Context, resource, packageType string) ([]CrossplanePackage, error) {
	out, err := c.kubectlNoNS(ctx, "get", resource, "-o", "json")
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Package string `json:"package"`
			} `json:"spec"`
			Status struct {
				CurrentRevision string `json:"currentRevision"`
				Conditions      []struct {
					Type    string `json:"type"`
					Status  string `json:"status"`
					Message string `json:"message"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse crossplane packages: %w", err)
	}

	packages := make([]CrossplanePackage, 0, len(result.Items))
	for _, item := range result.Items {
		pkg := CrossplanePackage{
			Name:        item.Metadata.Name,
			Package:     item.Spec.Package,
			Revision:    item.Status.CurrentRevision,
			Age:         formatAge(item.Metadata.CreationTimestamp),
			PackageType: packageType,
		}
		for _, cond := range item.Status.Conditions {
			switch cond.Type {
			case "Healthy":
				pkg.Healthy = cond.Status == "True"
				if cond.Status != "True" && pkg.Message == "" {
					pkg.Message = cond.Message
				}
			case "Installed":
				pkg.Installed = cond.Status == "True"
				if cond.Status != "True" && pkg.Message == "" {
					pkg.Message = cond.Message
				}
			}
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func (c *Client) getCrossplaneCompositions(ctx context.Context) ([]CrossplaneComposition, error) {
	out, err := c.kubectlNoNS(ctx, "get", "compositions.apiextensions.crossplane.io", "-o", "json")
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				CompositeTypeRef struct {
					Kind string `json:"kind"`
				} `json:"compositeTypeRef"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse compositions: %w", err)
	}

	compositions := make([]CrossplaneComposition, 0, len(result.Items))
	for _, item := range result.Items {
		compositions = append(compositions, CrossplaneComposition{
			Name:          item.Metadata.Name,
			CompositeKind: item.Spec.CompositeTypeRef.Kind,
			Age:           formatAge(item.Metadata.CreationTimestamp),
		})
	}
	return compositions, nil
}

func (c *Client) getCrossplaneXRDs(ctx context.Context) ([]CrossplaneXRD, error) {
	out, err := c.kubectlNoNS(ctx, "get", "compositeresourcedefinitions.apiextensions.crossplane.io", "-o", "json")
	if err != nil {
		return nil, err
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string    `json:"name"`
				CreationTimestamp time.Time `json:"creationTimestamp"`
			} `json:"metadata"`
			Spec struct {
				Names struct {
					Kind string `json:"kind"`
				} `json:"names"`
				ClaimNames struct {
					Kind string `json:"kind"`
				} `json:"claimNames"`
			} `json:"spec"`
		} `json:"items"`
	}

	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse xrds: %w", err)
	}

	xrds := make([]CrossplaneXRD, 0, len(result.Items))
	for _, item := range result.Items {
		xrds = append(xrds, CrossplaneXRD{
			Name:      item.Metadata.Name,
			Kind:      item.Spec.Names.Kind,
			ClaimKind: item.Spec.ClaimNames.Kind,
			Age:       formatAge(item.Metadata.CreationTimestamp),
		})
	}
	return xrds, nil
}

func detectCrossplaneIssues(status *CrossplaneStatus) []string {
	var issues []string
	for _, pkg := range status.Providers {
		if !pkg.Installed || !pkg.Healthy {
			msg := pkg.Message
			if msg == "" {
				msg = "Provider not healthy"
			}
			issues = append(issues, fmt.Sprintf("Provider %s: %s", pkg.Name, truncate(msg, 60)))
		}
	}
	for _, pkg := range status.Configurations {
		if !pkg.Installed || !pkg.Healthy {
			msg := pkg.Message
			if msg == "" {
				msg = "Configuration not healthy"
			}
			issues = append(issues, fmt.Sprintf("Configuration %s: %s", pkg.Name, truncate(msg, 60)))
		}
	}
	return issues
}

// detectIssues identifies potential problems in the cluster status.
func (c *Client) detectIssues(status *ClusterStatus) []string {
	var issues []string

	// Check for pods with restarts
	for _, pod := range status.Pods {
		if pod.Restarts > 0 {
			issues = append(issues, fmt.Sprintf("Pod %s has %d restart(s)", pod.Name, pod.Restarts))
		}
	}

	// Check for non-running pods
	for _, pod := range status.Pods {
		if pod.Status != "Running" && pod.Status != "Succeeded" {
			issues = append(issues, fmt.Sprintf("Pod %s is in %s state", pod.Name, pod.Status))
		}
	}

	// Check for unbound PVCs
	for _, pvc := range status.PVCs {
		if pvc.Status != "Bound" {
			issues = append(issues, fmt.Sprintf("PVC %s is %s", pvc.Name, pvc.Status))
		}
	}

	// Check Helm release status
	if status.Release != nil && !status.Release.Ready {
		issues = append(issues, fmt.Sprintf("Helm release not ready: %s", status.Release.Message))
	}

	// Check for warning events
	for _, event := range status.Events {
		if event.Type == "Warning" {
			issues = append(issues, fmt.Sprintf("Warning: %s - %s", event.Reason, truncate(event.Message, 50)))
		}
	}

	return issues
}

// Namespace returns the configured namespace.
func (c *Client) Namespace() string {
	return c.namespace
}

// ReleaseName returns the configured release name.
func (c *Client) ReleaseName() string {
	return c.releaseName
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	d := time.Since(t)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
