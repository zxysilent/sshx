# sshrun — Lightweight SSH Remote Execution Tool

A standalone CLI binary built on `golang.org/x/crypto/ssh`. Supports remote command execution,
interactive PTY shells, and SCP file transfers.

**Dependencies**: Go standard library + `golang.org/x/crypto` + `golang.org/x/term` only.
No external CLI frameworks.

## Installation

### `go install` (recommended)

```bash
go install github.com/zxysilent/sshrun@latest
```

Requires Go 1.26+.

### Build from source

```bash
git clone https://github.com/zxysilent/sshrun.git
cd sshrun
go build -o sshrun .
```

## Subcommands

### 1. exec — Execute a command on remote hosts

Supports multiple hosts. Runs sequentially by default; use `-c <n>` to control concurrency (1-128, default 1).

Each `-H` accepts `[user[:password]@]host[:port]` format. Per-host credentials override
global `-u`/`-P`/`-p` flags. Use key-based auth (`-k`) for heterogeneous environments.

```bash
# Single host
sshrun exec -H 172.22.1.xx "ls -la /"

# Multiple hosts (sequential, default)
sshrun exec -H host1 -H host2 -H host3 "apt update"

# Multiple hosts (concurrent, at most 4 at a time)
sshrun exec -H host1 -H host2 -H host3 -c 4 "uptime"

# Per-host credentials (overrides global -u/-P)
sshrun exec -H root:pass1@host1 -H root:pass2@host2 "hostname"

# Mixed: global default with one host overriding
sshrun exec -H root:admin123@host1:2222 -H host2 -u root -P globalpass "df -h"
```

### 2. shell — Interactive PTY shell

Launches an interactive terminal with PTY support (vim, top, etc. work).
Window size is synced automatically via SIGWINCH.

```bash
sshrun shell -H 172.22.1.xx
sshrun shell -H 172.22.1.xx -u root
```

### 3. push — Upload a file via SCP

Uploads a single file (directories not supported).

```bash
sshrun push -H 172.22.1.xx ./local.txt /tmp/remote.txt
```

### 4. pull — Download a file via SCP

Downloads a single file from the remote host.

```bash
sshrun pull -H 172.22.1.xx /etc/hostname ./hostname.txt
```

## Common Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-H <host>` | Target host (`[user:pass@]host[:port]`, repeatable for `exec`) | **required** |
| `-p <port>` | SSH port (overridable per-host via `-H host:port`) | `22` |
| `-u <user>` | SSH username (overridable per-host via `-H user@host`) | current user |
| `-P <passwd>` | SSH password (overridable per-host via `-H user:pass@host`) | (empty) |
| `-k <key>` | Private key path | `~/.ssh/id_rsa` |
| `-t <timeout>` | Connection timeout | `30s` |

### exec-only flags

| Flag | Description | Default |
|------|-------------|---------|
| `-c <n>` | Max concurrent hosts (1=sequential, 128=max) | `1` |

## Authentication Strategy

1. **Private key first**: `-k` path or `~/.ssh/id_rsa` if readable
2. **Password fallback**: `-P` password if key is unavailable
3. **Error**: exits if neither is available

## Technical Details

- **SCP protocol**: hand-rolled (no `github.com/pkg/sftp` dependency), using `ssh.Session`
  stdin/stdout pipes with SCP source/sink mode
- **Concurrency**: `exec` uses a semaphore (buffered channel) to cap goroutine count;
  `-c <n>` controls the limit (min 1 = sequential, max 128)
- **PTY**: raw mode + SIGWINCH window resize sync via `golang.org/x/term`
