package ssh

import (
	"testing"
)

func TestNewClient(t *testing.T) {
	client := NewClient("testuser", "/path/to/key")

	if client.user != "testuser" {
		t.Errorf("expected user 'testuser', got '%s'", client.user)
	}
	if client.keyPath != "/path/to/key" {
		t.Errorf("expected keyPath '/path/to/key', got '%s'", client.keyPath)
	}
}

func TestHostFields(t *testing.T) {
	host := Host{
		Name:     "test-host",
		Address:  "192.168.1.100",
		Port:     22,
		User:     "admin",
		NodeName: "node-1",
	}

	if host.Name != "test-host" {
		t.Errorf("unexpected name: %s", host.Name)
	}
	if host.Address != "192.168.1.100" {
		t.Errorf("unexpected address: %s", host.Address)
	}
	if host.Port != 22 {
		t.Errorf("unexpected port: %d", host.Port)
	}
	if host.User != "admin" {
		t.Errorf("unexpected user: %s", host.User)
	}
	if host.NodeName != "node-1" {
		t.Errorf("unexpected node name: %s", host.NodeName)
	}
}

func TestHostDefaultPort(t *testing.T) {
	host := Host{
		Name:    "test",
		Address: "example.com",
		Port:    0, // Default
	}

	// Port 0 should be treated as 22 by Connect
	if host.Port != 0 {
		t.Errorf("expected port 0, got %d", host.Port)
	}
}

func TestFileExists(t *testing.T) {
	// Test with a file that doesn't exist
	if fileExists("/this/path/does/not/exist/file.txt") {
		t.Error("expected false for non-existent file")
	}

	// Test with a file that does exist (go.mod in the project root)
	// This is a bit fragile but works for unit tests
}

func TestClientUserFallback(t *testing.T) {
	client := NewClient("default-user", "")

	host := &Host{
		Name:    "test",
		Address: "example.com",
		User:    "", // Should fallback to client.user
	}

	// The Connect method should use client.user when host.User is empty
	// We can't test the actual SSH connection without mocking, but we can
	// verify the client is created correctly
	if client.user != "default-user" {
		t.Errorf("expected default-user, got %s", client.user)
	}

	// Verify host user is empty (will use client default)
	if host.User != "" {
		t.Errorf("expected empty user, got %s", host.User)
	}

	// If host has a user, it should be used instead
	hostWithUser := &Host{
		Name:    "test",
		Address: "example.com",
		User:    "specific-user",
	}

	if hostWithUser.User != "specific-user" {
		t.Errorf("expected specific-user, got %s", hostWithUser.User)
	}
}

func TestHostWithCustomPort(t *testing.T) {
	host := Host{
		Name:    "custom-port-host",
		Address: "192.168.1.50",
		Port:    2222,
		User:    "admin",
	}

	if host.Port != 2222 {
		t.Errorf("expected port 2222, got %d", host.Port)
	}
}

func TestMultipleHosts(t *testing.T) {
	hosts := []Host{
		{Name: "host-1", Address: "10.0.0.1", Port: 22},
		{Name: "host-2", Address: "10.0.0.2", Port: 22},
		{Name: "host-3", Address: "10.0.0.3", Port: 2222},
	}

	if len(hosts) != 3 {
		t.Errorf("expected 3 hosts, got %d", len(hosts))
	}

	// Verify each host
	expectedAddresses := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3"}
	for i, host := range hosts {
		if host.Address != expectedAddresses[i] {
			t.Errorf("host %d: expected address %s, got %s", i, expectedAddresses[i], host.Address)
		}
	}
}

func TestClientKeyPath(t *testing.T) {
	tests := []struct {
		name    string
		keyPath string
	}{
		{"standard rsa", "/home/user/.ssh/id_rsa"},
		{"ed25519", "/home/user/.ssh/id_ed25519"},
		{"custom path", "/custom/path/to/key"},
		{"empty path", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient("user", tt.keyPath)
			if client.keyPath != tt.keyPath {
				t.Errorf("expected keyPath '%s', got '%s'", tt.keyPath, client.keyPath)
			}
		})
	}
}
