---
name: sshx
description: 'Remote SSH command execution, interactive PTY shell, SCP file transfer, and jump-host access. Use for remote commands, file copy, and cluster checks.'
argument-hint: '[flags] <host| -H hosts...> [command]'
user-invocable: true
disable-model-invocation: false
---

# sshx

`sshx` behaves like `ssh` for one host, adds repeatable `-H` for multi-host execution, and has a `scp` subcommand for one-file upload/download.

## Use

```bash
sshx [flags] <host> [command...]                         # single host; no command opens PTY shell
sshx [flags] -H <host> [-H ...] [-c n] [-f script] <cmd> # multi-host command or script
sshx scp [flags] <source> <target>                       # one-file upload/download
```

Common flags:

- `-H [user[:pass]@]host[:port]`: target host, repeatable for multi-host mode.
- `-u`, `-P`, `-i`, `-p`, `-t`: username, password, identity file, port, timeout.
- `-J [user:pass@]host[:port]`: jump host, repeatable for multi-hop chains.
- `-c`: multi-host concurrency; `-f`: local script piped to `bash -s` on each target.
- `--strict-host-key --known-hosts ~/.ssh/known_hosts`: enable host-key verification. Default is no host-key check for compatibility.

Examples:

```bash
sshx -i ~/.ssh/id_ed25519 admin@192.168.1.10 "df -h"
sshx -H admin@h1 -H admin@h2 -c 2 "uptime"
sshx -J admin@bastion -H admin@internal "hostname"
sshx scp -p 2222 ./config.yaml admin@host:/etc/app/config.yaml
```

## Notes

- Prefer keys or `$SSHX_PASSWD`; inline passwords and literal `-P secret` can leak through history, process args, logs, or transcripts.
- Multi-host mode skips failed connections if at least one host connects; remote failures print `[error]`. Inspect per-host output instead of trusting only the exit code.
- `scp` is single-host, single-file, no directories; remote paths use `[user[:pass]@]host:/path`, with ports supplied by `-p`.
