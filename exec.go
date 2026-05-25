package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// runCommand executes a command on a single host and returns stdout/stderr.
func runCommand(client *ssh.Client, cmd string) (stdout, stderr string, err error) {
	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		return "", "", fmt.Errorf("failed to create session: %w", sessionErr)
	}
	defer session.Close()

	var outBuf, errBuf strings.Builder
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	err = session.Run(cmd)
	return outBuf.String(), errBuf.String(), err
}

// runCommandScript executes a shell script on a single host by piping its
// content via stdin to "bash -s".
func runCommandScript(client *ssh.Client, content string) (stdout, stderr string, err error) {
	session, sessionErr := client.NewSession()
	if sessionErr != nil {
		return "", "", fmt.Errorf("failed to create session: %w", sessionErr)
	}
	defer session.Close()

	stdinPipe, _ := session.StdinPipe()
	var outBuf, errBuf strings.Builder
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	go func() {
		io.WriteString(stdinPipe, content)
		stdinPipe.Close()
	}()

	err = session.Run("bash -s")
	return outBuf.String(), errBuf.String(), err
}

// runCommandSerialExec dispatches a command or script to hosts sequentially.
func runCommandSerialExec(clients map[string]*ssh.Client, cmd string, isScript bool) {
	for host, client := range clients {
		fmt.Fprintf(os.Stderr, "===== exec on %s =====\n", host)
		var stdout, stderr string
		var err error
		if isScript {
			stdout, stderr, err = runCommandScript(client, cmd)
		} else {
			stdout, stderr, err = runCommand(client, cmd)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", err)
		}
		if stdout != "" {
			fmt.Print(stdout)
		}
		if stderr != "" {
			fmt.Fprint(os.Stderr, stderr)
		}
	}
}

// runCommandParallelExec dispatches a command or script to hosts concurrently.
func runCommandParallelExec(clients map[string]*ssh.Client, cmd string, concurrency int, isScript bool) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex

	for host, client := range clients {
		wg.Add(1)
		go func(h string, c *ssh.Client) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			var stdout, stderr string
			var err error
			if isScript {
				stdout, stderr, err = runCommandScript(c, cmd)
			} else {
				stdout, stderr, err = runCommand(c, cmd)
			}
			mu.Lock()
			fmt.Fprintf(os.Stderr, "===== exec on %s =====\n", h)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[error] %v\n", err)
			}
			if stdout != "" {
				fmt.Print(stdout)
			}
			if stderr != "" {
				fmt.Fprint(os.Stderr, stderr)
			}
			mu.Unlock()
		}(host, client)
	}
	wg.Wait()
}

// runCommandDirect runs a command on a single host, streaming stdout/stderr
// directly to the provided writers.
func runCommandDirect(client *ssh.Client, cmd string, stdout, stderr io.Writer) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	session.Stdout = stdout
	session.Stderr = stderr
	return session.Run(cmd)
}
