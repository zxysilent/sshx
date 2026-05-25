//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

// startShell launches an interactive PTY shell over the SSH client.
//
// Features:
//   - Sets the local terminal to raw mode for transparent I/O passthrough
//   - Listens for SIGWINCH signals to sync terminal window size changes
//   - Automatically restores terminal state on exit
func startShell(client *ssh.Client) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return fmt.Errorf("failed to set raw mode: %w", err)
	}
	defer term.Restore(fd, oldState)

	// Get current terminal size
	width, height, err := term.GetSize(fd)
	if err != nil {
		width, height = 80, 40
	}

	// Request PTY
	if err := session.RequestPty("xterm-256color", height, width, ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}); err != nil {
		return fmt.Errorf("failed to request PTY: %w", err)
	}

	// Wire up I/O
	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Start shell
	if err := session.Shell(); err != nil {
		return fmt.Errorf("failed to start shell: %w", err)
	}

	// Forward stdin
	go func() {
		io.Copy(stdinPipe, os.Stdin)
	}()

	// Watch for window size changes
	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
	}()

	for {
		select {
		case sig := <-winch:
			if sig == nil {
				continue
			}
			w, h, err := term.GetSize(fd)
			if err == nil {
				session.WindowChange(h, w)
			}
		case err := <-done:
			if err != nil {
				if exitErr, ok := err.(*ssh.ExitError); ok {
					return fmt.Errorf("shell exited with code: %d", exitErr.ExitStatus())
				}
				return fmt.Errorf("shell exited abnormally: %w", err)
			}
			return nil
		}
	}
}
