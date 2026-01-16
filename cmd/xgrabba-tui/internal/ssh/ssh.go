// Package ssh provides SSH connection management for the XGrabba TUI.
package ssh

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Client manages SSH connections.
type Client struct {
	user    string
	keyPath string
}

// NewClient creates a new SSH client.
func NewClient(user, keyPath string) *Client {
	return &Client{
		user:    user,
		keyPath: keyPath,
	}
}

// Host represents an SSH target host.
type Host struct {
	Name     string
	Address  string
	Port     int
	User     string
	NodeName string // Kubernetes node name if applicable
}

// Connection represents an active SSH connection.
type Connection struct {
	Host *Host
	cmd  *exec.Cmd
}

// Connect establishes an interactive SSH connection.
func (c *Client) Connect(ctx context.Context, host *Host) *exec.Cmd {
	user := host.User
	if user == "" {
		user = c.user
	}

	port := host.Port
	if port == 0 {
		port = 22
	}

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
	}

	if c.keyPath != "" && fileExists(c.keyPath) {
		args = append(args, "-i", c.keyPath)
	}

	args = append(args, "-p", fmt.Sprintf("%d", port))
	args = append(args, fmt.Sprintf("%s@%s", user, host.Address))

	cmd := exec.CommandContext(ctx, "ssh", args...)
	return cmd
}

// RunCommand executes a command on a remote host.
func (c *Client) RunCommand(ctx context.Context, host *Host, command string) (string, error) {
	user := host.User
	if user == "" {
		user = c.user
	}

	port := host.Port
	if port == 0 {
		port = 22
	}

	args := []string{
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-o", "BatchMode=yes",
	}

	if c.keyPath != "" && fileExists(c.keyPath) {
		args = append(args, "-i", c.keyPath)
	}

	args = append(args, "-p", fmt.Sprintf("%d", port))
	args = append(args, fmt.Sprintf("%s@%s", user, host.Address), command)

	cmd := exec.CommandContext(ctx, "ssh", args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// GetKubernetesNodes retrieves Kubernetes node addresses.
func GetKubernetesNodes(ctx context.Context, kubeContext string) ([]Host, error) {
	args := []string{"get", "nodes", "-o", "jsonpath={range .items[*]}{.metadata.name}|{.status.addresses[?(@.type==\"InternalIP\")].address}\\n{end}"}
	if kubeContext != "" {
		args = append([]string{"--context", kubeContext}, args...)
	}

	cmd := exec.CommandContext(ctx, "kubectl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get nodes: %w", err)
	}

	var hosts []Host
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 2 {
			hosts = append(hosts, Host{
				Name:     parts[0],
				NodeName: parts[0],
				Address:  parts[1],
				Port:     22,
			})
		}
	}

	return hosts, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
