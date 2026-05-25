package main

import (
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	flag "github.com/spf13/pflag"

	"golang.org/x/crypto/ssh"
)

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

func defaultUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "root"
}

// sshCfg holds parsed SSH connection parameters shared across subcommands.
type sshCfg struct {
	port     int
	user     string
	passwd   string
	key      string
	timeout  time.Duration
	jumps    []string
	showHelp bool
}

func (c *sshCfg) defaults() {
	c.port = 22
	c.user = defaultUser()
	c.passwd = os.Getenv("SSHX_PASSWD")
	c.timeout = 10 * time.Second
}

// bindFlags registers shared SSH flags on fs.
func (c *sshCfg) bindFlags(fs *flag.FlagSet) {
	fs.IntVarP(&c.port, "port", "p", c.port, "SSH port")
	fs.StringVarP(&c.user, "user", "u", c.user, "SSH username")
	fs.StringVarP(&c.passwd, "passwd", "P", c.passwd, "SSH password (supports $VAR env refs; default $SSHX_PASSWD)")
	fs.StringVarP(&c.key, "identity", "i", "", "private key path (~/.ssh/id_rsa)")
	fs.DurationVarP(&c.timeout, "timeout", "t", c.timeout, "connection timeout")
	fs.StringArrayVarP(&c.jumps, "jump", "J", nil, "jump/bastion host (repeatable for chain)")
	fs.BoolVarP(&c.showHelp, "help", "h", false, "show help")
}

func (c *sshCfg) connect(raw string) (*ssh.Client, string, error) {
	hc := parseHost(raw)
	if hc.User == "" {
		hc.User = c.user
	}
	if hc.Password == "" {
		hc.Password = c.passwd
	}
	if hc.Port == 0 {
		hc.Port = c.port
	}
	client, err := connectHost(hc.Host, hc.Port, hc.User, hc.Password, c.jumps, c.user, c.passwd, c.key, c.port, c.timeout)
	return client, hc.Display, err
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.SetInterspersed(true)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage of %s:\n", name)
		fs.PrintDefaults()
	}
	return fs
}

// exec

func runExec(raw []string) {
	var cfg sshCfg
	cfg.defaults()
	var hosts []string
	var concurrency int
	var filePath string

	fs := newFlagSet("exec")
	cfg.bindFlags(fs)
	fs.StringArrayVarP(&hosts, "host", "H", nil, "target host ([user:pass@]host[:port], repeatable)")
	fs.IntVarP(&concurrency, "concurrency", "c", 1, "max concurrent (1=seq, 128=max; capped at host count)")
	fs.StringVarP(&filePath, "file", "f", "", "local shell script to run on remote hosts")
	fs.Parse(raw)
	if cfg.showHelp {
		fs.Usage()
		return
	}

	if len(hosts) == 0 {
		fmt.Fprintln(os.Stderr, "error: at least one host is required (-H)")
		os.Exit(1)
	}

	isScript := filePath != ""
	var cmdOrContent string

	if isScript {
		if fs.NArg() > 0 {
			fmt.Fprintln(os.Stderr, "error: -f and inline command are mutually exclusive")
			os.Exit(1)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read script %s: %v\n", filePath, err)
			os.Exit(1)
		}
		cmdOrContent = string(data)
	} else {
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "error: command is required (or use -f for a script)")
			os.Exit(1)
		}
		cmdOrContent = strings.Join(fs.Args(), " ")
	}

	clients := make(map[string]*ssh.Client, len(hosts))
	for _, raw := range hosts {
		client, label, err := cfg.connect(raw)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[error] %s: %v\n", label, err)
			continue
		}
		clients[label] = client
		defer client.Close()
	}
	if len(clients) == 0 {
		fmt.Fprintln(os.Stderr, "error: failed to establish any connections")
		os.Exit(1)
	}
	if concurrency <= 1 {
		runCommandSerialExec(clients, cmdOrContent, isScript)
	} else {
		runCommandParallelExec(clients, cmdOrContent, concurrency, isScript)
	}
}

// shell

func runShell(raw []string) {
	var cfg sshCfg
	cfg.defaults()
	var host string

	fs := newFlagSet("shell")
	cfg.bindFlags(fs)
	fs.StringVarP(&host, "host", "H", "", "target host ([user:pass@]host[:port])")
	fs.Parse(raw)
	if cfg.showHelp {
		fs.Usage()
		return
	}

	if host == "" {
		fmt.Fprintln(os.Stderr, "error: host is required (-H)")
		os.Exit(1)
	}
	client, _, err := cfg.connect(host)
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

// push

func runPush(raw []string) {
	var cfg sshCfg
	cfg.defaults()
	var host string

	fs := newFlagSet("push")
	cfg.bindFlags(fs)
	fs.StringVarP(&host, "host", "H", "", "target host ([user:pass@]host[:port])")
	fs.Parse(raw)
	if cfg.showHelp {
		fs.Usage()
		return
	}

	if host == "" {
		fmt.Fprintln(os.Stderr, "error: host is required (-H)")
		os.Exit(1)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: local-path and remote-path are required")
		os.Exit(1)
	}
	localPath, remotePath := fs.Arg(0), fs.Arg(1)

	client, _, err := cfg.connect(host)
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

// pull

func runPull(raw []string) {
	var cfg sshCfg
	cfg.defaults()
	var host string

	fs := newFlagSet("pull")
	cfg.bindFlags(fs)
	fs.StringVarP(&host, "host", "H", "", "target host ([user:pass@]host[:port])")
	fs.Parse(raw)
	if cfg.showHelp {
		fs.Usage()
		return
	}

	if host == "" {
		fmt.Fprintln(os.Stderr, "error: host is required (-H)")
		os.Exit(1)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: remote-path and local-path are required")
		os.Exit(1)
	}
	remotePath, localPath := fs.Arg(0), fs.Arg(1)

	client, _, err := cfg.connect(host)
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
	fmt.Fprint(os.Stderr, "sshx - lightweight SSH remote execution tool\n\n"+
		"Usage:\n  sshx <subcommand> [flags...]\n\n"+
		"Subcommands:\n"+
		"  exec    execute a command on remote hosts\n"+
		"  shell   start an interactive PTY shell\n"+
		"  push    upload a file via SCP\n"+
		"  pull    download a file via SCP\n\n"+
		`Use "sshx <subcommand> -h" for detailed subcommand help.`+"\n\n"+
		"Examples:\n"+
		"  sshx exec -f deploy.sh -H host1 -H host2\n"+
		`  sshx exec -H 192.168.1.10 "ls -la /"`+"\n"+
		`  sshx exec -H host1 -H host2 "uptime"`+"\n"+
		"  sshx exec -c 4 -H host1 -H host2 "+
		"# c capped at len(hosts)=2\n"+
		`  sshx exec -J 192.168.1.10 -H 192.168.1.20 "hostname"`+"\n"+
		"  sshx shell -H 192.168.1.10\n"+
		"  sshx push -H 192.168.1.10 ./local.txt /tmp/remote.txt\n"+
		"  sshx pull -H 192.168.1.10 /tmp/remote.txt ./local.txt\n")
}
