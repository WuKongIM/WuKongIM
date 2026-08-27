import assert from "node:assert/strict";
import test from "node:test";

import { decodeTextPayload } from "../src/client/payload";

test("the SDK adapter decodes the documented text payload used by offline sync", () => {
  const payload = "eyJ0eXBlIjoxLCJjb250ZW50Ijoid2hpbGUgb2ZmbGluZSJ9";

  assert.deepEqual(decodeTextPayload(payload), {
    type: 1,
    text: "while offline",
  });
});
