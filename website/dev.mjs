import http from "node:http";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { Readable } from "node:stream";
import path from "node:path";
import { createHandler } from "./netlify/functions/releases.mjs";

const root = fileURLToPath(new URL("./dist/", import.meta.url));
const handler = createHandler({ token: process.env.LINKPORT_GITHUB_TOKEN });
const types = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".json": "application/json", ".ttf": "font/ttf" };
const port = Number(process.env.PORT || 4173);
http.createServer(async (req, res) => {
  try {
    const url = new URL(req.url, `http://127.0.0.1:${port}`);
    if (url.pathname.startsWith("/api/") || url.pathname.startsWith("/updates/") || url.pathname.startsWith("/downloads/")) {
      const response = await handler(new Request(url, { method: req.method }));
      res.writeHead(response.status, Object.fromEntries(response.headers));
      if (response.body) Readable.fromWeb(response.body).pipe(res);
      else res.end();
      return;
    }
    const filename = path.resolve(root, "." + decodeURIComponent(url.pathname === "/" ? "/index.html" : url.pathname));
    if (!filename.startsWith(root)) { res.writeHead(404).end(); return; }
    const contents = await readFile(filename);
    res.writeHead(200, { "Content-Type": types[path.extname(filename)] || "application/octet-stream", "Cache-Control": "no-store" });
    res.end(contents);
  } catch { res.writeHead(404).end(); }
}).listen(port, "127.0.0.1", () => console.log(`Linkport preview: http://127.0.0.1:${port}`));
