package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// envVarPattern matches $VAR and ${VAR} in password strings.
var envVarPattern = regexp.MustCompile(`\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?`)

// expandEnv expands $VAR or ${VAR} references in s, fetching values from
// the process environment. Unknown variables are left unchanged.
func expandEnv(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimPrefix(match, "${")
		name = strings.TrimSuffix(name, "}")
		name = strings.TrimPrefix(name, "$")
		if val, ok := os.LookupEnv(name); ok {
			return val
		}
		return match
	})
}

// hostConfig holds the parsed components of a -H value.
type hostConfig struct {
	Display  string
	User     string
	Password string
	Host     string
	Port     int
}

// parseHost parses a -H value of the form [user[:password]@]host[:port].
// Display is sanitized: passwords are never shown in output.
func parseHost(raw string) hostConfig {
	cfg := hostConfig{Display: raw}
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
	} else {
		cfg.User = ""
	}
	if h, p, err := net.SplitHostPort(addrPart); err == nil {
		cfg.Host = h
		if port, perr := fmt.Sscanf(p, "%d", &cfg.Port); port != 1 || perr != nil {
			cfg.Port = 0
		}
	} else {
		cfg.Host = addrPart
	}
	display := cfg.Host
	if cfg.Port != 0 {
		display = net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	}
	if cfg.User != "" {
		display = cfg.User + "@" + display
	}
	cfg.Display = display
	return cfg
}

// buildAuthMethods returns ssh.AuthMethod slice from key file and/or password.
func buildAuthMethods(username, password, keyPath string) ([]ssh.AuthMethod, error) {
	authMethods := []ssh.AuthMethod{}
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
	if password != "" {
		authMethods = append(authMethods, ssh.Password(expandEnv(password)))
	}
	if len(authMethods) == 0 {
		return nil, errors.New("no valid authentication methods (key not found and password is empty)")
	}
	return authMethods, nil
}

func defaultKnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func (c *sshCfg) hostKeyCallback() (ssh.HostKeyCallback, error) {
	if !c.strictHostKey {
		return ssh.InsecureIgnoreHostKey(), nil
	}
	if c.knownHosts == "" {
		return nil, errors.New("--known-hosts is required with --strict-host-key")
	}
	callback, err := knownhosts.New(c.knownHosts)
	if err != nil {
		return nil, fmt.Errorf("load known_hosts %s: %w", c.knownHosts, err)
	}
	return callback, nil
}

// sshClient creates a direct SSH client connection.
func sshClient(addr, username, password, keyPath string, timeout time.Duration) (*ssh.Client, error) {
	return sshClientWithHostKeyCallback(addr, username, password, keyPath, timeout, ssh.InsecureIgnoreHostKey())
}

func sshClientWithHostKeyCallback(
	addr, username, password, keyPath string,
	timeout time.Duration,
	hostKeyCallback ssh.HostKeyCallback,
) (*ssh.Client, error) {
	authMethods, err := buildAuthMethods(username, password, keyPath)
	if err != nil {
		return nil, err
	}
	if hostKeyCallback == nil {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}
	config := &ssh.ClientConfig{
		Timeout:         timeout,
		User:            username,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
	}
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("ssh dial %s failed: %w", addr, err)
	}
	return client, nil
}

// dialViaJump dials a TCP address through an SSH jump client, with a timeout.
func dialViaJump(jump *ssh.Client, addr string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return jump.DialContext(ctx, "tcp", addr)
}

// connectHost connects to a target host through optional jump hosts.
func connectHost(targetHost string, targetPort int, targetUser, targetPass string,
	jumps []string, globalUser, globalPass, globalKey string, globalPort int, timeout time.Duration,
) (*ssh.Client, error) {
	return connectHostWithHostKeyCallback(
		targetHost, targetPort, targetUser, targetPass,
		jumps, globalUser, globalPass, globalKey, globalPort, timeout,
		ssh.InsecureIgnoreHostKey(),
	)
}

func connectHostWithHostKeyCallback(targetHost string, targetPort int, targetUser, targetPass string,
	jumps []string, globalUser, globalPass, globalKey string, globalPort int, timeout time.Duration,
	hostKeyCallback ssh.HostKeyCallback,
) (*ssh.Client, error) {
	if hostKeyCallback == nil {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}
	targetAddr := net.JoinHostPort(targetHost, fmt.Sprintf("%d", targetPort))
	if len(jumps) == 0 {
		return sshClientWithHostKeyCallback(targetAddr, targetUser, targetPass, globalKey, timeout, hostKeyCallback)
	}
	type jumpCfg struct {
		user, pass, addr string
		client           *ssh.Client
	}
	jumpCfgs := make([]jumpCfg, len(jumps))
	for i, raw := range jumps {
		jc := parseHost(raw)
		u := jc.User
		if u == "" {
			u = globalUser
		}
		p := jc.Password
		if p == "" {
			p = globalPass
		}
		port := jc.Port
		if port == 0 {
			port = globalPort
		}
		jumpCfgs[i] = jumpCfg{
			user: u, pass: p,
			addr: net.JoinHostPort(jc.Host, fmt.Sprintf("%d", port)),
		}
	}
	auth, err := buildAuthMethods(jumpCfgs[0].user, jumpCfgs[0].pass, globalKey)
	if err != nil {
		return nil, fmt.Errorf("jump[0] %s auth: %w", jumpCfgs[0].addr, err)
	}
	first, err := ssh.Dial("tcp", jumpCfgs[0].addr, &ssh.ClientConfig{
		Timeout: timeout, User: jumpCfgs[0].user, Auth: auth, HostKeyCallback: hostKeyCallback,
	})
	if err != nil {
		return nil, fmt.Errorf("jump[0] %s dial failed: %w", jumpCfgs[0].addr, err)
	}
	jumpCfgs[0].client = first
	prevJump := first
	prevAddr := jumpCfgs[0].addr
	for i := 1; i < len(jumpCfgs); i++ {
		auth, err := buildAuthMethods(jumpCfgs[i].user, jumpCfgs[i].pass, globalKey)
		if err != nil {
			return nil, fmt.Errorf("jump[%d] %s auth: %w", i, jumpCfgs[i].addr, err)
		}
		conn, err := dialViaJump(prevJump, jumpCfgs[i].addr, timeout)
		if err != nil {
			return nil, fmt.Errorf("tunnel %s -> jump[%d] %s failed: %w", prevAddr, i, jumpCfgs[i].addr, err)
		}
		sc, chans, reqs, err := ssh.NewClientConn(conn, jumpCfgs[i].addr, &ssh.ClientConfig{
			Timeout: timeout, User: jumpCfgs[i].user, Auth: auth, HostKeyCallback: hostKeyCallback,
		})
		if err != nil {
			return nil, fmt.Errorf("jump[%d] %s handshake via %s: %w", i, jumpCfgs[i].addr, prevAddr, err)
		}
		jumpCfgs[i].client = ssh.NewClient(sc, chans, reqs)
		prevJump = jumpCfgs[i].client
		prevAddr = jumpCfgs[i].addr
	}
	if targetPass == "" && jumpCfgs[len(jumpCfgs)-1].pass != "" {
		targetPass = jumpCfgs[len(jumpCfgs)-1].pass
	}
	targetAuth, err := buildAuthMethods(targetUser, targetPass, globalKey)
	if err != nil {
		return nil, fmt.Errorf("target auth: %w", err)
	}
	tunnelConn, err := dialViaJump(prevJump, targetAddr, timeout)
	if err != nil {
		return nil, fmt.Errorf("tunnel %s -> %s failed: %w", prevAddr, targetAddr, err)
	}
	targetConn, chans, reqs, err := ssh.NewClientConn(tunnelConn, targetAddr, &ssh.ClientConfig{
		Timeout: timeout, User: targetUser, Auth: targetAuth, HostKeyCallback: hostKeyCallback,
	})
	if err != nil {
		return nil, fmt.Errorf("target ssh handshake via %s: %w", prevAddr, err)
	}
	return ssh.NewClient(targetConn, chans, reqs), nil
}

// shellQuote wraps s in single quotes for shell safety.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
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
