# sshx — 轻量级 SSH 远程执行工具

> English version: [README.en.md](./README.en.md)

## 安装

```bash
# go install（推荐）
go install github.com/zxysilent/sshx@latest

# 从源码编译
git clone https://github.com/zxysilent/sshx.git
cd sshx
go build -o sshx .
```

## 子命令速览

| 子命令  | 用途 | 多主机 |
|--------|------|:-----:|
| `exec` | 远程执行命令或本地脚本 | ✅ |
| `shell`| 交互式 PTY 终端 | ❌ |
| `push` | 上传文件 (SCP) | ❌ |
| `pull` | 下载文件 (SCP) | ❌ |

## `-H` 格式

```
-H [用户名[:密码]@]主机[:端口]
```

行内凭据覆盖全局 `-u`/`-P`/`-p`。省略字段回退到全局值。

```bash
-H 192.168.1.10                  # 裸主机
-H root@192.168.1.10             # 指定用户
-H root:pass@192.168.1.10        # 用户 + 密码
-H root:pass@192.168.1.10:2222   # 全部指定
```

## 全局参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-H` | **必填** | 目标主机（exec 可多次指定） |
| `-p` | `22` | SSH 端口 |
| `-u` | 当前用户 | SSH 用户名 |
| `-P` | `$SSHX_PASSWD` | SSH 密码 |
| `-i` | `~/.ssh/id_rsa` | 私钥路径 |
| `-t` | `10s` | 连接超时 |
| `-J` | 无 | 跳板机（可多次指定） |

### exec 专属

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-c` | `1` | 最大并发数 (1=串行, 128=最大) |
| `-f` | 无 | 本地 Shell 脚本路径 |

## 认证策略

1. **私钥优先**: `-i` 路径或 `~/.ssh/id_rsa`
2. **密码回退**: `-P` 参数 → `$SSHX_PASSWD` 环境变量 → `-H` 行内指定
3. **报错退出**: 全部不可用时报错

## 跳板机 (`-J`)

支持多次指定，按顺序逐跳建立隧道。所有子命令均支持。

```bash
# 单跳板
sshx exec -J root:pass@192.168.1.10 -H 192.168.1.20 "hostname"

# 多跳链
sshx exec -J hop1 -J hop2 -H target "uptime"

# 文件传输过跳板
sshx push -J 192.168.1.10 -H 192.168.1.20 ./local.txt /tmp/remote.txt
```

## 密码环境变量

避免密码出现在 shell 历史和进程列表中，使用 `SSHX_PASSWD`:

```bash
export SSHX_PASSWD="your-secret"
sshx exec -H 192.168.1.10 "uptime"
```

也支持 `-P $MY_ENV_VAR` 引用其他环境变量（自动 `$VAR` 展开）。

## 参数混排

通过 `pflag` 支持标志与命令参数任意位置交错，不需要把所有 `-H` 放在前面：

```bash
# 以下写法全部等价
sshx exec -H host1 -H host2 "ls -la"
sshx exec "ls -la" -H host1 -H host2
sshx exec -c 4 ls -la -H host1 -H host2 /tmp
```