# sshx — Lightweight SSH Remote Execution Tool

## Installation

### `go install` (recommended)

```bash
go install github.com/zxysilent/sshx@latest
```

### Build from source (with version info)

```bash
git clone https://github.com/zxysilent/sshx.git
cd sshx
buildSha=$(git rev-parse --short=8 HEAD)
go build -ldflags "-s -w -X 'main.buildSha=${buildSha}' -X 'main.buildTime=$(date +'%Y-%m-%d %H:%M:%S')' -X 'main.version=v0.2.1'" -o sshx .
```

## Quick Start

Behaves like native `ssh`, with built-in multi-host support:

```bash
# Interactive shell (like ssh)
sshx 192.168.1.10

# Single host command
sshx 192.168.1.10 "ls -la /"

# Multi-host (repeatable -H)
sshx -H host1 -H host2 -H host3 "df -h"

# Concurrent with -c
sshx -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# File transfer
sshx push -H 192.168.1.10 ./local.txt /tmp/remote.txt
sshx pull -H 192.168.1.10 /etc/hostname ./hostname.txt
```

## Subcommands

| Subcommand | Purpose | Multi-`-H` |
|------------|---------|:----------:|
| *(default)* | Interactive shell or command execution | ✅ |
| `push` | Upload file via SCP | ❌ |
| `pull` | Download file via SCP | ❌ |

> `exec` is kept as an alias for the default mode.

## `-H` Format

```
-H [user[:password]@]host[:port]
```

Per-host credentials override global `-u`/`-P`/`-p`. Omitted fields fall back to globals.

```bash
-H 192.168.1.10                     # bare host
-H root@192.168.1.10                # custom user
-H root:pass@192.168.1.10           # user + password
-H root:pass@192.168.1.10:2222      # all fields
```

## Global Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-H` | — | Target host for multi-host mode (repeatable) |
| `-p` | `22` | SSH port |
| `-u` | current user | SSH username |
| `-P` | `$SSHX_PASSWD` | SSH password (supports `$VAR` expansion) |
| `-i` | `~/.ssh/id_rsa` | Private key path |
| `-t` | `10s` | Connection timeout |
| `-J` | — | Jump/bastion host (repeatable for chaining) |
| `-c` | `1` | Max concurrency (1=sequential, 128=max) |
| `-f` | — | Local shell script to run on remote hosts |
| `-h` | — | Show help |

`-c` / `-f` only take effect in multi-host mode (`-H`).

## Usage Patterns

### Single-host mode (like ssh)

```bash
# Interactive shell
sshx 192.168.1.10
sshx root@192.168.1.10

# Single command
sshx 192.168.1.10 "df -h"
sshx -u admin -P secret 192.168.1.10 "hostname"

# Via jump host
sshx -J bastion 192.168.1.20 "uptime"
```

### Multi-host mode (`-H`)

```bash
# Sequential (default -c 1)
sshx -H host1 -H host2 -H host3 "df -h"

# Concurrent
sshx -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# Per-host credentials
sshx -H root:pass1@host1 -H root:pass2@host2 "whoami"

# Local script on multiple hosts
sshx -f deploy.sh -H host1 -H host2 -H host3

# Script + concurrency
sshx -f script.sh -H h1 -H h2 -c 4
```

### File Transfer (push / pull)

```bash
# Upload
sshx push -H 192.168.1.10 ./config.yaml /etc/app/config.yaml

# Download
sshx pull -H 192.168.1.10 /var/log/app.log ./app.log

# Via jump host
sshx push -J 192.168.1.10 -H 192.168.1.20 ./config.yaml /tmp/config.yaml
```

## Jump Host (`-J`)

Tunnel through one or more bastion hosts in order:

```bash
# Single jump
sshx -J root:pass@192.168.1.10 -H 192.168.1.20 "hostname"

# Multi-hop chain
sshx -J hop1 -J hop2 -H target "uptime"

# File transfer via jump
sshx push -J 192.168.1.10 -H 192.168.1.20 ./local.txt /tmp/remote.txt
```

## Authentication Strategy

1. **Private key first**: `-i` path or `~/.ssh/id_rsa` if readable
2. **Password fallback**: `-P` flag → `$SSHX_PASSWD` env var → inline `-H user:pass@host`
3. **Error**: exits if neither is available

Avoid passwords in shell history:

```bash
export SSHX_PASSWD="your-secret"
sshx -H 192.168.1.10 "uptime"
```

## Flag Interleaving

Flags and positional arguments can be freely interleaved:

```bash
# All equivalent
sshx -H host1 -H host2 "ls -la"
sshx "ls -la" -H host1 -H host2
sshx -c 4 ls -la -H host1 -H host2 /tmp
```
