# 自建轻量中继：架构与运行

当前提供独立的 `cmd/linkport-relay` 服务，技术基线为 Go 1.26.11 和固定社区实现
`github.com/tmc/go-iroh v0.3.0`。本地服务实现与公网部署是两项独立交付；东京公网节点已部署；实测范围和限制见 [部署记录](relay-deployment.md)。

## 服务边界

```text
客户端 A ── HTTPS / WebSocket ── 中继 ── HTTPS / WebSocket ── 客户端 B
              内层是两端之间经过公钥验证的加密 QUIC 数据报
```

`internal/relayservice` 直接包装依赖中的 `relayserver` HTTP handler，没有 fork、复制或
重写中继协议。它接收 `/relay` 的 WebSocket 连接，使用上游公钥认证协议注册连接，并按
目标公钥转发数据报；`/ping` 保留上游的连通延迟探测行为。固定版本支持
`iroh-relay-v1`、`iroh-relay-v2` 协商，这不是已验证所有 Rust iroh 版本互通的声明。

外层增加启动配置、TLS、总连接数限制、HTTP 头超时、健康检查及退出回收。每个 TCP
连接都被跟踪，包括已升级且脱离 HTTP Server 管理的 WebSocket。SIGINT/SIGTERM 到达时，
服务停止接入、取消请求并关闭所有已跟踪连接，再在期限内等待 handler 结束。
这是有界资源回收，不是无损迁移：依赖此中继的数据路径会中断。

中继仅保留运行时连接和路由状态，不保存客户端身份私钥、配对邀请、信任名单、串口清单
或串口明文。中继可观察客户端公钥、来源 IP、时间和流量元数据。默认没有访问日志，
命令启动时输出监听地址，退出时输出汇总计数；不要开启包含邀请或密钥的调试日志。
中继 TLS 证书私钥是服务器自身凭据，与客户端设备身份私钥不同。

该服务不提供 pkarr 地址发现、全局设备目录或业务在线状态。客户端凭邀请中的 ticket、
LAN 发现或另行配置的发现服务定位对端。中继接入认证只证明公钥持有权；Linkport 的
邀请配对、当前信任、显式共享和独占物理端口检查继续在两端执行。双向打洞、路径选择
与升级继续由客户端 iroh 负责。

## 本机启动

从仓库根目录运行：

```sh
go run ./cmd/linkport-relay --listen 127.0.0.1:3340
curl --fail http://127.0.0.1:3340/healthz
curl --fail http://127.0.0.1:3340/ping
```

`/healthz` 返回 `200` 和 `ok`，仅表示本服务能够处理 HTTP 请求；不表示端到端传输、
对端在线、串口可用或公网可达。只接受 GET，未知路径返回 404，不公开连接名单或 metrics。

无 TLS 时，命令仅允许字面量 loopback IP，拒绝 `localhost`、空地址与公网绑定。
可使用 `--listen 127.0.0.1:0` 分配临时端口。Ctrl-C 退出；独立运行建议先构建二进制，
避免把 `go run` 包装进服务管理器。

## 配置与容量

```sh
go build -o /tmp/linkport-relay ./cmd/linkport-relay
/tmp/linkport-relay --help
```

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `--listen` | `127.0.0.1:3340` | TCP 监听地址 |
| `--tls-cert` / `--tls-key` | 空 | PEM 证书链和私钥，必须成对配置 |
| `--max-connections` | 128 | 全部已接受 TCP 连接上限，含未认证、HTTP、TLS 和 WebSocket |
| `--client-bytes-per-second` | 4194304 | 每客户端接收速率；上游允许最多 1 MiB 的初始突发 |
| `--read-header-timeout` | 5s | HTTP 请求头与 TLS 握手等待限制 |
| `--idle-timeout` | 30s | 普通 HTTP keep-alive 空闲限制，不限制已升级中继会话寿命 |
| `--shutdown-timeout` | 10s | 退出时等待 HTTP 和中继 handler 的期限 |

限制和超时必须为正。满额后新 TCP 连接立即关闭，不保证返回 HTTP 503；健康探针也
共享这个连接预算。客户端需要遵循 iroh 自身的重试行为。容量限制不是防 DDoS，也没有
接入白名单、租户配额或全局带宽整形；持有公钥的外部设备可以接入公网中继并消耗预算。
需要私有服务时，应在部署层限制网络来源。

固定上游内部限制包括：认证 30s、单次写入 2s、WebSocket 帧 1 MiB、每客户端排队数据
8 MiB、最多 256 个待认证连接。除客户端接收速率外，这些不是本服务可调参数。
总 TCP 上限同时约束未认证连接数量。128 个客户端的排队额度理论上约 1 GiB，此外还有
帧、连接和运行时开销；请依据机器内存调小连接数，不要把轻量实现误认为固定低内存。

上游队列满或目的端不在线时会丢弃数据报并累计 `datagrams_dropped`，QUIC 负责丢包恢复；
这不会把串口字节静默删掉，但持续拥塞可能导致连接失败。退出汇总还包含
`clients_accepted`、`datagrams_forwarded` 和 `pings`。当前不提供在线 metrics HTTP 接口。

## HTTPS 部署示例

本轮只交付配置，不购买、修改或部署任何公网服务器。服务器与域名确定后再执行部署。

提供 [systemd 示例](../deploy/relay/linkport-relay.service)：

1. 在目标 Linux 主机创建专用的 `linkport-relay` 系统用户和组。
2. 为目标主机构建二进制，安装到 `/usr/local/bin/linkport-relay`。
3. 将有效的域名证书链和私钥放在 `/etc/linkport-relay/`，仅授予服务用户必要读取权限。
4. 安装 unit，检查内存限制、连接数和端口，再启动服务。
5. 在防火墙开放配置的 TCP 端口，以 `https://实际域名:3340` 接入。

示例直接由服务终止 TLS，避免额外代理；它使用非特权端口 3340。证书需要可信 CA 签发，
客户端不能靠跳过证书校验上线。当前证书在启动时读取，续期后需要重启服务，已有中继连接
会断开。客户端数据会话仍需要手动重新连接，不承诺透明恢复。

如果改用反向代理，后端应保持 loopback，代理必须透传 `/relay` WebSocket 升级和
`/ping`，并为长连接配置合理的超时、连接及带宽限制。代理终止 TLS 后后端不具有原始 TLS
导出材料，上游会使用挑战认证；该拓扑已在东京节点验证；具体环境与跨网限制见部署记录。

公共中继配置由维护者签名目录统一分发，用户不能通过 CLI 或桌面填写自定义中继。
可使用 `go run ./cmd/linkport network --relay-mode default` 或 `disabled` 切换维护者中继与仅直连。
TLS 反向代理的实际验证以 [部署记录](relay-deployment.md) 为准，loopback 测试仍保持隔离。

## 本地验证与边界

```sh
go test -race -count=1 -timeout=90s ./internal/relayservice ./cmd/linkport-relay ./internal/p2p
```

测试服务使用临时 loopback 端口与隔离配置目录。`TestPairingAndDataWorkWithRelayOnly`
通过 `WithoutIPTransports` 和 `WithoutNetReport` 禁止直接数据路径与公网网络探测，强制
两个真实 iroh 端点经此包装服务配对。覆盖包含 `0x00`、`0x8e`、`0xff` 的双向二进制回显、
单流关闭后的独立通信、活动流撤销与关闭时连接回收。回显是隔离 HTTP 测试处理器，
不打开用户硬件串口，也不是完整物理串口验收。

服务测试另覆盖健康检查、未知路由、连接满额、慢请求头回收，以及 HTTP/TLS 下已升级
WebSocket 的退出关闭。TLS 使用测试证书并由测试客户端显式信任，不影响生产证书校验。

根模块和桌面模块完整检查遵循 [CI 文档](ci.md)。本次不增加日常 runner job，
新测试由现有根模块测试任务运行。真实双 NAT 公网、官方 Rust 客户端互通、硬件与驱动、
公网容量及证书运维仍需目标环境验证。

## 独立进程集成测试

`internal/p2p/relay_integration_test.go` 提供 `TestStandaloneRelayIntegration`，构建真实的
`linkport-relay` 二进制并在子进程中启动；设备端点运行于测试进程，使用临时配置目录，
禁止直接 IP 传输及公网发现。服务 handler 测试与独立进程测试共用配对/传输场景，
避免两套断言随实现变化而偏离。

```sh
go test -race -count=1 -v -timeout=3m \
  -run '^TestStandaloneRelayIntegration$' ./internal/p2p
```

| 子场景 | 断言 |
| --- | --- |
| `pairing-bytes-stream-isolation-revocation` | 有效邀请期间仍拒绝未配对清单/连接请求；邀请单次使用；两端双向二进制回显；单流关闭不影响其他流；撤销终止活动流并拒绝后续访问 |
| `health-and-http-boundaries` | 健康与 ping 返回 200，非 GET 返回 405，未知路径和未公开的 metrics 返回 404 |
| `shutdown-closes-online-endpoints` | 两个已认证端点建立真实 QUIC 流；服务在流仍打开时收到 SIGTERM，退出码为 0、连接数归零、监听端口关闭 |
| `connection-cap-and-timeout-recovery` | 未认证 WebSocket 占满连接预算时拒绝新连接；关闭后容量恢复；不完整 HTTP 请求头超时关闭后仍可处理健康请求 |
| `unsafe-public-plaintext-rejected` | 未配置 TLS 时拒绝绑定公网地址，进程非零退出 |

子进程使用普通 `go build`，`-race` 检查覆盖测试进程中的端点和断言；服务包装层另由
`internal/relayservice` 的进程内 race 测试覆盖。没有将普通子进程构建描述为 race 构建。
进程信号用例只在 Unix 执行；Windows 明确跳过 SIGTERM 子场景，其他场景在结束时强制
回收测试子进程，handler 层的取消/连接回收仍由跨平台测试覆盖。端点可能等待 QUIC
自身超时后才判定路径失效，本套件不承诺服务退出后串口会话立即报错或自动恢复。

该套件随现有根模块测试运行，不添加 CI runner job，不访问用户串口或持久化身份。

## Desktop 内嵌与协同发现

`internal/relayservice.Server` 和 `relayservice.Host` 是可复用模块，包含 TLS 校验、监听器所有权、
连接上限和退出回收。独立运行和维护者程序嵌入复用相同协议实现。
普通 desktop 用户不再拥有公网宿主配置入口，生产网络拒绝该配置接口；隔离测试保留本机宿主用例。
对外中继公告不自动修改其他设备的候选目录。这不是 NAT 后 desktop 多跳转发协议。

### 已配对设备之间的信息同步

`internal/p2p/discovery.go` 通过已有的公钥认证 iroh 通道提供 `POST /v1/discovery`：

- 请求携带发送设备自己的签名联系方式，必须与实际握手公钥一致。
- 响应携带服务端自己的记录；可选查询一个明确设备 ID，不提供信任名单或清单枚举。
- 查询方仅接受本机已经配对设备的记录；记录同步不能配对、共享串口或恢复已撤销授权。
- 记录包含版本、设备 ID、持久化单调序号、有效期、endpoint ticket、可选中继公告和签名。
  签名使用设备密钥及独立域分隔，ticket 中的公钥必须匹配记录主体。
- 默认有效期一小时，在剩余半小时或联系方式变化时更新。拒绝过期、异常未来有效期、
  旧序号和同序号不同内容。过期缓存保留版本下限，但不能用来覆盖新的有效记录。
- 缓存与本机序号保存在受原有进程锁及原子替换保护的 `devices.json`，重启后继续使用。
  文件加载时验证签名和授权范围；移除设备清理其缓存，身份重置清空缓存和本机序号。
- 后台同步最多四路并发，每轮八秒总预算，平时每三十秒同步，并在配对和宿主状态变化后唤醒。
  轮换起点，避免前面的离线设备让后面的在线设备长期得不到同步。
- 拨号先复用连接和本地有效地址；失败后，在五秒总预算内向其他已配对设备并发查询目标，
  然后再使用现有地址解析兜底。查询请求绕过递归发现，避免互相查询形成调用环。

这些记录只在已认证的配对通道内交换，可包含配对双方已有权限看到的联系方式；没有新增
公网设备目录，既有公共 pkarr 发布过滤不变。记录仅证明签发者给出的联系提示，不证明当前
在线或串口可用；实际串口清单仍经原有已配对业务探测获取。

本轮先实现一层辅助查询：例如 A 与 C 已配对但地址过期，A 可询问也持有 C 记录的 B。
没有实现全图广播、公共 DHT、无缓存的纯离线引导、发现记录的任意多跳查询，或 desktop
之间替第三方转发串口数据。公网入口故障后是否仍可冷启动，取决于现存地址与网络可达性。

### 多中继恢复

维护者目录包含两个以上 relay 时，后台检测 home relay 失联：持续约两秒后暂时移除该候选，
让现有 endpoint 选择另一个配置的 relay，三十秒后重新允许失败候选。拨号时过滤冷却中的
地址，并尝试签名目录中的其他中继，避免旧 ticket 继续只指向死中继。处理不加入目录之外的
relay，不改设备身份或配对关系，也不重建 endpoint。

这是连通性恢复机制，不是串口写入重放或持久化消息队列。已有 QUIC 会话是否存活取决于
路径建立与超时；会话真正结束后仍需手动重新连接，不保证无感切换。

新增 `TestRelayFailureRecoversWithoutEndpointRestart` 使用两个真实 loopback 中继、禁止
直接 IP 数据传输，关闭实际承载流量的中继，再验证备用中继承载新的精确二进制请求，
以及身份、配对和撤销语义。`TestPeerAssistedDiscoveryAfterAddressChangeAndRestart` 验证
三设备辅助发现、换址及缓存重启；另有篡改、过期、回滚、未配对访问和请求大小测试。
Desktop 的 token 接口测试及 Chromium fixture 覆盖维护者中继策略和更新入口。所有这些都是隔离本机验证，
不能代替真实 NAT、公网线路、USB 硬件或跨版本互通验收。

## 当前桌面策略与目录更新（2026-11-17）

此前的本机中继配置界面已移除。普通用户不能指定中继、pkarr URL 或公网监听配置；
`relayservice.Host` 仍是可嵌入的复用模块，独立部署由维护者操作。
默认候选来自内嵌或缓存的签名目录，使用独立 Ed25519 公钥验证，拒绝新配置的过期、降版本与同版本冲突。
已有签名配置在临时离线时保留，设备签名公告不授予添加任意中继候选的权限。
新目录先加入候选、原子保存，再移除过时地址；不重建 endpoint，不重放串口数据。

桌面每六小时检查应用和中继目录，启动后首次延迟三十秒。自动更新关闭仍显示可用更新，
开启时自动应用已验证目录；应用包暂存后仍需用户重启。目录维护者需要在有效期内续签并发布新版本，
服务端不保存目录签名私钥。新目录不会重新启用用户选择的仅直连模式。
