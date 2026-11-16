# Linkport

Linkport 希望通过 Tailscale 发现机器，把对方主动共享的串口映射到本机，并提供桌面日志监听和收发。目标平台为 Windows、macOS、Linux，当前开发版本 **0.0.1**。

桌面版使用真实数据：从已安装的 Tailscale 读取当前可访问设备，从操作系统枚举本机串口，并探测对端 Linkport 的实际共享清单。连接后通过 RFC 2217 数据通道建立本地 PTY/com0com 映射，监听和发送均作用于真实会话。独立的 `design` 命令保留演示稿，原生桌面不运行演示逻辑。

## 原生桌面

```sh
cd desktop
go run -tags production .
```

桌面模块需要 Go 1.26.11，CLI 模块保留 Go 1.23.4。从仓库根目录执行 `python3 scripts/desktop.py` 可构建本机桌面归档；macOS 同时生成 `dist/desktop/Linkport.app`，可直接打开。关闭窗口默认保留菜单栏图标，菜单可重新打开连接/共享/监听、进入设置、登录 Tailscale或退出。

界面采用黑白灰、插座与线缆组成的扁平图形标识。固定侧栏与内容区适配窗口高度，日志独立滚动。右上角齿轮管理文字大小、语言、主题、关闭窗口行为和更新偏好。原生设置保存到本机，浏览器预览使用独立的本地偏好。Tailscale 登录只会在用户选择入口后发起。

自动升级采用后台定时检查 GitHub Release、下载并校验、重启时替换的方式。没有内嵌发布公钥的开发包不会自动安装更新。配置签名和发布的完整流程见 [docs/desktop.md](docs/desktop.md)。

两台机器分别安装并连接 Tailscale，在共享端运行 Linkport，勾选要共享的串口；使用端从「连接」中选择该机器和串口，指定波特率后连接。外部串口工具打开界面中显示的实际本地映射路径。「监听」可选择会话、查看 RX/TX、发送命令或保存完整缓冲日志。未运行 Linkport 的 Tailscale 设备仍会显示，状态为服务不可达；设备清单以本机可见的 Tailscale 网络图为准。

「监听」右上角的串口按钮也能直接打开本机串口：选择实际端口和波特率后开始监听，无需共享、Tailscale 或虚拟串口驱动。会话选择器区分本机与远端；关闭按钮释放当前串口，暂停只冻结显示。本机监听和远端访问共用物理端口占用管理，不能同时控制同一个串口；本机设备拔出会关闭对应会话并保留日志。

本机串口约每秒检查一次，远端设备和共享清单在上次检查完成后约五秒再次刷新。设备离线或取消共享会关闭受影响的数据会话并保留日志；恢复后需重新连接。实际物理串口、跨机器网络和 Windows 驱动仍需按使用环境联调；自动化验证使用独立的本机 PTY 与隔离的模拟物理端。

## 桌面设计预览

```sh
go run ./cmd/linkport design
```

打开 `http://127.0.0.1:7710`。左侧只保留「连接、共享、监听、插件」导航，右侧展示对应内容。连接画布显示本机、对端机器/串口及映射路径，完整日志在监听区。系统自动识别。节点用虚线边框与曲线连接，已连接曲线流动，收发时有方向光点；系统减少动态效果设置会关闭动画。

设计稿可以演示连接、断开、共享选择、日志筛选、暂停、文本/HEX、发送和导出；插拔样例通过设计场景参数提供。设备清单和串口流量为演示数据，不会访问真实串口。字体随包嵌入，SVG 图标为本项目绘制。

可同时连接多个机器/串口，连接区按会话展示独立映射；监听区可切换会话，分别暂停、清空、发送或导出日志。断开一个会话不会影响其他会话。

设计说明见 [DESIGN.md](DESIGN.md)，原始稿位于 `docs/design/desktop-review.fragment.html`。

编辑设计稿时可用 `go run ./cmd/linkport design --source docs/design/desktop-review.fragment.html`，刷新浏览器即可读取修改；默认命令使用内嵌版本。

## 本机清单与共享规则

需要 Go 1.23.4 或兼容工具链。macOS 完整 USB 身份枚举需要 Xcode Command Line Tools/IOKit；禁用 cgo 时可枚举路径，但没有 USB 序列号身份信息。

```sh
go run ./cmd/linkport version
go run ./cmd/linkport ports
go run ./cmd/linkport ports --json
go run ./cmd/linkport share /dev/cu.usbserial-example
go run ./cmd/linkport unshare /dev/cu.usbserial-example
```

替换示例设备路径为 `ports` 输出中的实际路径或身份 ID。Windows 端使用实际 `COMx`。参数放在串口路径前，例如 `share --config /tmp/linkport-shares.json /dev/example`。

所有串口默认未共享，规则保存在 `os.UserConfigDir()/linkport/shares.json`。有 USB 序列号时按 VID/PID/序列号识别重插的设备；无可靠序列号时退回路径，无法保证区分同一路径上的替换硬件。重复 USB 身份会暂停自动匹配，需在本机重新确认。配置文件原子替换，但多个进程同时修改仍采用最后一次写入的结果。

```sh
go run ./cmd/linkport watch
go run ./cmd/linkport serve
go run ./cmd/linkport serve --tailscale
```

`watch` 每秒左右枚举并输出已共享清单的 JSON 快照。运行中的进程同时读取共享配置变化，CLI 的共享/取消共享选择会传播到监听器。

`serve` 提供 loopback 开发接口；`serve --tailscale` 运行真正的共享端服务，适用于无桌面的 Linux 等机器：

| 接口 | 内容 |
| --- | --- |
| `GET /v1/info` | 版本、Tailscale 节点身份与能力；真实共享端 `remote_data_bridge` 为 `true` |
| `GET /v1/ports` | 仅已共享串口的最新快照 |
| `GET /v1/events` | SSE 完整快照，首次连接和状态变化时推送 |
| `POST /v1/connect?id=...&baud=...` | 经鉴权后升级为 RFC 2217 二进制串口通道；只接受当前显式共享且在线的端口 |

真实共享端仅绑定本机 Tailscale IPv4/IPv6 的 TCP 7700，并通过官方 CLI `tailscale whois --json` 验证来源身份和地址。Tailscale 访问策略需要允许目标机器的 TCP 7700。连接目标只能来自实际设备目录，探测不跟随重定向或使用系统 HTTP 代理。远程接口不会泄漏未共享串口，也不支持远程开启共享。默认 `serve` 仍只接受 loopback。

## 虚拟串口

`internal/vport` 的统一接口包括 `Read/Write/Close`、`Path()`、`Events()`、`Capabilities()` 和 `SetModemStatus()`。

| 平台 | 实现 | 限制 |
| --- | --- | --- |
| macOS / Linux | `pty_unix.go` | 纯用户态；可观察终端参数，DTR/RTS 不透传；PTY 参数轮询不能保证快速变化的每一个中间状态 |
| Windows | `com0com_windows.go` | 依赖兼容的 com0com 驱动与预建端口对；可观察配对端参数和控制线；默认接线只能呈现部分 modem 输入 |

Windows 的 `Options.Path` 是应用打开的端口，如 `COM10`；`PairDevice` 是 Linkport 打开的另一端，如 `CNCB0`。构建 Windows arm64 程序不代表已验证 com0com arm64 驱动可用。驱动不随包分发。三平台虚拟串口能力均在首版范围内。

Windows 桌面连接时填写已存在的正确端口对，每条并行连接需要独立的一对。macOS/Linux 自动创建 `~/.linkport/dev/<主机>-<会话 ID>`，断开时移除链接。RFC 2217 实现二进制转义、协商、波特率/数据位/校验/停止位确认及可用的 DTR/RTS、modem 状态；不提供硬件流控或持续 BREAK 控制。内置发送与外部程序写入会在同一会话中串行化。

## 构建、检查与 Release

```sh
go test -race ./...
go vet ./...
python3 scripts/release.py --self-test
python3 scripts/release.py
```

本机构建输出到 `dist/`，包含二进制、版本与说明、字体许可证和 SHA-256 校验文件。macOS 归档目前未签名和公证。

GitHub Actions 对 macOS/Linux/Windows 的 amd64、arm64 分别构建 CLI 与 Wails 桌面归档。已有的 `v0.0.1` tag 必须与 `VERSION` 和提交一致，Release workflow 才会汇总归档并创建 **Draft / Pre-release**。工作流不会自动公开发布；发布签名只在配置对应密钥后生成。

完整流程见 [docs/releasing.md](docs/releasing.md)，架构与代码树见 [docs/architecture.md](docs/architecture.md)。
