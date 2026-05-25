package main

import (
	"testing"
	"time"
)

func TestResolveAddr(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		expected string
	}{
		{"plain host", "127.0.0.1", 22, "127.0.0.1:22"},
		{"host with port", "127.0.0.1:2222", 22, "127.0.0.1:2222"},
		{"hostname", "server.local", 2222, "server.local:2222"},
		{"hostname with port", "server.local:99", 2222, "server.local:99"},
		{"ipv6 plain", "::1", 22, "[::1]:22"},
		{"ipv6 with port", "[::1]:8022", 22, "[::1]:8022"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveAddr(tt.host, tt.port)
			if got != tt.expected {
				t.Errorf("resolveAddr(%q, %d) = %q, want %q", tt.host, tt.port, got, tt.expected)
			}
		})
	}
}

func TestSSHClientValidAuth(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	client, err := sshClient(srv.Addr(), "testuser", "testpass", "", 5*time.Second)
	if err != nil {
		t.Fatalf("sshClient failed: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	session.Close()
}

func TestSSHClientInvalidPassword(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	_, err := sshClient(srv.Addr(), "testuser", "wrongpass", "", 1*time.Second)
	if err == nil {
		t.Fatal("expected error for wrong password, got nil")
	}
}

func TestSSHClientNoAuth(t *testing.T) {
	_, err := sshClient("127.0.0.1:22", "nobody", "", "/nonexistent/key", 100*time.Millisecond)
	if err == nil {
		t.Fatal("expected error when no auth methods available, got nil")
	}
}

func TestSSHClientTimeout(t *testing.T) {
	// Connect to a non-routable address to trigger timeout
	_, err := sshClient("10.255.255.1:22", "root", "", "", 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestDefaultUser(t *testing.T) {
	u := defaultUser()
	if u == "" {
		t.Fatal("defaultUser returned empty string")
	}
}

func TestHostsFlag(t *testing.T) {
	var h hostsFlag
	h.Set("host1")
	h.Set("host2")
	if len(h) != 2 {
		t.Fatalf("expected 2 hosts, got %d", len(h))
	}
	if h[0] != "host1" || h[1] != "host2" {
		t.Fatalf("unexpected hosts: %v", h)
	}
	if h.String() != "host1,host2" {
		t.Fatalf("unexpected String(): %s", h.String())
	}
}

func TestParseHost(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		display  string
		user     string
		password string
		host     string
		port     int
	}{
		{"bare host", "172.22.1.9", "172.22.1.9", "", "", "172.22.1.9", 0},
		{"host:port", "172.22.1.9:2222", "172.22.1.9:2222", "", "", "172.22.1.9", 2222},
		{"user@host", "root@172.22.1.9", "root@172.22.1.9", "root", "", "172.22.1.9", 0},
		{"user:pass@host", "root:123@172.22.1.9", "root:123@172.22.1.9", "root", "123", "172.22.1.9", 0},
		{"user:pass@host:port", "root:123@172.22.1.9:2222", "root:123@172.22.1.9:2222", "root", "123", "172.22.1.9", 2222},
		{"hostname", "server.local", "server.local", "", "", "server.local", 0},
		{"user@hostname:port", "admin@server.local:8022", "admin@server.local:8022", "admin", "", "server.local", 8022},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseHost(tt.raw)
			if cfg.Display != tt.display {
				t.Errorf("Display = %q, want %q", cfg.Display, tt.display)
			}
			if cfg.User != tt.user {
				t.Errorf("User = %q, want %q", cfg.User, tt.user)
			}
			if cfg.Password != tt.password {
				t.Errorf("Password = %q, want %q", cfg.Password, tt.password)
			}
			if cfg.Host != tt.host {
				t.Errorf("Host = %q, want %q", cfg.Host, tt.host)
			}
			if cfg.Port != tt.port {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.port)
			}
		})
	}
}
