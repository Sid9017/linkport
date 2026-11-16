globalThis.initializeLinkportLive = (root, request) => {
  const find = selector => root.querySelector(selector);
  const all = selector => [...root.querySelectorAll(selector)];
  const platform = document.querySelector('meta[name="linkport-host-platform"]')?.content;
  const svgNamespace = "http://www.w3.org/2000/svg";
  let state = { tailscale: {}, localPorts: [], sharedPorts: [], devices: [], sessions: [] };
  let selected = null;
  let pendingTarget = null;
  let refreshing = false;
  let toastTimer;
  let catalogSignature = "";
  let shareSignature = "";
  let mappingSignature = "";
  let logSignature = "";
  let localInventorySignature = "";
  let routes = [];
  let scrollFrame = 0;
  const expanded = new Map();
  const paused = new Map();
  const sequences = new Map();
  const log = find("[data-log]");
  log.setAttribute("aria-label", "串口日志");
  const dialog = find("[data-connect-dialog]");
  const form = find(".lp-connect-form");
  document.title = "Linkport";
  root.classList.add("lp-live");
  find(".lp-window").setAttribute("aria-label", "Linkport");
  find(".lp-preview-label").hidden = true;
  find("[data-local-monitor-open]").hidden = false;
  find("[data-action=disconnect]").hidden = false;
  find(".lp-devices").replaceChildren();
  find(".lp-sharelist").replaceChildren();
  find("[data-mappings]").replaceChildren();
  const windows = platform === "windows";
  find("[data-windows-pair]").hidden = !windows;
  for (const input of find("[data-windows-pair]").querySelectorAll("input")) {
    input.disabled = !windows;
    input.required = windows;
  }
  function element(tag, className, text) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  }
  function icon(name, className = "") {
    const node = document.createElementNS(svgNamespace, "svg");
    node.setAttribute("class", "lp-icon " + className);
    node.setAttribute("aria-hidden", "true");
    const use = document.createElementNS(svgNamespace, "use");
    use.setAttribute("href", "#lp-icon-" + name);
    node.append(use);
    return node;
  }
  function button(name, label, action, primary = false) {
    const node = element("button", "lp-iconbutton" + (primary ? " lp-primary" : ""));
    node.type = "button";
    node.setAttribute("aria-label", label);
    node.title = label;
    node.append(icon(name));
    node.addEventListener("click", action);
    return node;
  }
  function toast(message) {
    clearTimeout(toastTimer);
    const node = find("[data-live-toast]");
    node.textContent = message;
    node.hidden = false;
    toastTimer = setTimeout(() => { node.hidden = true; }, 6000);
    find("[data-notice]").textContent = message;
  }
  const key = (nodeID, portID) => JSON.stringify([nodeID, portID]);
  const active = () => state.sessions.filter(session => session.connected && !session.local);
  const matching = (nodeID, portID) => active().find(session => session.nodeID === nodeID && session.portID === portID);
  const currentSession = () => state.sessions.find(session => session.id === selected);
  const bytes = record => record.data ? Uint8Array.from(atob(record.data), value => value.charCodeAt(0)) : new Uint8Array();
  const hex = content => [...content].map(value => value.toString(16).padStart(2, "0")).join(" ");
  function time(value) {
    const date = new Date(value);
    return date.toLocaleTimeString("en-GB", { hour12: false }) + "." + String(date.getMilliseconds()).padStart(3, "0");
  }
  function showScreen(name) {
    for (const screen of all("[data-screen]")) screen.hidden = screen.dataset.screen !== name;
    for (const nav of all("[data-nav]")) {
      if (nav.dataset.nav === name) nav.setAttribute("aria-current", "page");
      else nav.removeAttribute("aria-current");
    }
    drawCurves();
  }
  for (const nav of all("[data-nav]")) nav.addEventListener("click", () => showScreen(nav.dataset.nav));
  function openConnection(device, port) {
    pendingTarget = { nodeID: device.id, portID: port.id };
    find("#lp-connect-title").textContent = "连接串口";
    find("[data-local-monitor-field]").hidden = true;
    find("[data-connect-target]").hidden = false;
    find("[data-windows-pair]").hidden = !windows;
    for (const input of find("[data-windows-pair]").querySelectorAll("input")) input.disabled = !windows;
    find("[data-connect-submit]").disabled = false;
    find("[data-connect-submit]").textContent = "连接";
    find("[data-connect-target]").textContent = device.hostName + " / " + port.path;
    find("[data-connect-error]").textContent = "";
    form.elements.baudRate.value = "115200";
    dialog.showModal();
  }
  function renderLocalPicker() {
    const signature = JSON.stringify(state.localPorts.map(port => [port.id, port.path, port.product]));
    if (signature === localInventorySignature) return;
    localInventorySignature = signature;
    const picker = form.elements.localPort;
    const selectedPath = picker.value;
    picker.replaceChildren();
    for (const port of state.localPorts) {
      const option = element("option", "", port.product ? port.product + " · " + port.path : port.path);
      option.value = port.path;
      option.dataset.id = port.id;
      picker.append(option);
    }
    if (!state.localPorts.length) picker.append(element("option", "", "未检测到串口"));
    picker.disabled = !state.localPorts.length;
    if ([...picker.options].some(option => option.value === selectedPath)) picker.value = selectedPath;
    if (pendingTarget?.local) find("[data-connect-submit]").disabled = !state.localPorts.length;
  }
  function openLocalMonitor() {
    pendingTarget = { local: true };
    find("#lp-connect-title").textContent = "监听本机串口";
    find("[data-local-monitor-field]").hidden = false;
    find("[data-connect-target]").hidden = true;
    find("[data-windows-pair]").hidden = true;
    for (const input of find("[data-windows-pair]").querySelectorAll("input")) input.disabled = true;
    find("[data-connect-error]").textContent = "";
    find("[data-connect-submit]").textContent = "开始监听";
    find("[data-connect-submit]").disabled = !state.localPorts.length;
    form.elements.baudRate.value = "115200";
    renderLocalPicker();
    dialog.showModal();
  }
  find("[data-local-monitor-open]").addEventListener("click", openLocalMonitor);
  for (const cancel of all("[data-connect-cancel]")) cancel.addEventListener("click", () => dialog.close());
  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (!pendingTarget) return;
    const submit = find("[data-connect-submit]");
    submit.disabled = true;
    submit.textContent = pendingTarget.local ? "正在打开" : "正在连接";
    find("[data-connect-error]").textContent = "";
    try {
      const selectedPort = form.elements.localPort.selectedOptions[0];
      const session = pendingTarget.local ? await request("monitor/local", "POST", {
        id: selectedPort?.dataset.id || "", path: selectedPort?.value || "", baudRate: Number(form.elements.baudRate.value)
      }) : await request("connect", "POST", {
        ...pendingTarget, baudRate: Number(form.elements.baudRate.value),
        path: windows ? form.elements.path.value.trim() : "",
        pairDevice: windows ? form.elements.pairDevice.value.trim() : ""
      });
      selected = session.id;
      dialog.close();
      await refresh();
    } catch (error) {
      find("[data-connect-error]").textContent = error.message;
    } finally {
      submit.disabled = Boolean(pendingTarget?.local && !state.localPorts.length);
      submit.textContent = pendingTarget?.local ? "开始监听" : "连接";
    }
  });
  async function disconnect(session) {
    try {
      await request("disconnect", "POST", { id: session.id });
      await refresh();
    } catch (error) { toast(error.message); }
  }
  function renderDevices() {
    const signature = JSON.stringify([state.devices, active().map(session => [session.id, session.baudRate])]);
    if (signature === catalogSignature) return;
    catalogSignature = signature;
    const list = find(".lp-devices");
    const previousScroll = list.scrollTop;
    list.replaceChildren();
    for (const device of state.devices) {
      const article = element("article", "lp-device");
      article.dataset.node = device.id;
      const available = device.service === "available";
      const machine = element(available ? "button" : "div", "lp-machine" + (!device.online ? " lp-offline" : ""));
      if (available) machine.type = "button";
      machine.append(element("span", "lp-dot"), icon("machine"));
      const name = element("span", "lp-machine-name", device.hostName);
      name.title = device.dnsName || device.hostName;
      machine.append(name, element("code", "lp-machine-meta", device.ips?.[0] || ""));
      const labels = { offline: "离线", unreachable: "服务不可达", incompatible: "版本不兼容" };
      const label = available ? (device.ports.length ? device.ports.length + " 个共享串口" : "没有共享串口") : (labels[device.service] || "服务不可达");
      const meta = element("span", "lp-right", label);
      if (!available && device.online) meta.title = "请在对端运行 Linkport，并允许 Tailscale 访问 TCP 7700";
      machine.append(meta);
      article.append(machine);
      if (available) {
        if (!expanded.has(device.id)) expanded.set(device.id, device.ports.length > 0);
        const tree = element("div", "lp-porttree");
        tree.id = "lp-live-" + list.childElementCount;
        tree.hidden = !expanded.get(device.id);
        machine.setAttribute("aria-expanded", String(!tree.hidden));
        machine.setAttribute("aria-controls", tree.id);
        machine.addEventListener("click", () => {
          tree.hidden = !tree.hidden;
          expanded.set(device.id, !tree.hidden);
          machine.setAttribute("aria-expanded", String(!tree.hidden));
          updateTopology();
          drawCurves();
        });
        for (const port of device.ports) {
          const session = matching(device.id, port.id);
          const row = element("div", "lp-portrow");
          row.dataset.portKey = key(device.id, port.id);
          row.dataset.active = String(Boolean(session));
          const details = element("div", "lp-portdesc");
          const title = port.product || port.path;
          details.append(element("strong", "", title));
          if (port.product) details.append(document.createTextNode(" "), element("code", "", port.path));
          const attributes = [];
          if (port.vid && port.pid) attributes.push(port.vid + ":" + port.pid);
          if (session) attributes.push(session.baudRate + " · 8N1");
          if (port.ambiguous) attributes.push("串口身份不明确");
          else if (!port.online) attributes.push("设备离线");
          if (attributes.length) details.append(element("span", "lp-caption", attributes.join(" · ")));
          row.append(icon("port", "lp-porticon"), details);
          if (session) row.append(element("span", "lp-connected", "已连接"));
          const control = button(session ? "disconnect" : "link", (session ? "断开 " : "连接 ") + title,
            () => session ? disconnect(session) : openConnection(device, port), !session);
          control.classList.add("lp-portaction");
          control.disabled = !session && (!device.bridge || !port.online || port.ambiguous);
          if (!device.bridge) control.title = "请升级对端 Linkport 以启用串口连接";
          row.append(control);
          tree.append(row);
        }
        article.append(tree);
      }
      list.append(article);
    }
    updateTopology();
    list.scrollTop = previousScroll;
    drawCurves();
  }
  function updateTopology() {
    const visiblePorts = all(".lp-portrow").filter(port => !port.closest(".lp-porttree")?.hidden).length;
    find(".lp-topology").dataset.scrollable = String(state.devices.length + visiblePorts > 6);
  }
  function renderSharing() {
    const saved = new Set(state.sharedPorts.map(port => port.id));
    const ports = state.localPorts.map(port => ({ ...port, online: true, shared: port.shared || saved.has(port.id) }));
    for (const saved of state.sharedPorts) {
      if (!ports.some(port => port.id === saved.id)) ports.push({ ...saved, shared: true });
    }
    const signature = JSON.stringify(ports);
    if (signature !== shareSignature) {
      shareSignature = signature;
      const list = find(".lp-sharelist");
      list.replaceChildren();
      if (!ports.length) list.append(element("p", "lp-empty", "未检测到串口"));
      for (const port of ports) {
        const row = element("div", "lp-shareitem");
        const details = element("div", "lp-portdesc");
        const title = port.product || port.path;
        details.append(element("strong", "", title));
        const caption = element("span", "lp-caption");
        const attributes = [];
        if (port.product) caption.append(element("code", "", port.path));
        if (port.vid && port.pid) attributes.push(port.vid + ":" + port.pid);
        if (port.ambiguous) attributes.push("串口身份不明确");
        else if (!port.online) attributes.push("设备离线");
        if (attributes.length) caption.append(document.createTextNode((port.product ? " · " : "") + attributes.join(" · ")));
        if (caption.childNodes.length) details.append(caption);
        const label = element("label", "lp-sharelabel");
        const checkbox = element("input");
        checkbox.type = "checkbox";
        checkbox.checked = Boolean(port.shared);
        checkbox.disabled = Boolean(port.ambiguous && !port.shared);
        checkbox.setAttribute("aria-label", "共享 " + port.path);
        checkbox.addEventListener("change", async () => {
          checkbox.disabled = true;
          try {
            state = await request("share", "PUT", { id: port.id, shared: checkbox.checked });
            shareSignature = "";
            render();
          } catch (error) {
            checkbox.checked = Boolean(port.shared);
            toast(error.message);
          } finally { checkbox.disabled = Boolean(port.ambiguous && !port.shared); }
        });
        label.append(checkbox);
        row.append(icon("port"), details, label);
        list.append(row);
      }
    }
    find("[data-share-count]").textContent = state.sharedPorts.length + " 个共享";
    const service = find("[data-service-status]");
    service.textContent = state.localError ? "串口读取失败：" + state.localError :
      state.serviceError ? "共享服务：" + state.serviceError :
      state.sharedPorts.length && state.tailscale.state !== "Running" ? "连接 Tailscale 后可供其他机器访问" : "";
    service.hidden = !service.textContent;
  }
  function renderMappings() {
    const sessions = active();
    const signature = JSON.stringify(state.sessions.map(session => [session.id, session.local, session.connected, session.localPath, session.error]));
    if (signature !== mappingSignature) {
      mappingSignature = signature;
      const list = find("[data-mappings]");
      list.replaceChildren();
      for (const session of sessions) {
        const row = element("div", "lp-mapping");
        row.dataset.mapping = session.id;
        const source = element("div", "lp-mapping-source", session.label);
        source.append(element("span", "", session.hostName + " · " + session.portPath));
        const capability = element("span", "lp-capability", windows ? "COM" : "PTY");
        capability.setAttribute("aria-label", windows ? "使用独立的 com0com 端口对" : "PTY 映射不支持 DTR/RTS 透传");
        row.append(source, icon("arrow"), element("code", "", session.localPath), capability,
          button("disconnect", "断开 " + session.hostName + " / " + session.portPath, () => disconnect(session)));
        list.append(row);
      }
      const picker = find("[data-session-select]");
      picker.replaceChildren();
      const orderedSessions = [...state.sessions].sort((first, second) => Number(Boolean(second.local)) - Number(Boolean(first.local)));
      for (const session of orderedSessions) {
        const option = element("option", "", (session.local ? "本机" : session.hostName) + " / " + session.portPath + (session.connected ? "" : " · 已断开"));
        option.value = session.id;
        picker.append(option);
      }
      if (!state.sessions.length) picker.append(element("option", "", "暂无会话"));
      picker.disabled = !state.sessions.length;
      drawCurves();
    }
    if (!state.sessions.some(session => session.id === selected)) selected = state.sessions[0]?.id || null;
    if (selected) find("[data-session-select]").value = selected;
    find("[data-connection-count]").textContent = sessions.length + " 条连接";
    find(".lp-mapping-heading").hidden = !sessions.length;
    find("[data-mappings]").hidden = !sessions.length;
  }
  function renderLog(force = false) {
    const session = currentSession();
    const records = paused.has(selected) ? paused.get(selected) : session?.records || [];
    const signature = JSON.stringify([selected, records, find("[data-format]").value, find("[data-filter]").value]);
    if (force || signature !== logSignature) {
      logSignature = signature;
      const follow = log.scrollHeight - log.scrollTop - log.clientHeight < 24;
      const previousScroll = log.scrollTop;
      log.replaceChildren();
      const keyword = find("[data-filter]").value.toLowerCase();
      const showHex = find("[data-format]").value === "hex";
      const textDecoders = { RX: new TextDecoder(), TX: new TextDecoder() };
      for (const record of records) {
        const content = bytes(record);
        const decoded = record.message || textDecoders[record.kind]?.decode(content, { stream: true }) || "";
        const message = showHex && record.kind !== "SYS" ? hex(content) : decoded.replace(/\r/g, "\\r").replace(/\n/g, "\\n");
        if (keyword && !(record.kind + " " + message).toLowerCase().includes(keyword)) continue;
        const row = element("div", "lp-logline");
        row.dataset.kind = record.kind;
        row.append(element("time", "", time(record.time)), element("span", "lp-direction", record.kind), element("span", "lp-message", message));
        log.append(row);
      }
      log.classList.toggle("lp-log-empty", !session && !keyword);
      if (!log.childElementCount) {
        const empty = element("div", "lp-empty" + (!session ? " lp-monitor-empty" : ""), keyword ? "没有匹配的日志" : session ? "暂无日志，等待此会话的数据" : "选择一个串口开始监听");
        if (!session && !keyword) {
          const open = element("button", "lp-primary", "监听本机串口");
          open.type = "button";
          open.addEventListener("click", openLocalMonitor);
          empty.append(open);
        }
        log.append(empty);
      }
      log.scrollTop = follow || force ? log.scrollHeight : previousScroll;
    }
    find("[data-session-label]").textContent = session ? (session.local ? "本机" : session.hostName) + " / " + session.portPath : "暂无会话";
    const status = find("[data-session-status]");
    status.textContent = session?.connected ? (paused.has(selected) ? "显示已暂停" : "监听中") : "未连接";
    status.title = session?.error || "";
    find("[data-send]").disabled = !session?.connected;
    find("[data-action=disconnect]").disabled = !session?.connected;
    find(".lp-send input").disabled = !session?.connected;
    for (const action of ["pause", "clear", "export"]) find("[data-action=" + action + "]").disabled = !session;
    const pause = find("[data-action=pause]");
    pause.setAttribute("aria-label", paused.has(selected) ? "继续显示" : "暂停显示");
    pause.querySelector("use").setAttribute("href", paused.has(selected) ? "#lp-icon-play" : "#lp-icon-pause");
    find("[data-action=export]").setAttribute("aria-label", "导出完整会话");
    find("[data-action=export]").title = "导出当前会话全部缓冲日志，包含筛选隐藏的内容";
  }
  function render() {
    const network = state.tailscale;
    const label = network.stale ? "Tailscale 状态待确认" : network.state === "Running" ? "Tailscale 已连接" :
      network.state === "NeedsLogin" ? "未登录 Tailscale" :
      network.state === "NotInstalled" ? "未安装 Tailscale" :
      network.state ? "Tailscale 未连接" : "正在读取 Tailscale";
    find("[data-network-state]").textContent = label;
    find("[data-host-name]").textContent = network.hostName || "本机";
    find("[data-host-ip]").textContent = network.ips?.[0] || "—";
    const online = state.devices.filter(device => device.online).length;
    find("[data-discovery-summary]").textContent = state.devices.length ? online + " 台可用 · " + (state.devices.length - online) + " 台离线" : "";
    const empty = !state.devices.length;
    find(".lp-topology").hidden = empty;
    find("[data-connection-empty]").hidden = !empty;
    find("[data-empty-title]").textContent = network.state === "Running" ? "尚未发现其他设备" : label;
    find("[data-empty-copy]").textContent = network.state === "Running" ? "在另一台机器上连接同一 Tailscale 网络。" :
      network.error || (network.state === "NeedsMachineAuth" ? "请在 Tailscale 管理后台批准此设备。" :
      network.state === "Stopped" ? "请在 Tailscale 中开启网络连接。" : "登录后自动刷新设备清单。");
    find("[data-live-login]").hidden = !empty || network.state === "Running" || !network.state;
    find("[data-live-login]").textContent = network.state === "Stopped" ? "连接 Tailscale" : network.state === "NotInstalled" ? "安装 Tailscale" : "登录 Tailscale";
    renderDevices();
    renderSharing();
    renderLocalPicker();
    renderMappings();
    renderLog();
    for (const refresh of all("[data-refresh]")) {
      refresh.dataset.refreshing = String(Boolean(state.refreshing));
      refresh.disabled = Boolean(state.refreshing);
    }
  }
  function drawCurves() {
    const layout = find(".lp-topology");
    const canvas = find(".lp-connections");
    const bounds = layout.getBoundingClientRect();
    if (!bounds.width || !bounds.height) return;
    canvas.replaceChildren();
    routes = [];
    canvas.setAttribute("viewBox", "0 0 " + bounds.width + " " + bounds.height);
    const local = find(".lp-local").getBoundingClientRect();
    const viewport = find(".lp-devices").getBoundingClientRect();
    const clipped = layout.dataset.scrollable === "true";
    const path = (commands, keys) => {
      const drawing = document.createElementNS(svgNamespace, "path");
      drawing.setAttribute("d", commands);
      if (keys.length) drawing.dataset.link = "active";
      canvas.append(drawing);
      if (keys.length) routes.push({ commands, keys });
    };
    for (const device of all(".lp-device")) {
      const machine = device.querySelector(".lp-machine").getBoundingClientRect();
      if (clipped && (machine.bottom <= viewport.top || machine.top >= viewport.bottom)) continue;
      const identities = active().filter(session => session.nodeID === device.dataset.node).map(session => session.id);
      const horizontal = machine.left >= local.right;
      const startX = (horizontal ? local.right : local.left + local.width / 2) - bounds.left;
      const startY = (horizontal ? local.top + local.height / 2 : local.bottom) - bounds.top;
      const endX = machine.left - bounds.left;
      const endY = machine.top + machine.height / 2 - bounds.top;
      const bend = horizontal ? Math.max(18, (endX - startX) * .6) : 24;
      const firstControl = horizontal ? `${startX + bend} ${startY}` : `${startX} ${startY + bend}`;
      path(`M ${startX} ${startY} C ${firstControl}, ${endX - bend} ${endY}, ${endX} ${endY}`, identities);
      for (const port of device.querySelectorAll(".lp-portrow")) {
        if (!port.getClientRects().length) continue;
        const destination = port.getBoundingClientRect();
        if (clipped && (destination.bottom <= viewport.top || destination.top >= viewport.bottom)) continue;
        const firstX = machine.left + 12 - bounds.left;
        const firstY = machine.bottom - bounds.top;
        const targetX = destination.left - bounds.left;
        const targetY = destination.top + destination.height / 2 - bounds.top;
        const session = active().find(entry => key(entry.nodeID, entry.portID) === port.dataset.portKey);
        path(`M ${firstX} ${firstY} C ${firstX} ${targetY}, ${firstX} ${targetY}, ${targetX} ${targetY}`, session ? [session.id] : []);
      }
    }
  }
  function animateTraffic(identity, direction) {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    for (const route of routes.filter(route => route.keys.includes(identity))) {
      const dot = document.createElementNS(svgNamespace, "circle");
      dot.setAttribute("r", "2.5");
      dot.setAttribute("class", "lp-traffic-dot");
      dot.dataset.direction = direction;
      const motion = document.createElementNS(svgNamespace, "animateMotion");
      motion.setAttribute("path", route.commands);
      motion.setAttribute("dur", "1.1s");
      motion.setAttribute("keyPoints", direction === "RX" ? "1;0" : "0;1");
      motion.setAttribute("keyTimes", "0;1");
      motion.setAttribute("calcMode", "linear");
      dot.append(motion);
      find(".lp-connections").append(dot);
      if (motion.beginElement) motion.beginElement();
      setTimeout(() => dot.remove(), 1150);
    }
  }
  async function refresh() {
    if (refreshing) return;
    refreshing = true;
    try {
      state = await request("state");
      render();
      for (const session of state.sessions) {
        const previous = sequences.get(session.id);
        if (previous !== undefined && session.connected) {
          const records = session.records.filter(record => record.sequence > previous);
          for (const kind of ["RX", "TX"]) if (records.some(record => record.kind === kind)) animateTraffic(session.id, kind);
        }
        sequences.set(session.id, session.records.at(-1)?.sequence || previous || 0);
      }
    } catch (error) {
      find("[data-network-state]").textContent = "无法读取状态";
      find("[data-notice]").textContent = error.message;
    } finally { refreshing = false; }
  }
  for (const control of all("[data-refresh]")) control.addEventListener("click", async () => {
    control.disabled = true;
    control.dataset.refreshing = "true";
    try { await request("refresh", "POST"); await refresh(); }
    catch (error) { toast(error.message); }
  });
  find("[data-live-login]").addEventListener("click", () => find("[data-tailscale-login]").click());
  find("[data-session-select]").addEventListener("change", event => { selected = event.target.value; renderLog(true); });
  find("[data-action=disconnect]").addEventListener("click", () => {
    const session = currentSession();
    if (session) disconnect(session);
  });
  find("[data-filter]").addEventListener("input", () => renderLog());
  find("[data-format]").addEventListener("change", () => renderLog());
  find("[data-action=pause]").addEventListener("click", () => {
    if (paused.has(selected)) paused.delete(selected);
    else paused.set(selected, [...(currentSession()?.records || [])]);
    renderLog();
  });
  find("[data-action=clear]").addEventListener("click", async () => {
    try {
      await request("clear", "POST", { id: selected });
      if (paused.has(selected)) paused.set(selected, []);
      await refresh();
    } catch (error) { toast(error.message); }
  });
  find("[data-action=export]").addEventListener("click", async () => {
    try {
      const result = await request("export", "POST", { id: selected });
      if (result.saved) toast("已保存完整会话：" + result.name);
    } catch (error) { toast(error.message); }
  });
  find(".lp-send").addEventListener("submit", async event => {
    event.preventDefault();
    const input = event.currentTarget.elements.command;
    if (!input.value || !currentSession()?.connected) return;
    const endings = { none: "", cr: "\r", lf: "\n", crlf: "\r\n" };
    const content = new TextEncoder().encode(input.value + endings[event.currentTarget.elements.ending.value]);
    if (content.length > 65536) { toast("发送内容不能超过 65536 字节"); return; }
    const identity = selected;
    const original = input.value;
    find("[data-send]").disabled = true;
    try {
      let encoded = "";
      for (let offset = 0; offset < content.length; offset += 8192) encoded += String.fromCharCode(...content.subarray(offset, offset + 8192));
      await request("send", "POST", { id: identity, data: btoa(encoded) });
      if (input.value === original) input.value = "";
      await refresh();
    } catch (error) { toast(error.message); }
    finally { find("[data-send]").disabled = !currentSession()?.connected; }
  });
  const observer = new ResizeObserver(drawCurves);
  find(".lp-devices").addEventListener("scroll", () => {
    if (!scrollFrame) scrollFrame = requestAnimationFrame(() => { scrollFrame = 0; drawCurves(); });
  });
  for (const node of all(".lp-topology, .lp-local, .lp-devices")) observer.observe(node);
  document.fonts.ready.then(drawCurves);
  render();
  refresh();
  const interval = setInterval(refresh, 1000);
  window.addEventListener("pagehide", () => { clearInterval(interval); observer.disconnect(); cancelAnimationFrame(scrollFrame); }, { once: true });
  return drawCurves;
};
