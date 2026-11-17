import { isDownloadAsset, publicRelease, releaseChannel, repository, selectChannels } from "../lib/releases.mjs";

const api = `https://api.github.com/repos/${repository}`;

export function createHandler({ token, fetcher = fetch, now = Date.now } = {}) {
  let cached;
  const request = async (path, accept = "application/vnd.github+json", redirect = "follow") => {
    const response = await fetcher(api + path, {
      headers: {
        Authorization: `Bearer ${token}`, Accept: accept,
        "X-GitHub-Api-Version": "2022-11-28",
        "User-Agent": "Linkport-public-releases",
      },
      redirect, signal: AbortSignal.timeout(20000),
    });
    if (!response.ok && !(redirect === "manual" && response.status === 302)) throw new Error("release source unavailable");
    return response;
  };
  const readAsset = async asset => {
    if (asset.size > 65536) throw new Error("release metadata too large");
    const response = await request(`/releases/assets/${asset.id}`, "application/octet-stream", "manual");
    if (response.status === 302) {
      const target = new URL(response.headers.get("Location"));
      if (target.protocol !== "https:" || !["release-assets.githubusercontent.com", "objects.githubusercontent.com"].includes(target.hostname)) {
        throw new Error("invalid asset redirect");
      }
      const content = await fetcher(target, { signal: AbortSignal.timeout(20000) });
      if (!content.ok) throw new Error("release metadata unavailable");
      const text = await content.text();
      if (text.length > 65536) throw new Error("release metadata too large");
      return text;
    }
    const text = await response.text();
    if (text.length > 65536) throw new Error("release metadata too large");
    return text;
  };
  return async req => {
    if (!["GET", "HEAD"].includes(req.method)) return new Response(null, { status: 405, headers: { Allow: "GET, HEAD" } });
    const url = new URL(req.url);
    const segments = url.pathname.split("/").filter(Boolean).map(decodeURIComponent);
    const channel = url.searchParams.get("channel") || (segments[0] === "updates" ? segments[1]?.replace(/\.json$/, "") : null);
    const tag = url.searchParams.get("tag") || (segments[0] === "downloads" ? segments[1] : null);
    const filename = url.searchParams.get("filename") || (segments[0] === "downloads" ? segments[2] : null);
    if (channel && !["stable", "nightly"].includes(channel)) return new Response(null, { status: 404 });
    if (tag && !isDownloadAsset(tag, filename)) return new Response(null, { status: 404 });
    if (!token) return Response.json({ error: "发布服务尚未配置，请稍后再试" }, { status: 503, headers: { "Cache-Control": "no-store" } });
    try {
      if (tag) {
        const release = await (await request(`/releases/tags/${encodeURIComponent(tag)}`)).json();
        if (!releaseChannel(release)) return new Response(null, { status: 404 });
        const asset = release.assets.find(item => item.name === filename);
        if (!asset) return new Response(null, { status: 404 });
        const response = await request(`/releases/assets/${asset.id}`, "application/octet-stream", "manual");
        if (response.status === 302) {
          const location = new URL(response.headers.get("Location"));
          if (location.protocol !== "https:" || !["release-assets.githubusercontent.com", "objects.githubusercontent.com"].includes(location.hostname)) {
            throw new Error("invalid download redirect");
          }
          return new Response(null, { status: 302, headers: { Location: location.href, "Cache-Control": "private, no-store" } });
        }
        return new Response(req.method === "HEAD" ? null : response.body, { headers: {
          "Content-Type": "application/octet-stream", "Content-Disposition": `attachment; filename="${filename}"`,
          "X-Content-Type-Options": "nosniff", "Cache-Control": "private, no-store",
        } });
      }
      if (!cached || now() - cached.at > 60000) {
        const list = await (await request("/releases?per_page=100")).json();
        const channels = selectChannels(list);
        // The latest stable may predate the hundred most recent nightlies.
        const latest = await fetcher(api + "/releases/latest", { headers: {
          Authorization: `Bearer ${token}`, Accept: "application/vnd.github+json",
          "X-GitHub-Api-Version": "2022-11-28", "User-Agent": "Linkport-public-releases",
        }, signal: AbortSignal.timeout(20000) });
        if (latest.ok) {
          const stable = await latest.json();
          if (releaseChannel(stable) === "stable") channels.stable = selectChannels([stable, ...list]).stable;
        } else if (latest.status !== 404) throw new Error("stable release source unavailable");
        const data = { stable: null, nightly: null };
        await Promise.all(Object.entries(channels).map(async ([name, release]) => {
          if (release) data[name] = await publicRelease(release, readAsset);
        }));
        cached = { at: now(), data };
      }
      const headers = { "Cache-Control": "public, max-age=60, stale-if-error=300", "X-Content-Type-Options": "nosniff" };
      if (channel && !cached.data[channel]) return new Response(null, { status: 204, headers });
      return req.method === "HEAD" ? new Response(null, { headers }) : Response.json(channel ? cached.data[channel] : cached.data, { headers });
    } catch {
      // Never return upstream bodies, URLs or credentials to the public client.
      return Response.json({ error: "发布服务暂不可用，请稍后再试" }, { status: 503, headers: { "Cache-Control": "no-store" } });
    }
  };
}

let runtimeHandler;
export default async function handler(req) {
  runtimeHandler ||= createHandler({ token: process.env.LINKPORT_GITHUB_TOKEN });
  return runtimeHandler(req);
}
