---
name: sshrun
description: 'SSH remote command execution, interactive PTY shell, and SCP file transfer on remote hosts. Use when asked to: run commands on remote servers, open interactive SSH sessions, upload or download files over SSH, check service status across a cluster.'
argument-hint: 'Describe what to run and on which hosts'
user-invocable: true
disable-model-invocation: false
---

# sshrun

Lightweight SSH tool (4 subcommands, zero external deps).

## Quick reference

| Subcommand | Usage | Multi-`-H` |
|------------|-------|:----------:|
| `exec` | `sshrun exec [flags] <command>` | ✅ |
| `shell` | `sshrun shell [flags]` | ❌ |
| `push` | `sshrun push [flags] <local> <remote>` | ❌ |
| `pull` | `sshrun pull [flags] <remote> <local>` | ❌ |

## `-H` format

```
-H [user[:password]@]host[:port]
```

Inline fields **override** global `-u`/`-P`/`-p`.

```
-H 172.22.1.9                     # bare host
-H 172.22.1.9:2222                # custom port
-H root@172.22.1.9                # custom user
-H root:pass@172.22.1.9           # user + password
-H root:pass@172.22.1.9:2222      # all fields
```

## Global flags

| Flag | Default | Overridable per `-H` |
|------|---------|:--------------------:|
| `-u` | `$USER` | ✅ |
| `-P` | (empty) | ✅ |
| `-k` | `~/.ssh/id_rsa` | ❌ |
| `-p` | `22` | ✅ |
| `-t` | `30s` | ❌ |

Auth: key first → password fallback.

## exec — run commands on remote hosts

Sequential by default. `-c <n>` enables parallel (1–128).

```bash
# single
sshrun exec -H 172.22.1.9 "hostname"

# multi, sequential
sshrun exec -H h1 -H h2 -H h3 "df -h"

# multi, 4 concurrent
sshrun exec -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# per-host passwords
sshrun exec -H root:p1@h1 -H root:p2@h2 "whoami"

# complex command
sshrun exec -H 172.22.1.9 "sudo systemctl status kubelet | head -20"
```

`-c`: default 1 for <5 hosts; 4–8 for >10 hosts / quick reads; 128 max.

## shell — interactive PTY

**Do not invoke in automated scripts.** Single host only.

```bash
sshrun shell -H 172.22.1.9
sshrun shell -H root@172.22.1.9
```

## push / pull — SCP file transfer

Single file, no directories, single host.

```bash
# upload
sshrun push -H 172.22.1.9 ./config.yaml /etc/app/config.yaml

# download
sshrun pull -H 172.22.1.9 /var/log/app.log ./app.log
```

## Task patterns

```bash
# run on multiple hosts
sshrun exec -H A -H B -H C "<cmd>"

# check service cluster-wide
sshrun exec -H h1 -H h2 -H h3 -c 3 "systemctl is-active kubelet"

# deploy config
sshrun push -H host ./local.yaml /etc/app/config.yaml

# grab logs
sshrun pull -H host /var/log/app.log ./app.log

# disk usage cluster-wide
sshrun exec -H h1 -H h2 -H h3 -c 3 "df -h /"
```

## Constraints

- **Never invoke `shell` in automated scripts** — requires a real terminal.
- `push`/`pull`/`shell` are single-host.
- All hosts share one private key (`-k`); use inline `user:pass@host` for different passwords.
- No sudo password prompt support.
- SCP has no progress indicator.
