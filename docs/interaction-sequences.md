# Linkport：交互时序

实现快照：**2026-11-16，0.0.1 开发预览**。以下按当前工作区代码整理，
不把未来官网、自建云服务或尚未实现的自动重连画成现有功能。

整体职责见 [端与云服务架构](system-architecture.md)。除特别说明外，
**A 为邀请方或物理串口提供方，B 为导入邀请或使用串口的一方**；两端可以交换角色。

## 1. 首次启动与身份恢复

```mermaid
sequenceDiagram
    actor User as 本机用户
    participant Host as 桌面宿主或 CLI
    participant Store as 设备身份与信任存储
    participant Net as iroh 端点
    participant Aux as 所选辅助服务
    participant Engine as 本机运行时
    User->>Host: 启动 Linkport
    Note over Host: 桌面先处理单实例，再取得配置锁
    Host->>Store: 独占打开 devices.json
    alt 首次使用且文件不存在
        Store->>Store: 生成独立密钥，创建空信任名单
        Store->>Store: 同步并原子保存
    else 已有有效身份
        Store->>Store: 校验并读取原身份与配置
    else 配置损坏或被其他进程占用
        Store-->>Host: 报错，保留原文件
        Note over Host: 不静默覆盖身份
    end
    Store-->>Host: 本机身份与网络设置
    Host->>Net: 用已有密钥启动端点和协议监听
    opt 按配置启用发现或中继
        Net->>Aux: 建立联络、发布或解析联系方式
    end
    Host->>Engine: 启动串口枚举与已配对设备探测
    Engine-->>Host: 本机状态、设备和会话
    Host-->>User: 展示界面或运行共享端
```

网络服务不是身份签发者。辅助服务不可达与本机身份不存在是两种不同状态；
`Network.state = Running` 也不证明对端服务或串口可用。
`Disabled` 是开发接口/隔离测试等不启动网络端点时的状态，
与“端点已运行、仅禁用中继”的 `disabled` 中继模式不同。

源码：[store.go](../internal/p2p/store.go)、[network.go](../internal/p2p/network.go)、
[main.go](../desktop/main.go)、[engine.go](../internal/engine/engine.go)。

## 2. 生成邀请与双向配对

```mermaid
sequenceDiagram
    actor UA as A 的用户
    participant A as A 的 Linkport
    actor UB as B 的用户
    participant B as B 的 Linkport
    participant SA as A 的本机存储
    participant SB as B 的本机存储
    UA->>A: 生成配对邀请
    A->>A: 生成随机令牌，保存摘要与八小时期限
    A-->>UA: 公钥、ticket、令牌和期限组成的邀请
    UA-->>UB: 通过可信渠道传递完整邀请
    UB->>B: 粘贴邀请并添加设备
    B->>B: 校验格式、期限和目标公钥
    B->>A: 建立公钥绑定的 iroh 加密连接
    Note over A,B: 每个新流先完成随机挑战，再处理 HTTP
    B->>A: POST /v1/pair，提交令牌与 B 的设备信息
    A->>A: 检查令牌及期限，核对 B 信息与握手公钥
    A->>SA: 保存对 B 的信任
    A->>A: 消费单次邀请
    A-->>B: 返回 A 的设备信息
    B->>B: 核对响应公钥仍为邀请中的 A
    B->>SB: 保存对 A 的信任
    B-->>UB: 配对成功
```

配对过程通过端到端加密连接执行，邀请本身还需要用户通过可信渠道传递。生成邀请是 A 的
明确授权动作，导入邀请是 B 的明确确认动作；当前没有额外的远程审批弹窗或云端批准流程。

失败与恢复：

- 邀请过期、被替换、已使用、令牌被篡改，或提交公钥不匹配握手身份时拒绝。
- 邀请保存在内存，重启后失效。普通 endpoint ticket 不等于配对授权。
- 移除设备同时撤销当前邀请及该设备尚未完成的配对；即使随后重新发起配对，
  旧请求也不能借用新请求的状态恢复信任。重新添加需要新邀请和明确操作。
- 当前双方保存并不是跨机器的原子事务：A 保存成功后，如果响应丢失或 B 保存失败，
  可能只有 A 记住 B。重新生成邀请并配对可恢复；必要时先在 A 移除未完成的设备。
- 配对成功只建立设备信任，默认仍不共享任何新串口。

本机入口：`POST /native/v1/devices/invite`、`POST /native/v1/devices`。
对端入口：`POST /v1/pair`。源码：`CreateInvitation`、`AddDevice`、`acceptPair`。

## 3. 定位设备、复用连接与直连/中继选择

```mermaid
sequenceDiagram
    participant App as 发起端 Linkport
    participant Local as 本机 p2p 与 iroh
    participant Lookup as ticket 或地址发现
    participant Relay as 可用的中继
    participant Peer as 对端 iroh
    App->>Local: 联系已配对公钥
    alt 已有仍可用的连接
        Local->>Peer: 在现有连接上打开独立双向流
    else 需要新连接
        Local->>Lookup: 读取已保存 ticket
        Local->>Peer: 按已知地址尝试连接
        opt 缓存尝试失败且请求未超时
            Local->>Lookup: 按稳定公钥重新查询联系方式
            Lookup-->>Local: LAN、已配对协同发现或固定公共发现结果
        end
        opt 启用中继且连接尝试需要它
            Local->>Relay: 联络对端、交换连接所需信息
            Relay->>Peer: 转交联络信息或加密握手报文
        end
        Note over Local,Peer: iroh 在两端执行连通探测与路径选择
        alt 成功建立可用直连
            Local<<->>Peer: 直接传输加密数据
        else 只能使用中继路径
            Local<<->>Relay: 加密数据
            Relay<<->>Peer: 加密数据
        else 没有可用路径
            Local-->>App: 连接失败或超时
        end
    end
    Note over Local,Peer: 成功后双向都可发起流，每条流单独校验
```

这是连接职责图，不是固定的逐包握手实现。不能把它理解成“每次都必须先绕中继”，也不能
把“先发起业务连接的一端”理解成唯一发送打洞探测包的一端。

当前 `p2p.dial` 的缓存地址尝试有三秒上限，失败后才进行按 ID 的重新解析；调用方还有
总超时。辅助服务按网络模式启用，`disabled` 模式不会访问公共中继或公共地址发现。

中继运行状态不等于设备业务在线状态。已经存在的连接还需在每次业务请求时检查当前信任名单。

## 4. 显式共享与远端清单刷新

```mermaid
sequenceDiagram
    actor User as A 的用户
    participant UIA as A 的界面
    participant Share as A 的共享管理器
    participant API as A 的远端只读 API
    participant Probe as B 的设备探测
    participant UIB as B 的界面
    User->>UIA: 勾选共享某个实际串口
    UIA->>Share: PUT /native/v1/share
    Share->>Share: 刷新枚举、校验身份、持久化选择
    Share-->>API: 已共享端口快照更新
    loop 每轮探测结束后约五秒，或手动刷新
        Probe->>API: GET /v1/info，验证版本与设备 ID
        Probe->>API: GET /v1/ports
        API->>API: 验证公钥信任，仅返回已共享项
        API-->>Probe: 已共享清单及设备能力
        Probe-->>UIB: 通过本机状态 API 更新设备视图
    end
```

本机枚举约每秒一次，桌面界面约每秒读取本机运行时状态。远端设备/清单探测不是固定五秒
内完成的实时推送：每一轮会花费自身查询时间，结束后再等待约五秒。

远端 API 提供 `/v1/events` SSE，但当前桌面设备目录主要使用周期性 GET 探测；
串口桥接撤销订阅的是共享端进程内的共享快照。不能把两者混成“桌面已通过云推送实时同步”。

探测失败把设备标记为不可达，不会只因一次探测失败就拆掉仍正常传输的串口会话。

## 5. 建立远端串口会话

```mermaid
sequenceDiagram
    actor User as B 的用户
    participant UI as B 的界面
    participant Session as B 的引擎与会话管理器
    participant P2P as 已验证的 iroh 连接
    participant Bridge as A 的串口桥接
    participant UART as A 的物理串口
    participant VPort as B 的 PTY 或 com0com
    User->>UI: 选择机器、串口、波特率并连接
    UI->>Session: POST /native/v1/connect
    Session->>Session: 检查已配对目录、能力、端口状态和参数
    Session->>P2P: 打开独立流并完成新鲜挑战
    P2P->>Bridge: POST /v1/connect，申请协议升级
    Bridge->>Bridge: 再查公钥信任、显式共享、身份及占用
    Bridge->>UART: 独占打开物理串口
    alt 端口不可用或被占用
        Bridge-->>Session: 拒绝连接
        Session-->>UI: 显示原因
    else 物理端口已打开
        Bridge-->>Session: HTTP 101，升级为 RFC 2217 通道
        Session->>VPort: 创建 PTY 映射或打开用户指定端口对
        Session<<->>Bridge: 协商二进制与串口控制能力
        Session-->>UI: 返回会话、映射路径和实际能力
        UI-->>User: 可以用本机串口工具打开映射
    end
```

物理端口在远端协议升级前打开，本机虚拟端口在升级成功后建立。后续步骤失败时关闭该流，
释放已经取得的资源；不能把“点击连接”或“HTTP 101”单独当成整个串口会话已经可用。

本机直接监听和远端访问共享同一个物理端口 broker，因此不会同时控制同一物理串口。
Windows 每个并行映射需要独立、已存在的 com0com 端口对。会话身份由对端公钥和端口身份共同确定。

## 6. 串口收发、观察与导出

```mermaid
sequenceDiagram
    participant Tool as B 的外部串口工具
    participant Session as B 的会话与写入仲裁
    participant Monitor as B 的内置监听界面
    participant Bridge as A 的 RFC 2217 桥接
    participant UART as A 的物理串口
    Tool->>Session: 写入本机虚拟串口
    Session->>Session: 串行化写入，先确认待应用的参数
    Session->>Bridge: 发送字节流
    Bridge->>UART: 写物理串口
    Session-->>Monitor: 记录本次 TX
    UART->>Bridge: 实际收到设备数据
    Bridge->>Session: 返回字节流
    Session-->>Tool: 写回本机虚拟串口
    Session-->>Monitor: 记录 RX，随本机状态刷新展示
    opt 用户从内置界面发送
        Monitor->>Session: 发送文本或 HEX
        Note over Session: 与外部工具共用写锁及参数确认路径
        Session->>Bridge: 发送真实数据
    end
    opt 用户导出
        Monitor->>Session: 导出该会话全部留存记录
        Session-->>Monitor: 时间戳、HEX 与转义文本
        Note over Monitor: 经本机保存对话框写文件
    end
```

日志是发起映射会话一端的 RX/TX 旁路记录，不会为了观察而第二次打开物理串口。
共享端当前不额外提供一个“所有入站会话的持久化审计日志”或云端日志中心。

暂停只冻结显示；底层传输、采集和有界日志仍继续。每会话留存最多 500 条 / 128 KiB，
导出只包含当时仍留存的记录，且不受界面筛选影响，不代表保留整个会话的无限历史。
虚拟串口消费者持续不读取而导致队列满时，会报告错误并关闭会话。

## 7. 本机直接监听

```mermaid
sequenceDiagram
    actor User as 本机用户
    participant UI as 监听界面
    participant Engine as 本机引擎
    participant Broker as 物理端口 broker
    participant UART as 本机串口
    User->>UI: 选择本机串口和波特率
    UI->>Engine: POST /native/v1/monitor/local
    Engine->>Engine: 核对当前枚举的身份与路径
    Engine->>Broker: 请求独占打开
    Broker->>UART: 打开串口
    UART-->>Engine: 实际 RX
    Engine-->>UI: 本机会话日志
    UI->>Engine: 发送数据
    Engine->>UART: 实际 TX
    User->>UI: 关闭当前串口
    UI->>Engine: 断开本机会话
    Engine->>Broker: 关闭句柄并释放占用
```

此路径不需要设备配对、共享选择或虚拟串口映射。网络暂停不会主动关闭本机监听；
物理设备移除则会关闭对应本机会话并保留记录。

## 8. 取消共享或拔掉设备

```mermaid
sequenceDiagram
    actor User as A 的用户或物理设备
    participant Share as A 的共享管理器
    participant Bridge as A 的活动桥接
    participant UART as A 的物理串口
    participant Session as B 的会话
    participant UI as B 的界面
    User->>Share: 取消共享，或枚举检测到端口移除
    Share->>Share: 更新共享快照
    Share-->>Bridge: 进程内订阅收到状态变化
    Bridge->>UART: 关闭并释放端口
    Bridge-->>Session: 关闭该串口数据流
    Session->>Session: 关闭映射，记录断开原因
    Session-->>UI: 会话已断开，日志保留
    Note over Bridge,Session: 其他端口会话及设备配对不受此操作主动撤销
```

这属于端口资格变化，与移除整台已配对设备不同。设备恢复、重新共享或再次可达后，
当前版本仍需用户重新建立串口会话。

## 9. 移除已配对设备

```mermaid
sequenceDiagram
    actor User as A 的用户
    participant UI as A 的设备管理
    participant Net as A 的 p2p
    participant Store as A 的信任名单
    participant B as B 的 Linkport
    User->>UI: 移除并断开 B
    UI->>Net: DELETE /native/v1/devices
    Net->>Store: 持久化删除 B 的公钥
    Net->>B: 关闭与 B 的连接和全部流
    Note over Net,B: 相关串口会话释放端口与映射，保留本地日志
    B->>Net: 后续请求或尝试重连
    Net-->>B: 拒绝未获授权的业务访问
    Note over B: 本机列表可能仍保留 A，需由 B 自己清理
```

撤销是本机授权变化，不依赖云端在线或中心账号同步。若想以后重新信任，需要新的有效邀请。

## 10. 网络变更与身份重置

```mermaid
sequenceDiagram
    actor User as 本机用户
    participant UI as 设备管理界面
    participant Store as 本机配置
    participant Net as 本机网络
    participant Sessions as 会话管理
    User->>UI: 保存网络设置，或确认重置身份
    alt 网络配置变更
        UI->>Store: 校验并保存中继及发现设置
        Note over Store: 保留原身份与配对名单
    else 重置设备身份
        UI->>Store: 用完整当前设备 ID 确认
        Store->>Store: 生成新密钥，清空本机配对
    end
    UI->>Net: 使邀请失效并暂停旧传输
    Net-->>Sessions: 远端连接关闭
    Note over Sessions: 本机直接监听继续运行
    UI-->>User: 提示重启
    User->>Net: 重启 Linkport
    Net->>Store: 加载新的身份或网络配置
    Note over User,Store: 身份重置后还需在其他机器移除旧设备并重新配对
```

配置保存失败时不当成变更成功。身份重置不会通知所有对端删除旧公钥，也不能撤销被盗
旧私钥在其他机器上的已有授权。

## 11. 签名更新与官网的关系

```mermaid
sequenceDiagram
    actor User as 用户
    participant Site as 未来官网
    participant Host as Linkport 更新器
    participant Release as Release 分发服务
    opt 用户浏览项目
        User->>Site: 阅读介绍、原理和文档
        Site-->>User: 版本说明与真实下载入口
    end
    opt 已配置发布公钥
        Host->>Release: 查询版本及匹配平台的安装包
        Release-->>Host: 安装包、SHA-256 和签名
        Host->>Host: 校验并暂存
        Host-->>User: 更新就绪
        User->>Host: 选择重启并更新
        Host->>Host: 更新 helper 替换并重启
    end
    Note over Site,Release: 不处理设备配对、串口权限或会话数据
```

官网尚未上线。开发构建已内嵌发布公钥；维护者已授权并完成 GitHub Actions 发布签名密钥配置。下载包的签名密钥和 iroh
设备身份密钥属于两套独立信任关系，不能混用或把设备密钥交给官网。

## 12. API 边界索引

### 本机桌面管理：均要求 `X-Linkport-Token`

| 接口 | 用途 |
| --- | --- |
| `GET /native/v1/state`、`POST /native/v1/refresh` | 获取本机状态、请求刷新 |
| `GET /native/v1/network`、`PUT /native/v1/network` | 网络状态、配置变更与暂停 |
| `GET /native/v1/devices` | 已配对设备 |
| `POST /native/v1/devices/invite` | 生成单次邀请 |
| `POST /native/v1/devices`、`DELETE /native/v1/devices` | 导入邀请、移除设备 |
| `POST /native/v1/identity/reset` | 确认后重置本机身份 |
| `PUT /native/v1/share` | 本机改变共享选择 |
| `POST /native/v1/connect` | 建立远端串口映射 |
| `POST /native/v1/monitor/local` | 直接监听本机串口 |
| `POST /native/v1/send`、`disconnect`、`clear`、`export` | 操作本机会话及日志，后三者使用相同前缀 |
| `GET/PUT /native/v1/preferences` | 本机偏好 |
| `GET /native/v1/update`、`POST /native/v1/update/{check,download,restart}` | 本机更新流程 |

### 对端接口：在经过验证和随机挑战的 iroh 流内

| 接口 | 访问条件 |
| --- | --- |
| `POST /v1/pair` | 有效邀请、实际握手身份与提交信息匹配 |
| `GET /v1/info`、`GET /v1/ports`、`GET /v1/events` | 已配对公钥；清单只包含显式共享项 |
| `POST /v1/connect` | 已配对，端口共享、身份明确、可独占打开，参数合法 |

`serve --local-api` 是单独的 loopback 只读开发接口，不等于上述真实 iroh 共享服务。
它不提供设备管理或真实远端串口数据通道。

## 13. 当前固定中继策略

当前固定使用内置签名目录中的东京中继 `207.148.110.58:443`。启动时仍校验维护者
Ed25519 签名，但不读取旧目录缓存，不查询远程目录，也不接受运行时目录替换。
节点增减通过后续应用版本交付。设置显示“随应用更新”，不提供单独检查或应用中继配置的按钮。
应用版本检查仍在启动三十秒后和每分钟执行，和中继连通性分开。
仅直连模式继续禁止公共中继与公共地址发现。所有保留的 `/native/v1/relay-update`
与 `check/apply` 接口仍要求本次启动 token；检查不发网络请求，应用接口拒绝运行时替换。
