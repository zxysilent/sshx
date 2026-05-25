package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPushFileDirectory(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	dir := t.TempDir()
	err := pushFile(client, dir, "/tmp/dummy")
	if err == nil {
		t.Fatal("expected error when pushing a directory, got nil")
	}
}

func TestPushFileNotFound(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	err := pushFile(client, "/nonexistent/path/file.txt", "/tmp/dummy")
	if err == nil {
		t.Fatal("expected error for non-existent file, got nil")
	}
}

func TestPushFileSuccess(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	// Create a local file
	localPath := filepath.Join(t.TempDir(), "test_push.txt")
	content := "hello from sshrun test"
	if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
		t.Fatalf("write local file: %v", err)
	}

	remotePath := filepath.Join(srv.TempDir(), "received.txt")
	err := pushFile(client, localPath, remotePath)
	if err != nil {
		t.Fatalf("pushFile failed: %v", err)
	}

	// Verify remote file was received
	data, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatalf("read remote file: %v", err)
	}
	if string(data) != content {
		t.Errorf("remote file content mismatch: got %q, want %q", string(data), content)
	}
}

func TestPullFileSuccess(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	// Create a file on the "remote" (test server's tmpDir)
	remotePath := filepath.Join(srv.TempDir(), "test_pull.txt")
	content := "download me please"
	if err := os.WriteFile(remotePath, []byte(content), 0644); err != nil {
		t.Fatalf("write remote file: %v", err)
	}

	localPath := filepath.Join(t.TempDir(), "downloaded.txt")
	err := pullFile(client, remotePath, localPath)
	if err != nil {
		t.Fatalf("pullFile failed: %v", err)
	}

	// Verify local file
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read local file: %v", err)
	}
	if string(data) != content {
		t.Errorf("local file content mismatch: got %q, want %q", string(data), content)
	}
}

func TestPullFileNotFound(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	localPath := filepath.Join(t.TempDir(), "should_not_exist.txt")
	err := pullFile(client, "/nonexistent/remote/file.txt", localPath)
	if err == nil {
		t.Fatal("expected error for non-existent remote file, got nil")
	}
}

func TestPushPullRoundtrip(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	// Push a file
	localSrc := filepath.Join(t.TempDir(), "roundtrip_src.txt")
	content := "roundtrip data\nline 2\nline 3"
	os.WriteFile(localSrc, []byte(content), 0644)

	remotePath := filepath.Join(srv.TempDir(), "roundtrip_remote.txt")
	if err := pushFile(client, localSrc, remotePath); err != nil {
		t.Fatalf("push failed: %v", err)
	}

	// Pull it back to a different location
	localDst := filepath.Join(t.TempDir(), "roundtrip_dst.txt")
	if err := pullFile(client, remotePath, localDst); err != nil {
		t.Fatalf("pull failed: %v", err)
	}

	// Compare
	got, _ := os.ReadFile(localDst)
	if string(got) != content {
		t.Errorf("roundtrip mismatch: got %q, want %q", string(got), content)
	}
}
