package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"
)

// pushFile uploads a local file to a remote host via SCP.
func pushFile(client *ssh.Client, localPath, remotePath string) error {
	fi, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat local file %s failed: %w", localPath, err)
	}
	if fi.IsDir() {
		return fmt.Errorf("directory upload not supported: %s", localPath)
	}

	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file failed: %w", err)
	}
	defer f.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	stdinPipe, _ := session.StdinPipe()
	stdoutPipe, _ := session.StdoutPipe()

	// Start remote scp -t (sink mode)
	if err := session.Start("scp -t " + shellQuote(remotePath)); err != nil {
		return fmt.Errorf("failed to start scp: %w", err)
	}

	// SCP protocol: send C<mode> <size> <filename>
	// First, read the remote ACK (byte 0)
	ackBuf := make([]byte, 1)
	if _, err := stdoutPipe.Read(ackBuf); err != nil {
		return fmt.Errorf("scp: wait for ACK failed: %w", err)
	}
	if ackBuf[0] != 0 {
		return fmt.Errorf("scp: remote error: %s", string(ackBuf))
	}

	mode := fi.Mode().Perm()
	size := fi.Size()
	name := filepath.Base(remotePath)
	cMsg := fmt.Sprintf("C%04o %d %s\n", mode, size, name)
	if _, err := fmt.Fprint(stdinPipe, cMsg); err != nil {
		return fmt.Errorf("scp: send C message failed: %w", err)
	}

	// Wait for ACK
	if _, err := stdoutPipe.Read(ackBuf); err != nil {
		return fmt.Errorf("scp: wait for file ACK failed: %w", err)
	}
	if ackBuf[0] != 0 {
		return fmt.Errorf("scp: remote rejected file: %s", string(ackBuf))
	}

	// Send file data
	if _, err := io.Copy(stdinPipe, f); err != nil {
		return fmt.Errorf("scp: send file data failed: %w", err)
	}

	// Send end-of-file marker + close stdin to signal EOF
	fmt.Fprint(stdinPipe, "\x00")
	stdinPipe.Close()

	// Wait for final ACK
	if _, err := stdoutPipe.Read(ackBuf); err != nil {
		return fmt.Errorf("scp: wait for final ACK failed: %w", err)
	}
	if ackBuf[0] != 0 {
		return fmt.Errorf("scp: transfer complete but remote returned error: %s", string(ackBuf))
	}

	fmt.Fprintf(os.Stderr, "uploaded: %s -> %s:%s (%d bytes)\n", localPath, client.RemoteAddr(), remotePath, size)
	return session.Wait()
}

// pullFile downloads a file from a remote host via SCP.
func pullFile(client *ssh.Client, remotePath, localPath string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	stdinPipe, _ := session.StdinPipe()
	stdoutPipe, _ := session.StdoutPipe()

	// Start remote scp -f (source mode)
	if err := session.Start("scp -f " + shellQuote(remotePath)); err != nil {
		return fmt.Errorf("failed to start scp: %w", err)
	}

	// Send ACK (byte 0) — ready to receive
	fmt.Fprint(stdinPipe, "\x00")

	// Parse SCP response header
	// Format: C<mode> <size> <filename>\n
	header := make([]byte, 1024)
	n, err := stdoutPipe.Read(header)
	if err != nil {
		return fmt.Errorf("scp: read response header failed: %w", err)
	}
	headerStr := string(header[:n])

	if headerStr[0] == 1 {
		return fmt.Errorf("scp: remote error: %s", headerStr[1:])
	}
	if headerStr[0] == 2 {
		return fmt.Errorf("scp: remote fatal error: %s", headerStr[1:])
	}

	// Parse C message
	if headerStr[0] != 'C' {
		return fmt.Errorf("scp: unknown response type: %c", headerStr[0])
	}

	parts := strings.SplitN(headerStr[1:], " ", 3)
	if len(parts) < 3 {
		return fmt.Errorf("scp: invalid response header: %s", headerStr)
	}

	fileSize, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return fmt.Errorf("scp: parse file size failed: %s", parts[1])
	}

	// Send ACK
	fmt.Fprint(stdinPipe, "\x00")

	// Ensure local directory exists
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("create local directory failed: %w", err)
	}

	// Create local file
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("create local file failed: %w", err)
	}
	defer localFile.Close()

	// Receive file data
	if _, err := io.CopyN(localFile, stdoutPipe, fileSize); err != nil {
		return fmt.Errorf("receive file data failed: %w", err)
	}

	// Read trailing null byte
	ackBuf := make([]byte, 1)
	if _, err := stdoutPipe.Read(ackBuf); err != nil {
		return fmt.Errorf("scp: wait for end marker failed: %w", err)
	}

	// Send final ACK + close stdin to signal EOF
	fmt.Fprint(stdinPipe, "\x00")
	stdinPipe.Close()

	fmt.Fprintf(os.Stderr, "downloaded: %s:%s -> %s (%d bytes)\n", client.RemoteAddr(), remotePath, localPath, fileSize)
	return session.Wait()
}
