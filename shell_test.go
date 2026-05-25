//go:build !windows

package main

import (
	"testing"
)

func TestStartShellErrorNoTerminal(t *testing.T) {
	// startShell requires a real terminal (os.Stdin must be a tty).
	// In test environments os.Stdin is typically not a terminal,
	// so term.MakeRaw should fail.
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	err := startShell(client)
	if err == nil {
		t.Fatal("expected error when stdin is not a terminal, got nil")
	}
	// The error should mention raw mode
	t.Logf("expected error: %v", err)
}
