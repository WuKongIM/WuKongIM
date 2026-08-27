import assert from "node:assert/strict";
import test from "node:test";

import { readServerConfig } from "../src/server/config";

test("the development server defaults to a loopback-only listener", () => {
  assert.deepEqual(readServerConfig({}), {
    host: "127.0.0.1",
    port: 5173,
    productHttpUrl: "http://127.0.0.1:5001",
  });
});

test("the development server refuses a non-loopback listener", () => {
  assert.throws(
    () => readServerConfig({ WK_DOCS_QUICKSTART_HOST: "0.0.0.0" }),
    /must bind to 127\.0\.0\.1, localhost, or ::1/,
  );
});
