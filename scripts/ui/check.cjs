const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { execFileSync } = require("node:child_process");
const playwright = require("playwright");

const origin = process.argv[2];
const output = process.env.LINKPORT_UI_OUTPUT || path.resolve(__dirname, "../../dist/ci/ui");
const browsers = (process.env.LINKPORT_UI_BROWSERS || "chromium").split(/\s+/).filter(Boolean);
const platform = { win32: "windows", darwin: "darwin", linux: "linux" }[process.platform];
const wailsDirectory = execFileSync("go", [
  "list", "-m", "-f", "{{.Dir}}", "github.com/wailsapp/wails/v3",
], { encoding: "utf8", cwd: path.resolve(__dirname, "../../desktop") }).trim();
const runtime = fs.readFileSync(path.join(wailsDirectory, "internal/assetserver/bundledassets/runtime.debug.js"), "utf8");
fs.mkdirSync(output, { recursive: true });

async function initializePage(page) {
  let preferences = { fontSize: 13, language: "zh-CN", theme: "light", closeToTray: true, autoUpdate: false };
  await page.addInitScript(({ platform }) => {
    window.nativeMessages = [];
    window._wails = { environment: { OS: platform }, flags: {} };
    const postMessage = message => window.nativeMessages.push(message);
    if (platform === "windows") {
      window.chrome ||= {};
      window.chrome.webview = { postMessage };
    } else {
      window.webkit = { messageHandlers: { external: { postMessage } } };
    }
  }, { platform });
  await page.route("**/wails/runtime.js", route => route.fulfill({
    contentType: "application/javascript", body: runtime,
  }));
  await page.route("**/native/v1/relay-update", route=>route.fulfill({contentType:"application/json",body:'{"state":"bundled","version":1}'}));
  await page.route("**/native/v1/update", route => route.fulfill({
    contentType: "application/json", body: '{"configured":false,"state":"unconfigured"}',
  }));
  await page.route("**/native/v1/preferences", route => {
    if (route.request().method() === "PUT") preferences = route.request().postDataJSON();
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(preferences) });
  });
}

async function settleMotion(page) {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await Promise.all(document.getAnimations()
      .filter(animation => animation.effect.getComputedTiming().iterations !== Infinity)
      .map(animation => animation.finished.catch(() => {})));
  });
}

async function checkCloseFrame(page, selector) {
  const button = page.locator(selector);
  await button.waitFor({ state: "visible" });
  await button.focus();
  const frame = await button.evaluate(node => {
    const style = getComputedStyle(node);
    return { border: style.borderTopWidth, outline: style.outlineStyle, shadow: style.boxShadow };
  });
  assert.deepEqual(frame, { border: "0px", outline: "none", shadow: "none" });
}

async function checkFunctional(page, browserName) {
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await initializePage(page);
  await page.goto(origin, { waitUntil: "networkidle" });
  assert.equal(await page.locator("[data-nav=connect]").innerText(), "设备");
  await page.waitForFunction(() => window.nativeMessages.includes("wails:runtime:ready"));
  assert.equal(await page.locator('meta[name="linkport-host-platform"]').getAttribute("content"), platform);
  await page.mouse.move(400, 25);
  await page.mouse.down();
  await page.mouse.move(440, 30, { steps: 4 });
  await page.mouse.up();
  assert.ok(await page.evaluate(() => window.nativeMessages.includes("wails:drag")), "title bar did not dispatch native dragging");
  const beforeDrag = await page.evaluate(() => window.nativeMessages.filter(message => message === "wails:drag").length);
  await page.locator("[data-settings-open]").click();
  await checkCloseFrame(page, "[data-settings-close]");
  await page.screenshot({ path: path.join(output, `${browserName}-settings-close-focus.png`) });
  await page.locator("[data-settings-close]").click();
  assert.equal(await page.evaluate(() => window.nativeMessages.filter(message => message === "wails:drag").length), beforeDrag);
  const initial = await page.evaluate(async () => {
    const token = document.querySelector('meta[name="linkport-api-token"]').content;
    return (await fetch("/native/v1/state", { headers: { "X-Linkport-Token": token } })).json();
  });
  assert.equal(initial.network.state, "Disabled");
  assert.equal(initial.devices.length, 0);
  assert.equal(initial.sessions.length, 0);
  assert.equal(initial.localPorts.length, 1);
  assert.equal(initial.localPorts[0].path, "/dev/frontend-fixture");
  assert.equal(await page.locator(".lp-preview-label").isVisible(), false);
  assert.equal(await page.locator("[data-mapping]").count(), 0);

  const peer = {
    id: "fixture-peer", hostName: "test-device", ips: ["100.64.255.254"], online: true,
    os: "linux", service: "available", bridge: true,
    ports: [{ id: "fixture-port", path: "/dev/test-serial", product: "Test serial fixture", online: true }],
  };
  const fixture = {
    network: { state: "Running", nodeID: "a".repeat(64), hostName: "local-test", config: { relayMode: "disabled", relayURLs: [] } },
    devices: [peer], localPorts: [
      { id: "local-test", path: "/dev/local-fixture", product: "Local fixture", shared: false, is_usb: true },
      { id: "bluetooth-test", path: "/dev/rfcomm0", product: "Bluetooth fixture", shared: false },
      { id: "uart-test", path: "/dev/ttyS0", product: "UART fixture", shared: false },
    ],
    sharedPorts: [], sessions: [], refreshing: false,
  };
  const sent = [];
  const openedLocal = [];
  const shared = [];
  let rejectConnect = true;
  let beforeStateResponse;
  await page.route("**/native/v1/**", async route => {
    const operation = new URL(route.request().url()).pathname.split("/").at(-1);
    assert.equal(route.request().headers()["x-linkport-token"], "browser-test-only");
    const body = route.request().method() === "GET" ? {} : JSON.parse(route.request().postData() || "{}");
    let response;
    if (operation === "state") {
      const beforeResponse = beforeStateResponse;
      beforeStateResponse = null;
      if (beforeResponse) await beforeResponse();
      response = fixture;
    } else if (operation === "network") response = fixture.network;
    else if (operation === "devices") response = fixture.devices;
    else if (operation === "local") {
      assert.equal(body.path, "/dev/local-fixture");
      assert.equal(body.id, "local-test");
      openedLocal.push(body);
      response = {
        id: "local-session", local: true, nodeID: "local", hostName: "local-test", portID: "local-test",
        portPath: body.path, label: "Local fixture", localPath: body.path, connected: true, baudRate: body.baudRate,
        records: [{ sequence: 1, time: "2026-11-16T07:02:03.456Z", kind: "RX", data: Buffer.from("local only").toString("base64") }],
      };
      fixture.sessions = [...fixture.sessions.filter(session => session.id !== response.id), response];
    } else if (operation === "share") {
      shared.push(body);
      fixture.localPorts[0].shared = body.shared;
      fixture.sharedPorts = body.shared ? [{ ...fixture.localPorts[0], online: true }] : [];
      response = fixture;
    } else if (operation === "connect") {
      if (platform === "windows") {
        assert.equal(body.path, "COM121");
        assert.equal(body.pairDevice, "COM122");
      } else {
        assert.equal(body.path, "");
        assert.equal(body.pairDevice, "");
      }
      if (rejectConnect) {
        rejectConnect = false;
        return route.fulfill({ status: 409, body: "测试：串口被占用" });
      }
      assert.equal(body.nodeID, "fixture-peer");
      assert.equal(body.portID, "fixture-port");
      assert.equal(body.baudRate, 9600);
      response = {
        id: "fixture-session", nodeID: peer.id, hostName: peer.hostName, portID: "fixture-port",
        portPath: "/dev/test-serial", label: "Test serial fixture", localPath: "/dev/test-virtual-only",
        connected: true, baudRate: 9600,
        records: [{ sequence: 1, time: "2026-11-16T07:00:00.123Z", kind: "RX", data: Buffer.from([255, 0, 13, 10, 195, 169]).toString("base64") }],
      };
      response.records.push(...Array.from({ length: 70 }, (_, index) => ({
        sequence: index + 2, time: "2026-11-16T07:00:00.456Z", kind: "SYS", message: "history " + index,
      })));
      fixture.sessions = [response];
    } else if (operation === "send") {
      sent.push(body);
      const session = fixture.sessions.find(session => session.id === body.id);
      session.records.push({
        sequence: Math.max(0, ...session.records.map(record => record.sequence)) + 1,
        time: "2026-11-16T07:00:01.234Z", kind: "TX", data: body.data,
      });
      return route.fulfill({ status: 204 });
    } else if (operation === "clear") {
      fixture.sessions.find(session => session.id === body.id).records = [];
      return route.fulfill({ status: 204 });
    } else if (operation === "disconnect") {
      fixture.sessions.find(session => session.id === body.id).connected = false;
      return route.fulfill({ status: 204 });
    } else if (operation === "export") response = { saved: true, name: "fixture.log" };
    else if (operation === "update") response = { state: "unconfigured", configured: false };
    else if (operation === "preferences") response = { fontSize: 13, language: "zh-CN", theme: "light", closeToTray: true, autoUpdate: false };
    else return route.fulfill({ status: 202 });
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(response) });
  });

  await page.waitForSelector(".lp-device [data-port-key]");
  await page.getByRole("button", { name: "连接 Test serial fixture", exact: true }).click();
  await checkCloseFrame(page, ".lp-settings-head [data-connect-cancel]");
  assert.equal(await page.locator("[data-windows-pair]").isVisible(), platform === "windows");
  if (platform === "windows") {
    await page.getByRole("textbox", { name: "本地 COM 端口", exact: true }).fill("COM121");
    await page.getByRole("textbox", { name: "com0com 配对端口", exact: true }).fill("COM122");
  }
  await page.locator("[data-connect-submit]").click();
  await page.waitForFunction(() => document.querySelector("[data-connect-error]").textContent.includes("串口被占用"));
  assert.equal(await page.locator("[data-mapping]").count(), 0);
  await page.getByRole("spinbutton", { name: "波特率", exact: true }).fill("9600");
  await page.locator("[data-connect-submit]").click();
  await page.waitForSelector("[data-mapping]");
  assert.ok((await page.locator("[data-mappings]").innerText()).includes("/dev/test-virtual-only"));
  await page.locator("[data-nav=share]").click();
  await Promise.all([
    page.waitForResponse(response => new URL(response.url()).pathname === "/native/v1/share" && response.request().method() === "PUT"),
    page.getByRole("checkbox", { name: "共享 /dev/local-fixture", exact: true }).check(),
  ]);
  assert.deepEqual(shared[0], { id: "local-test", shared: true });
  for (const type of ["usb", "bluetooth", "uart"]) {
    assert.equal(await page.locator(`.lp-shareitem [data-transport="${type}"]`).count(), 1);
  }
  await page.locator("[data-nav=monitor]").click();
  await page.waitForFunction(() => {
    const log = document.querySelector("[data-log]");
    return log.scrollHeight - log.scrollTop - log.clientHeight < 3;
  });
  assert.equal(await page.locator("[data-hex]").isChecked(), false);
  assert.equal(await page.locator("[data-format], [data-filter], select[name=ending]").count(), 0);
  await page.locator("[data-hex]").check();
  assert.ok((await page.getByRole("log").innerText()).includes("ff 00 0d 0a c3 a9"));
  assert.ok((await page.locator(".lp-logline time").first().innerText()).endsWith(".123"));
  await page.getByLabel("发送内容", { exact: true }).fill("must-not-send");
  await page.keyboard.press("Control+f");
  await page.getByLabel("搜索日志", { exact: true }).fill("ff");
  await page.getByLabel("搜索日志", { exact: true }).press("Enter");
  assert.equal(await page.locator("[data-search-count]").textContent(), "1/1");
  assert.equal(sent.length, 0);
  assert.equal(await page.getByLabel("发送内容", { exact: true }).inputValue(), "must-not-send");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => document.querySelector("[data-log-search]").hidden);
  await page.locator("[data-log]").evaluate(node => { node.scrollTop = 0; });
  await page.getByLabel("发送内容", { exact: true }).fill("ping");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForFunction(() => document.querySelector(".lp-send input").value === "");
  assert.equal(Buffer.from(sent[0].data, "base64").toString(), "ping\r\n");
  await page.locator("[data-log-latest]").waitFor({ state: "visible" });
  await page.locator("[data-log-latest]").click();
  await page.waitForFunction(() => {
    const log = document.querySelector("[data-log]");
    return log.scrollHeight - log.scrollTop - log.clientHeight < 3;
  });
  await page.locator("[data-hex]").uncheck();
  const remoteSession = fixture.sessions[0];
  const receive = content => {
    remoteSession.records.push({
      sequence: Math.max(0, ...remoteSession.records.map(record => record.sequence)) + 1,
      time: "2026-11-16T07:00:02.345Z", kind: "RX", data: Buffer.from(content).toString("base64"),
    });
  };
  const send = async command => {
    await page.locator("[data-connect-dialog]").waitFor({ state: "hidden" });
    await page.getByLabel("发送内容", { exact: true }).fill(command);
    assert.equal(await page.getByLabel("发送内容", { exact: true }).inputValue(), command);
    await page.getByRole("button", { name: "发送", exact: true }).click();
    await page.waitForFunction(() => document.querySelector(".lp-send input").value === "");
    return Buffer.from(sent.at(-1).data, "base64").toString();
  };
  receive("line-feed\n");
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("line-feed"));
  assert.equal(await send("auto-lf"), "auto-lf\n");
  receive("carriage-return\r");
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("carriage-return"));
  assert.equal(await send("auto-cr"), "auto-cr\r");
  receive("\n");
  const splitCount = remoteSession.records.length;
  await page.waitForFunction(count => document.querySelectorAll(".lp-logline").length === count, splitCount);
  assert.equal(await send("auto-crlf"), "auto-crlf\r\n");
  for (let index = 0; index < 90; index++) receive((index % 35 === 5 ? "needle " : "row ") + index + "\n");
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("row 89"));
  await page.keyboard.press("Meta+f");
  await page.getByLabel("搜索日志", { exact: true }).fill("needle");
  await settleMotion(page);
  assert.equal(await page.locator("[data-search-count]").textContent(), "1/3");
  await page.locator("[data-log-query]").evaluate(node => node.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", isComposing: true, bubbles: true })));
  assert.equal(await page.locator("[data-search-count]").textContent(), "1/3", "IME composition must not advance the selected match");
  assert.equal(await page.locator(".lp-logline").count(), remoteSession.records.length, "search must retain unmatched log rows");
  await page.locator("[data-search-next]").click();
  await page.waitForFunction(() => document.querySelector("[data-search-count]").textContent === "2/3");
  await page.waitForFunction(() => {
    const row = document.querySelector('[data-search-current="true"]').getBoundingClientRect();
    const log = document.querySelector("[data-log]").getBoundingClientRect();
    return Math.abs((row.top + row.bottom - log.top - log.bottom) / 2) < 3;
  });
  await page.screenshot({ path: path.join(output, `${browserName}-monitor-search.png`) });
  const currentMatch = await page.locator('[data-search-current="true"]').getAttribute("data-record-key");
  const scrollPosition = await page.locator("[data-log]").evaluate(node => node.scrollTop);
  receive("background update\n");
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("background update"));
  assert.equal(await page.locator('[data-search-current="true"]').getAttribute("data-record-key"), currentMatch);
  assert.ok(Math.abs(await page.locator("[data-log]").evaluate(node => node.scrollTop) - scrollPosition) < 3);
  while (remoteSession.records.length < 500) receive("retained tail " + remoteSession.records.length + "\n");
  await page.waitForFunction(() => document.querySelectorAll(".lp-logline").length === 500);
  const readingPosition = () => page.locator("[data-log]").evaluate(log => {
    const viewport = log.getBoundingClientRect();
    const row = [...log.querySelectorAll("[data-record-key]")].find(row => row.getBoundingClientRect().bottom > viewport.top);
    return { key: row.dataset.recordKey, offset: row.getBoundingClientRect().top - viewport.top };
  });
  const retainAndCheckPosition = async label => {
    const position = await readingPosition();
    remoteSession.records.splice(0, 12);
    receive(label + "\n");
    await page.waitForFunction(label => document.querySelector("[data-log]").textContent.includes(label), label);
    const retained = await readingPosition();
    assert.equal(retained.key, position.key, label + ": visible record changed after eviction");
    assert.ok(Math.abs(retained.offset - position.offset) < 3, label + ": visible record moved after eviction");
  };
  await retainAndCheckPosition("search eviction");
  assert.equal(await page.locator('[data-search-current="true"]').getAttribute("data-record-key"), currentMatch);
  beforeStateResponse = async () => {
    const start = await page.locator("[data-log]").evaluate(log => log.scrollTop);
    await page.locator("[data-search-next]").click();
    await page.waitForFunction(start => {
      const row = document.querySelector('[data-search-current="true"]').getBoundingClientRect();
      const log = document.querySelector("[data-log]");
      const viewport = log.getBoundingClientRect();
      return log.scrollTop > start + 1 && Math.abs((row.top + row.bottom - viewport.top - viewport.bottom) / 2) > 20;
    }, start);
    remoteSession.records.splice(0, 12);
    receive("animation eviction\n");
  };
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("animation eviction"));
  await page.waitForFunction(() => {
    const row = document.querySelector('[data-search-current="true"]').getBoundingClientRect();
    const log = document.querySelector("[data-log]").getBoundingClientRect();
    return Math.abs((row.top + row.bottom - log.top - log.bottom) / 2) < 3;
  }, null, { timeout: 2000 });
  await page.locator("[data-log]").evaluate(log => new Promise(resolve => {
    let previous = log.scrollTop;
    let stableSince = performance.now();
    const frame = now => {
      if (log.scrollTop !== previous) { previous = log.scrollTop; stableSince = now; }
      if (now - stableSince >= 100) resolve();
      else requestAnimationFrame(frame);
    };
    requestAnimationFrame(frame);
  }));
  assert.ok(await page.evaluate(() => {
    const row = document.querySelector('[data-search-current="true"]').getBoundingClientRect();
    const log = document.querySelector("[data-log]").getBoundingClientRect();
    return Math.abs((row.top + row.bottom - log.top - log.bottom) / 2) < 3;
  }), "an old animation frame moved the match away after log eviction");
  await page.locator("[data-search-prev]").click();
  await page.waitForFunction(() => {
    const row = document.querySelector('[data-search-current="true"]').getBoundingClientRect();
    const log = document.querySelector("[data-log]").getBoundingClientRect();
    return Math.abs((row.top + row.bottom - log.top - log.bottom) / 2) < 3;
  });
  await page.locator("[data-search-prev]").click();
  assert.equal(await page.locator("[data-search-count]").textContent(), "1/3");
  await page.getByLabel("搜索日志", { exact: true }).press("Shift+Enter");
  assert.equal(await page.locator("[data-search-count]").textContent(), "3/3");
  await page.getByLabel("搜索日志", { exact: true }).fill("missing-query");
  assert.equal(await page.locator("[data-search-count]").textContent(), "0/0");
  assert.equal(await page.locator("[data-search-next]").isDisabled(), true);
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => document.querySelector("[data-log-search]").hidden);
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.keyboard.press("Control+f");
  await page.locator("[data-log-query]").fill("needle");
  await page.locator("[data-search-next]").click();
  assert.equal(await page.locator("[data-log-search]").evaluate(node => node.getAnimations().length), 0);
  assert.ok(await page.evaluate(() => {
    const row = document.querySelector('[data-search-current="true"]').getBoundingClientRect();
    const log = document.querySelector("[data-log]").getBoundingClientRect();
    return Math.abs((row.top + row.bottom - log.top - log.bottom) / 2) < 3;
  }));
  await page.keyboard.press("Escape");
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await settleMotion(page);
  await page.locator("[data-log]").evaluate(log => {
    const row = log.querySelectorAll("[data-record-key]")[200];
    log.scrollTop += row.getBoundingClientRect().top - log.getBoundingClientRect().top + 7;
  });
  await retainAndCheckPosition("reading eviction");
  const hiddenPosition = await readingPosition();
  await page.locator("[data-nav=share]").click();
  remoteSession.records.splice(0, 12);
  receive("hidden eviction\n");
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("hidden eviction"));
  await page.locator("[data-nav=monitor]").click();
  await settleMotion(page);
  const restoredPosition = await readingPosition();
  assert.equal(restoredPosition.key, hiddenPosition.key, "hidden monitor lost its visible record after eviction");
  assert.ok(Math.abs(restoredPosition.offset - hiddenPosition.offset) < 3, "hidden monitor lost its record offset after eviction");
  await page.locator("[data-port-picker-toggle]").click();
  for (const type of ["usb", "bluetooth", "uart", "serial"]) {
    assert.ok(await page.locator(`[data-port-option] [data-transport="${type}"]`).count() > 0);
  }
  await settleMotion(page);
  await page.screenshot({ path: path.join(output, `${browserName}-monitor-ports.png`) });
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "导出完整会话", exact: true }).click();
  await page.waitForFunction(() => !document.querySelector("[data-live-toast]").hidden);
  assert.ok((await page.locator("[data-live-toast]").innerText()).includes("fixture.log"));
  await settleMotion(page);
  assert.equal(await page.evaluate(() => {
    const toast = document.querySelector("[data-live-toast]").getBoundingClientRect();
    const commandbar = document.querySelector(".lp-commandbar").getBoundingClientRect();
    return toast.bottom <= commandbar.top - 8;
  }), true, "export feedback must leave the command bar visible");
  await page.getByRole("button", { name: "暂停显示", exact: true }).click();
  await page.getByRole("button", { name: "清空日志", exact: true }).click();
  await page.waitForFunction(() => document.querySelectorAll(".lp-logline").length === 0);
  await page.locator("[data-port-picker-toggle]").click();
  await page.locator('[data-port-option][data-path="/dev/local-fixture"]').click();
  assert.equal(await page.locator("[data-windows-pair]").isVisible(), false);
  assert.ok((await page.locator("[data-connect-target]").textContent()).includes("/dev/local-fixture"));
  const inventory = fixture.localPorts;
  fixture.localPorts = inventory.filter(port => port.id !== "local-test");
  await page.waitForFunction(() => document.querySelector("[data-connect-submit]").disabled &&
    document.querySelector("[data-connect-error]").textContent.includes("已离线"));
  assert.equal(openedLocal.length, 0, "removing a selected port must not silently open another port");
  fixture.localPorts = inventory;
  await page.waitForFunction(() => !document.querySelector("[data-connect-submit]").disabled);
  await page.locator("[data-connect-submit]").click();
  await page.waitForFunction(() => document.querySelector("[data-session-label]").textContent.includes("/dev/local-fixture"));
  assert.equal(openedLocal.length, 1);
  assert.equal(await page.locator("[data-mapping]").count(), 1);
  await page.locator("[data-hex]").uncheck();
  assert.ok((await page.getByRole("log").innerText()).includes("local only"));
  assert.equal((await page.getByRole("log").innerText()).includes("ff 00"), false);
  assert.equal(await send("local-default"), "local-default\r\n", "line endings must be learned independently for each session");
  const localSession = fixture.sessions.find(session => session.id === "local-session");
  localSession.records.push({
    sequence: Math.max(...localSession.records.map(record => record.sequence)) + 1,
    time: "2026-11-16T07:02:04.456Z", kind: "RX", data: Buffer.from("local line-feed\n").toString("base64"),
  });
  await page.waitForFunction(() => document.querySelector("[data-log]").textContent.includes("local line-feed"));
  await page.getByRole("button", { name: "关闭串口", exact: true }).click();
  await page.waitForFunction(() => document.querySelector("[data-session-status]").textContent === "未连接");
  await page.locator("[data-port-picker-toggle]").click();
  await page.locator('[data-port-option][data-path="/dev/local-fixture"]').first().click();
  await page.locator("[data-connect-submit]").click();
  await page.waitForFunction(() => document.querySelector("[data-session-status]").textContent === "监听中");
  assert.equal(await send("reopened-default"), "reopened-default\r\n", "reopening the same session ID must reset learned line endings");
  await page.getByRole("button", { name: "关闭串口", exact: true }).click();
  await page.waitForFunction(() => document.querySelector("[data-session-status]").textContent === "未连接");

  for (const viewport of [{ width: 1120, height: 780 }, { width: 760, height: 540 }]) {
    await page.setViewportSize(viewport);
    for (const screen of ["connect", "share", "monitor", "plugins"]) {
      await page.locator(`[data-nav=${screen}]`).click();
      await settleMotion(page);
      assert.equal(await page.evaluate(() =>
        document.documentElement.scrollHeight > document.documentElement.clientHeight ||
        document.documentElement.scrollWidth > document.documentElement.clientWidth), false);
      const aligned = await page.evaluate(() => {
        const active = document.querySelector('[data-nav][aria-current="page"]').getBoundingClientRect();
        const indicator = document.querySelector(".lp-nav-indicator").getBoundingClientRect();
        return Math.abs(active.top - indicator.top) < 1 && Math.abs(active.height - indicator.height) < 1;
      });
      assert.equal(aligned, true, "navigation indicator must follow the selected item");
      await page.screenshot({ path: path.join(output, `${browserName}-${screen}-${viewport.width}.png`) });
    }
  }
  await page.locator("[data-settings-open]").click();
  await page.locator("[data-preference=fontSize]").selectOption("18");
  await page.locator("[data-preference=theme]").selectOption("dark");
  await page.locator("[data-settings-close]").click();
  await page.locator("[data-settings-dialog]").waitFor({ state: "hidden" });
  await page.locator("[data-nav=monitor]").click();
  await settleMotion(page);
  assert.ok(await page.evaluate(() => {
    const title = document.querySelector(".lp-monitor-title").getBoundingClientRect();
    return [...document.querySelectorAll(".lp-monitor-actions button, [data-port-picker-toggle]")].every(node => {
      const bounds = node.getBoundingClientRect();
      return Math.abs((bounds.top + bounds.bottom - title.top - title.bottom) / 2) < 3;
    });
  }), "large-font desktop monitoring controls must remain in one row");
  await page.screenshot({ path: path.join(output, `${browserName}-monitor-large-font.png`) });
  await page.locator("[data-settings-open]").click();
  await page.locator("[data-preference=fontSize]").selectOption("13");
  await page.locator("[data-preference=theme]").selectOption("light");
  await page.locator("[data-settings-close]").click();
  await page.locator("[data-settings-dialog]").waitFor({ state: "hidden" });
  await page.locator("[data-nav=connect]").click();
  await page.getByRole("button", { name: "断开 Test serial fixture", exact: true }).click();
  await page.waitForFunction(() => document.querySelectorAll("[data-mapping]").length === 0);
  assert.equal(sent.length, 6);
  assert.deepEqual(errors, []);
  await page.screenshot({ path: path.join(output, `${browserName}-connections.png`) });
}

async function checkEmptyLayout(page, browserName) {
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await initializePage(page);
  await page.route("**/native/v1/state", route => route.fulfill({
    contentType: "application/json", body: JSON.stringify({
      network: { state: "Running", nodeID: "b".repeat(64), hostName: "layout-test", config: { relayMode: "disabled", relayURLs: [] } },
      devices: [], localPorts: [], sharedPorts: [], sessions: [], refreshing: false,
    }),
  }));
  await page.route("**/native/v1/network", route => route.fulfill({
    contentType: "application/json", body: '{"state":"Running","nodeID":"layout-id","config":{"relayMode":"disabled","relayURLs":[]}}',
  }));
  await page.goto(origin, { waitUntil: "networkidle" });
  await page.waitForFunction(() => !document.querySelector("[data-connection-empty]").hidden);
  for (const viewport of [{ width: 1120, height: 780 }, { width: 760, height: 540 }, { width: 390, height: 780 }]) {
    await page.setViewportSize(viewport);
    const layout = await page.evaluate(() => {
      const empty = document.querySelector("[data-connection-empty]").getBoundingClientRect();
      const title = document.querySelector("[data-empty-title]").getBoundingClientRect();
      const copy = document.querySelector("[data-empty-copy]").getBoundingClientRect();
      const button = document.querySelector("[data-live-add]").getBoundingClientRect();
      return {
        centerDifference: Math.abs((title.top + button.bottom - empty.top - empty.bottom) / 2),
        horizontalDifference: Math.abs((button.left + button.right - empty.left - empty.right) / 2),
        titleToCopy: copy.top - title.bottom, copyToButton: button.top - copy.bottom,
        overflow: document.documentElement.scrollHeight > document.documentElement.clientHeight ||
          document.documentElement.scrollWidth > document.documentElement.clientWidth,
      };
    });
    assert.ok(layout.centerDifference < 2, JSON.stringify(layout));
    assert.ok(layout.horizontalDifference < 1, JSON.stringify(layout));
    assert.ok(Math.abs(layout.titleToCopy - 8) < 1, JSON.stringify(layout));
    assert.ok(Math.abs(layout.copyToButton - 20) < 1, JSON.stringify(layout));
    assert.equal(layout.overflow, false);
  }
  assert.deepEqual(errors, []);
  await page.locator("[data-nav=monitor]").click();
  await page.locator("[data-port-picker-toggle]").click();
  assert.equal(await page.locator("[data-port-picker]").isVisible(), true);
  await page.keyboard.press("Escape");
  assert.equal(await page.locator("[data-port-picker]").isVisible(), false);
  await page.screenshot({ path: path.join(output, `${browserName}-empty.png`) });
}

async function checkDeviceManagement(page, browserName) {
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await initializePage(page);
  await page.addInitScript(() => {
    window.copiedText = "";
    Object.defineProperty(navigator, "clipboard", {
      configurable: true, value: { writeText: async text => { window.copiedText = text; } },
    });
  });
  const id = "a".repeat(64);
  const peer = { id: "b".repeat(64), hostName: "Paired test machine" };
  const network = { state: "Running", nodeID: id, config: { relayMode: "disabled", relayURLs: [] } };
  let peers = [];
  let removed = false;
  let reset = false;
  await page.route("**/native/v1/network", async route => {
    if (route.request().method() === "PUT") {
      const cfg = route.request().postDataJSON();
      assert.equal(cfg.relayMode, "disabled");
      assert.deepEqual(cfg.relayURLs, []);
      network.config = cfg;
      network.restartRequired = true;
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(network) });
  });
  let relayUpdate={state:"available",version:1,next:2};
  await page.route("**/native/v1/relay-update",route=>route.fulfill({contentType:"application/json",body:JSON.stringify(relayUpdate)}));
  await page.route("**/native/v1/relay-update/apply",route=>{
    assert.equal(route.request().headers()["x-linkport-token"],"browser-test-only");
    relayUpdate={state:"up-to-date",version:2};return route.fulfill({status:202,body:""});
  });
  await page.route("**/native/v1/devices/invite", route => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ code: "linkport1.test-invitation", expiresAt: new Date(Date.now() + 8 * 60 * 60 * 1000).toISOString() }),
  }));
  await page.route("**/native/v1/devices", async route => {
    const request = route.request();
    if (request.method() === "POST") {
      const { code } = request.postDataJSON();
      if (code === "expired") {
        await route.fulfill({ status: 409, body: "配对邀请已过期，请重新生成" });
        return;
      }
      assert.equal(code, "linkport1.from-other-device");
      peers = [peer];
      await route.fulfill({ contentType: "application/json", body: JSON.stringify(peer) });
      return;
    }
    if (request.method() === "DELETE") {
      assert.equal(request.postDataJSON().id, peer.id);
      removed = true;
      peers = [];
    }
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(peers) });
  });
  await page.route("**/native/v1/identity/reset", async route => {
    const { confirmation } = route.request().postDataJSON();
    if (confirmation !== id) {
      await route.fulfill({ status: 409, body: "请使用完整的本机设备 ID 确认重置" });
      return;
    }
    reset = true;
    network.nodeID = "c".repeat(64);
    await route.fulfill({ contentType: "application/json", body: JSON.stringify(network) });
  });
  await page.goto(origin, { waitUntil: "networkidle" });
  await page.locator("[data-update-badge]").waitFor({state:"visible"});
  await page.locator("[data-update-badge]").click();
  await page.locator("[data-relay-update-action]").click();
  await page.locator("[data-update-badge]").waitFor({state:"hidden"});
  relayUpdate={state:"bundled",version:1};
  await page.waitForFunction(()=>document.querySelector("[data-relay-update-status]").textContent.includes("随应用更新"));
  assert.equal(await page.locator("[data-relay-update-action]").isVisible(),false);
  await page.locator("[data-settings-close]").click();
  // A click completes before the modal's closing animation. Wait until its
  // backdrop leaves the top layer before clicking the underlying title bar.
  await page.locator("[data-settings-dialog]").waitFor({ state: "hidden" });

  await page.locator("[data-device-manager-open]").first().click();
  await page.locator("[data-devices-dialog]").waitFor({ state: "visible" });
  await checkCloseFrame(page, "[data-devices-close]");
  await page.waitForFunction(() => document.querySelector("[data-device-id]").textContent.length === 64);
  assert.equal(await page.locator("[data-device-id]").textContent(), id);
  assert.equal(await page.locator("[data-device-id-short]").textContent(), "aaaaaaaaaa…aaaaaa");
  assert.equal(await page.locator("[data-device-id]").isVisible(), false);
  assert.equal(await page.locator("[data-invitation-panel]").isVisible(), false);
  assert.equal(await page.locator("[data-pair-add]").isDisabled(), true);
  await settleMotion(page);
  const compactHeight = (await page.locator("[data-devices-dialog]").boundingBox()).height;
  await page.screenshot({ path: path.join(output, `${browserName}-devices-default.png`) });
  const expansion = await page.locator("[data-device-id-toggle]").evaluate(button => {
    button.click();
    const node = document.getElementById(button.getAttribute("aria-controls"));
    const animation = node.getAnimations()[0];
    animation.pause();
    animation.currentTime = 0;
    const start = node.getBoundingClientRect().height;
    const dialogStart = node.closest("dialog").getBoundingClientRect().height;
    animation.currentTime = 100;
    const middle = node.getBoundingClientRect().height;
    const dialogMiddle = node.closest("dialog").getBoundingClientRect().height;
    animation.currentTime = 220;
    const end = node.getBoundingClientRect().height;
    const dialogEnd = node.closest("dialog").getBoundingClientRect().height;
    animation.play();
    return { start, middle, end, dialogStart, dialogMiddle, dialogEnd };
  });
  assert.ok(expansion.start < expansion.middle && expansion.middle < expansion.end, JSON.stringify(expansion));
  assert.ok(expansion.dialogStart < expansion.dialogMiddle && expansion.dialogMiddle < expansion.dialogEnd, JSON.stringify(expansion));
  await settleMotion(page);
  assert.equal(await page.locator("[data-device-id]").isVisible(), true);
  await page.locator("[data-device-id-copy]").click();
  assert.equal(await page.evaluate(() => window.copiedText), id);
  await page.evaluate(() => {
    const button = document.querySelector("[data-device-id-toggle]");
    button.click(); button.click(); button.click();
  });
  await settleMotion(page);
  assert.equal(await page.locator("[data-device-id]").isVisible(), false);
  assert.equal(await page.locator("[data-device-id-toggle]").getAttribute("aria-expanded"), "false");
  await page.locator("[data-invite-create]").click();
  await page.waitForFunction(() => document.querySelector("[data-invitation]").value.startsWith("linkport1."));
  await settleMotion(page);
  assert.equal(await page.locator("[data-invitation]").inputValue(), "linkport1.test-invitation");
  assert.match(await page.locator("[data-invitation-expiry]").innerText(), /8 小时/);
  assert.equal(await page.locator("[data-invitation-panel]").isVisible(), true);
  assert.ok((await page.locator("[data-devices-dialog]").boundingBox()).height > compactHeight + 30);
  await page.locator("[data-invite-copy]").click();
  assert.equal(await page.evaluate(() => window.copiedText), "linkport1.test-invitation");
  await page.evaluate(() => {
    navigator.clipboard.writeText = async () => { throw new Error("clipboard unavailable"); };
    document.execCommand = command => {
      if (command !== "copy") return false;
      const input = document.querySelector("[data-invitation]");
      window.copiedText = input.value.slice(input.selectionStart, input.selectionEnd);
      return true;
    };
    window.copiedText = "";
  });
  await page.locator("[data-invite-copy]").click();
  assert.equal(await page.evaluate(() => window.copiedText), "linkport1.test-invitation");
  await page.evaluate(() => {
    navigator.clipboard.writeText = async text => { window.copiedText = text; };
  });
  await page.setViewportSize({ width: 1120, height: 600 });
  await page.locator("[data-pair-code]").fill("expired");
  await page.locator("[data-pair-add]").click();
  await page.waitForFunction(() => document.querySelector("[data-device-message]").textContent.includes("已过期"));
  await page.waitForFunction(() => {
    const message = document.querySelector("[data-device-message]");
    const body = document.querySelector("[data-devices-dialog] .lp-settings-body").getBoundingClientRect();
    const bounds = message.getBoundingClientRect();
    return bounds.top >= body.top && bounds.bottom <= body.bottom &&
      message.closest(".lp-settings-section").contains(document.querySelector("[data-pair-code]"));
  });
  assert.equal(await page.locator("[data-pair-code]").inputValue(), "expired");
  await settleMotion(page);
  await page.screenshot({ path: path.join(output, `${browserName}-pairing-error-short-window.png`) });
  await page.setViewportSize({ width: 1120, height: 780 });
  await page.locator("[data-pair-code]").fill("linkport1.from-other-device");
  await page.locator("[data-pair-add]").click();
  await page.locator("[data-device-remove]").waitFor();
  assert.equal(await page.locator("[data-paired-count]").textContent(), "1");
  assert.equal(await page.locator("[data-pair-code]").inputValue(), "");
  assert.equal(await page.locator("[data-pair-add]").isDisabled(), true);
  await page.locator("[data-device-remove]").click();
  await page.waitForFunction(() => document.querySelector("[data-paired-devices]").textContent.includes("尚未配对"));
  assert.equal(removed, true);
  await page.locator("[data-network-toggle]").click();
  await settleMotion(page);
  assert.equal(await page.locator("[data-relay-urls]").count(),0);
  assert.equal(await page.locator("[data-discovery-url]").count(),0);
  assert.equal(await page.locator("[data-hosted-relay-save]").count(),0);
  assert.equal(await page.locator('[data-relay-mode] option[value="custom"]').count(),0);
  await page.locator("[data-relay-mode]").selectOption("default");
  await settleMotion(page);
  assert.equal(await page.locator("[data-network-mode-label]").innerText(),"公共中继");
  await page.screenshot({path:path.join(output,`${browserName}-devices-managed.png`)});
  await page.locator("[data-relay-mode]").selectOption("disabled");
  await settleMotion(page);
  await page.locator("[data-network-save]").click();
  await page.waitForFunction(() => document.querySelector("[data-device-message]").textContent.includes("请重启"));
  await page.locator("[data-network-toggle]").click();
  await page.locator("[data-identity-toggle]").click();
  assert.equal(await page.locator("[data-identity-reset]").isDisabled(), true);
  await page.locator("[data-reset-confirmation]").fill("wrong");
  await page.locator("[data-identity-reset]").click();
  await page.waitForFunction(() => document.querySelector("[data-device-message]").textContent.includes("完整"));
  assert.equal(reset, false);
  await page.locator("[data-reset-confirmation]").fill(id);
  await page.locator("[data-identity-reset]").click();
  await page.waitForFunction(() => document.querySelector("[data-device-id]").textContent.startsWith("cccc"));
  assert.equal(reset, true);
  await page.screenshot({ path: path.join(output, `${browserName}-devices.png`) });
  await page.locator("[data-devices-close]").click();
  await page.waitForFunction(() => document.querySelector("[data-invitation]").value === "");
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.waitForFunction(() => window.matchMedia("(prefers-reduced-motion: reduce)").matches);
  await page.locator("[data-device-manager-open]").first().click();
  await settleMotion(page);
  assert.equal(await page.locator("[data-device-id]").isVisible(), false);
  assert.equal(await page.locator("[data-invitation-panel]").isVisible(), false);
  assert.equal(await page.locator("#lp-network-options").isVisible(), false);
  assert.equal(await page.locator("#lp-identity-options").isVisible(), false);
  await page.locator("[data-network-toggle]").press("Enter");
  await page.locator("#lp-network-options").waitFor({ state: "visible" });
  assert.equal(await page.locator("[data-network-toggle]").getAttribute("aria-expanded"), "true");
  assert.equal(await page.locator("#lp-network-options").isVisible(), true);
  assert.equal(await page.locator("#lp-network-options").evaluate(node => node.getAnimations().length), 0);
  await page.keyboard.press("Escape");
  await page.locator("[data-devices-dialog]").waitFor({ state: "hidden" });
  assert.equal(await page.locator("[data-devices-dialog]").isVisible(), false);
  await page.locator("[data-settings-open]").click();
  await page.locator('[data-preference="fontSize"]').selectOption("18");
  await page.locator('[data-preference="theme"]').selectOption("dark");
  await page.screenshot({ path: path.join(output, `${browserName}-settings-dark.png`) });
  await page.locator("[data-settings-close]").click();
  await page.setViewportSize({ width: 760, height: 540 });
  await page.locator("[data-device-manager-open]").first().click();
  await page.screenshot({ path: path.join(output, `${browserName}-devices-dark-large-font.png`) });
  assert.equal(await page.evaluate(() => {
    const bounds = document.querySelector("[data-devices-dialog]").getBoundingClientRect();
    return bounds.top >= 0 && bounds.left >= 0 && bounds.bottom <= innerHeight && bounds.right <= innerWidth;
  }), true);
  await page.locator("[data-devices-close]").click();
  await page.locator("[data-settings-open]").click();
  await page.locator('[data-preference="language"]').selectOption("en");
  assert.equal(await page.locator("[data-nav=connect]").innerText(), "Device");
  await page.locator("[data-settings-close]").click();
  await page.locator("[data-device-manager-open]").first().click();
  await page.locator("[data-network-toggle]").click();
  await page.locator("[data-relay-mode]").selectOption("default");
  assert.equal(await page.locator("[data-network-mode-label]").innerText(), "Public relays");
  await page.screenshot({ path: path.join(output, `${browserName}-devices-english.png`) });
  assert.deepEqual(errors, []);
}

async function checkTopbar(page, browserName) {
  await initializePage(page);
  let network = { state: "Running", relayConnected: false, detectedRelayCount: 0, publicRelays: ["https://one.example", "https://two.example"], nodeID: "a".repeat(64), config: {relayMode: "default", relayURLs: []} };
  let update = { configured: true, state: "up-to-date", version: "0.0.7", channel: "stable" };
  let unavailable = false;
  await page.route("**/native/v1/network", route => route.fulfill(unavailable ? {status:503,body:"Unavailable"} : {contentType:"application/json",body:JSON.stringify(network)}));
  await page.route("**/native/v1/state", route => route.fulfill(unavailable ? {status:503,body:"Unavailable"} : {contentType:"application/json",body:JSON.stringify({network,localPorts:[],sharedPorts:[],devices:[],sessions:[]})}));
  await page.route("**/native/v1/devices", route => route.fulfill({contentType:"application/json",body:"[]"}));
  await page.route("**/native/v1/update", route => route.fulfill({contentType:"application/json",body:JSON.stringify(update)}));
  await page.goto(origin, {waitUntil:"networkidle"});
  const status = page.locator("[data-relay-status]");
  await page.waitForFunction(() => document.querySelector("[data-network-state]").textContent === "中继 0");
  assert.equal(await status.getAttribute("data-connected"), "false");
  const repeatedStatusMutations = await page.evaluate(() => new Promise(resolve => {
    const indicator = document.querySelector("[data-relay-status]");
    let mutations = 0;
    const observer = new MutationObserver(records => { mutations += records.length; });
    observer.observe(indicator, {attributes:true,childList:true,characterData:true,subtree:true});
    document.querySelector("#linkport-review").addEventListener("linkport:network", () => {
      queueMicrotask(() => { observer.disconnect(); resolve(mutations); });
    }, {once:true});
  }));
  assert.equal(repeatedStatusMutations, 0, "unchanged relay polls must not repeatedly announce a live status");
  const disconnectedColor = await status.locator(".lp-dot").evaluate(node => getComputedStyle(node).backgroundColor);
  assert.equal(await page.locator("[data-update-badge]").isVisible(), false);
  await status.click();
  assert.equal(await page.locator("[data-devices-dialog]").isVisible(), false, "relay status must not open pairing");
  await page.locator(".lp-titletools [data-device-manager-open]").click();
  await page.locator("[data-devices-dialog]").waitFor({state:"visible"});
  await page.locator("[data-devices-close]").click();
  await page.locator("[data-devices-dialog]").waitFor({state:"hidden"});
  network = {...network, relayConnected: true, detectedRelayCount: 2};
  await page.waitForFunction(() => document.querySelector("[data-relay-status]").dataset.connected === "true" && document.querySelector("[data-network-state]").textContent === "中继 2");
  const connectedColor = await status.locator(".lp-dot").evaluate(node => getComputedStyle(node).backgroundColor);
  assert.notEqual(connectedColor, disconnectedColor);
  const rgb = connectedColor.match(/\d+/g).map(Number);
  assert.ok(rgb[1] > rgb[0] && rgb[1] > rgb[2], "a connected relay uses a green indicator");
  update = {...update, state:"available", next:"0.0.8"};
  await page.locator("[data-settings-open]").click();
  await page.locator("[data-update-badge]").waitFor({state:"visible"});
  assert.equal(await page.locator("[data-update-label]").innerText(), "更新");
  assert.equal(await page.locator("[data-update-badge] use").getAttribute("href"), "#lp-icon-update");
  await page.locator("[data-settings-close]").click();
  await page.locator("[data-settings-dialog]").waitFor({state:"hidden"});
  await page.screenshot({path:path.join(output,`${browserName}-topbar-update-available.png`)});
  await page.locator("[data-update-badge]").click();
  await page.locator("[data-settings-dialog]").waitFor({state:"visible"});
  update = {...update,state:"downloading",progress:0.42};
  await page.waitForFunction(() => document.querySelector("[data-update-label]").textContent.includes("42%"));
  assert.equal(await page.locator("[data-update-badge]").isDisabled(),true);
  update = {...update,state:"ready"};
  await page.waitForFunction(() => document.querySelector("[data-update-label]").textContent === "更新就绪");
  assert.equal(await page.locator("[data-update-badge]").isDisabled(),false);
  await page.locator('[data-preference="language"]').selectOption("en");
  await page.locator('[data-preference="theme"]').selectOption("dark");
  await page.locator('[data-preference="fontSize"]').selectOption("18");
  await page.setViewportSize({width:760,height:540});
  await page.waitForFunction(() => document.querySelector("[data-network-state]").textContent === "Relays 2");
  assert.match(await status.getAttribute("aria-label"), /Relay connected/);
  assert.equal(await page.locator("[data-update-label]").innerText(),"Update ready");
  await page.locator("[data-settings-close]").click();
  await page.locator("[data-settings-dialog]").waitFor({state:"hidden"});
  await page.evaluate(async () => { await document.fonts.ready; });
  const frames = await page.locator(".lp-titlebar").evaluate(bar => [...bar.querySelectorAll(".lp-brand, .lp-titletools")].map(node=>{const b=node.getBoundingClientRect();return {left:b.left,right:b.right,top:b.top,bottom:b.bottom};}));
  assert.ok(frames[0].right <= frames[1].left && frames[1].right <= 760, "large-text titlebar must not overlap or clip controls");
  await page.screenshot({path:path.join(output,`${browserName}-topbar-dark-compact.png`)});
  network = {...network,relayConnected:false,detectedRelayCount:0,config:{relayMode:"disabled",relayURLs:[]}};
  await page.waitForFunction(() => document.querySelector("[data-relay-status]").dataset.connected === "false" && document.querySelector("[data-relay-status]").getAttribute("aria-label").includes("Direct only"));
  unavailable = true;
  await page.waitForFunction(() => document.querySelector("[data-network-state]").textContent === "Relays —");
  assert.equal(await status.getAttribute("data-connected"),"false");
}

async function checkApplicationUpdates(page, browserName) {
  await initializePage(page);
  let update = { configured: true, state: "available", version: "0.0.2-nightly.20261117000000.1", channel: "nightly", next: "0.0.2-nightly.20261118000000.2" };
  let restarted = false;
  await page.route("**/native/v1/update", route => route.fulfill({ contentType: "application/json", body: JSON.stringify(update) }));
  await page.route("**/native/v1/update/download", route => {
    assert.equal(route.request().headers()["x-linkport-token"], "browser-test-only");
    update.state = "ready";
    return route.fulfill({ status: 202, body: "" });
  });
  await page.route("**/native/v1/update/restart", route => {
    restarted = true;
    return route.fulfill({ status: 202, body: "" });
  });
  await page.goto(origin, { waitUntil: "networkidle" });
  await page.locator("[data-update-badge]").waitFor({ state: "visible" });
  assert.equal(await page.locator(".lp-version").innerText(), update.version);
  await page.locator("[data-update-badge]").click();
  assert.equal(await page.locator("[data-app-channel]").innerText(), "Nightly");
  await page.locator("[data-update-action]").click();
  await page.waitForFunction(() => document.querySelector("[data-update-action]").getAttribute("aria-label") === "重启并更新");
  assert.equal(restarted, false, "download must not restart the application");
  await page.screenshot({ path: path.join(output, `${browserName}-nightly-update-ready.png`) });
  await page.locator("[data-update-action]").click();
  assert.equal(restarted, true);
  for (const [code, message] of [
    ["check-timeout", "检查更新超时"],
    ["download-timeout", "下载超时"],
    ["verification-failed", "更新校验失败"],
    ["install-failed", "准备更新失败"],
  ]) {
    update = { ...update, state: "error", errorCode: code, error: "Get https://private.example/archive?token=fixture-only: failed" };
    await page.waitForFunction(message => document.querySelector("[data-update-status]").textContent.includes(message), message);
    const status = await page.locator("[data-update-status]").innerText();
    assert.equal(status.includes("private.example"), false, "raw download URLs must not be displayed");
    assert.equal(status.includes("服务暂不可用"), false);
    assert.equal(await page.locator("[data-update-action]").isDisabled(), false);
  }
  await page.locator('[data-preference="language"]').selectOption("en");
  update = { ...update, state: "error", errorCode: "download-timeout" };
  await page.waitForFunction(() => document.querySelector("[data-update-status]").textContent.includes("Download timed out"));
  await page.screenshot({ path: path.join(output, `${browserName}-update-download-timeout.png`) });
  update = { ...update, state: "downloading", errorCode: "", error: "", progress: 0.42 };
  await page.waitForFunction(() => document.querySelector("[data-update-status]").textContent.includes("Downloading · 42%"));
  assert.equal(await page.locator("[data-update-action]").isDisabled(), true);
  await page.screenshot({ path: path.join(output, `${browserName}-update-download-progress.png`) });
  update = { ...update, state: "package-ready", errorCode: "", error: "" };
  await page.waitForFunction(() => document.querySelector("[data-update-status]").textContent.includes("Package verified"));
  assert.equal(await page.locator("[data-update-action]").getAttribute("aria-label"), "Open installer");
  restarted = false;
  await page.locator("[data-update-action]").click();
  assert.equal(restarted, true, "verified DEB must use the installer action");
  update.state = "installer-opened";
  await page.waitForFunction(() => document.querySelector("[data-update-status]").textContent.includes("Complete installation"));
  assert.equal(await page.locator("[data-update-action]").isDisabled(), false);
  await page.locator('[data-preference="language"]').selectOption("zh-CN");
  update = { configured: false, state: "development", version: "0.0.2-dev.1794913200.g012345abcdef", channel: "dev" };
  await page.reload({ waitUntil: "networkidle" });
  await page.locator("[data-settings-open]").click();
  await page.waitForFunction(() => document.querySelector("[data-update-status]").textContent.includes("本地构建"));
  assert.equal(await page.locator("[data-update-action]").isDisabled(), true);
  assert.equal(await page.locator("[data-update-badge]").isVisible(), false);
}

(async () => {
  const results = [];
  for (const browserName of browsers) {
    let browser;
    let context;
    try {
      assert.ok(playwright[browserName], `unknown browser ${browserName}`);
      const launch = { headless: true };
      if (browserName === "chromium" && process.env.LINKPORT_CHROMIUM_PATH) {
        launch.executablePath = process.env.LINKPORT_CHROMIUM_PATH;
      }
      console.log(`[${browserName}] launching browser`);
      browser = await playwright[browserName].launch(launch);
      console.log(`[${browserName}] creating context`);
      context = await browser.newContext({ viewport: { width: 1120, height: 780 }, colorScheme: "light" });
      context.setDefaultTimeout(30000);
      context.setDefaultNavigationTimeout(30000);
      console.log(`[${browserName}] starting trace`);
      await context.tracing.start({ screenshots: true, snapshots: true });
      console.log(`[${browserName}] functional checks`);
      await checkFunctional(await context.newPage(), browserName);
      console.log(`[${browserName}] empty layout checks`);
      await checkEmptyLayout(await context.newPage(), browserName);
      console.log(`[${browserName}] device management checks`);
      await checkDeviceManagement(await context.newPage(), browserName);
      console.log(`[${browserName}] topbar checks`);
      await checkTopbar(await context.newPage(), browserName);
      console.log(`[${browserName}] update checks`);
      await checkApplicationUpdates(await context.newPage(), browserName);
      results.push({ browser: browserName, platform, status: "passed", source: "isolated API fixtures and bundled Wails runtime" });
    } catch (error) {
      results.push({ browser: browserName, platform, status: "failed", error: error.stack || String(error) });
      if (context) {
        for (const [index, page] of context.pages().entries()) {
          await page.screenshot({ path: path.join(output, `${browserName}-failure-${index}.png`) }).catch(() => {});
        }
      }
      process.exitCode = 1;
    } finally {
      console.log(`[${browserName}] stopping trace and closing browser`);
      if (context) await context.tracing.stop({ path: path.join(output, `${browserName}-trace.zip`) });
      if (browser) await browser.close();
      fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(results, null, 2));
      console.log(`[${browserName}] complete`);
    }
  }
  console.log(JSON.stringify(results));
})().catch(error => { console.error(error); process.exitCode = 1; });
