package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// ---- table-driven edge cases ----

func TestShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"simple", "'simple'"},
		{"/path/to/file", "'/path/to/file'"},
		{"it's a test", "'it'\\''s a test'"},
		{"a'b'c", "'a'\\''b'\\''c'"},
		{"", "''"},
	}
	for _, tt := range tests {
		got := shellQuote(tt.in)
		if got != tt.want {
			t.Errorf("shellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStripShellQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{"'quoted'", "quoted"},
		{"'/a/b/c'", "/a/b/c"},
		{"noquotes", "noquotes"},
		{"''", ""},
		{"'single", "'single"}, // unbalanced — unchanged
	}
	for _, tt := range tests {
		got := stripShellQuote(tt.in)
		if got != tt.want {
			t.Errorf("stripShellQuote(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseHostIPv6(t *testing.T) {
	tests := []struct {
		raw      string
		wantHost string
		wantPort int
		wantUser string
	}{
		{"user:pass@[::1]:8022", "::1", 8022, "user"},
		{"[::1]", "::1", 0, ""},
		{"[::1]:22", "::1", 22, ""},
		{"root@[fe80::1%eth0]:2222", "fe80::1%eth0", 2222, "root"},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			cfg := parseHost(tt.raw)
			if cfg.Host != tt.wantHost {
				t.Errorf("Host = %q, want %q", cfg.Host, tt.wantHost)
			}
			if cfg.Port != tt.wantPort {
				t.Errorf("Port = %d, want %d", cfg.Port, tt.wantPort)
			}
			if cfg.User != tt.wantUser {
				t.Errorf("User = %q, want %q", cfg.User, tt.wantUser)
			}
		})
	}
}

func TestBuildAuthMethodsEnvExpansion(t *testing.T) {
	os.Setenv("PASS_FUZZ", "my-pw")
	defer os.Unsetenv("PASS_FUZZ")

	methods, err := buildAuthMethods("testuser", "$PASS_FUZZ", "")
	if err != nil {
		t.Fatalf("buildAuthMethods: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}
}

func TestRunCommandScriptExecSerial(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	clients := map[string]*ssh.Client{"h1": srv.newClient(t)}
	defer clients["h1"].Close()

	oldOut, oldErr := os.Stdout, os.Stderr
	rO, wO, _ := os.Pipe()
	rE, wE, _ := os.Pipe()
	os.Stdout, os.Stderr = wO, wE

	runCommandSerialExec(clients, "echo via script", true)

	wO.Close()
	wE.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	out, _ := io.ReadAll(rO)
	errOut, _ := io.ReadAll(rE)
	combined := string(out) + string(errOut)

	if !strings.Contains(combined, "===== exec on h1 =====") {
		t.Errorf("missing host marker in output: %q", combined)
	}
}

func TestRunCommandScriptExecParallel(t *testing.T) {
	srv := newTestSSHServer(t)
	clients := map[string]*ssh.Client{
		"h1": srv.newClient(t), "h2": srv.newClient(t),
	}
	defer clients["h1"].Close()
	defer clients["h2"].Close()

	oldOut, oldErr := os.Stdout, os.Stderr
	rO, wO, _ := os.Pipe()
	rE, wE, _ := os.Pipe()
	os.Stdout, os.Stderr = wO, wE

	runCommandParallelExec(clients, "echo p", 2, true)

	wO.Close()
	wE.Close()
	os.Stdout, os.Stderr = oldOut, oldErr

	out, _ := io.ReadAll(rO)
	errOut, _ := io.ReadAll(rE)
	combined := string(out) + string(errOut)

	if !strings.Contains(combined, "===== exec on h1 =====") || !strings.Contains(combined, "===== exec on h2 =====") {
		t.Errorf("missing host markers in output: %q", combined)
	}
}

// ---- native fuzz tests (run with -fuzz) ----

func FuzzParseHost(f *testing.F) {
	seeds := []string{
		"127.0.0.1", "host:22", "root@host", "root:pass@host:2222",
		"[::1]:8022", "user@[::1]", "a:b@c:d@e:f", "",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		cfg := parseHost(raw)
		// Never crash, never leak password in Display
		if strings.Contains(cfg.Display, cfg.Password) && cfg.Password != "" {
			t.Errorf("Display %q leaks password %q", cfg.Display, cfg.Password)
		}
	})
}

func FuzzExpandEnv(f *testing.F) {
	os.Setenv("FUZZ_A", "alpha")
	defer os.Unsetenv("FUZZ_A")

	seeds := []string{"$HOME", "${USER}", "$FUZZ_A", "no-var", "$UNKNOWN", "$$", "$"}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, s string) {
		result := expandEnv(s)
		_ = result // no crash, no panic
	})
}

func FuzzPushPullRoundtrip(f *testing.F) {
	f.Add([]byte("hello fuzz"), "testfile.txt")
	f.Add([]byte(""), "empty.txt")
	f.Add(make([]byte, 4096), "big.bin")

	f.Fuzz(func(t *testing.T, data []byte, fname string) {
		if fname == "" || len(data) > 65536 {
			return
		}
		srv := newTestSSHServer(t)
		defer srv.Close()
		client := srv.newClient(t)
		defer client.Close()

		src := filepath.Join(t.TempDir(), fname)
		if err := os.WriteFile(src, data, 0644); err != nil {
			t.Fatal(err)
		}
		remotePath := filepath.Join(srv.TempDir(), fname)
		if err := pushFile(client, src, remotePath); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(t.TempDir(), "roundtrip_"+fname)
		if err := pullFile(client, remotePath, dst); err != nil {
			t.Fatal(err)
		}
		got, _ := os.ReadFile(dst)
		if string(got) != string(data) {
			t.Errorf("roundtrip mismatch for %q (%d bytes)", fname, len(data))
		}
	})
}
