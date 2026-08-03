import assert from "node:assert/strict";
import test from "node:test";

import {
  buildUpstreamHeaders,
  enforceOutputBudget,
  normalizeAPIKey,
  parseArguments,
} from "./responses-budget-proxy.mjs";

const limit = 32_768;

function rewrite(request) {
  return JSON.parse(
    enforceOutputBudget(Buffer.from(JSON.stringify(request)), limit).toString(
      "utf8",
    ),
  );
}

test("adds the protected output budget when Codex omits one", () => {
  assert.deepEqual(rewrite({ model: "moonshotai/kimi-k3", stream: true }), {
    model: "moonshotai/kimi-k3",
    stream: true,
    max_output_tokens: limit,
  });
});

test("clamps larger and null output budgets", () => {
  assert.equal(rewrite({ max_output_tokens: 65_536 }).max_output_tokens, limit);
  assert.equal(rewrite({ max_output_tokens: null }).max_output_tokens, limit);
});

test("preserves a valid smaller output budget", () => {
  assert.equal(rewrite({ max_output_tokens: 4_096 }).max_output_tokens, 4_096);
});

test("rejects malformed request bodies and invalid budgets", () => {
  for (const body of [
    Buffer.from("not-json"),
    Buffer.from("[]"),
    Buffer.from('{"max_output_tokens":0}'),
    Buffer.from('{"max_output_tokens":1.5}'),
    Buffer.from('{"max_output_tokens":"32768"}'),
  ]) {
    assert.throws(() => enforceOutputBudget(body, limit));
  }
});

test("replaces caller credentials with the protected proxy credential", () => {
  const headers = buildUpstreamHeaders(
    {
      accept: "text/event-stream",
      authorization: "Bearer caller-controlled",
      connection: "keep-alive",
      cookie: "session=caller-controlled",
      "keep-alive": "timeout=5",
      te: "trailers",
      "x-api-key": "caller-controlled",
    },
    "openrouter-secret",
    123,
  );
  assert.deepEqual(headers, {
    accept: "text/event-stream",
    authorization: "Bearer openrouter-secret",
    "content-length": "123",
  });
});

test("accepts exactly one non-empty API key line", () => {
  assert.equal(normalizeAPIKey(Buffer.from("openrouter-secret\n")), "openrouter-secret");
  for (const body of [Buffer.from(""), Buffer.from("\n"), Buffer.from("a\nb\n")]) {
    assert.throws(() => normalizeAPIKey(body));
  }
});

test("accepts only the exact OpenRouter Responses endpoint", () => {
  const parsed = parseArguments([
    "--api-key-file",
    "/tmp/api-key",
    "--max-output-tokens",
    String(limit),
    "--server-info",
    "/tmp/server-info",
    "--upstream-url",
    "https://openrouter.ai/api/v1/responses",
  ]);
  assert.equal(parsed.upstreamUrl.href, "https://openrouter.ai/api/v1/responses");
  assert.throws(() =>
    parseArguments([
      "--api-key-file",
      "/tmp/api-key",
      "--max-output-tokens",
      String(limit),
      "--server-info",
      "/tmp/server-info",
      "--upstream-url",
      "http://127.0.0.1:1234/v1/responses",
    ]),
  );
});
