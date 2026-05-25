package main

import (
	"net"
	"os"
	"testing"
	"time"
)

func TestSSHCfgDefaults(t *testing.T) {
	os.Setenv("SSHX_PASSWD", "env-secret")
	defer os.Unsetenv("SSHX_PASSWD")

	var cfg sshCfg
	cfg.defaults()

	if cfg.port != 22 {
		t.Errorf("port = %d, want 22", cfg.port)
	}
	if cfg.user == "" {
		t.Error("user should not be empty")
	}
	if cfg.passwd != "env-secret" {
		t.Errorf("passwd = %q, want env-secret", cfg.passwd)
	}
	if cfg.timeout != 10*time.Second {
		t.Errorf("timeout = %v, want 10s", cfg.timeout)
	}
}

func TestNewFlagSet(t *testing.T) {
	fs := newFlagSet("testcmd")
	if fs == nil {
		t.Fatal("newFlagSet returned nil")
	}
	if fs.Name() != "testcmd" {
		t.Errorf("name = %q, want testcmd", fs.Name())
	}
}

func TestSSHCfgBindFlags(t *testing.T) {
	var cfg sshCfg
	cfg.defaults()
	cfg.passwd = "global"

	fs := newFlagSet("test")
	cfg.bindFlags(fs)
	fs.Parse([]string{
		"--port", "2222",
		"--user", "admin",
		"-P", "override",
		"-J", "jump1",
		"-J", "jump2",
	})

	if cfg.port != 2222 {
		t.Errorf("port = %d, want 2222", cfg.port)
	}
	if cfg.user != "admin" {
		t.Errorf("user = %q, want admin", cfg.user)
	}
	if cfg.passwd != "override" {
		t.Errorf("passwd = %q, want override", cfg.passwd)
	}
	if len(cfg.jumps) != 2 || cfg.jumps[0] != "jump1" || cfg.jumps[1] != "jump2" {
		t.Errorf("jumps = %v, want [jump1 jump2]", cfg.jumps)
	}
}

func TestSSHCfgBindFlagsHelp(t *testing.T) {
	var cfg sshCfg
	cfg.defaults()
	fs := newFlagSet("testhelp2")
	cfg.bindFlags(fs)
	fs.Parse([]string{"-h"})
	if !cfg.showHelp {
		t.Error("showHelp should be true after -h")
	}
}

func TestSSHCfgConnectMerge(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	var cfg sshCfg
	cfg.defaults()
	cfg.user = "testuser"
	cfg.passwd = "testpass"
	cfg.port = intPort(srv)

	client, label, err := cfg.connect("root@" + srvAddr(srv))
	if err != nil {
		t.Fatalf("cfg.connect failed: %v", err)
	}
	defer client.Close()

	if label != "root@"+srvAddr(srv) {
		t.Errorf("label = %q, want root@...", label)
	}
}

func srvAddr(s *testSSHServer) string {
	return s.listener.Addr().(*net.TCPAddr).String()
}
