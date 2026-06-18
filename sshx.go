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
	version   = "v0.5.0"
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
	case "scp":
		runScp(os.Args[2:])
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
	port          int
	user          string
	passwd        string
	key           string
	timeout       time.Duration
	jumps         []string
	strictHostKey bool
	knownHosts    string
	showHelp      bool
}

func (c *sshCfg) defaults() {
	c.port = 22
	c.user = defaultUser()
	c.passwd = os.Getenv("SSHX_PASSWD")
	c.timeout = 10 * time.Second
	c.knownHosts = defaultKnownHostsPath()
}

// bindFlags registers shared SSH flags on fs.
func (c *sshCfg) bindFlags(fs *flag.FlagSet) {
	fs.IntVarP(&c.port, "port", "p", c.port, "SSH port")
	fs.StringVarP(&c.user, "user", "u", c.user, "SSH username")
	fs.StringVarP(&c.passwd, "passwd", "P", c.passwd, "SSH password (supports $VAR env refs; default $SSHX_PASSWD)")
	fs.StringVarP(&c.key, "identity", "i", "", "private key path (~/.ssh/id_rsa)")
	fs.DurationVarP(&c.timeout, "timeout", "t", c.timeout, "connection timeout")
	fs.StringArrayVarP(&c.jumps, "jump", "J", nil, "jump/bastion host (repeatable for chain)")
	fs.BoolVar(&c.strictHostKey, "strict-host-key", c.strictHostKey, "verify host keys against known_hosts")
	fs.StringVar(&c.knownHosts, "known-hosts", c.knownHosts, "known_hosts file for --strict-host-key")
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
	hostKeyCallback, err := c.hostKeyCallback()
	if err != nil {
		return nil, hc.Display, err
	}
	client, err := connectHostWithHostKeyCallback(
		hc.Host, hc.Port, hc.User, hc.Password,
		c.jumps, c.user, c.passwd, c.key, c.port, c.timeout,
		hostKeyCallback,
	)
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

// scp

type scpEndpoint struct {
	raw      string
	remote   bool
	hostSpec string
	path     string
}

func parseScpEndpoint(raw string) (scpEndpoint, error) {
	for _, sep := range []string{":/", ":~/", ":./"} {
		if idx := strings.Index(raw, sep); idx >= 0 {
			hostSpec := raw[:idx]
			if hostSpec == "" {
				return scpEndpoint{}, fmt.Errorf("missing remote host in %q", raw)
			}
			if hc := parseHost(hostSpec); hc.Port != 0 {
				return scpEndpoint{}, fmt.Errorf("remote endpoint port is not supported in %q: use -p/--port", raw)
			}
			return scpEndpoint{
				raw:      raw,
				remote:   true,
				hostSpec: hostSpec,
				path:     raw[idx+1:],
			}, nil
		}
	}
	if strings.Contains(raw, ":") {
		return scpEndpoint{}, fmt.Errorf("ambiguous remote path %q: use host:/path, host:~/path, or host:./path", raw)
	}
	return scpEndpoint{raw: raw, path: raw}, nil
}

func runScp(raw []string) {
	var cfg sshCfg
	cfg.defaults()

	fs := newFlagSet("scp")
	cfg.bindFlags(fs)
	fs.Parse(raw)
	if cfg.showHelp {
		fs.Usage()
		return
	}

	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "error: source and target are required")
		osExit(1)
	}
	src, err := parseScpEndpoint(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		osExit(1)
	}
	dst, err := parseScpEndpoint(fs.Arg(1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		osExit(1)
	}

	switch {
	case !src.remote && dst.remote:
		client, _, err := cfg.connect(dst.hostSpec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			osExit(1)
		}
		defer client.Close()
		if err := pushFile(client, src.path, dst.path); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			osExit(1)
		}
	case src.remote && !dst.remote:
		client, _, err := cfg.connect(src.hostSpec)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			osExit(1)
		}
		defer client.Close()
		if err := pullFile(client, src.path, dst.path); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			osExit(1)
		}
	case src.remote && dst.remote:
		fmt.Fprintln(os.Stderr, "error: remote-to-remote scp is not supported")
		osExit(1)
	default:
		fmt.Fprintln(os.Stderr, "error: local-to-local scp is not supported")
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
		"  sshx [flags] -H <host> [-H <host>...] [-c n] [-f script] <command> multi-host execution\n"+
		"  sshx scp [flags] <source> <target>               copy one file via SCP\n\n"+
		"Flags:\n"+
		"  -p, --port int        SSH port (default 22)\n"+
		"  -u, --user string     SSH username\n"+
		"  -P, --passwd string   SSH password (supports $VAR env refs; default $SSHX_PASSWD)\n"+
		"  -i, --identity        private key path (~/.ssh/id_rsa)\n"+
		"  -t, --timeout         connection timeout (default 10s)\n"+
		"  -J, --jump strings    jump/bastion host (repeatable for chain)\n"+
		"      --strict-host-key verify host keys against known_hosts (default off)\n"+
		"      --known-hosts     known_hosts file for --strict-host-key\n"+
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
		"  sshx scp -p 2222 ./local.txt root:pass@192.168.1.10:/tmp/remote.txt\n"+
		"  sshx scp -J bastion 192.168.1.10:/tmp/remote.txt ./local.txt\n")
}
