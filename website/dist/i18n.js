(() => {
  const messages = {
  "Linkport 把远端共享串口映射到本机，继续使用熟悉的调试工具。无需账号，无需另装 VPN。支持 macOS、Windows 和 Linux。": "Map shared remote serial ports to your machine and keep using familiar tools. No account or extra VPN. Available for macOS, Windows and Linux.",
  "Linkport · 远端串口，本机调试。": "Linkport · Remote ports. Local tools.",
  "配对机器，选择串口，接入本机。由 iroh 建立端到端加密连接。": "Pair your machines, choose a port and connect locally. End-to-end encrypted with iroh.",
  "跳到主要内容": "Skip to main content",
  "Linkport 首页": "Linkport home",
  "主要导航": "Main navigation",
  "使用方式": "How it works",
  "连接原理": "Connectivity",
  "安全与控制": "Security",
  "常见问题": "FAQ",
  "安装与使用指南": "Installation guide",
  "获取桌面版": "Get Desktop",
  "打开导航": "Open navigation",
  "移动导航": "Mobile navigation",
  "连接你的串口与工具": "Connect your ports and tools",
  "串口在远端，": "Remote ports.",
  "调试在眼前。": "Local tools.",
  "把远端串口接到本机，继续用熟悉的工具。": "Bring remote serial ports to your machine. Keep using the tools you know.",
  "无需账号，也无需另装 VPN。": "No account. No extra VPN.",
  "获取开发预览": "Get the preview",
  "看看怎么用": "See how it works",
  "开发预览": "Preview",
  "端到端加密": "End-to-end encrypted",
  "USB-A 转九针串口适配器": "USB-A to nine-pin serial adapter",
  "已共享": "Shared",
  "本机终端": "Local terminal",
  "本地映射已建立": "Local mapping connected",
  "远端物理串口": "Remote physical port",
  "本机虚拟串口": "Local virtual port",
  "核心能力": "Core features",
  "邀请配对": "Pair by invitation",
  "按需共享": "Share selected ports",
  "加密连接": "Encrypted connections",
  "独立会话与日志": "Separate sessions and logs",
  "三步，把串口接过来。": "Your remote port, in three steps.",
  "两台机器都安装 Linkport，剩下的很简单。": "Install Linkport on both machines. Then pair, share and connect.",
  "连接的三个步骤": "Three steps to connect",
  "配对机器": "Pair machines",
  "共享串口": "Share a port",
  "本机连接": "Connect locally",
  "邀请一次，记住彼此。": "One invitation. A trusted pair.",
  "每台新设备单独邀请，导入后自动交换公钥，双方互相信任。": "Invite each device separately. Import the invitation to exchange public keys and establish mutual trust.",
  "8 小时有效 · 每次配对一台设备": "Valid for 8 hours · One device per invitation",
  "下一步：共享串口": "Next: share a port",
  "连接流程": "Connection walkthrough",
  "共享端": "Sharing device",
  "单次邀请": "Single-use invite",
  "使用端": "Your machine",
  "交换公钥，私钥留在各自设备。": "Exchange public keys. Private keys stay on each device.",
  "本机串口": "Local serial ports",
  "1 个已共享": "1 shared",
  "共享中": "Sharing",
  "共享 USB Serial": "Share USB Serial",
  "未共享": "Not shared",
  "未选中的串口，对端不可见。": "Unselected ports stay hidden from the other device.",
  "远端串口": "Remote port",
  "本机映射": "Local mapping",
  "在串口工具中打开本机映射即可。": "Open the local mapping in your serial tool.",
  "还没有共享的串口": "No ports are shared yet",
  "返回共享": "Back to sharing",
  "多串口并行": "Multiple ports at once",
  "每条连接独立管理。": "Manage each connection independently.",
  "收发一目了然。": "See every byte.",
  "收发一目了然": "See every byte",
  "文本、HEX 与日志导出。": "Text, HEX and log export.",
  "也能只用本机": "Local monitoring, too",
  "直接打开本机串口监听。": "Monitor a local serial port directly.",
  "多条路径，同一条连接。": "Multiple paths. One connection.",
  "由 iroh 寻找设备之间的可达路径。": "iroh finds a reachable path between your devices.",
  "局域网": "Local network",
  "跨网直连": "Direct connection",
  "中继": "Relay",
  "端到端加密，串口内容只在两端解密。": "End-to-end encrypted. Serial data is decrypted only at the endpoints.",
  "暂停连接动画": "Pause connection animation",
  "暂停": "Pause",
  "内置东京中继，也可选择仅直连。": "Tokyo relay included. Direct-only mode is also available.",
  "了解 iroh": "About iroh",
  "连接可以远，": "Connect from afar.",
  "权限留在本机。": "Stay in control.",
  "你决定连接谁，也决定共享什么。": "You choose who connects and which ports to share.",
  "身份自己保管": "Your device, your identity",
  "设备本机生成密钥，通过单次邀请配对。": "Keys are generated on each device. Pair with single-use invitations.",
  "只共享选中的串口": "Share only selected ports",
  "新插入的设备默认不共享，远端不能代你开启。": "Newly plugged-in devices are not shared by default. Remote peers cannot enable sharing for you.",
  "随时撤销访问": "Revoke access anytime",
  "移除设备后，关闭它与本机的连接和会话。": "Removing a device closes its connections and sessions with your machine.",
  "选好系统，开始连接。": "Choose your system. Start connecting.",
  "正在获取可下载版本…": "Loading available releases…",
  "发布渠道": "Release channel",
  "正式版": "Stable release",
  "Stable 仅接收正式版本更新。": "Stable receives official releases only.",
  "14.5 及以上": "14.5 or later",
  "开发包尚未公证": "Preview builds are not notarized",
  "远端映射需配置 com0com": "Remote mapping requires com0com",
  "ARM64 驱动兼容性待验证": "ARM64 driver compatibility is not yet verified",
  "安装与使用指南 →": "Installation guide →",
  "版本记录": "Changelog",
  "SHA-256 校验文件": "SHA-256 checksums",
  "启用 JavaScript 后可选择渠道和安装包。": "Enable JavaScript to choose a channel and download.",
  "查看公开版本数据": "View public release data",
  "还有几个问题。": "A few more questions.",
  "查看使用指南": "Read the guide",
  "两台机器必须在同一个网络吗？": "Do both machines need to be on the same network?",
  "不必。iroh 尝试跨网络直连，受限时使用内置东京中继；实际效果取决于网络环境。也可以选择仅直连模式，当前不提供用户自定义中继入口。": "No. iroh tries a direct connection across networks and uses the built-in Tokyo relay when needed. Reachability depends on network conditions. Direct-only mode is available; custom relays are not currently supported.",
  "能继续用现有的串口工具吗？": "Can I keep using my existing serial tools?",
  "可以通过本机映射访问。macOS/Linux 使用 PTY；Windows 需要兼容的 com0com 驱动和端口对。PTY 不透传 DTR/RTS，当前不支持硬件流控或持续 BREAK，具体工具与硬件需实测。": "Yes, through a local mapping. macOS and Linux use a PTY; Windows needs a compatible com0com driver and port pair. PTYs do not forward DTR/RTS. Hardware flow control and continuous BREAK are not currently supported. Test your specific tools and hardware.",
  "中继能看到串口内容吗？": "Can the relay read my serial data?",
  "中继转发端到端加密的数据，无法解密串口内容，但可以观察 IP、连接时间和转发量等元数据。设备私钥保存在本机受限文件中，尚未接入系统钥匙串。": "The relay forwards end-to-end encrypted data and cannot decrypt serial content. It can observe metadata such as IP addresses, connection times and traffic volume. Device private keys are stored in restricted local files, not the system keychain.",
  "断线后会自动恢复吗？": "Do serial sessions reconnect automatically?",
  "串口数据会话真正断开后，当前需要手动重新连接。底层网络的路径选择不等于串口会话自动重连。": "A disconnected serial session currently needs to be reconnected manually. Network path selection does not provide automatic serial-session reconnection.",
  "日志会上传或一直保存吗？": "Are logs uploaded or kept indefinitely?",
  "不上传云端。每条会话在本机最多留存 500 条 / 128 KiB 日志，可导出仍在缓冲中的记录。暂停只冻结显示，不停止采集。": "Logs stay on your machine. Each session retains up to 500 entries or 128 KiB; you can export records still in the buffer. Pausing freezes the display, not collection.",
  "重置身份会撤销所有机器上的授权吗？": "Does resetting identity revoke access on every machine?",
  "不会。重置会清空本机配对，其他机器仍需手动移除旧设备。项目没有中心账号找回，设备密钥应自行妥善保管。": "No. Resetting clears local pairings. You must remove the old device manually on other machines. There is no central account recovery, so keep your device keys safe.",
  "从你的设备，到你的设备。": "From your device to your device.",
  "安装与使用 · Linkport": "Installation and usage · Linkport",
  "安装，然后配对。": "Install. Then pair.",
  "Linkport 在已配对的设备之间共享串口，无需账号或额外 VPN。0.0.x 仍处于早期阶段。": "Linkport shares serial ports between paired devices, without an account or extra VPN. The 0.0.x series is still at an early stage.",
  "选择渠道": "Choose a channel",
  "下载与更新只有两个渠道：Stable（正式版）和 Nightly（开发预览）。Nightly 的版本号包含时间和构建编号。两个渠道各自只接收同渠道的新版本，切换渠道需要下载对应安装包。升级保留本机身份与已配对设备。自行从源码编译的版本标为“本地构建”，包含提交标识，不自动更新。": "Downloads and updates have two channels: Stable and Nightly. Nightly versions include a timestamp and build number. Each channel only receives newer versions from the same channel; switching requires downloading the corresponding package. Updates preserve your device identity and pairings. Source builds are marked as local builds, include a commit identifier and do not update automatically.",
  "选择系统与架构": "Choose your system and architecture",
  "各平台的构建与隔离测试不代表所有 USB 串口、NAT 或驱动均已验收。macOS/Linux PTY 不透传 DTR/RTS；当前不支持硬件流控与持续 BREAK。": "Platform builds and isolated tests do not certify every USB serial device, NAT environment or driver. macOS and Linux PTYs do not forward DTR/RTS. Hardware flow control and continuous BREAK are not currently supported.",
  "三步开始": "Get started in three steps",
  "两台机器都启动 Linkport。在“设备与配对”生成邀请，复制完整内容，在另一台机器导入。邀请有效期为八小时、只能使用一次；重新生成或退出邀请方进程会使旧邀请失效。": "Launch Linkport on both machines. Generate an invitation in Devices and pairing, copy it in full and import it on the other machine. Invitations last eight hours and can be used once. Regenerating an invitation or quitting the inviting process invalidates the previous invitation.",
  "在共享端的“共享”页面勾选目标串口。新插入的设备默认不共享，远端不能替你开启。": "On the sharing device, select the port on the Share page. Newly plugged-in devices are not shared by default, and remote peers cannot enable sharing for you.",
  "在使用端选择机器、串口和波特率，连接成功后用原有串口工具打开本机 PTY/com0com 映射。也可以直接使用 Linkport 的监听、发送和日志导出。": "On your machine, choose the device, port and baud rate. After connecting, open the local PTY/com0com mapping with your existing serial tool. You can also monitor, send and export logs directly in Linkport.",
  "更新与校验": "Updates and verification",
  "下载区提供 SHA-256 校验文件。macOS/Linux 可以使用": "SHA-256 checksums are provided in the download section. On macOS/Linux, use",
  "，Windows 可以使用": "; on Windows, use",
  "。应用更新签名与 Apple 公证、Windows Authenticode 属于不同机制。": ". Update signatures are separate from Apple notarization and Windows Authenticode.",
  "连接、权限和日志": "Connections, permissions and logs",
  "当前版本内置东京公共中继，节点随应用更新，也可选择仅直连模式。网络模式改变后需要重启。底层尝试直连，必要时转发密文，实际可达性取决于网络条件。串口数据会话真正断开后需要手动重新连接。": "The app includes a Tokyo public relay, updated with the app, and offers direct-only mode. Restart after changing network mode. The transport tries direct connections and relays encrypted traffic when necessary. Reachability depends on network conditions. Reconnect disconnected serial sessions manually.",
  "设备私钥保存在本机受限配置文件中。移除设备会撤销它对本机的访问并关闭相关会话；重置身份不会自动清除其他机器上的旧信任。日志留存在本机，每个会话最多五百条或 128 KiB，导出只包含仍在缓冲中的记录。": "Device private keys are stored in restricted local configuration files. Removing a device revokes its access to your machine and closes related sessions. Resetting identity does not clear old trust on other machines. Logs stay local, with up to 500 entries or 128 KiB per session. Exports contain only records still buffered.",
  "下载桌面版 →": "Download Desktop →",
  "只开放你选中的串口。": "Share only the ports you choose.",
  "在共享端勾选目标端口，对端便能看到。试试右侧的共享开关。": "Select a port on the sharing device to make it visible to its peer. Try the switch on the right.",
  "新接入的设备默认不共享": "New devices are not shared by default",
  "下一步：本机连接": "Next: connect locally",
  "工具照旧，串口换个位置。": "Same tools. A different port location.",
  "选择设备和串口，连接后用本机工具打开映射路径。": "Choose a device and port, connect, then open the mapped path in your local tool.",
  "也可在 Linkport 中直接监听与发送": "You can also monitor and send directly in Linkport",
  "再看一次：配对机器": "Start again: pair machines",
  "0 个已共享": "0 shared",
  "当前没有共享串口，对端无法连接。": "No ports are shared. The other device cannot connect.",
  "播放连接动画": "Play connection animation",
  "播放": "Play",
  "关闭导航": "Close navigation",
  "此渠道尚无可下载的安装包": "No downloads are available for this channel yet",
  "Nightly 包含最新开发变更，仅更新到较新的 Nightly。调试前请保存工作。": "Nightly includes the latest development changes and only updates to newer Nightly builds. Save your work before debugging.",
  "Stable 仅接收正式版本更新。0.0.x 仍处于早期阶段，请留意平台与硬件限制。": "Stable receives official releases only. The 0.0.x series is still early; check platform and hardware limitations.",
  "尚未发布 {channel} 安装包": "No {channel} packages have been published yet",
  "暂时无法获取版本，请稍后刷新。": "Releases are temporarily unavailable. Please refresh later.",
  "版本记录 · Linkport": "Changelog · Linkport",
  "Stable 是正式发布渠道，Nightly 是持续开发的预览渠道。0.0.x 仍处于早期阶段，硬件和驱动兼容性以说明为准。": "Stable is the official release channel. Nightly previews ongoing development. The 0.0.x series is still early; consult the notes for hardware and driver limitations.",
  "获取桌面版 →": "Get Desktop →",
  "语言": "Language",
  "Stable 和 Nightly 在启动三十秒后检查新版本，此后每分钟检查一次；设置里可以手动检查。关闭后台自动更新仍会显示更新提示。安装包需要通过 SHA-256 和维护者 Ed25519 签名校验，暂存完成后由你选择重启更新。本地构建不检查或替换自己。": "Stable and Nightly check for updates thirty seconds after launch, then every minute. You can also check manually in Settings. Disabling automatic updates still allows update notifications. Packages must pass SHA-256 and publisher Ed25519 verification. Once staged, you choose when to restart and update. Local builds do not check for or replace themselves.",
  "macOS：14.5 及以上，选择 Apple Silicon 或 Intel。打开 DMG 并把 Linkport.app 拖入“应用程序”。当前包有本地签名，但未经 Apple 公证，系统可能提示无法验证开发者；是否允许运行由你判断。": "macOS: 14.5 or later. Choose Apple Silicon or Intel. Open the DMG and drag Linkport.app to Applications. Current builds are ad-hoc signed but not notarized by Apple. macOS may report an unverified developer; decide whether to allow the app to run.",
  "Windows：直接运行下载的 EXE，内含命令行工具。远端映射需要自行安装兼容的 com0com 驱动，并预先配置一对互连端口。ARM64 的驱动兼容性仍待实机验证。": "Windows: run the downloaded EXE directly; the CLI is embedded. Remote mapping requires a compatible com0com driver and a preconfigured connected port pair. ARM64 driver compatibility still needs verification on real hardware.",
  "Linux：Debian/Ubuntu 使用 DEB，通过系统安装器安装，或运行 sudo apt install ./下载的文件.deb。需要 GTK 4 与 WebKitGTK 6。升级时在应用内打开已校验的安装包，在系统安装器中完成安装后重新启动应用。": "Linux: install the DEB on Debian/Ubuntu with the system installer or sudo apt install ./downloaded-file.deb. GTK 4 and WebKitGTK 6 are required. For updates, open the verified package from the app, complete installation in the system installer, then relaunch.",
  "0.0.6 及更早版本需要手动安装一次 0.0.7 或更新版本，以切换到原生安装包。之后升级会自动匹配当前系统与架构。": "If you use 0.0.6 or earlier, manually install 0.0.7 or later once to migrate from ZIP updates to native packages. Later updates select the package matching your operating system and architecture.",
  "shasum -a 256 下载的文件": "shasum -a 256 downloaded-file",
  "Get-FileHash 下载的文件 -Algorithm SHA256": "Get-FileHash downloaded-file -Algorithm SHA256"
};

  const chinese = new Map(Object.entries(messages).map(([cn, en]) => [en, cn]));
  const storageKey = "linkport.website.language";
  let language = "en";
  try { if (localStorage.getItem(storageKey) === "zh-CN") language = "zh-CN"; } catch {}

  function t(text, values = {}) {
    let translated = language === "en" ? (messages[text] ?? text) : (chinese.get(text) ?? text);
    for (const [key, value] of Object.entries(values)) translated = translated.replaceAll(`{${key}}`, String(value));
    return translated;
  }

  function applyLanguage() {
    document.documentElement.lang = language;
    const walker = document.createTreeWalker(document.documentElement, NodeFilter.SHOW_TEXT);
    while (walker.nextNode()) {
      const node = walker.currentNode;
      if (node.parentElement?.closest("script, style, pre, [data-language-switch]")) continue;
      const original = node.nodeValue.trim();
      if (original) node.nodeValue = node.nodeValue.replace(original, t(original));
    }
    for (const element of document.querySelectorAll("[aria-label], [title], meta[content]")) {
      for (const name of ["aria-label", "title", "content"]) {
        const value = element.getAttribute(name);
        if (value) element.setAttribute(name, t(value));
      }
    }
    document.querySelectorAll("[data-site-language]").forEach(button => {
      button.setAttribute("aria-pressed", String(button.dataset.siteLanguage === language));
    });
  }

  function setLanguage(next) {
    if (!["en", "zh-CN"].includes(next)) return;
    language = next;
    try { localStorage.setItem(storageKey, language); } catch {}
    applyLanguage();
    window.dispatchEvent(new CustomEvent("linkport:languagechange"));
  }

  window.linkportI18n = { get language() { return language; }, t, setLanguage };
  document.querySelectorAll("[data-site-language]").forEach(button => {
    button.addEventListener("click", () => setLanguage(button.dataset.siteLanguage));
  });
  applyLanguage();
})();
