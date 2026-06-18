package main

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testSSHServer is an in-process SSH server for unit testing.
type testSSHServer struct {
	listener net.Listener
	addr     string
	hostKey  ssh.PublicKey
	done     chan struct{}
	tmpDir   string
}

// newTestSSHServer starts an SSH server that accepts password "testpass"
// and handles exec/shell/scp-t/scp-f requests.
func newTestSSHServer(t *testing.T) *testSSHServer {
	t.Helper()

	config := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if string(pass) == "testpass" {
				return &ssh.Permissions{}, nil
			}
			return nil, fmt.Errorf("password rejected")
		},
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}
	config.AddHostKey(signer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	s := &testSSHServer{
		listener: listener,
		addr:     listener.Addr().String(),
		hostKey:  signer.PublicKey(),
		done:     make(chan struct{}),
	}
	// Create a temp dir for scp file tests
	s.tmpDir, err = os.MkdirTemp("", "sshrun-test-*")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}

	go s.serve(config)
	return s
}

func (s *testSSHServer) Addr() string    { return s.addr }
func (s *testSSHServer) TempDir() string { return s.tmpDir }

func (s *testSSHServer) Close() {
	s.listener.Close()
	<-s.done
	os.RemoveAll(s.tmpDir)
}

func (s *testSSHServer) serve(config *ssh.ServerConfig) {
	defer close(s.done)
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn, config)
	}
}

func (s *testSSHServer) handleConn(conn net.Conn, config *ssh.ServerConfig) {
	srvConn, chans, reqs, err := ssh.NewServerConn(conn, config)
	if err != nil {
		conn.Close()
		return
	}
	defer srvConn.Close()
	go ssh.DiscardRequests(reqs)

	for newChannel := range chans {
		switch newChannel.ChannelType() {
		case "session":
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			s.handleSession(channel, requests)
		case "direct-tcpip":
			channel, requests, err := newChannel.Accept()
			if err != nil {
				continue
			}
			s.handleDirectTCPIP(channel, requests, newChannel.ExtraData())
		default:
			newChannel.Reject(ssh.UnknownChannelType, "unsupported")
		}
	}
}

func (s *testSSHServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()

	for req := range requests {
		switch req.Type {
		case "exec":
			s.handleExec(channel, req)
			return
		case "shell":
			s.handleShell(channel, req)
			return
		case "pty-req":
			req.Reply(true, nil)
		case "window-change":
			// no-op
		default:
			req.Reply(false, nil)
		}
	}
}

func (s *testSSHServer) handleDirectTCPIP(channel ssh.Channel, requests <-chan *ssh.Request, extra []byte) {
	defer channel.Close()
	go ssh.DiscardRequests(requests)

	if len(extra) < 8 {
		return
	}
	hostLen := binary.BigEndian.Uint32(extra[:4])
	host := string(extra[4 : 4+hostLen])
	port := binary.BigEndian.Uint32(extra[4+hostLen : 4+hostLen+4])

	target := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	conn, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		return
	}
	defer conn.Close()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { io.Copy(channel, conn); wg.Done() }()
	go func() { io.Copy(conn, channel); wg.Done() }()
	wg.Wait()
}

func (s *testSSHServer) handleExec(channel ssh.Channel, req *ssh.Request) {
	var payload struct{ Command string }
	if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
		req.Reply(false, nil)
		return
	}

	if strings.HasPrefix(payload.Command, "scp -t ") {
		s.handleScpReceive(channel, req, payload.Command)
		return
	}
	if strings.HasPrefix(payload.Command, "scp -f ") {
		s.handleScpSend(channel, req, payload.Command)
		return
	}

	req.Reply(true, nil)

	// "exit:N" prefix → exit with code N (and echo rest as stdout)
	exitCode := uint32(0)
	cmd := payload.Command
	if strings.HasPrefix(cmd, "exit:") {
		rest := strings.TrimPrefix(cmd, "exit:")
		if n, _, found := strings.Cut(rest, " "); found {
			if code, err := strconv.Atoi(n); err == nil && code >= 0 && code <= 255 {
				exitCode = uint32(code)
				cmd = rest[len(n)+1:]
			}
		} else {
			if code, err := strconv.Atoi(rest); err == nil && code >= 0 && code <= 255 {
				channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{uint32(code)}))
				return
			}
		}
	}

	// "bash -s" → read stdin, echo as stdout (simulate script execution)
	if cmd == "bash -s" {
		data, _ := io.ReadAll(channel)
		channel.Write(data)
		channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{exitCode}))
		return
	}

	// Default: echo command back + optional exit code from "exit:N <command>" pattern
	io.WriteString(channel, cmd)
	channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{exitCode}))
}

func (s *testSSHServer) handleShell(channel ssh.Channel, req *ssh.Request) {
	req.Reply(true, nil)
	io.WriteString(channel, "pseudo-terminal ready\n")
	channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{0}))
}

// handleScpReceive implements server-side scp -t (sink).
func (s *testSSHServer) handleScpReceive(channel ssh.Channel, req *ssh.Request, cmd string) {
	req.Reply(true, nil)

	// Send initial ACK
	channel.Write([]byte{0})

	// Read C message header
	buf := make([]byte, 4096)
	n, err := channel.Read(buf)
	if err != nil || n == 0 {
		return
	}
	header := string(buf[:n])
	if header[0] != 'C' {
		channel.Write([]byte{1})
		channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{1}))
		return
	}

	// Parse mode, size, name
	header = strings.TrimRight(header, "\n")
	parts := strings.SplitN(header[1:], " ", 3)
	if len(parts) < 3 {
		channel.Write([]byte{1})
		return
	}
	size, _ := strconv.ParseInt(parts[1], 10, 64)

	// ACK
	channel.Write([]byte{0})

	// Read file data
	destPath := stripShellQuote(strings.TrimPrefix(cmd, "scp -t "))
	os.MkdirAll(filepath.Dir(destPath), 0755)
	f, err := os.Create(destPath)
	if err == nil {
		io.CopyN(f, channel, size)
		f.Close()
	}

	// Read trailing null byte
	nullBuf := make([]byte, 1)
	channel.Read(nullBuf)

	// Final ACK
	channel.Write([]byte{0})
	channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{0}))
}

// handleScpSend implements server-side scp -f (source).
func (s *testSSHServer) handleScpSend(channel ssh.Channel, req *ssh.Request, cmd string) {
	req.Reply(true, nil)

	srcPath := stripShellQuote(strings.TrimPrefix(cmd, "scp -f "))

	// Read client ACK
	ack := make([]byte, 1)
	channel.Read(ack)
	if ack[0] != 0 {
		return
	}

	// Stat the source file
	fi, err := os.Stat(srcPath)
	if err != nil {
		// Send error: byte 1 + message
		errMsg := fmt.Sprintf("%c%s", 1, err.Error())
		channel.Write([]byte(errMsg))
		return
	}
	if fi.IsDir() {
		channel.Write([]byte{1, 'd', 'i', 'r', ' ', 'n', 'o', 't', ' ', 's', 'u', 'p', 'p', 'o', 'r', 't', 'e', 'd', '\n'})
		return
	}

	// Send C message
	cMsg := fmt.Sprintf("C%04o %d %s\n", fi.Mode().Perm(), fi.Size(), filepath.Base(srcPath))
	channel.Write([]byte(cMsg))

	// Read client ACK
	channel.Read(ack)
	if ack[0] != 0 {
		return
	}

	// Send file data
	f, err := os.Open(srcPath)
	if err != nil {
		return
	}
	defer f.Close()
	io.Copy(channel, f)

	// Send end marker
	channel.Write([]byte{0})

	// Read final ACK
	channel.Read(ack)
	channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{0}))
}

// stripShellQuote removes surrounding single quotes from a path.
func stripShellQuote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return s[1 : len(s)-1]
	}
	return s
}

// mkConn creates an SSH client connected to the test server with password auth.
func (s *testSSHServer) newClient(t *testing.T) *ssh.Client {
	t.Helper()
	client, err := sshClient(s.addr, "testuser", "testpass", "", 5*time.Second)
	if err != nil {
		t.Fatalf("connect to test server: %v", err)
	}
	return client
}

func TestConnectHostWithJump(t *testing.T) {
	// Start two servers: jump and target
	jumpSrv := newTestSSHServer(t)
	defer jumpSrv.Close()
	targetSrv := newTestSSHServer(t)
	defer targetSrv.Close()

	// Connect via jump to target
	client, err := connectHost(
		"127.0.0.1", targetSrv.listener.Addr().(*net.TCPAddr).Port,
		"testuser", "testpass",
		[]string{jumpSrv.addr},
		"testuser", "testpass", "", 22, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("connectHost via jump failed: %v", err)
	}
	defer client.Close()

	// Run a command through the tunnel
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession via jump failed: %v", err)
	}
	defer session.Close()

	out, err := session.Output("echo hello-jump")
	if err != nil {
		t.Fatalf("session.Output via jump failed: %v", err)
	}
	if string(out) != "echo hello-jump" {
		t.Errorf("got %q, want %q", string(out), "echo hello-jump")
	}
}

func TestConnectHostWithJumpPasswordFallback(t *testing.T) {
	jumpSrv := newTestSSHServer(t)
	defer jumpSrv.Close()
	targetSrv := newTestSSHServer(t)
	defer targetSrv.Close()

	// Target has no password — should reuse jump's password
	client, err := connectHost(
		"127.0.0.1", targetSrv.listener.Addr().(*net.TCPAddr).Port,
		"testuser", "", // empty target password
		[]string{jumpSrv.addr},
		"testuser", "testpass", "", 22, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("connectHost password fallback failed: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	defer session.Close()

	if _, err := session.Output("echo ok"); err != nil {
		t.Fatalf("session.Output failed: %v", err)
	}
}

func TestConnectHostMultiHopChain(t *testing.T) {
	// Setup: client → jump1 → jump2 → target
	jump1 := newTestSSHServer(t)
	defer jump1.Close()
	jump2 := newTestSSHServer(t)
	defer jump2.Close()
	target := newTestSSHServer(t)
	defer target.Close()

	jumps := []string{
		jump1.addr,
		"testuser:testpass@" + jump2.addr,
	}

	client, err := connectHost(
		"127.0.0.1", intPort(target),
		"testuser", "testpass",
		jumps,
		"testuser", "testpass", "", 22, 5*time.Second,
	)
	if err != nil {
		t.Fatalf("multi-hop connectHost failed: %v", err)
	}
	defer client.Close()

	out, err := client.NewSession()
	if err != nil {
		t.Fatalf("NewSession via multi-hop: %v", err)
	}
	defer out.Close()

	// Execute a command through 2-hop chain
	var b strings.Builder
	out.Stdout = &b
	if err := out.Run("echo multi-hop-ok"); err != nil {
		t.Fatalf("Run via multi-hop: %v", err)
	}
	if b.String() != "echo multi-hop-ok" {
		t.Errorf("got %q, want %q", b.String(), "echo multi-hop-ok")
	}
}
