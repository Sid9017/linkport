import { readFileSync, writeFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";

const root = path.dirname(fileURLToPath(import.meta.url));
const version = readFileSync(path.join(root, "../VERSION"), "utf8").trim();
const pkg = JSON.parse(readFileSync(path.join(root, "package.json"), "utf8"));
if (pkg.version !== version) throw new Error("website/package.json must match VERSION");
for (const file of ["dist/i18n.js", "dist/app.js", "dist/downloads.js", "netlify/functions/releases.mjs", "netlify/lib/releases.mjs"]) {
  execFileSync(process.execPath, ["--check", path.join(root, file)], { stdio: "inherit" });
}
const escape = text => text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;").replaceAll('"', "&quot;");
const changes = escape(readFileSync(path.join(root, "../CHANGELOG.md"), "utf8"));
writeFileSync(path.join(root, "dist/changelog.html"), `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Changelog · Linkport</title><link rel="stylesheet" href="style.css"><link rel="icon" href="assets/linkport.svg"><script src="i18n.js" defer></script></head><body><main class="wrap docs-page"><div class="docs-toolbar"><a href="/">← Linkport</a><div class="language-switch" data-language-switch role="group" aria-label="Language"><button type="button" data-site-language="en" lang="en" aria-pressed="true">EN</button><button type="button" data-site-language="zh-CN" lang="zh-CN" aria-pressed="false">中文</button></div></div><h1>Changelog</h1><p>Stable is the official release channel. Nightly previews ongoing development. The 0.0.x series is still early; consult the notes for hardware and driver limitations.</p><pre class="changelog">${changes}</pre><a href="/#download">Get Desktop →</a></main></body></html>\n`);
writeFileSync(path.join(root, "dist/version.json"), JSON.stringify({ baseVersion: version, channels: ["stable", "nightly"] }) + "\n");
console.log(`Validated Linkport website ${version}; generated changelog.`);
