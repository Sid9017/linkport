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
}

async function checkFunctional(page, browserName) {
  const errors = [];
  page.on("pageerror", error => errors.push(error.message));
  await initializePage(page);
  await page.goto(origin, { waitUntil: "networkidle" });
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
  assert.equal(initial.tailscale.state, "NotInstalled");
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
    tailscale: { state: "Running", installed: true, hostName: "local-test", ips: ["100.64.255.253"] },
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
    else if (operation === "tailscale") response = fixture.tailscale;
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
  await page.getByRole("checkbox", { name: "共享 /dev/local-fixture", exact: true }).check();
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
      assert.equal(await page.evaluate(() =>
        document.documentElement.scrollHeight > document.documentElement.clientHeight ||
        document.documentElement.scrollWidth > document.documentElement.clientWidth), false);
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
      tailscale: { state: "Stopped", installed: true, hostName: "layout-test", ips: [] },
      devices: [], localPorts: [], sharedPorts: [], sessions: [], refreshing: false,
    }),
  }));
  await page.route("**/native/v1/tailscale", route => route.fulfill({
    contentType: "application/json", body: '{"state":"Stopped","installed":true}',
  }));
  await page.goto(origin, { waitUntil: "networkidle" });
  await page.waitForFunction(() => !document.querySelector("[data-connection-empty]").hidden);
  for (const viewport of [{ width: 1120, height: 780 }, { width: 760, height: 540 }, { width: 390, height: 780 }]) {
    await page.setViewportSize(viewport);
    const layout = await page.evaluate(() => {
      const empty = document.querySelector("[data-connection-empty]").getBoundingClientRect();
      const title = document.querySelector("[data-empty-title]").getBoundingClientRect();
      const copy = document.querySelector("[data-empty-copy]").getBoundingClientRect();
      const button = document.querySelector("[data-live-login]").getBoundingClientRect();
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
