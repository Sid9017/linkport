const channelButtons = [...document.querySelectorAll("[data-release-channel]")];
const downloadLinks = [...document.querySelectorAll("[data-download-target]")];
const releaseStatus = document.querySelector("[data-release-status]");
const releaseNotes = document.querySelector("[data-release-notes]");
let releases = null;
let releaseError = false;
let selectedChannel = "stable";

function renderDownloads() {
  channelButtons.forEach(button => button.setAttribute("aria-pressed", String(button.dataset.releaseChannel === selectedChannel)));
  const release = releases?.[selectedChannel];
  for (const link of downloadLinks) {
    const [platform, arch] = link.dataset.downloadTarget.split("_");
    const artifact = release?.artifacts?.find(item => item.platform === platform && item.arch === arch);
    const valid = artifact && artifact.url.startsWith("/downloads/") && !artifact.url.includes("..");
    if (valid) link.href = artifact.url;
    else link.removeAttribute("href");
    link.setAttribute("aria-disabled", String(!valid));
    link.tabIndex = valid ? 0 : -1;
    link.title = valid ? `${release.version} · SHA-256 ${artifact.sha256}` : "此渠道尚无可下载的安装包";
  }
  const checksums = document.querySelector("[data-checksums]");
  checksums.hidden = !release;
  if (release) checksums.href = release.checksums;
  releaseNotes.textContent = selectedChannel === "nightly"
    ? "Nightly 包含最新开发变更，仅更新到较新的 Nightly。调试前请保存工作。"
    : "Stable 仅接收正式版本更新。0.0.x 仍处于早期阶段，请留意平台与硬件限制。";
  if (release) releaseStatus.textContent = `${selectedChannel === "stable" ? "Stable" : "Nightly"} ${release.version} · ${new Date(release.publishedAt).toLocaleDateString("zh-CN")}`;
  else if (releases) releaseStatus.textContent = `尚未发布 ${selectedChannel === "stable" ? "Stable" : "Nightly"} 安装包`;
  else releaseStatus.textContent = releaseError ? "暂时无法获取版本，请稍后刷新。" : "正在获取可下载版本…";
}

channelButtons.forEach(button => button.addEventListener("click", () => {
  selectedChannel = button.dataset.releaseChannel;
  renderDownloads();
}));
renderDownloads();
fetch("/api/releases", { signal: AbortSignal.timeout(25000) })
  .then(response => {
    if (!response.ok) throw new Error("release service unavailable");
    return response.json();
  })
  .then(data => { releases = data; renderDownloads(); })
  .catch(() => { releaseError = true; renderDownloads(); });
