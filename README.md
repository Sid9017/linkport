# Linkport

Linkport 使用 iroh 在已配对的机器之间共享串口，把对方主动共享的串口映射到本机，并提供桌面日志监听和收发。无需第三方账号或独立 VPN 客户端。目标平台为 Windows、macOS、Linux，当前开发版本 **0.0.1**。

桌面版使用真实数据：从本机信任名单读取已配对设备，通过 iroh 验证公钥身份并探测连通性，从操作系统枚举本机串口，并探测对端 Linkport 的实际共享清单。连接后通过 RFC 2217 数据通道建立本地 PTY/com0com 映射，监听和发送均作用于真实会话。独立的 `design` 命令保留演示稿，原生桌面不运行演示逻辑。

## 文档

从 [文档索引](docs/README.md) 开始。完整设计分为 [端与云服务架构](docs/system-architecture.md)、
[交互时序](docs/interaction-sequences.md) 和 [官网内容与展示方案](docs/website-brief.md)。
它们分别说明当前实现的职责与数据边界、关键操作的交互过程，以及后续官网可使用的文案和素材。

## 原生桌面

```sh
cd desktop
go run -tags production .
```

桌面与 CLI 模块统一使用 Go 1.26.11。从仓库根目录执行 `python3 scripts/desktop.py` 可构建本机桌面归档；macOS 同时生成 `dist/desktop/Linkport.app`，可直接打开。关闭窗口默认保留菜单栏图标，菜单可重新打开连接/共享/监听、进入设置、添加设备或退出。

界面采用黑白灰、插座与线缆组成的扁平图形标识。固定侧栏与内容区适配窗口高度，日志独立滚动。右上角齿轮管理文字大小、语言、主题、关闭窗口行为和更新偏好。原生设置保存到本机，浏览器预览使用独立的本地偏好。页头和菜单栏的「添加设备」打开设备配对窗口，无登录步骤。

自动升级采用后台定时检查 GitHub Release、下载并校验、重启时替换的方式。没有内嵌发布公钥的开发包不会自动安装更新。配置签名和发布的完整流程见 [docs/desktop.md](docs/desktop.md)。

两台机器分别运行 Linkport。在其中一台打开「设备与配对」生成邀请，将完整邀请复制到另一台的「另一台设备的邀请」，点击「添加设备」。邀请只可用一次、五分钟后过期；生成新邀请会使旧邀请失效。双方保存对方的设备公钥后，在共享端勾选串口，使用端选择机器/串口和波特率连接。外部工具打开界面中的实际本地映射路径，监听和发送作用于真实会话。

设备身份持久化在用户配置目录 `linkport/devices.json`，包含本机私钥、已配对公钥和网络设置。Unix 文件权限为 `0600`；Windows 继承用户配置目录的访问控制。不要公开或复制此文件给其他机器。移除设备会撤销本机授权并断开该设备的会话；重置本机身份会生成新密钥并清空本机配对名单，其他机器还需移除旧设备并重新配对。详见 [docs/iroh.md](docs/iroh.md)。

默认使用公共 iroh 中继和地址发现，优先直连；公共地址发现只发布中继地址。局域网发现只帮助定位设备，不自动赋予串口访问权限。「网络与密钥」可改为自定义 HTTPS 中继/地址发现服务，或完全禁用公共服务的「仅直连」模式。保存会暂停网络，重启后应用；本机串口监听不受影响。该仓库不创建或部署中继服务器。

「监听」右上角的串口按钮也能直接打开本机串口：选择实际端口和波特率后开始监听，无需共享、网络连接或虚拟串口驱动。会话选择器区分本机与远端；关闭按钮释放当前串口，暂停只冻结显示。本机监听和远端访问共用物理端口占用管理，不能同时控制同一个串口；本机设备拔出会关闭对应会话并保留日志。

本机串口约每秒检查一次，远端设备和共享清单在上次检查完成后约五秒再次刷新。一次探测失败不会主动拆除仍正常传输的会话。取消共享、设备移除或实际通道断开时，关闭受影响会话并保留日志；恢复后需重新连接。实际物理串口、跨机器网络和 Windows 驱动仍需按使用环境联调；自动化验证使用独立的本机 PTY 与隔离的模拟物理端。

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

需要 Go 1.26.11 或兼容工具链。macOS 完整 USB 身份枚举需要 Xcode Command Line Tools/IOKit；禁用 cgo 时可枚举路径，但没有 USB 序列号身份信息。

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
go run ./cmd/linkport serve --local-api
```

`watch` 每秒左右枚举并输出已共享清单的 JSON 快照。运行中的进程同时读取共享配置变化，CLI 的共享/取消共享选择会传播到监听器。

`serve` 运行 iroh 共享端，适用于无桌面的 Linux 等机器。首次配对可以先在共享端运行 `go run ./cmd/linkport invite`，保持进程在线；另一台运行 `go run ./cmd/linkport pair '完整邀请'`。配对结束后退出邀请进程，再运行 `serve`。`identity` 查看本机公钥，`devices` 列出已配对设备，`forget DEVICE_ID` 撤销授权。这些设备管理命令需要独占设备配置；桌面或共享端运行时，请使用桌面的设备管理入口，或先退出占用配置的进程。

网络 API 在经过设备身份认证的 iroh 双向流中运行，不绑定公网 TCP 7700：

| 接口 | 内容 |
| --- | --- |
| `POST /v1/pair` | 仅使用有效单次邀请建立双向配对；公钥必须匹配真实握手身份 |
| `GET /v1/info` | 版本、本机公钥与能力 |
| `GET /v1/ports` | 仅已共享串口的最新快照 |
| `GET /v1/events` | SSE 完整快照，首次连接和状态变化时推送 |
| `POST /v1/connect?id=...&baud=...` | 仅配对设备可升级为 RFC 2217 数据通道，且端口必须显式共享、在线并空闲 |

HTTP 中的设备 ID 或来源地址不能替代 QUIC 握手验证的公钥。探测不跟随重定向或使用系统 HTTP 代理，清单大小与并发有界。未知设备默认拒绝；邀请有效期内允许进行配对握手，但仍无法读取串口清单。`serve --local-api` 单独提供原有 loopback 只读开发接口，不开放真实远端串口通道。

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

GitHub Actions 的手动全量检查和发版流程对 macOS/Linux/Windows 的 amd64、arm64 分别构建 CLI 与 Wails 桌面归档。已有的 `v0.0.1` tag 必须与 `VERSION` 和提交一致，Release workflow 才会汇总归档并创建 **Draft / Pre-release**。工作流不会自动公开发布；发布签名只在配置对应密钥后生成。

PR 和 `main` 推送运行 Windows、Linux、macOS 三个 CI 流程，日常共五个 runner job：三平台各一个原生后端/虚拟串口/桌面模块检查，加 Linux 和 Windows 前端检查。功能分支不再重复触发 push 检查，新提交取消旧的 PR 检查。Unix 使用真实 PTY 和 race 检查；Windows 验证适配器、com0com 解码器并编译驱动测试。双架构、macOS 浏览器检查和安装包生成放到手动全量或发版流程，真实 com0com 驱动只在专用 runner 上手动测试。日常只保存失败报告，完整检查报告保留三天、归档保留七天。覆盖范围和操作方式见 [docs/ci.md](docs/ci.md)。

完整流程见 [docs/releasing.md](docs/releasing.md)，架构与代码树见 [docs/architecture.md](docs/architecture.md)。
