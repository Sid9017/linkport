import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.dirname(fileURLToPath(import.meta.url));
const version = readFileSync(path.join(root, "../VERSION"), "utf8").trim();
const pkg = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
if (pkg.version !== version) throw new Error("website/package.json must match VERSION");
for (const file of ["dist/app.js", "dist/downloads.js", "netlify/functions/releases.mjs", "netlify/lib/releases.mjs"]) {
  execFileSync(process.execPath, ["--check", path.join(root, file)], { stdio: "inherit" });
}
const escape = text => text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
const changes = escape(readFileSync(path.join(root, "../CHANGELOG.md"), "utf8"));
writeFileSync(path.join(root, "dist/changelog.html"), `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>版本记录 · Linkport</title><link rel="stylesheet" href="style.css"><link rel="icon" href="assets/linkport.svg"></head><body><main class="wrap docs-page"><a href="/">← Linkport</a><h1>版本记录</h1><p>Stable 是正式发布渠道，Nightly 是持续开发的预览渠道。0.0.x 仍处于早期阶段，硬件和驱动兼容性以说明为准。</p><pre class="changelog">${changes}</pre><a href="/#download">获取桌面版 →</a></main></body></html>\n`);
writeFileSync(path.join(root, "dist/version.json"), JSON.stringify({ baseVersion: version, channels: ["stable", "nightly", "dev"] }) + "\n");
console.log(`Validated Linkport website ${version}; generated changelog.`);
