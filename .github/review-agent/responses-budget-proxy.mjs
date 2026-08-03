#!/usr/bin/env node

import { readFile, unlink, writeFile } from "node:fs/promises";
import http from "node:http";
import https from "node:https";
import path from "node:path";
import { pathToFileURL } from "node:url";

// This root-owned process is the only model transport. It clamps every request
// before adding the credential, so runner-user Codex has no unclamped route.
const listenHost = "127.0.0.1";
const responsesPath = "/v1/responses";
const openRouterResponsesPath = "/api/v1/responses";
const maxRequestBytes = 8 * 1024 * 1024;
const strippedRequestHeaders = new Set([
  "authorization",
  "connection",
  "content-length",
  "cookie",
  "host",
  "keep-alive",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
  "x-api-key",
]);
const strippedResponseHeaders = new Set([
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "transfer-encoding",
  "upgrade",
]);

export function enforceOutputBudget(body, maxOutputTokens) {
  if (!Number.isSafeInteger(maxOutputTokens) || maxOutputTokens < 1) {
    throw new Error("max output tokens must be a positive safe integer");
  }
  const request = JSON.parse(Buffer.from(body).toString("utf8"));
  if (request === null || Array.isArray(request) || typeof request !== "object") {
    throw new Error("Responses request body must be a JSON object");
  }

  const requested = request.max_output_tokens;
  if (requested === undefined || requested === null) {
    request.max_output_tokens = maxOutputTokens;
  } else {
    if (!Number.isSafeInteger(requested) || requested < 1) {
      throw new Error("max_output_tokens must be a positive safe integer");
    }
    request.max_output_tokens = Math.min(requested, maxOutputTokens);
  }
  return Buffer.from(JSON.stringify(request));
}

function filteredHeaders(headers, stripped) {
  return Object.fromEntries(
    Object.entries(headers).filter(([name, value]) => {
      return value !== undefined && !stripped.has(name.toLowerCase());
    }),
  );
}

export function normalizeAPIKey(body) {
  if (body.length > 16 * 1024) {
    throw new Error("API key exceeds protected byte limit");
  }
  const value = Buffer.from(body).toString("utf8");
  const apiKey = value.endsWith("\n") ? value.slice(0, -1) : value;
  if (
    apiKey.length === 0 ||
    !/^[\x21-\x7e]+$/.test(apiKey)
  ) {
    throw new Error("API key file must contain one printable ASCII line");
  }
  return apiKey;
}

export function buildUpstreamHeaders(headers, apiKey, contentLength) {
  const result = filteredHeaders(headers, strippedRequestHeaders);
  result.authorization = `Bearer ${apiKey}`;
  result["content-length"] = String(contentLength);
  return result;
}

function sendError(response, status, message) {
  if (response.headersSent) {
    response.destroy();
    return;
  }
  const body = Buffer.from(JSON.stringify({ error: message }));
  response.writeHead(status, {
    "content-type": "application/json",
    "content-length": body.length,
  });
  response.end(body);
}

function createProxy(upstreamUrl, maxOutputTokens, apiKey) {
  return http.createServer((request, response) => {
    if (request.method !== "POST" || request.url !== responsesPath) {
      sendError(response, 403, "request is not allowed");
      request.resume();
      return;
    }

    const chunks = [];
    let bodyBytes = 0;
    let bodyTooLarge = false;
    request.on("data", (chunk) => {
      bodyBytes += chunk.length;
      if (bodyBytes > maxRequestBytes) {
        bodyTooLarge = true;
        return;
      }
      chunks.push(chunk);
    });
    request.on("error", () => sendError(response, 400, "invalid request body"));
    request.on("end", () => {
      if (bodyTooLarge) {
        sendError(response, 413, "request body exceeds protected byte limit");
        return;
      }

      let body;
      try {
        body = enforceOutputBudget(Buffer.concat(chunks), maxOutputTokens);
      } catch {
        sendError(response, 400, "invalid Responses request body");
        return;
      }

      const headers = buildUpstreamHeaders(request.headers, apiKey, body.length);
      const upstreamRequest = https.request(
        upstreamUrl,
        { method: "POST", headers },
        (upstreamResponse) => {
          response.writeHead(
            upstreamResponse.statusCode ?? 502,
            filteredHeaders(upstreamResponse.headers, strippedResponseHeaders),
          );
          upstreamResponse.pipe(response);
        },
      );
      upstreamRequest.on("error", () => {
        sendError(response, 502, "OpenRouter Responses API is unavailable");
      });
      response.on("close", () => {
        if (!response.writableEnded) {
          upstreamRequest.destroy();
        }
      });
      upstreamRequest.end(body);
    });
  });
}

export function parseArguments(argv) {
  const values = new Map();
  for (let index = 0; index < argv.length; index += 2) {
    const name = argv[index];
    const value = argv[index + 1];
    if (!name?.startsWith("--") || value === undefined || values.has(name)) {
      throw new Error("invalid command arguments");
    }
    values.set(name, value);
  }
  const allowed = new Set([
    "--api-key-file",
    "--max-output-tokens",
    "--server-info",
    "--upstream-url",
  ]);
  for (const name of values.keys()) {
    if (!allowed.has(name)) {
      throw new Error(`unsupported option: ${name}`);
    }
  }

  const maxOutputTokens = Number(values.get("--max-output-tokens"));
  if (!Number.isSafeInteger(maxOutputTokens) || maxOutputTokens < 1) {
    throw new Error("--max-output-tokens must be a positive safe integer");
  }
  const serverInfo = values.get("--server-info") ?? "";
  if (!path.isAbsolute(serverInfo)) {
    throw new Error("--server-info must be an absolute path");
  }
  const apiKeyFile = values.get("--api-key-file") ?? "";
  if (!path.isAbsolute(apiKeyFile) || apiKeyFile === serverInfo) {
    throw new Error("--api-key-file must be a distinct absolute path");
  }
  const upstreamUrl = new URL(values.get("--upstream-url") ?? "");
  if (
    upstreamUrl.protocol !== "https:" ||
    upstreamUrl.hostname !== "openrouter.ai" ||
    upstreamUrl.port !== "" ||
    upstreamUrl.pathname !== openRouterResponsesPath ||
    upstreamUrl.search !== "" ||
    upstreamUrl.hash !== "" ||
    upstreamUrl.username !== "" ||
    upstreamUrl.password !== ""
  ) {
    throw new Error("--upstream-url must be the exact OpenRouter Responses URL");
  }
  return { apiKeyFile, maxOutputTokens, serverInfo, upstreamUrl };
}

async function main() {
  const { apiKeyFile, maxOutputTokens, serverInfo, upstreamUrl } = parseArguments(
    process.argv.slice(2),
  );
  const apiKeyBody = await readFile(apiKeyFile);
  await unlink(apiKeyFile);
  const apiKey = normalizeAPIKey(apiKeyBody);
  const server = createProxy(upstreamUrl, maxOutputTokens, apiKey);
  server.on("clientError", (_error, socket) => {
    socket.end("HTTP/1.1 400 Bad Request\r\nConnection: close\r\n\r\n");
  });
  await new Promise((resolve, reject) => {
    server.once("error", reject);
    server.listen(0, listenHost, resolve);
  });
  const address = server.address();
  if (address === null || typeof address === "string") {
    server.close();
    throw new Error("failed to resolve proxy listener");
  }
  try {
    await writeFile(
      serverInfo,
      `${JSON.stringify({ port: address.port, pid: process.pid })}\n`,
      { flag: "wx", mode: 0o600 },
    );
  } catch (error) {
    server.close();
    throw error;
  }
  console.error(`responses-budget-proxy listening on ${listenHost}:${address.port}`);
}

const invokedUrl = process.argv[1]
  ? pathToFileURL(path.resolve(process.argv[1])).href
  : "";
if (import.meta.url === invokedUrl) {
  main().catch((error) => {
    console.error(error instanceof Error ? error.message : "proxy failed");
    process.exitCode = 1;
  });
}
