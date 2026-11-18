import { createPublicKey, verify } from "node:crypto";

export const repository = "Sid9017/linkport";
export const publicKey = "hRHv6nvu8IA0lGNwJUnXCCgQG+QUeVpI+PVcs3+aHW4=";
const key = createPublicKey({
  key: Buffer.concat([Buffer.from("302a300506032b6570032100", "hex"), Buffer.from(publicKey, "base64")]),
  format: "der", type: "spki",
});
export const tagPattern = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-nightly\.\d{14}\.[1-9]\d*)?$/;
const targets = ["darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"];

export function releaseChannel(release) {
  if (release.draft || !tagPattern.test(release.tag_name)) return null;
  const channel = release.tag_name.includes("-nightly.") ? "nightly" : "stable";
  return release.prerelease === (channel === "nightly") ? channel : null;
}

export function newer(a, b) {
  const parts = value => value.tag_name.slice(1).split(/[-.]/).filter(v => v !== "nightly").map(Number);
  const av = parts(a), bv = parts(b);
  for (let i = 0; i < Math.max(av.length, bv.length); i++) {
    if ((av[i] ?? Infinity) !== (bv[i] ?? Infinity)) return (av[i] ?? Infinity) > (bv[i] ?? Infinity);
  }
  return false;
}

export function selectChannels(releases) {
  const result = { stable: null, nightly: null };
  for (const release of releases) {
    const channel = releaseChannel(release);
    if (channel && (!result[channel] || newer(release, result[channel]))) result[channel] = release;
  }
  return result;
}

export function downloadURL(tag, filename) {
  return `/downloads/${encodeURIComponent(tag)}/${encodeURIComponent(filename)}`;
}

export async function publicRelease(release, readAsset, verificationKey = key) {
  const channel = releaseChannel(release);
  if (!channel) throw new Error("invalid published release");
  const version = release.tag_name.slice(1);
  const assets = new Map(release.assets.map(asset => [asset.name, asset]));
  const checksumAsset = assets.get("desktop-checksums.txt");
  if (!checksumAsset) throw new Error("release checksums missing");
  const checksums = new Map();
  const signatures = new Map();
  for (const line of (await readAsset(checksumAsset)).trim().split("\n")) {
    if (line.startsWith("# ed25519 ")) {
      const match = /^# ed25519 ([A-Za-z0-9+/]{86}==) (linkport-desktop_[^\s/\\]+\.zip)$/.exec(line);
      if (!match || signatures.has(match[2])) throw new Error("invalid release signatures");
      signatures.set(match[2], match[1]);
      continue;
    }
    const match = /^([a-f0-9]{64})  (linkport-desktop_[^\s/\\]+\.zip)$/.exec(line);
    if (!match || checksums.has(match[2])) throw new Error("invalid release checksums");
    checksums.set(match[2], match[1]);
  }
  const artifacts = await Promise.all(targets.map(async target => {
    const filename = `linkport-desktop_${version}_${target}.zip`;
    const asset = assets.get(filename), signatureAsset = assets.get(filename + ".sig");
    const digest = checksums.get(filename);
    if (!asset || !digest || (!signatures.has(filename) && !signatureAsset)) throw new Error("incomplete desktop release");
    // Existing releases used sidecar files. New releases carry their signatures
    // in the checksum metadata, keeping those files out of the download list.
    const signature = signatures.get(filename) ?? (await readAsset(signatureAsset)).trim();
    const decoded = Buffer.from(signature, "base64");
    if (decoded.length !== 64 || !verify(null, Buffer.from(digest, "hex"), verificationKey, decoded)) {
      throw new Error("publisher signature rejected");
    }
    const [platform, arch] = target.split("_");
    return {
      platform, arch, filename, filetype: "zip", size: asset.size,
      url: downloadURL(release.tag_name, filename),
      digestAlgo: "sha256", digest: Buffer.from(digest, "hex").toString("base64"),
      signatureAlgo: "ed25519", signature, sha256: digest,
    };
  }));
  return {
    schemaVersion: 1, version, channel, name: release.name,
    notes: release.body || "", publishedAt: release.published_at, artifacts,
    checksums: downloadURL(release.tag_name, "desktop-checksums.txt"),
    cli: targets.map(target => {
      const filename = `linkport_${version}_${target}${target.startsWith("windows") ? ".zip" : ".tar.gz"}`;
      return assets.has(filename) ? { target, filename, url: downloadURL(release.tag_name, filename) } : null;
    }).filter(Boolean),
  };
}

export function isDownloadAsset(tag, filename) {
  if (!tagPattern.test(tag) || !filename || filename.includes("/") || filename.includes("\\")) return false;
  if (["checksums.txt", "desktop-checksums.txt"].includes(filename)) return true;
  const version = tag.slice(1);
  return targets.some(target =>
    [`linkport-desktop_${version}_${target}.zip`, `linkport_${version}_${target}${target.startsWith("windows") ? ".zip" : ".tar.gz"}`]
      .some(name => filename === name || filename === name + ".sig"));
}
