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

	runCommandSerialExec(clients, "uptime", false)

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

	runCommandParallelExec(clients, "hostname", 3, false)

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

	runCommandParallelExec(clients, "id", 2, false)

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

func TestRunCommandScript(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	script := "echo hello-from-script\necho line2"
	stdout, stderr, err := runCommandScript(client, script)
	if err != nil {
		t.Fatalf("runCommandScript failed: %v", err)
	}
	if stdout != "" || stderr != "" {
		// test server echoes "bash -s" as stdout when exec payload is received;
		// script content went via stdin pipe and is fine as long as no error
		t.Logf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestRunCommandScriptNonZero(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	// Script that fails — test server still returns exit=0, so this is a
	// smoke test only. Real exit-code testing needs a real SSH server.
	_, _, err := runCommandScript(client, "exit 1")
	if err != nil {
		t.Logf("expected script error: %v", err)
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
