package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// cliRun captures stdout, stderr, and the exit code from a CLI handler.
// It replaces osExit with a panic-based interceptor so handlers don't
// terminate the test process.
func cliRun(fn func([]string), args []string) (stdout, stderr string, exitCode int) {
	origExit := osExit
	defer func() { osExit = origExit }()
	osExit = func(code int) {
		panic(&cliExit{code})
	}

	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	defer func() {
		wOut.Close()
		wErr.Close()
		os.Stdout, os.Stderr = oldOut, oldErr

		var outBuf, errBuf bytes.Buffer
		outBuf.ReadFrom(rOut)
		errBuf.ReadFrom(rErr)
		stdout = outBuf.String()
		stderr = errBuf.String()
	}()

	defer func() {
		if r := recover(); r != nil {
			if ce, ok := r.(*cliExit); ok {
				exitCode = ce.code
				return
			}
			panic(r) // unexpected panic
		}
	}()

	fn(args)
	return
}

type cliExit struct{ code int }

// newMultiSrv starts n test SSH servers and returns a helper that builds
// -H args and a cleanup function.
func newMultiSrv(t *testing.T, n int) (srvs []*testSSHServer, hosts []string, cleanup func()) {
	t.Helper()
	for i := 0; i < n; i++ {
		srv := newTestSSHServer(t)
		srvs = append(srvs, srv)
	}
	hosts = make([]string, n)
	for i, s := range srvs {
		hosts[i] = s.Addr()
	}
	cleanup = func() {
		for _, s := range srvs {
			s.Close()
		}
	}
	return
}

// ---- single-host mode tests ----

func TestCLI_SingleHost_Interactive(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	_, stderr, exitCode := cliRun(
		runDefault,
		[]string{"-p", fmt.Sprint(intPort(srv)), "-u", "testuser", "-P", "testpass",
			srv.Addr()},
	)

	// No command → should attempt PTY shell, which fails in test (no tty)
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1 (shell requires tty)", exitCode)
	}
	if !strings.Contains(stderr, "raw mode") && !strings.Contains(stderr, "error") {
		t.Errorf("expected PTY/raw-mode error in stderr, got: %s", stderr)
	}
}

func TestCLI_SingleHost_Command(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	stdout, _, exitCode := cliRun(
		runDefault,
		[]string{"-p", fmt.Sprint(intPort(srv)), "-u", "testuser", "-P", "testpass",
			srv.Addr(), "hello world"},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "hello world") {
		t.Errorf("expected 'hello world' in stdout, got: %s", stdout)
	}
}

func TestCLI_SingleHost_ExitError(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	_, _, exitCode := cliRun(
		runDefault,
		[]string{"-p", fmt.Sprint(intPort(srv)), "-u", "testuser", "-P", "testpass",
			srv.Addr(), "exit:42"},
	)

	if exitCode != 42 {
		t.Errorf("exitCode = %d, want 42", exitCode)
	}
}

func TestCLI_SingleHost_ExitErrorWithOutput(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	stdout, _, exitCode := cliRun(
		runDefault,
		[]string{"-p", fmt.Sprint(intPort(srv)), "-u", "testuser", "-P", "testpass",
			srv.Addr(), "exit:3 stuck"},
	)

	if exitCode != 3 {
		t.Errorf("exitCode = %d, want 3", exitCode)
	}
	if !strings.Contains(stdout, "stuck") {
		t.Errorf("expected stdout to contain 'stuck', got: %s", stdout)
	}
}

func TestCLI_SingleHost_WithFlags(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	stdout, _, exitCode := cliRun(
		runDefault,
		[]string{
			"-p", fmt.Sprint(intPort(srv)),
			"-u", "testuser",
			"-P", "testpass",
			"-t", "5s",
			srv.Addr(),
			"flags-test",
		},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "flags-test") {
		t.Errorf("expected 'flags-test' in stdout, got: %s", stdout)
	}
}

func TestCLI_Default_MissingHost(t *testing.T) {
	_, stderr, exitCode := cliRun(runDefault, []string{})
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "host is required") {
		t.Errorf("expected 'host is required' in stderr, got: %s", stderr)
	}
}

func TestCLI_Default_ConnectionFailed(t *testing.T) {
	// Use a non-routable IP with a short timeout
	_, stderr, exitCode := cliRun(
		runDefault,
		[]string{"-t", "50ms", "10.255.255.1:22", "cmd"},
	)
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "error") {
		t.Errorf("expected error in stderr, got: %s", stderr)
	}
}

// ---- multi-host mode tests ----

func TestCLI_MultiHost_Sequential(t *testing.T) {
	_, hosts, cleanup := newMultiSrv(t, 2)
	defer cleanup()

	args := []string{
		"-u", "testuser", "-P", "testpass",
	}
	for _, h := range hosts {
		args = append(args, "-H", h)
	}
	args = append(args, "echo-seq")

	stdout, stderr, exitCode := cliRun(runDefault, args)
	_ = stderr

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	// Each host should have its marker
	if !strings.Contains(stderr, "exec on "+hosts[0]) {
		t.Errorf("missing marker for %s", hosts[0])
	}
	if !strings.Contains(stderr, "exec on "+hosts[1]) {
		t.Errorf("missing marker for %s", hosts[1])
	}
	if !strings.Contains(stdout, "echo-seq") {
		t.Errorf("expected 'echo-seq' in stdout, got: %s", stdout)
	}
}

func TestCLI_MultiHost_Concurrent(t *testing.T) {
	_, hosts, cleanup := newMultiSrv(t, 3)
	defer cleanup()

	args := []string{
		"-u", "testuser", "-P", "testpass",
		"-c", "3",
	}
	for _, h := range hosts {
		args = append(args, "-H", h)
	}
	args = append(args, "hostname")

	stdout, stderr, exitCode := cliRun(runDefault, args)
	_ = stderr

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "hostname") {
		t.Errorf("expected 'hostname' in stdout, got: %s", stdout)
	}
	for _, h := range hosts {
		if !strings.Contains(stderr, "exec on "+h) {
			t.Errorf("missing marker for %s", h)
		}
	}
}

func TestCLI_MultiHost_Script(t *testing.T) {
	_, hosts, cleanup := newMultiSrv(t, 2)
	defer cleanup()

	scriptPath := filepath.Join(t.TempDir(), "test.sh")
	const scriptContent = "echo from-script"
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatal(err)
	}

	args := []string{
		"-u", "testuser", "-P", "testpass",
		"-f", scriptPath,
	}
	for _, h := range hosts {
		args = append(args, "-H", h)
	}

	stdout, _, exitCode := cliRun(runDefault, args)
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, scriptContent) {
		t.Errorf("expected script content in stdout, got: %s", stdout)
	}
}

func TestCLI_MultiHost_ScriptAndCmdConflict(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	scriptPath := filepath.Join(t.TempDir(), "conflict.sh")
	os.WriteFile(scriptPath, []byte("x"), 0644)

	_, stderr, exitCode := cliRun(
		runDefault,
		[]string{"-u", "testuser", "-P", "testpass",
			"-f", scriptPath, "-H", srv.Addr(),
			"inline-cmd"},
	)
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "mutually exclusive") {
		t.Errorf("expected 'mutually exclusive' error, got: %s", stderr)
	}
}

func TestCLI_MultiHost_MissingCommand(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	_, stderr, exitCode := cliRun(
		runDefault,
		[]string{"-u", "testuser", "-P", "testpass", "-H", srv.Addr()},
	)
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "command is required") {
		t.Errorf("expected 'command is required' error, got: %s", stderr)
	}
}

func TestCLI_MultiHost_BadScriptFile(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	_, stderr, exitCode := cliRun(
		runDefault,
		[]string{"-u", "testuser", "-P", "testpass",
			"-f", "/nonexistent/script.sh", "-H", srv.Addr()},
	)
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "read script") {
		t.Errorf("expected 'read script' error, got: %s", stderr)
	}
}

func TestCLI_MultiHost_NoConnections(t *testing.T) {
	_, stderr, exitCode := cliRun(
		runDefault,
		[]string{"-t", "1ms", "-H", "10.255.255.1:22", "-H", "10.255.255.2:22", "cmd"},
	)
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
	if !strings.Contains(stderr, "failed to establish any connections") {
		t.Errorf("expected 'failed to establish any connections' error, got: %s", stderr)
	}
}

func TestCLI_MultiHost_PartialFailure(t *testing.T) {
	// One good host, one bad — should still run on the good one
	srv := newTestSSHServer(t)
	defer srv.Close()

	stdout, stderr, exitCode := cliRun(
		runDefault,
		[]string{
			"-u", "testuser", "-P", "testpass",
			"-t", "50ms",
			"-H", srv.Addr(),
			"-H", "10.255.255.1:22", // will timeout
			"partial-test",
		},
	)
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stderr, "[error]") {
		t.Errorf("expected [error] for failed host, got stderr: %s", stderr)
	}
	if !strings.Contains(stdout, "partial-test") {
		t.Errorf("expected 'partial-test' in stdout, got: %s", stdout)
	}
}

// ---- concurrency boundary tests ----

func TestCLI_Concurrency_ZeroDefaultsToOne(t *testing.T) {
	_, hosts, cleanup := newMultiSrv(t, 2)
	defer cleanup()

	args := []string{"-u", "testuser", "-P", "testpass", "-c", "0"}
	for _, h := range hosts {
		args = append(args, "-H", h)
	}
	args = append(args, "id")

	_, stderr, exitCode := cliRun(runDefault, args)
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	// Sequential output markers
	for _, h := range hosts {
		if !strings.Contains(stderr, "exec on "+h) {
			t.Errorf("missing marker for %s (concurrency=0 should become 1=seq)", h)
		}
	}
}

func TestCLI_Concurrency_CappedAtHostCount(t *testing.T) {
	_, hosts, cleanup := newMultiSrv(t, 2)
	defer cleanup()

	args := []string{"-u", "testuser", "-P", "testpass", "-c", "10"}
	for _, h := range hosts {
		args = append(args, "-H", h)
	}
	args = append(args, "id")

	_, _, exitCode := cliRun(runDefault, args)
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0 (10 capped to 2)", exitCode)
	}
}

// ---- exec alias ----

func TestCLI_ExecAlias(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	stdout, _, exitCode := cliRun(
		runDefault,
		[]string{"-p", fmt.Sprint(intPort(srv)), "-u", "testuser", "-P", "testpass",
			"-H", srv.Addr(), "exec-via-alias"},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stdout, "exec-via-alias") {
		t.Errorf("expected 'exec-via-alias' in stdout, got: %s", stdout)
	}
}

// ---- scp ----

func remoteSpec(srv *testSSHServer, remotePath string) string {
	return "testuser:testpass@127.0.0.1:" + remotePath
}

func TestParseScpEndpoint_RemoteForms(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		hostSpec string
		path     string
	}{
		{"inline user pass", "root:pass@192.168.1.10:/tmp/a", "root:pass@192.168.1.10", "/tmp/a"},
		{"host abs path", "192.168.1.10:/tmp/a", "192.168.1.10", "/tmp/a"},
		{"password special chars", "user:p@ss:w0rd@host:/tmp/a", "user:p@ss:w0rd@host", "/tmp/a"},
		{"ipv6", "user@[::1]:/tmp/a", "user@[::1]", "/tmp/a"},
		{"home path", "host:~/a", "host", "~/a"},
		{"dot path", "host:./a", "host", "./a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ep, err := parseScpEndpoint(tt.raw)
			if err != nil {
				t.Fatalf("parseScpEndpoint failed: %v", err)
			}
			if !ep.remote {
				t.Fatal("endpoint should be remote")
			}
			if ep.hostSpec != tt.hostSpec {
				t.Errorf("hostSpec = %q, want %q", ep.hostSpec, tt.hostSpec)
			}
			if ep.path != tt.path {
				t.Errorf("path = %q, want %q", ep.path, tt.path)
			}
		})
	}
}

func TestParseScpEndpoint_AmbiguousRelativeRemote(t *testing.T) {
	_, err := parseScpEndpoint("host:relative/path")
	if err == nil {
		t.Fatal("expected ambiguous remote path error")
	}
	if !strings.Contains(err.Error(), "ambiguous remote path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseScpEndpoint_InlinePortRejected(t *testing.T) {
	_, err := parseScpEndpoint("root:pass@192.168.1.10:2222:/tmp/a")
	if err == nil {
		t.Fatal("expected inline port error")
	}
	if !strings.Contains(err.Error(), "use -p/--port") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCLI_ScpUpload(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	localPath := filepath.Join(t.TempDir(), "pushme.txt")
	const content = "push content"
	if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(srv.TempDir(), "pushed.txt")

	_, stderr, exitCode := cliRun(
		runScp,
		[]string{"-p", fmt.Sprint(intPort(srv)), "-u", "nobody", "-P", "wrong", localPath, remoteSpec(srv, remotePath)},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0; stderr=%s", exitCode, stderr)
	}
	if !strings.Contains(stderr, "uploaded") {
		t.Errorf("expected 'uploaded' in stderr, got: %s", stderr)
	}

	data, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("remote content = %q, want %q", string(data), content)
	}
}

func TestCLI_ScpDownload(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	remotePath := filepath.Join(srv.TempDir(), "pullme.txt")
	const content = "pull content"
	if err := os.WriteFile(remotePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(t.TempDir(), "pulled.txt")

	_, stderr, exitCode := cliRun(
		runScp,
		[]string{"-p", fmt.Sprint(intPort(srv)), remoteSpec(srv, remotePath), localPath},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0; stderr=%s", exitCode, stderr)
	}
	if !strings.Contains(stderr, "downloaded") {
		t.Errorf("expected 'downloaded' in stderr, got: %s", stderr)
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("local content = %q, want %q", string(data), content)
	}
}

func TestCLI_ScpUsesPortFlag(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	localPath := filepath.Join(t.TempDir(), "inline-port.txt")
	const content = "inline port wins"
	if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(srv.TempDir(), "inline-port.txt")

	_, stderr, exitCode := cliRun(
		runScp,
		[]string{"-p", fmt.Sprint(intPort(srv)), localPath, remoteSpec(srv, remotePath)},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0; stderr=%s", exitCode, stderr)
	}
	data, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("remote content = %q, want %q", string(data), content)
	}
}

func TestCLI_ScpUploadViaJump(t *testing.T) {
	jump := newTestSSHServer(t)
	defer jump.Close()
	target := newTestSSHServer(t)
	defer target.Close()

	localPath := filepath.Join(t.TempDir(), "jump-upload.txt")
	const content = "jump upload"
	if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(target.TempDir(), "jump-upload.txt")

	_, stderr, exitCode := cliRun(
		runScp,
		[]string{"-p", fmt.Sprint(intPort(target)), "-J", "testuser:testpass@" + jump.Addr(), localPath, remoteSpec(target, remotePath)},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0; stderr=%s", exitCode, stderr)
	}
	data, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("remote content = %q, want %q", string(data), content)
	}
}

func TestCLI_ScpDownloadViaJump(t *testing.T) {
	jump := newTestSSHServer(t)
	defer jump.Close()
	target := newTestSSHServer(t)
	defer target.Close()

	remotePath := filepath.Join(target.TempDir(), "jump-download.txt")
	const content = "jump download"
	if err := os.WriteFile(remotePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(t.TempDir(), "jump-download.txt")

	_, stderr, exitCode := cliRun(
		runScp,
		[]string{"-p", fmt.Sprint(intPort(target)), "-J", "testuser:testpass@" + jump.Addr(), remoteSpec(target, remotePath), localPath},
	)

	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0; stderr=%s", exitCode, stderr)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("local content = %q, want %q", string(data), content)
	}
}

func TestCLI_ScpErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing args", []string{"only-one"}, "source and target are required"},
		{"remote to remote", []string{"h1:/a", "h2:/b"}, "remote-to-remote"},
		{"local to local", []string{"a", "b"}, "local-to-local"},
		{"ambiguous relative remote", []string{"host:relative/path", "b"}, "ambiguous remote path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, exitCode := cliRun(runScp, tt.args)
			if exitCode != 1 {
				t.Errorf("exitCode = %d, want 1", exitCode)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr = %q, want substring %q", stderr, tt.want)
			}
		})
	}
}

func TestCLI_HelpFlag_Scp(t *testing.T) {
	_, stderr, exitCode := cliRun(runScp, []string{"-h"})
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if !strings.Contains(stderr, "Usage of scp") {
		t.Errorf("expected usage text, got: %s", stderr)
	}
}

// ---- help flags ----

func TestCLI_HelpFlag(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"-h", []string{"-h"}},
		{"--help", []string{"--help"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, exitCode := cliRun(runDefault, tt.args)

			// -h should NOT call osExit (showHelp → print usage and return)
			if exitCode != 0 {
				t.Errorf("exitCode = %d, want 0", exitCode)
			}
			if !strings.Contains(stderr, "sshx") {
				t.Errorf("expected usage text, got: %s", stderr)
			}
		})
	}
}

func TestCLI_HelpFlag_NoPushPullSubcommands(t *testing.T) {
	_, stderr, exitCode := cliRun(runDefault, []string{"-h"})
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if strings.Contains(stderr, "sshx push") || strings.Contains(stderr, "sshx pull") {
		t.Errorf("usage should not advertise push/pull, got: %s", stderr)
	}
}

// ---- main() entry point ----

func TestMain_NoArgs(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	origExit := osExit
	defer func() { osExit = origExit }()
	var exitCode int
	osExit = func(code int) {
		exitCode = code
		panic(&cliExit{code})
	}

	defer func() { recover() }()

	os.Args = []string{"sshx"}
	main()
	if exitCode != 1 {
		t.Errorf("exitCode = %d, want 1", exitCode)
	}
}

func TestMain_HelpArg(t *testing.T) {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()

	origExit := osExit
	defer func() { osExit = origExit }()
	osExit = func(code int) { panic(&cliExit{code}) }

	defer func() { recover() }()

	os.Args = []string{"sshx", "help"}
	// main with "help" calls printUsage, does not exit
	stdout, stderr, _ := cliRun(runDefault, []string{"--help"})
	_ = stdout
	if !strings.Contains(stderr, "sshx") {
		t.Errorf("expected usage text in stderr, got: %s", stderr)
	}
}

// ---- runCommandDirect robustness ----

func TestRunCommandDirect_ExitStatus(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	var outBuf, errBuf bytes.Buffer
	err := runCommandDirect(client, "exit:3 failed-command", &outBuf, &errBuf)
	if err == nil {
		t.Fatal("expected ExitError, got nil")
	}
	exitErr, ok := err.(*ssh.ExitError)
	if !ok {
		t.Fatalf("expected *ssh.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitStatus() != 3 {
		t.Errorf("exit status = %d, want 3", exitErr.ExitStatus())
	}
	if !strings.Contains(outBuf.String(), "failed-command") {
		t.Errorf("expected 'failed-command' in stdout, got: %q", outBuf.String())
	}
}

func TestRunCommandDirect_NoOutputExit(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	var outBuf, errBuf bytes.Buffer
	err := runCommandDirect(client, "exit:99", &outBuf, &errBuf)
	if err == nil {
		t.Fatal("expected ExitError, got nil")
	}
	exitErr, ok := err.(*ssh.ExitError)
	if !ok {
		t.Fatalf("expected *ssh.ExitError, got %T", err)
	}
	if exitErr.ExitStatus() != 99 {
		t.Errorf("exit status = %d, want 99", exitErr.ExitStatus())
	}
}

// ---- scp with spaces in path (shellQuote coverage) ----

func TestSCP_PushPathWithSpaces(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	localPath := filepath.Join(t.TempDir(), "file with spaces.txt")
	const content = "spaces in filename"
	if err := os.WriteFile(localPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(srv.TempDir(), "has spaces.txt")
	err := pushFile(client, localPath, remotePath)
	if err != nil {
		t.Fatalf("pushFile with spaces failed: %v", err)
	}

	data, err := os.ReadFile(remotePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestSCP_PullPathWithSpaces(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	remotePath := filepath.Join(srv.TempDir(), "remote space file.txt")
	const content = "pull spaces ok"
	if err := os.WriteFile(remotePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(t.TempDir(), "local space file.txt")
	err := pullFile(client, remotePath, localPath)
	if err != nil {
		t.Fatalf("pullFile with spaces failed: %v", err)
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("content = %q, want %q", string(data), content)
	}
}

func TestSCP_BinaryRoundtrip(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	// Create binary content with null bytes
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x00, 0x7F, 0x80}
	src := filepath.Join(t.TempDir(), "binary.bin")
	if err := os.WriteFile(src, binaryData, 0644); err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(srv.TempDir(), "binary.bin")
	if err := pushFile(client, src, remotePath); err != nil {
		t.Fatalf("pushFile binary failed: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "binary_back.bin")
	if err := pullFile(client, remotePath, dst); err != nil {
		t.Fatalf("pullFile binary failed: %v", err)
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, binaryData) {
		t.Errorf("binary data mismatch: got %v, want %v", got, binaryData)
	}
}

func TestSCP_EmptyFileRoundtrip(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()
	client := srv.newClient(t)
	defer client.Close()

	src := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(src, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}
	remotePath := filepath.Join(srv.TempDir(), "empty.txt")
	if err := pushFile(client, src, remotePath); err != nil {
		t.Fatalf("pushFile empty failed: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "empty_back.txt")
	if err := pullFile(client, remotePath, dst); err != nil {
		t.Fatalf("pullFile empty failed: %v", err)
	}
	data, _ := os.ReadFile(dst)
	if len(data) != 0 {
		t.Errorf("expected empty file, got %d bytes", len(data))
	}
}

// ---- auth edge cases ----

func TestBuildAuthMethods_KeyAndPassword(t *testing.T) {
	methods, err := buildAuthMethods("testuser", "mypass", "/nonexistent/key")
	if err != nil {
		t.Fatalf("expected success with password only, got: %v", err)
	}
	if len(methods) != 1 {
		t.Errorf("expected 1 method (password, key not found), got %d", len(methods))
	}
}

func TestParseHost_PortPriority(t *testing.T) {
	// -H host:2222 should use 2222, not the global -p 8022
	cfg := parseHost("root@host1:2222")
	if cfg.Port != 2222 {
		t.Errorf("port = %d, want 2222 (host-level port should be parsed)", cfg.Port)
	}

	cfg2 := parseHost("root@host1")
	if cfg2.Port != 0 {
		t.Errorf("port = %d, want 0 (no port specified)", cfg2.Port)
	}
}

func TestSSHCfgConnect_PortFromHost(t *testing.T) {
	srv := newTestSSHServer(t)
	defer srv.Close()

	var cfg sshCfg
	cfg.defaults()
	cfg.user = "testuser"
	cfg.passwd = "testpass"

	// Host has explicit port → should use it
	client, label, err := cfg.connect("root@" + srv.Addr())
	if err != nil {
		t.Fatalf("cfg.connect failed: %v", err)
	}
	defer client.Close()
	if !strings.Contains(label, "root@") {
		t.Errorf("label = %q, want root@...", label)
	}
}

func TestBuildAuthMethods_EnvExpansionInPassword(t *testing.T) {
	os.Setenv("MYPW", "env-pw-value")
	defer os.Unsetenv("MYPW")

	methods, err := buildAuthMethods("testuser", "$MYPW", "")
	if err != nil {
		t.Fatalf("buildAuthMethods: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}
}

// ---- parseHost empty password edge cases ----

func TestParseHost_EmptyPasswordAtColon(t *testing.T) {
	cfg := parseHost("root:@192.168.1.10")
	if cfg.Password != "" {
		t.Errorf("Password = %q, want empty", cfg.Password)
	}
	if cfg.User != "root" {
		t.Errorf("User = %q, want root", cfg.User)
	}
	if cfg.Host != "192.168.1.10" {
		t.Errorf("Host = %q, want 192.168.1.10", cfg.Host)
	}
}

func TestParseHost_PasswordWithSpecialChars(t *testing.T) {
	cfg := parseHost("root:p@ss:w0rd@host1")
	if cfg.Password != "p@ss:w0rd" {
		t.Errorf("Password = %q, want p@ss:w0rd", cfg.Password)
	}
	if cfg.User != "root" {
		t.Errorf("User = %q, want root", cfg.User)
	}
	if cfg.Host != "host1" {
		t.Errorf("Host = %q, want host1", cfg.Host)
	}
}
