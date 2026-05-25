# sshx — Lightweight SSH Remote Execution Tool

## Installation

### `go install` (recommended)

```bash
go install github.com/zxysilent/sshx@latest
```

Requires Go 1.26+.

### Build from source

```bash
git clone https://github.com/zxysilent/sshx.git
cd sshx
go build -o sshx .
```

## Subcommands

### 1. exec — Execute a command on remote hosts

Supports multiple hosts. Runs sequentially by default; use `-c <n>` to control concurrency (1-128, default 1).

Each `-H` accepts `[user[:password]@]host[:port]` format. Per-host credentials override
global `-u`/`-P`/`-p` flags. Use key-based auth (`-i`) for heterogeneous environments.

```bash
# Single host
sshx exec -H 192.168.1.10 "ls -la /"

# Multiple hosts (sequential, default)
sshx exec -H host1 -H host2 -H host3 "apt update"

# Multiple hosts (concurrent, at most 4 at a time)
sshx exec -H host1 -H host2 -H host3 -c 4 "uptime"

# Per-host credentials (overrides global -u/-P)
sshx exec -H root:pass1@host1 -H root:pass2@host2 "hostname"

# Mixed: global default with one host overriding
sshx exec -H root:admin123@host1:2222 -H host2 -u root -P globalpass "df -h"

# Via jump host (192.168.1.10 → 192.168.1.20)
sshx exec -J 192.168.1.10 -H 192.168.1.20 "hostname"

# Jump host with custom port and credentials
sshx exec -J root:pass@bastion:2222 -H 192.168.1.20 "uptime"

# Run a local script on remote hosts
sshx exec -f deploy.sh -H host1 -H host2 -H host3
```

### 2. shell — Interactive PTY shell

Launches an interactive terminal with PTY support (vim, top, etc. work).
Window size is synced automatically via SIGWINCH.

```bash
sshx shell -H 192.168.1.10
sshx shell -H 192.168.1.10 -u root
```

### 3. push — Upload a file via SCP

Uploads a single file (directories not supported).

```bash
sshx push -H 192.168.1.10 ./local.txt /tmp/remote.txt
```

### 4. pull — Download a file via SCP

Downloads a single file from the remote host.

```bash
sshx pull -H 192.168.1.10 /etc/hostname ./hostname.txt
```

## Common Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-H <host>` | Target host (`[user:pass@]host[:port]`, repeatable for `exec`) | **required** |
| `-p <port>` | SSH port (overridable per-host via `-H host:port`) | `22` |
| `-u <user>` | SSH username (overridable per-host via `-H user@host`) | current user |
| `-P <passwd>` | SSH password (overridable via `-H user:pass@host`; default from `$SSHX_PASSWD`) | (empty) |
| `-i <key>` | Private key path | `~/.ssh/id_rsa` |
| `-t <timeout>` | Connection timeout | `10s` |
| `-J <host>` | Jump/bastion host (repeatable for chain; `[user:pass@]host[:port]`) | (disabled) |

### exec-only flags

| Flag | Description | Default |
|------|-------------|---------|
| `-c <n>` | Max concurrent hosts (1=sequential, 128=max) | `1` |
| `-f <path>` | Local shell script to run on remote hosts | (none) |

## Jump Host (`-J`)

Use `-J` to connect through one or more bastion/jump hosts when the target is not
directly reachable. Specify multiple `-J` to chain: `-J hop1 -J hop2 -H target`.
All subcommands (`exec`, `shell`, `push`, `pull`) support `-J`.
Jump credentials use `[user:pass@]host[:port]` format — same as `-H`.
Missing fields fall back to global `-u`/`-P`/`-p`.

> When the target host has no explicit password, it automatically reuses the
> last jump host's password (common when bastion and internal hosts share credentials).

```bash
# Bare jump host
sshx exec -J 192.168.1.10 -H 192.168.1.20 "hostname"

# Jump host with credentials
sshx exec -J root:pass@192.168.1.10 -H 192.168.1.20 "uptime"

# shell via jump
sshx shell -J 192.168.1.10 -H 192.168.1.20

# multi-hop chain
sshx exec -J 10.10.0.1 -J 192.168.1.10 -H 192.168.1.20 "uptime"

# file transfer via jump
sshx push -J 192.168.1.10 -H 192.168.1.20 ./config.yaml /tmp/config.yaml
```

## Authentication Strategy

1. **Private key first**: `-i` path or `~/.ssh/id_rsa` if readable
2. **Password fallback**: `-P` flag, then `$SSHX_PASSWD` env var, then inline `-H user:pass@host`
3. **Error**: exits if neither is available

To avoid exposing passwords in shell history or process listings, use the
`SSHX_PASSWD` environment variable:

```bash
export SSHX_PASSWD="your-secret"
sshx exec -H 192.168.1.10 "uptime"
```

## Technical Details

- **SCP protocol**: hand-rolled (no `github.com/pkg/sftp` dependency), using `ssh.Session`
  stdin/stdout pipes with SCP source/sink mode
- **Concurrency**: `exec` uses a semaphore (buffered channel) to cap goroutine count;
  `-c <n>` controls the limit (min 1 = sequential, max 128)
- **PTY**: raw mode + SIGWINCH window resize sync via `golang.org/x/term`
