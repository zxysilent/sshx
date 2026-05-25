package main

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// testSSHServer is an in-process SSH server for unit testing.
type testSSHServer struct {
	listener net.Listener
	addr     string
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

func (s *testSSHServer) Addr() string     { return s.addr }
func (s *testSSHServer) TempDir() string  { return s.tmpDir }

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
		if newChannel.ChannelType() != "session" {
			newChannel.Reject(ssh.UnknownChannelType, "only session supported")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		s.handleSession(channel, requests)
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

	// Normal command: echo back
	req.Reply(true, nil)
	io.WriteString(channel, payload.Command)
	channel.SendRequest("exit-status", false, ssh.Marshal(&struct{ ExitStatus uint32 }{0}))
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
	destPath := strings.TrimPrefix(cmd, "scp -t ")
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

	srcPath := strings.TrimPrefix(cmd, "scp -f ")

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

// mkConn creates an SSH client connected to the test server with password auth.
func (s *testSSHServer) newClient(t *testing.T) *ssh.Client {
	t.Helper()
	client, err := sshClient(s.addr, "testuser", "testpass", "", 5*time.Second)
	if err != nil {
		t.Fatalf("connect to test server: %v", err)
	}
	return client
}
