package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// hostConfig holds the parsed components of a -H value.
type hostConfig struct {
	Display  string // original value for display labels
	User     string // empty if not specified inline
	Password string // empty if not specified inline
	Host     string // bare host (no port)
	Port     int    // 0 means use global default
}

// parseHost parses a -H value of the form [user[:password]@]host[:port].
func parseHost(raw string) hostConfig {
	cfg := hostConfig{Display: raw}

	// Split on last '@' to separate credentials from address
	addrPart := raw
	if idx := strings.LastIndex(raw, "@"); idx >= 0 {
		credPart := raw[:idx]
		addrPart = raw[idx+1:]

		if colon := strings.Index(credPart, ":"); colon >= 0 {
			cfg.User = credPart[:colon]
			cfg.Password = credPart[colon+1:]
		} else {
			cfg.User = credPart
		}
	}

	// Split address into host:port
	if h, p, err := net.SplitHostPort(addrPart); err == nil {
		cfg.Host = h
		if port, perr := fmt.Sscanf(p, "%d", &cfg.Port); port != 1 || perr != nil {
			cfg.Port = 0
		}
	} else {
		cfg.Host = addrPart
	}

	return cfg
}

// sshClient creates an SSH client connection.
// Auth strategy: private key first, then password fallback.
func sshClient(addr, username, password, keyPath string, timeout time.Duration) (*ssh.Client, error) {
	authMethods := []ssh.AuthMethod{}

	// 1. Try private key authentication
	resolvedKey := keyPath
	if resolvedKey == "" {
		if u, lookupErr := user.Lookup(username); lookupErr == nil {
			resolvedKey = filepath.Join(u.HomeDir, ".ssh", "id_rsa")
		}
	}
	if resolvedKey != "" {
		if privateKey, readErr := os.ReadFile(resolvedKey); readErr == nil {
			if signer, parseErr := ssh.ParsePrivateKey(privateKey); parseErr == nil {
				authMethods = append(authMethods, ssh.PublicKeys(signer))
			}
		}
	}

	// 2. Append password authentication
	if password != "" {
		authMethods = append(authMethods, ssh.Password(password))
	}

	if len(authMethods) == 0 {
		return nil, errors.New("no valid authentication methods (key not found and password is empty)")
	}

	config := &ssh.ClientConfig{
		Timeout:         timeout,
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s failed: %w", addr, err)
	}
	return client, nil
}

// resolveAddr parses host:port. If host already contains a port, it is used as-is;
// otherwise the default port is appended.
func resolveAddr(host string, defaultPort int) string {
	h, p, err := net.SplitHostPort(host)
	if err == nil {
		return net.JoinHostPort(h, p)
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", defaultPort))
}
