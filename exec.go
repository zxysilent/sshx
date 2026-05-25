package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// commandResult holds the result of executing a command on a single host.
type commandResult struct {
	Host   string
	Stdout string
	Stderr string
	Err    error
}

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

// runCommandSerial executes the same command on multiple hosts sequentially,
// printing results as each host completes.
func runCommandSerial(clients map[string]*ssh.Client, cmd string) {
	for host, client := range clients {
		fmt.Fprintf(os.Stderr, "===== exec on %s =====\n", host)
		stdout, stderr, err := runCommand(client, cmd)
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

// runCommandParallel executes the same command on multiple hosts concurrently,
// limiting the number of simultaneous goroutines to concurrency.
func runCommandParallel(clients map[string]*ssh.Client, cmd string, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	results := make(chan commandResult, len(clients))

	for host, client := range clients {
		wg.Add(1)
		go func(h string, c *ssh.Client) {
			defer wg.Done()
			sem <- struct{}{}        // acquire
			defer func() { <-sem }() // release
			stdout, stderr, err := runCommand(c, cmd)
			results <- commandResult{Host: h, Stdout: stdout, Stderr: stderr, Err: err}
		}(host, client)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	for r := range results {
		fmt.Fprintf(os.Stderr, "===== exec on %s =====\n", r.Host)
		if r.Err != nil {
			fmt.Fprintf(os.Stderr, "[error] %v\n", r.Err)
		}
		if r.Stdout != "" {
			fmt.Print(r.Stdout)
		}
		if r.Stderr != "" {
			fmt.Fprint(os.Stderr, r.Stderr)
		}
	}
}

// runCommandDirect runs a command on a single host, streaming stdout/stderr
// directly to the provided writers (useful for pre-flight checks, etc.).
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
