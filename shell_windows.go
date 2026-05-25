//go:build windows

package main

import (
	"fmt"

	"golang.org/x/crypto/ssh"
)

// startShell is a no-op on Windows (pty/raw-mode not available).
func startShell(client *ssh.Client) error {
	return fmt.Errorf("shell subcommand is not supported on Windows")
}
