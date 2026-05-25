package main

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestRunCommand(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	stdout, stderr, err := runCommand(client, "echo hello")
	if err != nil {
		t.Fatalf("runCommand failed: %v", err)
	}
	if stdout != "echo hello" {
		t.Errorf("expected stdout %q, got %q", "echo hello", stdout)
	}
	if stderr != "" {
		t.Errorf("expected empty stderr, got %q", stderr)
	}
}

func TestRunCommandSerial(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	// Create 3 clients to the same server
	clients := make(map[string]*ssh.Client)
	for i := 0; i < 3; i++ {
		c := srv.newClient(t)
		defer c.Close()
		clients[fmt.Sprintf("host%d", i)] = c
	}

	// Capture stdout and stderr
	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	runCommandSerial(clients, "uptime")

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var outBuf, errBuf bytes.Buffer
	outBuf.ReadFrom(rOut)
	errBuf.ReadFrom(rErr)
	output := outBuf.String() + errBuf.String()

	for i := 0; i < 3; i++ {
		marker := fmt.Sprintf("===== exec on host%d =====", i)
		if !strings.Contains(output, marker) {
			t.Errorf("output missing marker %q\nGot:\n%s", marker, output)
		}
	}
	if !strings.Contains(output, "uptime") {
		t.Errorf("output missing command output 'uptime'\nGot:\n%s", output)
	}
}

func TestRunCommandParallel(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	clients := make(map[string]*ssh.Client)
	for i := 0; i < 3; i++ {
		c := srv.newClient(t)
		defer c.Close()
		clients[fmt.Sprintf("host%d", i)] = c
	}

	oldStdout2, oldStderr2 := os.Stdout, os.Stderr
	rOut2, wOut2, _ := os.Pipe()
	rErr2, wErr2, _ := os.Pipe()
	os.Stdout = wOut2
	os.Stderr = wErr2

	runCommandParallel(clients, "hostname", 3)

	wOut2.Close()
	wErr2.Close()
	os.Stdout = oldStdout2
	os.Stderr = oldStderr2

	var outBuf2, errBuf2 bytes.Buffer
	outBuf2.ReadFrom(rOut2)
	errBuf2.ReadFrom(rErr2)
	output2 := outBuf2.String() + errBuf2.String()

	for i := 0; i < 3; i++ {
		marker := fmt.Sprintf("===== exec on host%d =====", i)
		if !strings.Contains(output2, marker) {
			t.Errorf("output missing marker %q\nGot:\n%s", marker, output2)
		}
	}
	if !strings.Contains(output2, "hostname") {
		t.Errorf("output missing command output 'hostname'\nGot:\n%s", output2)
	}
}

func TestRunCommandParallelLimit(t *testing.T) {
	// 5 clients, concurrency=2 — should still complete all hosts
	srv := newTestSSHServer(t)
	defer srv.Close()

	clients := make(map[string]*ssh.Client)
	for i := 0; i < 5; i++ {
		c := srv.newClient(t)
		defer c.Close()
		clients[fmt.Sprintf("host%d", i)] = c
	}

	oldStdout, oldStderr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout = wOut
	os.Stderr = wErr

	runCommandParallel(clients, "id", 2)

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	var outBuf, errBuf bytes.Buffer
	outBuf.ReadFrom(rOut)
	errBuf.ReadFrom(rErr)
	output := outBuf.String() + errBuf.String()

	for i := 0; i < 5; i++ {
		marker := fmt.Sprintf("===== exec on host%d =====", i)
		if !strings.Contains(output, marker) {
			t.Errorf("output missing marker %q\nGot:\n%s", marker, output)
		}
	}
	if !strings.Contains(output, "id") {
		t.Errorf("output missing command output 'id'\nGot:\n%s", output)
	}
}

func TestRunCommandDirect(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	var outBuf, errBuf bytes.Buffer
	err := runCommandDirect(client, "pwd", &outBuf, &errBuf)
	if err != nil {
		t.Fatalf("runCommandDirect failed: %v", err)
	}
	if outBuf.String() != "pwd" {
		t.Errorf("expected stdout %q, got %q", "pwd", outBuf.String())
	}
}
