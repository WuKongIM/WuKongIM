import { randomBytes } from "node:crypto";
import { readFile } from "node:fs/promises";
import { createServer, type IncomingMessage, type ServerResponse } from "node:http";

import { createBffHandler } from "./bff";
import { readServerConfig } from "./config";
import { ProductHttpClient } from "./product-http-client";

const MAX_REQUEST_BYTES = 16 * 1024;

const staticAssets = new Map<string, { file: string; contentType: string }>([
  ["/", { file: "index.html", contentType: "text/html; charset=utf-8" }],
  ["/index.html", { file: "index.html", contentType: "text/html; charset=utf-8" }],
  ["/session.html", { file: "session.html", contentType: "text/html; charset=utf-8" }],
  ["/assets/app.js", { file: "assets/app.js", contentType: "text/javascript; charset=utf-8" }],
  ["/assets/app.js.map", { file: "assets/app.js.map", contentType: "application/json" }],
  ["/assets/styles.css", { file: "assets/styles.css", contentType: "text/css; charset=utf-8" }],
]);

const config = readServerConfig(process.env);
const productClient = new ProductHttpClient({
  baseUrl: config.productHttpUrl,
});
const handleBff = createBffHandler({
  productClient,
  tokenFactory: () => `docs-dev-${randomBytes(24).toString("base64url")}`,
});

const server = createServer(async (request, response) => {
  try {
    const fetchRequest = await toFetchRequest(request);
    const url = new URL(fetchRequest.url);
    const fetchResponse = url.pathname.startsWith("/api/")
      ? await handleBff(fetchRequest)
      : await serveStatic(fetchRequest);
    await writeFetchResponse(response, fetchResponse, request.method === "HEAD");
  } catch (error) {
    const status = error instanceof RequestTooLargeError ? 413 : 500;
    if (status === 500) {
      console.error(
        "quickstart request failed:",
        error instanceof Error ? error.message : "unknown error",
      );
    }
    await writeFetchResponse(
      response,
      Response.json(
        { error: status === 413 ? "request body is too large" : "request failed" },
        { status },
      ),
      false,
    );
  }
});

server.listen(config.port, config.host, () => {
  const host = config.host === "::1" ? "[::1]" : config.host;
  console.log(`WuKongIM JavaScript quickstart: http://${host}:${config.port}`);
  console.log(`Trusted Product HTTP target: ${config.productHttpUrl}`);
});

for (const signal of ["SIGINT", "SIGTERM"] as const) {
  process.once(signal, () => {
    server.close((error) => {
      if (error) {
        console.error("quickstart shutdown failed:", error.message);
        process.exitCode = 1;
      }
    });
  });
}

class RequestTooLargeError extends Error {}

async function toFetchRequest(request: IncomingMessage): Promise<Request> {
  const method = request.method ?? "GET";
  const host = request.headers.host ?? `${config.host}:${config.port}`;
  const headers = new Headers();
  for (const [name, value] of Object.entries(request.headers)) {
    if (value === undefined) continue;
    headers.set(name, Array.isArray(value) ? value.join(", ") : value);
  }

  const chunks: Buffer[] = [];
  let total = 0;
  for await (const value of request) {
    const chunk = Buffer.isBuffer(value) ? value : Buffer.from(value);
    total += chunk.byteLength;
    if (total > MAX_REQUEST_BYTES) throw new RequestTooLargeError();
    chunks.push(chunk);
  }
  const bytes = Buffer.concat(chunks);
  const body =
    method === "GET" || method === "HEAD" || bytes.byteLength === 0
      ? undefined
      : bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);

  return new Request(new URL(request.url ?? "/", `http://${host}`), {
    method,
    headers,
    body,
  });
}

async function serveStatic(request: Request): Promise<Response> {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return Response.json({ error: "method not allowed" }, { status: 405 });
  }
  const asset = staticAssets.get(new URL(request.url).pathname);
  if (!asset) return Response.json({ error: "not found" }, { status: 404 });

  const contents = await readFile(new URL(`./public/${asset.file}`, import.meta.url));
  return new Response(contents, {
    headers: {
      "cache-control": "no-store",
      "content-security-policy": "default-src 'self'; connect-src 'self' ws: wss:; frame-ancestors 'self'; style-src 'self'; script-src 'self'",
      "content-type": asset.contentType,
      "referrer-policy": "no-referrer",
      "x-content-type-options": "nosniff",
    },
  });
}

async function writeFetchResponse(
  target: ServerResponse,
  source: Response,
  omitBody: boolean,
): Promise<void> {
  target.statusCode = source.status;
  source.headers.forEach((value, name) => target.setHeader(name, value));
  if (omitBody || source.body === null) {
    target.end();
    return;
  }
  target.end(Buffer.from(await source.arrayBuffer()));
}
