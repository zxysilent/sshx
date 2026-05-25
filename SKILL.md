---
name: sshx
description: 'SSH remote command execution, interactive PTY shell, and SCP file transfer on remote hosts. Use when asked to: run commands on remote servers, open interactive SSH sessions, upload or download files over SSH, check service status across a cluster.'
argument-hint: 'Describe what to run and on which hosts'
user-invocable: true
disable-model-invocation: false
---

# sshx

Lightweight SSH tool (4 subcommands).

## Quick reference

| Subcommand | Usage | Multi-`-H` |
|------------|-------|:----------:|
| `exec` | `sshx exec [flags] <command>` | ✅ |
| `shell` | `sshx shell [flags]` | ❌ |
| `push` | `sshx push [flags] <local> <remote>` | ❌ |
| `pull` | `sshx pull [flags] <remote> <local>` | ❌ |

## `-H` format

```
-H [user[:password]@]host[:port]
```

Inline fields **override** global `-u`/`-P`/`-p`.

```
-H 192.168.1.10                     # bare host
-H 192.168.1.10:2222                # custom port
-H root@192.168.1.10                # custom user
-H root:pass@192.168.1.10           # user + password
-H root:pass@192.168.1.10:2222      # all fields
```

## Global flags

| Flag | Default | Overridable per `-H` |
|------|---------|:--------------------:|
| `-u` | `$USER` | ✅ |
| `-P` | `$SSHX_PASSWD` or empty | ✅ |
| `-i` | `~/.ssh/id_rsa` | ❌ |
| `-p` | `22` | ✅ |
| `-t` | `10s` | ❌ |
| `-c` | `1` | exec only; capped at host count |
| `-J` | (none) | ❌ |

Auth: key first → password fallback.

### Jump host (`-J`)

Tunnel through bastion hosts: `-J [user:pass@]host[:port]` (repeatable for chaining).
Applies to all subcommands.

```bash
sshx exec -J 192.168.1.10 -H 192.168.1.20 "hostname"
sshx shell -J root:pass@192.168.1.10 -H 192.168.1.20
sshx exec -J hop1 -J hop2 -H internal.host "uptime"
```

## exec — run commands on remote hosts

Sequential by default. `-c <n>` enables parallel (1–128).

```bash
# single
sshx exec -H 192.168.1.10 "hostname"

# multi, sequential
sshx exec -H h1 -H h2 -H h3 "df -h"

# multi, 4 concurrent
sshx exec -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# per-host passwords
sshx exec -H root:p1@h1 -H root:p2@h2 "whoami"

# complex command
sshx exec -H 192.168.1.10 "sudo systemctl status kubelet | head -20"
```

`-c`: default 1 for <5 hosts; 4–8 for >10 hosts / quick reads; 128 max.

### `-f` — run a local script

```bash
sshx exec -f deploy.sh -H h1 -H h2 -H h3
sshx exec -f script.sh -H h1 -H h2 -c 4
```

Script content is piped via stdin to `bash -s` on each remote host.
Mutually exclusive with inline commands.

## shell — interactive PTY

**Do not invoke in automated scripts.** Single host only.

```bash
sshx shell -H 192.168.1.10
sshx shell -H root@192.168.1.10
```

## push / pull — SCP file transfer

Single file, no directories, single host.

```bash
# upload
sshx push -H 192.168.1.10 ./config.yaml /etc/app/config.yaml

# download
sshx pull -H 192.168.1.10 /var/log/app.log ./app.log
```

## Task patterns

```bash
# run on multiple hosts
sshx exec -H A -H B -H C "<cmd>"

# check service cluster-wide
sshx exec -H h1 -H h2 -H h3 -c 3 "systemctl is-active kubelet"
# run local script cluster-wide
  sshx exec -f deploy.sh -H host1 -H host2 -H host3
# deploy config
sshx push -H host ./local.yaml /etc/app/config.yaml

# grab logs
sshx pull -H host /var/log/app.log ./app.log

# disk usage cluster-wide
sshx exec -H h1 -H h2 -H h3 -c 3 "df -h /"

# reach a host behind a bastion
sshx exec -J bastion -H internal.host "ls /"
```

## Constraints

- **Never invoke `shell` in automated scripts** — requires a real terminal.
- `push`/`pull`/`shell` are single-host.
- All hosts share one private key (`-i`); use inline `user:pass@host` for different passwords.
- No sudo password prompt support.
- SCP has no progress indicator.
