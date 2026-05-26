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

// osExit is a variable to allow tests to intercept os.Exit calls.
var osExit = os.Exit

// Build metadata — injected via -ldflags at build time; defaults below.
var (
	version   = "v0.3.0"
	buildTime = "unknown"
	buildSha  = "unknown"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		osExit(1)
	}

	// Known subcommands; anything else is treated as the default mode.
	switch os.Args[1] {
	case "exec":
		// exec is just an alias for the default mode.
		runDefault(os.Args[2:])
	case "push":
		runPush(os.Args[2:])
	case "pull":
		runPull(os.Args[2:])
	case "-v", "--version", "-V", "version":
		printVersion()
		return
	case "-h", "--help", "help":
		printUsage()
		return
	default:
		runDefault(os.Args[1:])
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

// runDefault — universal single-host or multi-host handler.
// If -H is set: multi-host mode (command required, script via -f).
// If -H not set: positional host required, then interactive shell or single command.
func runDefault(raw []string) {
	var cfg sshCfg
	cfg.defaults()
	var hosts []string
	var concurrency int
	var filePath string

	fs := newFlagSet("sshx")
	cfg.bindFlags(fs)
	fs.StringArrayVarP(&hosts, "host", "H", nil, "target host ([user:pass@]host[:port], repeatable)")
	fs.IntVarP(&concurrency, "concurrency", "c", 1, "max concurrent (1=seq, 128=max; capped at host count)")
	fs.StringVarP(&filePath, "file", "f", "", "local shell script to run on remote hosts")
	fs.Parse(raw)
	if cfg.showHelp {
		fs.Usage()
		return
	}

	isMulti := len(hosts) > 0

	if !isMulti {
		// ---- single-host mode (like ssh) ----
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "error: host is required")
			osExit(1)
		}
		host := fs.Arg(0)
		cmdArgs := fs.Args()[1:]

		client, _, err := cfg.connect(host)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			osExit(1)
		}
		defer client.Close()

		if len(cmdArgs) == 0 {
			// Interactive shell
			if err := startShell(client); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				osExit(1)
			}
			return
		}

		// Single command execution (stream output directly)
		cmd := strings.Join(cmdArgs, " ")
		if err := runCommandDirect(client, cmd, os.Stdout, os.Stderr); err != nil {
			if exitErr, ok := err.(*ssh.ExitError); ok {
				osExit(exitErr.ExitStatus())
			}
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			osExit(1)
		}
		return
	}

	// ---- multi-host mode (like sshx exec) ----
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > 128 {
		concurrency = 128
	}
	if concurrency > len(hosts) {
		concurrency = len(hosts)
	}

	isScript := filePath != ""
	var cmdOrContent string

	if isScript {
		if fs.NArg() > 0 {
			fmt.Fprintln(os.Stderr, "error: -f and inline command are mutually exclusive")
			osExit(1)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: read script %s: %v\n", filePath, err)
			osExit(1)
		}
		cmdOrContent = string(data)
	} else {
		if fs.NArg() == 0 {
			fmt.Fprintln(os.Stderr, "error: command is required (or use -f for a script)")
			osExit(1)
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
		osExit(1)
	}
	if concurrency <= 1 {
		runCommandSerialExec(clients, cmdOrContent, isScript)
	} else {
		runCommandParallelExec(clients, cmdOrContent, concurrency, isScript)
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
		osExit(1)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: local-path and remote-path are required")
		osExit(1)
	}
	localPath, remotePath := fs.Arg(0), fs.Arg(1)

	client, _, err := cfg.connect(host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		osExit(1)
	}
	defer client.Close()
	if err := pushFile(client, localPath, remotePath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		osExit(1)
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
		osExit(1)
	}
	if fs.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "error: remote-path and local-path are required")
		osExit(1)
	}
	remotePath, localPath := fs.Arg(0), fs.Arg(1)

	client, _, err := cfg.connect(host)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		osExit(1)
	}
	defer client.Close()
	if err := pullFile(client, remotePath, localPath); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		osExit(1)
	}
}

func printVersion() {
	fmt.Printf("sshx version %s (commit %s, built %s)\n", version, buildSha, buildTime)
}

func printUsage() {
	fmt.Fprint(os.Stderr, "sshx - lightweight SSH remote execution tool\n\n"+
		"Usage:\n"+
		"  sshx [flags] <host> [command...]               single host (shell if no command)\n"+
		"  sshx [flags] -H <host> [-H <host>...] [-c n] [-f script] <command>\n"+
		"                                                   multi-host execution\n"+
		"  sshx push   [flags] <local> <remote>             upload file via SCP\n"+
		"  sshx pull   [flags] <remote> <local>             download file via SCP\n\n"+
		"Flags:\n"+
		"  -p, --port int        SSH port (default 22)\n"+
		"  -u, --user string     SSH username\n"+
		"  -P, --passwd string   SSH password (supports $VAR env refs; default $SSHX_PASSWD)\n"+
		"  -i, --identity        private key path (~/.ssh/id_rsa)\n"+
		"  -t, --timeout         connection timeout (default 10s)\n"+
		"  -J, --jump strings    jump/bastion host (repeatable for chain)\n"+
		"  -H, --host strings    target host for multi-host mode (repeatable)\n"+
		"  -c, --concurrency     max concurrent connections (default 1, max 128)\n"+
		"  -f, --file            local shell script to upload and run\n"+
		"  -h, --help            show help\n"+
		"  -V, --version         show version\n\n"+
		"Examples:\n"+
		"  sshx 192.168.1.10                              # interactive shell\n"+
		`  sshx 192.168.1.10 "ls -la /"`+"\n"+
		`  sshx -J bastion 192.168.1.10 "hostname"`+"\n"+
		"  sshx -H host1 -H host2 uptime                  # multi-host sequential\n"+
		"  sshx -H host1 -H host2 -c 4 uptime             # multi-host concurrent\n"+
		"  sshx -H host1 -H host2 -f deploy.sh            # script on multiple hosts\n"+
		"  sshx push 192.168.1.10 ./local.txt /tmp/remote.txt\n"+
		"  sshx pull 192.168.1.10 /tmp/remote.txt ./local.txt\n")
}
