globalThis.initializeLinkportMonitor = (root, motion) => {
  const find = selector => root.querySelector(selector);
  const log = find("[data-log]");
  const picker = find("[data-port-picker]");
  const pickerToggle = find("[data-port-picker-toggle]");
  const search = find("[data-log-search]");
  const searchInput = find("[data-log-query]");
  const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
  const svgNamespace = "http://www.w3.org/2000/svg";
  const endings = new Map();
  let entries = [];
  let pickerSignature = "";
  let choose = () => {};
  let matches = [];
  let matchIndex = -1;
  let matchKey = "";
  let scrollFrame = 0;
  let searchOrigin;
  function portType(port = {}) {
    const text = [port.path, port.portPath, port.product, port.label].filter(Boolean).join(" ");
    if (/bluetooth|rfcomm|bthenum/i.test(text)) return { icon: "bluetooth", label: "蓝牙串口", type: "bluetooth" };
    if (port.is_usb || port.isUSB || (port.vid && port.pid) || /\busb\b|tty(?:USB|ACM)|cu\.usb|usbserial|usbmodem/i.test(text)) {
      return { icon: "usb", label: "USB 串口", type: "usb" };
    }
    if (/tty(?:S|AMA|THS|O)\d|\buart\b/i.test(text)) return { icon: "uart", label: "UART 串口", type: "uart" };
    return { icon: "port", label: "通用串口", type: "serial" };
  }
  function icon(port) {
    const type = portType(port);
    const drawing = document.createElementNS(svgNamespace, "svg");
    drawing.setAttribute("class", "lp-icon");
    drawing.setAttribute("aria-label", type.label);
    drawing.setAttribute("role", "img");
    drawing.dataset.transport = type.type;
    const use = document.createElementNS(svgNamespace, "use");
    use.setAttribute("href", "#lp-icon-" + type.icon);
    drawing.append(use);
    return drawing;
  }
  function closePicker(restore = false) {
    picker.hidden = true;
    pickerToggle.setAttribute("aria-expanded", "false");
    if (restore) pickerToggle.focus();
  }
  function openPicker() {
    if (!search.hidden) closeSearch(false);
    picker.hidden = false;
    pickerToggle.setAttribute("aria-expanded", "true");
    motion.enter(picker, -4);
    (picker.querySelector('[aria-selected="true"]') || picker.querySelector("[data-port-option]"))?.focus();
  }
  pickerToggle.addEventListener("click", () => picker.hidden ? openPicker() : closePicker());
  pickerToggle.addEventListener("keydown", event => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); openPicker(); }
  });
  picker.addEventListener("click", event => {
    const option = event.target.closest("[data-port-option]");
    if (!option) return;
    const entry = entries.find(item => item.key === option.dataset.portOption);
    closePicker(true);
    if (entry) choose(entry);
  });
  picker.addEventListener("keydown", event => {
    const options = [...picker.querySelectorAll("[data-port-option]")];
    const index = options.indexOf(document.activeElement);
    if (event.key === "Escape") { event.preventDefault(); closePicker(true); }
    else if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
      event.preventDefault();
      const next = event.key === "Home" ? 0 : event.key === "End" ? options.length - 1 :
        (index + (event.key === "ArrowDown" ? 1 : -1) + options.length) % options.length;
      options[next]?.focus();
    } else if (event.key === "Tab") closePicker();
  });
  document.addEventListener("pointerdown", event => {
    if (!event.target.closest(".lp-port-picker")) closePicker();
  });
  function setPorts(next, selectedKey, onChoose) {
    entries = next;
    choose = onChoose;
    const signature = JSON.stringify([next.map(entry => ({
      key: entry.key, label: entry.label, title: entry.title, group: entry.group,
      path: entry.path, type: portType(entry.port).type, connected: entry.connected
    })), selectedKey]);
    if (signature === pickerSignature) return;
    pickerSignature = signature;
    const selected = next.find(entry => entry.key === selectedKey);
    find("[data-session-label]").textContent = selected?.title || selected?.label || "选择串口";
    find("[data-selected-port-icon]").replaceChildren(icon(selected?.port));
    const focusedKey = document.activeElement?.dataset.portOption;
    const position = picker.scrollTop;
    picker.replaceChildren();
    let group;
    for (const entry of entries) {
      if (group !== entry.group) {
        group = entry.group;
        const heading = document.createElement("div");
        heading.className = "lp-port-group";
        heading.setAttribute("role", "presentation");
        heading.textContent = group;
        picker.append(heading);
      }
      const option = document.createElement("button");
      option.type = "button";
      option.className = "lp-port-option";
      option.dataset.portOption = entry.key;
      option.dataset.path = entry.path || "";
      option.setAttribute("role", "option");
      option.setAttribute("aria-selected", String(entry.key === selectedKey));
      const text = document.createElement("span");
      text.className = "lp-port-option-text";
      const label = document.createElement("span");
      label.textContent = entry.label;
      text.append(label);
      if (entry.path && entry.path !== entry.label) {
        const detail = document.createElement("code");
        detail.textContent = entry.path;
        text.append(detail);
      }
      option.append(icon(entry.port), text);
      if (entry.connected) {
        const connected = document.createElement("span");
        connected.className = "lp-port-connected";
        connected.setAttribute("aria-label", "已连接");
        connected.textContent = "●";
        option.append(connected);
      }
      picker.append(option);
    }
    if (!entries.length) {
      const empty = document.createElement("p");
      empty.className = "lp-port-group";
      empty.textContent = "未检测到可连接的串口";
      picker.append(empty);
    }
    picker.scrollTop = position;
    if (focusedKey && !picker.hidden) [...picker.querySelectorAll("[data-port-option]")].find(node => node.dataset.portOption === focusedKey)?.focus();
  }
  function cancelScroll() { cancelAnimationFrame(scrollFrame); scrollFrame = 0; }
  function scrollToMatch(row) {
    cancelScroll();
    const bounds = row.getBoundingClientRect();
    const viewport = log.getBoundingClientRect();
    const start = log.scrollTop;
    const target = Math.max(0, Math.min(log.scrollHeight - log.clientHeight,
      start + bounds.top - viewport.top - (log.clientHeight - bounds.height) / 2));
    if (reduced.matches) { log.scrollTop = target; return; }
    const distance = target - start;
    const duration = Math.min(340, 150 + Math.sqrt(Math.abs(distance)) * 5);
    const started = performance.now();
    const normalizer = 1 - 11 * Math.exp(-10);
    const frame = now => {
      const progress = Math.min(1, (now - started) / duration);
      const amount = (1 - (1 + 10 * progress) * Math.exp(-10 * progress)) / normalizer;
      log.scrollTop = start + distance * amount;
      if (progress < 1) scrollFrame = requestAnimationFrame(frame);
      else { log.scrollTop = target; scrollFrame = 0; }
    };
    scrollFrame = requestAnimationFrame(frame);
  }
  for (const event of ["wheel", "touchstart", "pointerdown"]) log.addEventListener(event, cancelScroll, { passive: true });
  function selectMatch(index, navigate = true) {
    matchIndex = matches.length ? (index + matches.length) % matches.length : -1;
    matchKey = matches[matchIndex]?.dataset.recordKey || "";
    for (const [position, row] of matches.entries()) {
      row.dataset.searchCurrent = String(position === matchIndex);
      row.setAttribute("aria-current", String(position === matchIndex));
    }
    find("[data-search-count]").textContent = (matchIndex + 1) + "/" + matches.length;
    find("[data-search-prev]").disabled = !matches.length;
    find("[data-search-next]").disabled = !matches.length;
    if (navigate && matchIndex >= 0) scrollToMatch(matches[matchIndex]);
  }
  function clearMarks() {
    for (const mark of log.querySelectorAll("mark")) mark.replaceWith(document.createTextNode(mark.textContent));
    for (const row of log.querySelectorAll("[data-search-current]")) {
      delete row.dataset.searchCurrent;
      row.removeAttribute("aria-current");
    }
  }
  function refreshSearch(reset = false, navigate = false) {
    clearMarks();
    matches = [];
    const query = search.hidden || search.inert ? "" : searchInput.value.toLowerCase();
    if (query) for (const row of log.querySelectorAll(".lp-logline")) {
      let matched = false;
      for (const message of row.querySelectorAll("time, .lp-direction, .lp-message")) {
        const text = message.textContent;
        const lower = text.toLowerCase();
        let offset = 0;
        let index;
        const fragment = document.createDocumentFragment();
        while ((index = lower.indexOf(query, offset)) !== -1) {
          matched = true;
          fragment.append(document.createTextNode(text.slice(offset, index)));
          const mark = document.createElement("mark");
          mark.textContent = text.slice(index, index + query.length);
          fragment.append(mark);
          offset = index + query.length;
        }
        fragment.append(document.createTextNode(text.slice(offset)));
        message.replaceChildren(fragment);
      }
      if (matched) matches.push(row);
    }
    const previous = reset ? -1 : matches.findIndex(row => row.dataset.recordKey === matchKey);
    selectMatch(previous >= 0 ? previous : Math.max(0, Math.min(matchIndex, matches.length - 1)), navigate);
  }
  function openSearch() {
    closePicker();
    if (search.hidden || search.inert) searchOrigin = document.activeElement;
    motion.reveal(search, true);
    searchInput.focus({ preventScroll: true });
    searchInput.select();
    refreshSearch(false, true);
  }
  function closeSearch(restore = true) {
    cancelScroll();
    if (search.hidden) return;
    clearMarks();
    matches = [];
    if (restore) (searchOrigin?.isConnected ? searchOrigin : log).focus({ preventScroll: true });
    motion.reveal(search, false);
  }
  document.addEventListener("keydown", event => {
    if (event.isComposing || event.keyCode === 229) return;
    if (event.key === "Escape" && !picker.hidden) { event.preventDefault(); closePicker(true); return; }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "f" &&
        !find('[data-screen="monitor"]').hidden && !root.querySelector("dialog[open]")) {
      event.preventDefault();
      openSearch();
    } else if (event.key === "Escape" && !search.hidden && !root.querySelector("dialog[open]")) {
      event.preventDefault();
      closeSearch();
    }
  });
  searchInput.addEventListener("input", () => { matchIndex = 0; refreshSearch(true, true); });
  search.addEventListener("keydown", event => {
    if (event.isComposing || event.keyCode === 229) return;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeSearch(); }
    else if (["Enter", "ArrowDown", "ArrowUp"].includes(event.key)) {
      event.preventDefault();
      selectMatch(matchIndex + (event.key === "ArrowUp" || event.shiftKey ? -1 : 1));
    }
  });
  find("[data-search-prev]").addEventListener("click", () => selectMatch(matchIndex - 1));
  find("[data-search-next]").addEventListener("click", () => selectMatch(matchIndex + 1));
  find("[data-search-close]").addEventListener("click", () => closeSearch());
  for (const nav of root.querySelectorAll("[data-nav]")) nav.addEventListener("click", () => {
    closePicker();
    if (nav.dataset.nav !== "monitor") closeSearch(false);
  });
  function lineEnding(identity, records) {
    if (!endings.has(identity)) endings.set(identity, { sequence: -1, ending: "\r\n", carriageReturn: false, seen: new WeakSet(), minimumTime: -Infinity });
    const tracker = endings.get(identity);
    for (const record of records) {
      if (record.time && Date.parse(record.time) < tracker.minimumTime) continue;
      tracker.minimumTime = -Infinity;
      if (record.sequence === undefined) {
        if (tracker.seen.has(record)) continue;
        tracker.seen.add(record);
      } else {
        if (record.sequence <= tracker.sequence) continue;
        tracker.sequence = record.sequence;
      }
      if (record.kind !== "RX") continue;
      const data = record.data ? Uint8Array.from(atob(record.data), value => value.charCodeAt(0)) : new TextEncoder().encode(record.message || "");
      for (const byte of data) {
        if (tracker.carriageReturn && byte === 10) {
          tracker.ending = "\r\n";
          tracker.carriageReturn = false;
          continue;
        }
        tracker.carriageReturn = byte === 13;
        if (byte === 13) tracker.ending = "\r";
        else if (byte === 10) tracker.ending = "\n";
      }
    }
    return tracker.ending;
  }
  window.addEventListener("pagehide", cancelScroll, { once: true });
  return { portType, icon, setPorts, openPicker, refreshSearch, lineEnding, closeSearch,
    resetSession: (identity, records = []) => {
      endings.set(identity, { sequence: -1, ending: "\r\n", carriageReturn: false, seen: new WeakSet(),
        minimumTime: records[0]?.time ? Date.parse(records[0].time) : -Infinity });
      matchKey = ""; matchIndex = 0; cancelScroll();
    },
    isSearching: () => !search.hidden && !search.inert && Boolean(searchInput.value) };
};
