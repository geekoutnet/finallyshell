# 在真实机器上测试

[English](../en/testing.md) · 简体中文

## 用途

凡是需要真实 Linux 服务器的 Finallyshell 自动化测试或手工操作，都使用这个可随时重建的 Docker 测试箱。它运行真实服务，适合验证 SSH、文件传输、systemd、sudo、防火墙、Caddy 和 Docker 等功能。单元测试和进程内测试不需要启动它。智能体还应阅读[测试箱技能](../../.claude/skills/testbox/SKILL.md)。

## 环境要求

- 本机需要能运行特权容器、支持 cgroup 的 Linux Docker 引擎。Windows 和 macOS 可用 Docker Desktop，Linux 可用 Docker Engine。
- 使用 Bash；Windows 上用 Git Bash。首次生成凭据需要 Python 3，运行 Go 测试则需要 `go.mod` 中指定的工具链。
- 本机的 `2222`、`8080` 和 `8443` 端口必须空闲，或已由测试箱占用。确认 `docker info` 能正常返回。在 Windows 上，如果 Docker Desktop 安装在默认位置，`up` 可以在需要时启动它。

## 快速开始

在仓库根目录用 Bash 运行以下命令。如果要沿用协调者在 Windows 上已经搭好的测试箱，先把 `TESTBOX_ENV` 指向 `C:/Users/Administrator/codex-briefs/testbox/testbox.env`。

```bash
(
    set -e
    set +x
    box=.claude/skills/testbox/testbox.sh
    "$box" up
    "$box" status
    ssh_export=$("$box" ssh-env tester)
    eval "$ssh_export"
    unset ssh_export
    FINALLYSHELL_TEST_MUTATE=1 go test ./internal/app -run '^TestSSH$' -count=1 -v
)
```

`up` 创建或启动容器，并应用所选文件里的凭据。如果文件不存在，会在仓库外创建，默认路径为 `$HOME/.finallyshell-testbox.env`。`ssh-env` 会把密码写到标准输出，请像示例那样接收，不要单独运行或开启命令跟踪。

## 测试箱里有什么

- `fs-testbox` 使用 Debian 12 镜像 `finallyshell-testbox:debian12`，由 systemd 担任一号进程，并运行 OpenSSH。`root` 可用密码登录，`tester` 属于 `sudo` 组，提权时仍须输入自己的密码。
- 镜像内置网络和进程工具、Python、UFW、cron、rsyslog、logrotate、归档工具、SQLite、jq 和 Git。Nginx 和 Fail2ban 已安装但不默认启动；getty 和 logind 已屏蔽。
- 宿主机只在回环地址上开放端口：`127.0.0.1:2222` 对应 SSH，`127.0.0.1:8080` 对应 HTTP，`127.0.0.1:8443` 对应 HTTPS。容器以特权模式运行，支持 systemd 和嵌套 Docker。
- 现有测试箱已经运行 Caddy 2.11.9。新建测试箱后，可用 `.claude/skills/testbox/testbox.sh caddy` 从官方 Cloudsmith 稳定版软件源安装并启用 Caddy，同时在 `/etc/caddy` 写入站点示例，覆盖静态文件、配置导入、内部 TLS、反向代理、单页应用回退、基本认证和重定向。

## 安全规则

- 不要把真实服务器或生产服务器用作联机测试目标，也不要在上面手工验证会修改服务器的流程。处理用户反馈时，只在获得授权后收集必要的只读信息，再到测试箱里用脱敏样例复现。
- 凭据文件包含 `ROOT_PW`、`TESTER_PW` 和 `SSH`。Unix 下将权限设为 `0600`，Windows 下限制访问控制权限。不要提交或记录密码，不要把凭据文件复制进容器，也不要在合并请求中公开。
- 对外映射的端口只绑定回环地址。不要把特权容器当作运行不可信代码的安全沙箱，也不要挂载宿主机数据或 Docker 套接字。

## 联机测试与写入开关

设置 `FINALLYSHELL_TEST_SSH` 可启用联机测试，格式是 `user:password@127.0.0.1:2222`。普通 SSH 流程用 `ssh-env tester`，需要管理员权限时用 `ssh-env root`。不设置变量时，只能联机运行的用例会跳过，使用进程内测试服务的用例仍可运行。

`internal/app` 的 `TestSSH` 会验证终端、监控和文件传输。在包含 `TestCaddyLive*` 的分支上，先安装 Caddy 示例并载入 `root` 凭据，再运行 `go test ./internal/app -run '^TestCaddyLive' -count=1 -v`。需要修改服务器的用例，在命令前加上 `FINALLYSHELL_TEST_MUTATE=1`。部分较早的 SSH 用例不会检查这个开关，也会写文件或启动进程，所以别把未设开关当成只读保证。如果隧道用例要求客户端和服务端共享回环网络，不要设置 `FINALLYSHELL_TEST_SSH`，改用进程内测试服务。

要联机验证桌面模式的文件、此电脑、回收站和终端，请在 Bash 中对已经运行的测试箱执行以下命令。两个账号各运行一次；`tester` 用例会验证 sudo 提示。压缩和互操作用例使用 `zip`、`unzip` 和 `trash-cli`，远程下载用例使用 Caddy 示例站点。将 `SHOT_DIR` 设置为已存在的本地目录即可保存截图。如果凭据不在默认位置，请先设置 `TESTBOX_ENV`。

```bash
for account in root tester; do
    (
        set -e
        set +x
        ssh_export=$(.claude/skills/testbox/testbox.sh ssh-env "$account")
        eval "$ssh_export"
        unset ssh_export
        FINALLYSHELL_TEST_MUTATE=1 go test ./internal/app -run '^TestDesktopLive(Files|Trash|Terminal)' -count=1 -v -timeout 900s
    ) || exit 1
done
```

## 添加组件

额外组件装在可随时重建的容器里，不要装到宿主机上：

```bash
.claude/skills/testbox/testbox.sh exec apt-get update
.claude/skills/testbox/testbox.sh exec apt-get install -y docker.io
.claude/skills/testbox/testbox.sh exec systemctl enable --now docker
```

Docker 测试使用测试箱自己的守护进程。`caddy` 会覆盖示例配置和网页；如果要保留自定义测试设置，先创建快照。

## 重置与快照

- `down` 停止测试箱但保留文件，之后用 `up` 再次启动。
- `reset` 删除容器并用干净的基础镜像重建。仓库外的凭据文件会保留，后来装的组件（包括 Caddy）会被清除。
- `snapshot NAME` 将容器的可写文件系统保存为本地镜像 `finallyshell-testbox:NAME`。`restore NAME` 删除当前测试箱并从快照重建。快照不保存运行中的进程、临时文件系统或挂载卷里的内容。镜像可能含密码哈希和本地 TLS 密钥，不要推送或分享。
