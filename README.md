# sshx — 轻量级 SSH 远程执行工具

> English version: [README.en.md](./README.en.md)

## 安装

```bash
# go install（推荐）
go install github.com/zxysilent/sshx@latest

# 从源码编译（带版本信息）
git clone https://github.com/zxysilent/sshx.git
cd sshx
buildSha=$(git rev-parse --short=8 HEAD)
go build -ldflags "-s -w -X 'main.buildSha=${buildSha}' -X 'main.buildTime=$(date +'%Y-%m-%d %H:%M:%S')' -X 'main.version=v0.2.1'" -o sshx .
```

## 快速开始

行为对标原生 `ssh`，同时天然支持多主机并发：

```bash
# 交互式 shell（对齐 ssh）
sshx 192.168.1.10

# 单机执行命令
sshx 192.168.1.10 "ls -la /"

# 多机执行（-H 可重复）
sshx -H host1 -H host2 -H host3 "df -h"

# 多机并发（-c 控制并发度）
sshx -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# 文件传输
sshx push -H 192.168.1.10 ./local.txt /tmp/remote.txt
sshx pull -H 192.168.1.10 /etc/hostname ./hostname.txt
```

## 子命令

| 子命令 | 用途 | 多主机 |
|--------|------|:-----:|
| *(默认)* | 交互 shell 或命令执行 | ✅ |
| `push` | 上传文件 (SCP) | ❌ |
| `pull` | 下载文件 (SCP) | ❌ |

> `exec` 保留为默认模式的别名。

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
| `-H` | 无 | 多主机模式目标（可多次指定） |
| `-p` | `22` | SSH 端口 |
| `-u` | 当前用户 | SSH 用户名 |
| `-P` | `$SSHX_PASSWD` | SSH 密码（支持 `$VAR` 展开） |
| `-i` | `~/.ssh/id_rsa` | 私钥路径 |
| `-t` | `10s` | 连接超时 |
| `-J` | 无 | 跳板机（可多次指定，链式） |
| `-c` | `1` | 最大并发数 (1=串行, 128=最大) |
| `-f` | 无 | 本地 Shell 脚本路径 |
| `-h` | — | 显示帮助 |

`-c` / `-f` 仅在多主机模式 (`-H`) 下生效。

## 使用模式

### 单机模式（对齐 ssh）

```bash
# 交互式 shell
sshx 192.168.1.10
sshx root@192.168.1.10

# 单条命令
sshx 192.168.1.10 "df -h"
sshx -u admin -P secret 192.168.1.10 "hostname"

# 过跳板机
sshx -J bastion 192.168.1.20 "uptime"
```

### 多机模式（`-H`）

```bash
# 串行（默认 -c 1）
sshx -H host1 -H host2 -H host3 "df -h"

# 并发
sshx -H h1 -H h2 -H h3 -H h4 -c 4 "uptime"

# 混合 -H 凭据
sshx -H root:pass1@host1 -H root:pass2@host2 "whoami"

# 本地脚本推送到多机执行
sshx -f deploy.sh -H host1 -H host2 -H host3

# 脚本 + 并发
sshx -f script.sh -H h1 -H h2 -c 4
```

### 文件传输 (push / pull)

```bash
# 上传
sshx push -H 192.168.1.10 ./config.yaml /etc/app/config.yaml

# 下载
sshx pull -H 192.168.1.10 /var/log/app.log ./app.log

# 过跳板机传输
sshx push -J 192.168.1.10 -H 192.168.1.20 ./config.yaml /tmp/config.yaml
```

## 跳板机 (`-J`)

支持多次指定，按顺序逐跳建立隧道：

```bash
# 单跳板
sshx -J root:pass@192.168.1.10 -H 192.168.1.20 "hostname"

# 多跳链
sshx -J hop1 -J hop2 -H target "uptime"

# 文件传输过跳板
sshx push -J 192.168.1.10 -H 192.168.1.20 ./local.txt /tmp/remote.txt
```

## 认证策略

1. **私钥优先**: `-i` 路径或 `~/.ssh/id_rsa`
2. **密码回退**: `-P` 参数 → `$SSHX_PASSWD` 环境变量 → `-H` 行内指定
3. **报错退出**: 全部不可用时报错

避免密码出现在 shell 历史中：

```bash
export SSHX_PASSWD="your-secret"
sshx -H 192.168.1.10 "uptime"
```

也支持 `-P $MY_ENV_VAR` 引用其他环境变量（自动 `$VAR` 展开）。

## 参数混排

标志与位置参数可任意交错：

```bash
# 以下写法全部等价
sshx -H host1 -H host2 "ls -la"
sshx "ls -la" -H host1 -H host2
sshx -c 4 ls -la -H host1 -H host2 /tmp
```