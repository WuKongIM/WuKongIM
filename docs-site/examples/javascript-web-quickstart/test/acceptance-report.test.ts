import assert from "node:assert/strict";
import test from "node:test";

import {
  ACCEPTANCE_CHECK_IDS,
  DOCUMENTATION_QUALITY_CHECK_IDS,
  PRODUCTION_GATE_IDS,
  buildIntegrationAcceptanceReport,
  serializeIntegrationAcceptanceReport,
  type IntegrationAcceptanceReportInput,
} from "../src/acceptance/report";
import { acceptanceParticipantUids } from "../src/acceptance/scenario";
import { runIntegrationAcceptanceVerification } from "../src/acceptance/verification";

const input: IntegrationAcceptanceReportInput = {
  generatedAt: "2026-08-27T00:00:00.000Z",
  sourceRevision: "a".repeat(40),
  sourceClean: true,
  sampleLockSha256: "b".repeat(64),
  nodeVersion: "22.12.0",
  playwrightVersion: "1.62.1",
  chromiumRevision: "1234",
  chromiumVersion: "151.0.7922.34",
  sdkPackage: "wukongimjssdk",
  sdkVersion: "1.3.5",
  documentationPagesIncluded: false,
};

test("acceptance runs use isolated participant UIDs within the Product HTTP contract", () => {
  const first = acceptanceParticipantUids("m1-test");
  const second = acceptanceParticipantUids("m2-test");

  assert.deepEqual(first, {
    aliceUid: "docs-alice-m1-test",
    bobUid: "docs-bob-m1-test",
  });
  assert.notDeepEqual(first, second);
  for (const uid of Object.values(first)) {
    assert.match(uid, /^[A-Za-z0-9._-]{1,64}$/);
  }
  assert.throws(() => acceptanceParticipantUids("contains spaces"));
  assert.throws(() => acceptanceParticipantUids("x".repeat(65)));
});

test("the acceptance report proves compatibility smoke without claiming production readiness", () => {
  const report = buildIntegrationAcceptanceReport(input);

  assert.deepEqual(Object.keys(report), [
    "schema",
    "generated_at",
    "harness_source",
    "target",
    "compatibility_smoke",
    "documentation_quality",
    "production_readiness",
    "publication_attestation",
  ]);
  assert.equal(report.schema, "wukongim.docs.integration-acceptance/v1");
  assert.equal(report.compatibility_smoke.result, "passed");
  assert.deepEqual(
    report.compatibility_smoke.checks.map(({ id, result }) => [id, result]),
    ACCEPTANCE_CHECK_IDS.map((id) => [id, "passed"]),
  );
  assert.equal(report.target.cluster.source_identity, "not_assessed");
  assert.equal(report.documentation_quality.result, "not_assessed");
  assert.deepEqual(
    report.documentation_quality.checks.map(({ id, result }) => [id, result]),
    DOCUMENTATION_QUALITY_CHECK_IDS.map((id) => [id, "not_assessed"]),
  );
  assert.equal(report.production_readiness.result, "not_assessed");
  assert.deepEqual(
    report.production_readiness.gates.map(({ id, result }) => [id, result]),
    PRODUCTION_GATE_IDS.map((id) => [id, "not_assessed"]),
  );
  assert.equal(report.publication_attestation, "not_issued");
});

test("documentation quality is passed only when bilingual routes joined the E2E run", () => {
  const report = buildIntegrationAcceptanceReport({
    ...input,
    documentationPagesIncluded: true,
  });

  assert.equal(report.documentation_quality.result, "passed");
  assert.deepEqual(
    report.documentation_quality.checks.map(({ result }) => result),
    DOCUMENTATION_QUALITY_CHECK_IDS.map(() => "passed"),
  );
});

test("the report rejects an installed SDK identity that drifts from the shared target", () => {
  assert.throws(
    () =>
      buildIntegrationAcceptanceReport({
        ...input,
        sdkVersion: "1.3.6",
      }),
    /SDK identity/,
  );
});

test("the serialized report stays bounded and excludes endpoints and development tokens", () => {
  const report = buildIntegrationAcceptanceReport(input);
  const serialized = serializeIntegrationAcceptanceReport(report);

  assert.ok(Buffer.byteLength(serialized) <= 16 * 1024);
  assert.doesNotMatch(serialized, /docs-dev-/iu);
  assert.doesNotMatch(serialized, /(?:https?|wss?):\/\//iu);
  assert.throws(() =>
    serializeIntegrationAcceptanceReport({
      ...report,
      harness_source: {
        ...report.harness_source,
        revision: "docs-dev-secret",
      },
    }),
  );
  assert.throws(() =>
    serializeIntegrationAcceptanceReport({
      ...report,
      harness_source: {
        ...report.harness_source,
        revision: "https://cluster.example",
      },
    }),
  );
});

test("verification removes stale evidence and writes only after both gates pass", async () => {
  const events: string[] = [];
  await runIntegrationAcceptanceVerification({
    removeStaleReport: async () => {
      events.push("remove");
    },
    runStep: async (step) => {
      events.push(`run:${step}`);
    },
    collectInput: async () => {
      events.push("collect");
      return input;
    },
    writeReport: async () => {
      events.push("write");
    },
  });

  assert.deepEqual(events, ["remove", "run:sample-check", "run:real-e2e", "collect", "write"]);
});

test("a failed real E2E run leaves no passing report", async () => {
  const events: string[] = [];

  await assert.rejects(() =>
    runIntegrationAcceptanceVerification({
      removeStaleReport: async () => {
        events.push("remove");
      },
      runStep: async (step) => {
        events.push(`run:${step}`);
        if (step === "real-e2e") throw new Error("cluster unavailable");
      },
      collectInput: async () => {
        events.push("collect");
        return input;
      },
      writeReport: async () => {
        events.push("write");
      },
    }),
    /cluster unavailable/,
  );
  assert.deepEqual(events, ["remove", "run:sample-check", "run:real-e2e"]);
});
