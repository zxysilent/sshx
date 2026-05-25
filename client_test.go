package main

import (
	"net"
	"os"
	"testing"
	"time"

	flag "github.com/spf13/pflag"
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

func TestPflagInterleaved(t *testing.T) {
	// pflag with SetInterspersed(true) parses flags anywhere in args
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetInterspersed(true)
	var host string
	var port int
	var showHelp bool
	fs.StringVarP(&host, "host", "H", "", "target host")
	fs.IntVarP(&port, "port", "p", 22, "ssh port")
	fs.BoolVarP(&showHelp, "help", "h", false, "show help")

	// Flags interleaved with positional args
	err := fs.Parse([]string{"cmd1", "-H", "h1", "-p", "2222", "cmd2"})
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if host != "h1" {
		t.Errorf("host = %q, want h1", host)
	}
	if port != 2222 {
		t.Errorf("port = %d, want 2222", port)
	}
	if !showHelp {
		// -h wasn't passed, so ok
	}
	args := fs.Args()
	if len(args) != 2 || args[0] != "cmd1" || args[1] != "cmd2" {
		t.Errorf("positional args = %v, want [cmd1 cmd2]", args)
	}
}

func TestExpandEnv(t *testing.T) {
	os.Setenv("TEST_VAR", "secret")
	os.Setenv("TEST_USER", "admin")
	defer func() {
		os.Unsetenv("TEST_VAR")
		os.Unsetenv("TEST_USER")
	}()

	tests := []struct {
		name, input, want string
	}{
		{"no var", "plain", "plain"},
		{"dollar var", "$TEST_VAR", "secret"},
		{"brace var", "${TEST_VAR}", "secret"},
		{"multiple vars", "$TEST_USER:$TEST_VAR", "admin:secret"},
		{"unknown var", "$UNKNOWN", "$UNKNOWN"},
		{"mixed", "user-$TEST_USER-pass-$TEST_VAR", "user-admin-pass-secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandEnv(tt.input)
			if got != tt.want {
				t.Errorf("expandEnv(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseHostEdgeCases(t *testing.T) {
	tests := []struct {
		name                         string
		raw                          string
		wantDisplay                  string
		wantUser, wantPass, wantHost string
		wantPort                     int
	}{
		{"empty password at colon", "root:@192.168.1.10", "root@192.168.1.10", "root", "", "192.168.1.10", 0},
		{"password with special chars", "root:p@ss:w0rd@host1", "root@host1", "root", "p@ss:w0rd", "host1", 0},
		{"only user no pass", "root@host1", "root@host1", "root", "", "host1", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := parseHost(tt.raw)
			if cfg.Display != tt.wantDisplay {
				t.Errorf("Display = %q, want %q", cfg.Display, tt.wantDisplay)
			}
			if cfg.User != tt.wantUser {
				t.Errorf("User = %q, want %q", cfg.User, tt.wantUser)
			}
			if cfg.Password != tt.wantPass {
				t.Errorf("Password = %q, want %q", cfg.Password, tt.wantPass)
			}
			if cfg.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", cfg.Host, tt.wantHost)
			}
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.wantPort)
			}
		})
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
		{"bare host", "192.168.1.10", "192.168.1.10", "", "", "192.168.1.10", 0},
		{"host:port", "192.168.1.10:2222", "192.168.1.10:2222", "", "", "192.168.1.10", 2222},
		{"user@host", "root@192.168.1.10", "root@192.168.1.10", "root", "", "192.168.1.10", 0},
		{"user:pass@host", "root:123@192.168.1.10", "root@192.168.1.10", "root", "123", "192.168.1.10", 0},
		{"user:pass@host:port", "root:123@192.168.1.10:2222", "root@192.168.1.10:2222", "root", "123", "192.168.1.10", 2222},
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

func TestBuildAuthMethods(t *testing.T) {
	// Only password
	methods, err := buildAuthMethods("test", "pass123", "")
	if err != nil {
		t.Fatalf("buildAuthMethods with password failed: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}

	// Empty key path + empty password -> error
	_, err = buildAuthMethods("test", "", "/nonexistent/key/path")
	if err == nil {
		t.Fatal("expected error for nonexistent key with empty password, got nil")
	}

	// Password with env var expansion
	os.Setenv("SECRET", "env-pass")
	defer os.Unsetenv("SECRET")
	methods, err = buildAuthMethods("test", "$SECRET", "")
	if err != nil {
		t.Fatalf("buildAuthMethods with $SECRET failed: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}
}

func TestConnectHostDirect(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	client, err := connectHost("127.0.0.1", intPort(srv), "testuser", "testpass",
		nil, "testuser", "testpass", "", 22, 5*time.Second)
	if err != nil {
		t.Fatalf("connectHost direct failed: %v", err)
	}
	defer client.Close()

	session, _ := client.NewSession()
	defer session.Close()
	out, err := session.Output("echo direct")
	if err != nil {
		t.Fatalf("session.Output failed: %v", err)
	}
	if string(out) != "echo direct" {
		t.Errorf("got %q, want %q", string(out), "echo direct")
	}
}

func intPort(s *testSSHServer) int {
	return s.listener.Addr().(*net.TCPAddr).Port
}
