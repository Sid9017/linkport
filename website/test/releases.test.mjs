import test from "node:test";
import assert from "node:assert/strict";
import { createHash, generateKeyPairSync, sign } from "node:crypto";
import { isDownloadAsset, publicRelease, selectChannels } from "../netlify/lib/releases.mjs";
import { createHandler } from "../netlify/functions/releases.mjs";

const release = (tag, prerelease = false) => ({ tag_name: tag, prerelease, draft: false, assets: [], published_at: "2026-11-17T00:00:00Z" });

test("channels follow semantic version order and ignore drafts and dev packages", () => {
  const stable = release("v0.0.10");
  const nightly = release("v0.0.11-nightly.20261117000000.12", true);
  assert.deepEqual(selectChannels([
    release("v0.0.9"), stable, nightly,
    release("v0.0.11-nightly.20261116000000.999", true),
    { ...release("v9.0.0"), draft: true }, release("v9.0.0-dev.1", true),
    release("v9.0.0-nightly.20261118000000.1"),
  ]), { stable, nightly });
});

test("public download allowlist never exposes source archives or unrelated private assets", () => {
  assert.equal(isDownloadAsset("v0.0.2", "linkport-desktop_0.0.2_darwin_arm64.zip"), true);
  for (const filename of ["devices.json", "../private.key", "source.zip", "linkport-desktop_0.0.1_darwin_arm64.zip", "linkport-desktop_0.0.2_darwin_arm64.zip/extra"]) {
    assert.equal(isDownloadAsset("v0.0.2", filename), false);
  }
  assert.equal(isDownloadAsset("main", "checksums.txt"), false);
  assert.equal(isDownloadAsset("v0.0.2-dev.1", "checksums.txt"), false);
});

test("all six desktop packages require matching checksums and publisher signatures", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const r = release("v0.0.2");
  const contents = new Map();
  const digest = createHash("sha256").update("fixture package").digest();
  const sig = sign(null, digest, privateKey).toString("base64");
  const lines = [];
  for (const platform of ["darwin", "linux", "windows"]) {
    for (const arch of ["amd64", "arm64"]) {
      const filename = `linkport-desktop_0.0.2_${platform}_${arch}.zip`;
      r.assets.push({ name: filename, size: 15 }, { name: filename + ".sig", size: 89 });
      contents.set(filename + ".sig", sig);
      lines.push(`${digest.toString("hex")}  ${filename}`);
    }
  }
  r.assets.push({ name: "desktop-checksums.txt", size: 1000 });
  contents.set("desktop-checksums.txt", lines.join("\n"));
  const read = async asset => contents.get(asset.name);
  const data = await publicRelease(r, read, publicKey);
  assert.equal(data.artifacts.length, 6);
  assert.equal(data.channel, "stable");
  assert.match(data.artifacts[0].url, /^\/downloads\/v0\.0\.2\/linkport-desktop/);
  contents.set(r.assets[1].name, Buffer.alloc(64).toString("base64"));
  await assert.rejects(publicRelease(r, read, publicKey), /signature rejected/);
  r.assets.pop();
  await assert.rejects(publicRelease(r, read, publicKey), /checksums missing/);
});

test("inline checksum signatures support releases with no sidecar files and reject tampering", async () => {
  const { publicKey, privateKey } = generateKeyPairSync("ed25519");
  const r = release("v0.0.5");
  const digest = createHash("sha256").update("desktop fixture").digest();
  const signature = sign(null, digest, privateKey).toString("base64");
  const lines = [];
  for (const platform of ["darwin", "linux", "windows"]) {
    for (const arch of ["amd64", "arm64"]) {
      const filename = `linkport-desktop_0.0.5_${platform}_${arch}.zip`;
      r.assets.push({ name: filename, size: 15 });
      lines.push(`${digest.toString("hex")}  ${filename}`, `# ed25519 ${signature} ${filename}`);
    }
  }
  r.assets.push({ name: "desktop-checksums.txt", size: 2000 });
  let metadata = lines.join("\n");
  const read = async asset => {
    assert.equal(asset.name, "desktop-checksums.txt");
    return metadata;
  };
  const result = await publicRelease(r, read, publicKey);
  assert.equal(result.artifacts.length, 6);
  assert.deepEqual(result.cli, []);
  assert.ok(result.artifacts.every(artifact => artifact.signature === signature));
  metadata = lines.join("\n").replace(signature, Buffer.alloc(64).toString("base64"));
  await assert.rejects(publicRelease(r, read, publicKey), /signature rejected/);
  metadata = lines.slice(0, -1).join("\n");
  await assert.rejects(publicRelease(r, read, publicKey), /incomplete desktop release/);
  metadata = [...lines, lines[1]].join("\n");
  await assert.rejects(publicRelease(r, read, publicKey), /invalid release signatures/);
});

test("unconfigured and failed release services do not claim to be up to date or leak credentials", async () => {
  const notConfigured = createHandler({ fetcher: () => assert.fail("unconfigured service fetched GitHub") });
  assert.equal((await notConfigured(new Request("https://example.com/api/releases"))).status, 503);
  const configured = createHandler({ token: "fixture-secret", fetcher: async () => { throw new Error("fixture-secret private body"); } });
  const response = await configured(new Request("https://example.com/updates/stable.json"));
  assert.equal(response.status, 503);
  assert.equal((await response.text()).includes("fixture-secret"), false);
  assert.equal((await configured(new Request("https://example.com/downloads/main/devices.json"))).status, 404);
  assert.equal((await configured(new Request("https://example.com/updates/dev.json"))).status, 404);
});

test("absent channels return 204 while an upstream outage remains an error", async () => {
  const handler = createHandler({ token: "fixture-secret", fetcher: async url =>
    String(url).endsWith("/latest") ? new Response(null, { status: 404 }) : Response.json([]),
  });
  assert.equal((await handler(new Request("https://example.com/updates/stable.json"))).status, 204);
  const response = await handler(new Request("https://example.com/api/releases"));
  assert.deepEqual(await response.json(), { stable: null, nightly: null });
});
