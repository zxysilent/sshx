---
name: sshx
description: 'Remote SSH command execution, interactive PTY shell, and SCP file transfer. Use for: running commands on remote servers, opening interactive SSH sessions, uploading or downloading files, cluster-wide service checks.'
argument-hint: '[flags] <host| -H hosts...> [command]'
user-invocable: true
disable-model-invocation: false
---

# sshx

Lightweight SSH tool. Default mode behaves like `ssh`; add `-H` for multi-host.

## Usage

```bash
sshx [flags] <host> [command...]                          # single host (ssh-like)
sshx [flags] -H <host> [-H ...] [-c n] [-f script] <cmd>  # multi-host
sshx scp [flags] <source> <target>                        # upload/download one file
```

- No command → interactive PTY shell (single host)
- `exec` is a legacy alias for default mode

## Flags

| Flag | Default | Notes |
|------|---------|-------|
| `-H` | — | Repeatable; `[user[:pass]@]host[:port]` |
| `-u` | `$USER` | Per-host override via `-H user@host` |
| `-P` | `$SSHX_PASSWD` | Supports `$VAR` expansion; per-host override via `-H user:pass@host` |
| `-i` | `~/.ssh/id_rsa` | Private key |
| `-p` | `22` | Per-host override via `-H host:port` |
| `-t` | `10s` | Connection timeout |
| `-J` | — | Jump host, repeatable for chain; `[user:pass@]host[:port]` |
| `-c` | `1` | Concurrency (1=seq, 128=max; capped at host count) |
| `-f` | — | Local shell script to pipe to `bash -s` on remotes (multi-host only) |

Auth: key first → password fallback. `-f` and inline command are mutually exclusive.

## Task patterns

```bash
# Interactive shell
sshx 192.168.1.10

# Single command
sshx 192.168.1.10 "df -h"

# Multi-host sequential
sshx -H h1 -H h2 -H h3 "systemctl status kubelet"

# Multi-host concurrent
sshx -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# Script on many hosts
sshx -f deploy.sh -H h1 -H h2 -H h3

# Via jump host
sshx -J bastion -H internal.host "hostname"

# Multi-hop chain
sshx -J hop1 -J hop2 -H target "uptime"

# Upload config
sshx scp -p 2222 ./config.yaml user:pass@host:/etc/app/config.yaml

# Grab logs
sshx scp host:/var/log/app.log ./app.log
```

## Constraints

- `scp` is single-host, single-file, no directories.
- SCP remote paths use `[user[:pass]@]host:/path`; specify ports with `-p`, not `host:port:/path`.
- `shell` (PTY) requires a real terminal — don't invoke in automated scripts.
- All hosts share one private key (`-i`); use inline `user:pass@host` for different passwords.
- No sudo password prompt.
- SCP has no progress bar.
- Port `-` in `-H` or `-p` means default 22; password `-` reads `$SSHX_PASSWD`.
