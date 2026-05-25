package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// hostsFlag implements flag.Value to accumulate multiple -H values.
type hostsFlag []string

func (h *hostsFlag) String() string { return strings.Join(*h, ",") }
func (h *hostsFlag) Set(v string) error {
	*h = append(*h, v)
	return nil
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "exec":
		runExec(os.Args[2:])
	case "shell":
		runShell(os.Args[2:])
	case "push":
		runPush(os.Args[2:])
	case "pull":
		runPull(os.Args[2:])
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// ─────────────────── shared flag bindings ───────────────────

func defaultUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "root"
}

// bindSSHFlags registers common SSH flags on fs.
func bindSSHFlags(fs *flag.FlagSet, port *int, user, passwd, key *string, timeout *time.Duration) {
	fs.IntVar(port, "p", 22, "SSH port")
	fs.StringVar(user, "u", defaultUser(), "SSH username")
	fs.StringVar(passwd, "P", "", "SSH password")
	fs.StringVar(key, "k", "", "private key path (defaults to ~/.ssh/id_rsa)")
	fs.DurationVar(timeout, "t", 30*time.Second, "connection timeout")
}

// ─────────────────── exec subcommand ───────────────────

func printExecUsage(fs *flag.FlagSet) {
	fmt.Fprint(os.Stderr, `Usage: sshrun exec [flags] <command>

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Examples:
  sshrun exec -H 172.22.1.xx "ls -la /"
  sshrun exec -H host1 -H host2 "uptime"
  sshrun exec -H root:pass@host1:2222 "hostname"
  sshrun exec -H host1 -H host2 -c 4 "uptime"
`)
}

func runExec(args []string) {
	var hosts hostsFlag
	var port int
	var user, passwd, key string
	var timeout time.Duration
	var concurrency int

	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	fs.Usage = func() { printExecUsage(fs) }
	bindSSHFlags(fs, &port, &user, &passwd, &key, &timeout)
	fs.Var(&hosts, "H", "target host ([user:pass@]host[:port], repeatable)")
	fs.IntVar(&concurrency, "c", 1, "max concurrent hosts (1-128, 1=sequential)")
	fs.Parse(args)

	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 128 {
		concurrency = 128
	}

	if len(hosts) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one host is required (-H)")
		os.Exit(1)
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "error: command is required")
		os.Exit(1)
	}
	cmdline := strings.Join(fs.Args(), " ")

	// connect to all hosts, merging per-host credentials with global defaults
	clients := make(map[string]*ssh.Client, len(hosts))
	for _, raw := range hosts {
		hc := parseHost(raw)
		hUser := hc.User
		if hUser == "" {
			hUser = user
		}
		hPass := hc.Password
		if hPass == "" {
			hPass = passwd
		}
		hPort := hc.Port
		if hPort == 0 {
			hPort = port
		}
		addr := net.JoinHostPort(hc.Host, fmt.Sprintf("%d", hPort))
		client, err := sshClient(addr, hUser, hPass, key, timeout)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: %v\n", hc.Display, err)
			continue
		}
		clients[hc.Display] = client
		defer client.Close()
	}

	if len(clients) == 0 {
		fmt.Fprintln(os.Stderr, "error: failed to establish any connections")
		os.Exit(1)
	}

	if concurrency <= 1 {
		runCommandSerial(clients, cmdline)
	} else {
		runCommandParallel(clients, cmdline, concurrency)
	}
}

// ─────────────────── shell subcommand ───────────────────

func printShellUsage(fs *flag.FlagSet) {
	fmt.Fprint(os.Stderr, `Usage: sshrun shell [flags]

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Examples:
  sshrun shell -H 172.22.1.xx
  sshrun shell -H 172.22.1.xx -u root
`)
}

func runShell(args []string) {
	var host string
	var port int
	var user, passwd, key string
	var timeout time.Duration

	fs := flag.NewFlagSet("shell", flag.ExitOnError)
	fs.Usage = func() { printShellUsage(fs) }
	bindSSHFlags(fs, &port, &user, &passwd, &key, &timeout)
	fs.StringVar(&host, "H", "", "target host ([user:pass@]host[:port])")
	fs.Parse(args)

	if host == "" {
		fmt.Fprintln(os.Stderr, "error: host is required (-H)")
		os.Exit(1)
	}

	hc := parseHost(host)
	hUser := hc.User
	if hUser == "" {
		hUser = user
	}
	hPass := hc.Password
	if hPass == "" {
		hPass = passwd
	}
	hPort := hc.Port
	if hPort == 0 {
		hPort = port
	}
	addr := net.JoinHostPort(hc.Host, fmt.Sprintf("%d", hPort))
	client, err := sshClient(addr, hUser, hPass, key, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := startShell(client); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ─────────────────── push subcommand ───────────────────

func printPushUsage(fs *flag.FlagSet) {
	fmt.Fprint(os.Stderr, `Usage: sshrun push [flags] <local-path> <remote-path>

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Examples:
  sshrun push -H 172.22.1.xx ./local.txt /tmp/remote.txt
`)
}

func runPush(args []string) {
	var host string
	var port int
	var user, passwd, key string
	var timeout time.Duration

	fs := flag.NewFlagSet("push", flag.ExitOnError)
	fs.Usage = func() { printPushUsage(fs) }
	bindSSHFlags(fs, &port, &user, &passwd, &key, &timeout)
	fs.StringVar(&host, "H", "", "target host ([user:pass@]host[:port])")
	fs.Parse(args)

	if host == "" {
		fmt.Fprintln(os.Stderr, "error: host is required (-H)")
		os.Exit(1)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: local-path and remote-path are required")
		os.Exit(1)
	}

	localPath := fs.Arg(0)
	remotePath := fs.Arg(1)

	hc := parseHost(host)
	hUser := hc.User
	if hUser == "" {
		hUser = user
	}
	hPass := hc.Password
	if hPass == "" {
		hPass = passwd
	}
	hPort := hc.Port
	if hPort == 0 {
		hPort = port
	}
	addr := net.JoinHostPort(hc.Host, fmt.Sprintf("%d", hPort))
	client, err := sshClient(addr, hUser, hPass, key, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := pushFile(client, localPath, remotePath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// ─────────────────── pull subcommand ───────────────────

func printPullUsage(fs *flag.FlagSet) {
	fmt.Fprint(os.Stderr, `Usage: sshrun pull [flags] <remote-path> <local-path>

Flags:
`)
	fs.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Examples:
  sshrun pull -H 172.22.1.xx /tmp/remote.txt ./local.txt
`)
}

func runPull(args []string) {
	var host string
	var port int
	var user, passwd, key string
	var timeout time.Duration

	fs := flag.NewFlagSet("pull", flag.ExitOnError)
	fs.Usage = func() { printPullUsage(fs) }
	bindSSHFlags(fs, &port, &user, &passwd, &key, &timeout)
	fs.StringVar(&host, "H", "", "target host ([user:pass@]host[:port])")
	fs.Parse(args)

	if host == "" {
		fmt.Fprintln(os.Stderr, "error: host is required (-H)")
		os.Exit(1)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: remote-path and local-path are required")
		os.Exit(1)
	}

	remotePath := fs.Arg(0)
	localPath := fs.Arg(1)

	hc := parseHost(host)
	hUser := hc.User
	if hUser == "" {
		hUser = user
	}
	hPass := hc.Password
	if hPass == "" {
		hPass = passwd
	}
	hPort := hc.Port
	if hPort == 0 {
		hPort = port
	}
	addr := net.JoinHostPort(hc.Host, fmt.Sprintf("%d", hPort))
	client, err := sshClient(addr, hUser, hPass, key, timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	if err := pullFile(client, remotePath, localPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprint(os.Stderr, `sshrun — lightweight SSH remote execution tool

Usage:
  sshrun <subcommand> [flags...]

Subcommands:
  exec    execute a command on remote hosts (supports multiple hosts)
  shell   start an interactive PTY shell (single host only)
  push    upload a file via SCP (single host only)
  pull    download a file via SCP (single host only)

Use "sshrun <subcommand> -h" for detailed subcommand help.

Examples:
  sshrun exec -H 172.22.1.xx "ls -la /"
  sshrun exec -H host1 -H host2 "uptime"
  sshrun exec -H root:pass@host1 "hostname"
  sshrun exec -H host1 -H host2 -c 4 "uptime"
  sshrun shell -H 172.22.1.xx
  sshrun push -H 172.22.1.xx ./local.txt /tmp/remote.txt
  sshrun pull -H 172.22.1.xx /tmp/remote.txt ./local.txt
`)
}
