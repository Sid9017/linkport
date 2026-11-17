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
    devices: [peer], localPorts: [{ id: "local-test", path: "/dev/local-fixture", product: "Local fixture", shared: false }],
    sharedPorts: [], sessions: [], refreshing: false,
  };
  const sent = [];
  const openedLocal = [];
  const shared = [];
  let rejectConnect = true;
  await page.route("**/native/v1/**", async route => {
    const operation = new URL(route.request().url()).pathname.split("/").at(-1);
    assert.equal(route.request().headers()["x-linkport-token"], "browser-test-only");
    const body = route.request().method() === "GET" ? {} : JSON.parse(route.request().postData() || "{}");
    let response;
    if (operation === "state") response = fixture;
    else if (operation === "network") response = fixture.network;
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
      fixture.sessions.push(response);
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
      fixture.sessions = [response];
    } else if (operation === "send") {
      sent.push(body);
      fixture.sessions.find(session => session.id === body.id).records.push({
        sequence: sent.length + 1, time: "2026-11-16T07:00:01.234Z", kind: "TX", data: body.data,
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
  await page.locator("[data-nav=monitor]").click();
  await page.locator("[data-format]").selectOption("hex");
  assert.ok((await page.getByRole("log").innerText()).includes("ff 00 0d 0a c3 a9"));
  assert.ok((await page.locator(".lp-logline time").innerText()).endsWith(".123"));
  await page.getByLabel("发送内容", { exact: true }).fill("must-not-send");
  await page.getByLabel("筛选串口日志", { exact: true }).fill("ff");
  await page.getByLabel("筛选串口日志", { exact: true }).press("Enter");
  assert.equal(sent.length, 0);
  assert.equal(await page.getByLabel("发送内容", { exact: true }).inputValue(), "must-not-send");
  await page.getByLabel("筛选串口日志", { exact: true }).fill("");
  await page.getByLabel("发送内容", { exact: true }).fill("ping");
  await page.getByRole("button", { name: "发送", exact: true }).click();
  await page.waitForFunction(() => document.querySelector(".lp-send input").value === "");
  assert.equal(Buffer.from(sent[0].data, "base64").toString(), "ping\r\n");
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
  await page.locator("[data-local-monitor-open]").click();
  assert.equal(await page.locator("[data-windows-pair]").isVisible(), false);
  await page.getByRole("combobox", { name: "本机串口", exact: true }).selectOption("/dev/local-fixture");
  await page.locator("[data-connect-submit]").click();
  await page.waitForFunction(() => document.querySelector("[data-session-label]").textContent.includes("/dev/local-fixture"));
  assert.equal(openedLocal.length, 1);
  assert.equal(await page.locator("[data-mapping]").count(), 1);
  await page.locator("[data-format]").selectOption("text");
  assert.ok((await page.getByRole("log").innerText()).includes("local only"));
  assert.equal((await page.getByRole("log").innerText()).includes("ff 00"), false);
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
  await page.locator("[data-nav=connect]").click();
  await page.getByRole("button", { name: "断开 Test serial fixture", exact: true }).click();
  await page.waitForFunction(() => document.querySelectorAll("[data-mapping]").length === 0);
  assert.equal(sent.length, 1);
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
  await page.route("**/native/v1/devices/invite", route => route.fulfill({
    contentType: "application/json",
    body: JSON.stringify({ code: "linkport1.test-invitation", expiresAt: new Date(Date.now() + 300000).toISOString() }),
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
  await page.locator("[data-device-manager-open]").first().click();
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
  assert.equal(await page.locator("[data-invitation-panel]").isVisible(), true);
  assert.ok((await page.locator("[data-devices-dialog]").boundingBox()).height > compactHeight + 30);
  await page.locator("[data-invite-copy]").click();
  assert.equal(await page.evaluate(() => window.copiedText), "linkport1.test-invitation");
  await page.locator("[data-pair-code]").fill("expired");
  await page.locator("[data-pair-add]").click();
  await page.waitForFunction(() => document.querySelector("[data-device-message]").textContent.includes("已过期"));
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
  assert.equal(await page.locator("[data-custom-relay-fields]").isVisible(), false);
  assert.equal(await page.locator("[data-discovery-options]").isVisible(), false);
  await page.locator("[data-relay-mode]").selectOption("default");
  await settleMotion(page);
  assert.equal(await page.locator("[data-custom-relay-fields]").isVisible(), false);
  assert.equal(await page.locator("[data-discovery-options]").isVisible(), true);
  assert.equal(await page.locator("[data-discovery-url]").isVisible(), false);
  await page.locator("[data-relay-mode]").selectOption("custom");
  await settleMotion(page);
  assert.equal(await page.locator("[data-custom-relay-fields]").isVisible(), true);
  await page.locator("[data-relay-urls]").fill("https://relay.example.test");
  await page.locator("[data-discovery-toggle]").click();
  await page.locator("[data-discovery-url]").fill("https://discovery.example.test");
  await settleMotion(page);
  await page.screenshot({ path: path.join(output, `${browserName}-devices-custom.png`) });
  await page.locator("[data-relay-mode]").selectOption("disabled");
  await settleMotion(page);
  assert.equal(await page.locator("[data-custom-relay-fields]").isVisible(), false);
  assert.equal(await page.locator("[data-discovery-options]").isVisible(), false);
  await page.locator("[data-relay-mode]").selectOption("custom");
  await settleMotion(page);
  assert.equal(await page.locator("[data-relay-urls]").inputValue(), "https://relay.example.test");
  assert.equal(await page.locator("[data-discovery-url]").inputValue(), "https://discovery.example.test");
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
  await page.locator("[data-device-manager-open]").first().click();
  assert.equal(await page.locator("[data-device-id]").isVisible(), false);
  assert.equal(await page.locator("[data-invitation-panel]").isVisible(), false);
  assert.equal(await page.locator("#lp-network-options").isVisible(), false);
  assert.equal(await page.locator("#lp-identity-options").isVisible(), false);
  await page.locator("[data-network-toggle]").focus();
  await page.keyboard.press("Enter");
  assert.equal(await page.locator("#lp-network-options").isVisible(), true);
  assert.equal(await page.locator("#lp-network-options").evaluate(node => node.getAnimations().length), 0);
  await page.keyboard.press("Escape");
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
  await page.locator("[data-relay-mode]").selectOption("custom");
  await page.locator("[data-discovery-toggle]").click();
  assert.equal(await page.locator("[data-network-mode-label]").innerText(), "Custom relays");
  await page.screenshot({ path: path.join(output, `${browserName}-devices-english.png`) });
  assert.deepEqual(errors, []);
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
      browser = await playwright[browserName].launch(launch);
      context = await browser.newContext({ viewport: { width: 1120, height: 780 }, colorScheme: "light" });
      await context.tracing.start({ screenshots: true, snapshots: true });
      await checkFunctional(await context.newPage(), browserName);
      await checkEmptyLayout(await context.newPage(), browserName);
      await checkDeviceManagement(await context.newPage(), browserName);
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
      if (context) await context.tracing.stop({ path: path.join(output, `${browserName}-trace.zip`) });
      if (browser) await browser.close();
      fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(results, null, 2));
    }
  }
  console.log(JSON.stringify(results));
})().catch(error => { console.error(error); process.exitCode = 1; });
