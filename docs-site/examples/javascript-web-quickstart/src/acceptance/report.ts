import { JAVASCRIPT_WEB_QUICKSTART_TARGET } from "./target";

export const INTEGRATION_ACCEPTANCE_REPORT_SCHEMA =
  "wukongim.docs.integration-acceptance/v1" as const;

export const ACCEPTANCE_CHECK_IDS = [
  "sample-contracts",
  "route-connect",
  "bidirectional-persistent-send",
  "sendack-realtime-separation",
  "offline-realtime-absence",
  "reconnect-sync-recovery",
  "realtime-sync-deduplication",
  "sample-accessibility-baseline",
] as const;

export const DOCUMENTATION_QUALITY_CHECK_IDS = [
  "bilingual-documentation-accessibility",
] as const;

export const PRODUCTION_GATE_IDS = [
  "product-account-and-uid-authority",
  "gateway-stored-token-verification",
  "tls-and-ingress-control",
  "product-http-protection",
  "authorization-and-content-policy",
  "capacity-backpressure-and-rate-limits",
  "webhook-trust-durability-and-reconciliation",
  "operations-backup-audit-and-rollback",
] as const;

const MAX_REPORT_BYTES = 16 * 1024;
const GOLDEN_SCENARIO = JAVASCRIPT_WEB_QUICKSTART_TARGET.scenario;
const SDK = JAVASCRIPT_WEB_QUICKSTART_TARGET.sdk;

export interface IntegrationAcceptanceReportInput {
  generatedAt: string;
  sourceRevision: string;
  sourceClean: boolean;
  sampleLockSha256: string;
  nodeVersion: string;
  playwrightVersion: string;
  chromiumRevision: string;
  chromiumVersion: string;
  sdkPackage: string;
  sdkVersion: string;
  documentationPagesIncluded: boolean;
}

export interface IntegrationAcceptanceReport {
  schema: typeof INTEGRATION_ACCEPTANCE_REPORT_SCHEMA;
  generated_at: string;
  harness_source: {
    revision: string;
    clean: boolean;
  };
  target: {
    scenario: typeof GOLDEN_SCENARIO;
    package_lock_sha256: string;
    sdk: typeof SDK;
    cluster: {
      source_identity: "not_assessed";
    };
    runtime: {
      node: string;
      browser: {
        engine: "chromium";
        playwright_package: "@playwright/test";
        playwright_version: string;
        revision: string;
        browser_version: string;
      };
    };
  };
  compatibility_smoke: {
    result: "passed";
    checks: Array<{
      id: (typeof ACCEPTANCE_CHECK_IDS)[number];
      result: "passed";
    }>;
  };
  documentation_quality: {
    result: "passed" | "not_assessed";
    checks: Array<{
      id: (typeof DOCUMENTATION_QUALITY_CHECK_IDS)[number];
      result: "passed" | "not_assessed";
    }>;
  };
  production_readiness: {
    result: "not_assessed";
    gates: Array<{
      id: (typeof PRODUCTION_GATE_IDS)[number];
      result: "not_assessed";
    }>;
  };
  publication_attestation: "not_issued";
}

/** Builds local compatibility-smoke evidence without claiming production readiness. */
export function buildIntegrationAcceptanceReport(
  input: IntegrationAcceptanceReportInput,
): IntegrationAcceptanceReport {
  validateInput(input);

  return {
    schema: INTEGRATION_ACCEPTANCE_REPORT_SCHEMA,
    generated_at: input.generatedAt,
    harness_source: {
      revision: input.sourceRevision,
      clean: input.sourceClean,
    },
    target: {
      scenario: GOLDEN_SCENARIO,
      package_lock_sha256: input.sampleLockSha256,
      sdk: SDK,
      cluster: {
        source_identity: "not_assessed",
      },
      runtime: {
        node: input.nodeVersion,
        browser: {
          engine: "chromium",
          playwright_package: "@playwright/test",
          playwright_version: input.playwrightVersion,
          revision: input.chromiumRevision,
          browser_version: input.chromiumVersion,
        },
      },
    },
    compatibility_smoke: {
      result: "passed",
      checks: ACCEPTANCE_CHECK_IDS.map((id) => ({ id, result: "passed" })),
    },
    documentation_quality: {
      result: input.documentationPagesIncluded ? "passed" : "not_assessed",
      checks: DOCUMENTATION_QUALITY_CHECK_IDS.map((id) => ({
        id,
        result: input.documentationPagesIncluded ? "passed" : "not_assessed",
      })),
    },
    production_readiness: {
      result: "not_assessed",
      gates: PRODUCTION_GATE_IDS.map((id) => ({
        id,
        result: "not_assessed",
      })),
    },
    publication_attestation: "not_issued",
  };
}

/** Serializes only the exact, bounded, endpoint-free acceptance report shape. */
export function serializeIntegrationAcceptanceReport(report: unknown): string {
  validateReport(report);
  const serialized = `${JSON.stringify(report, null, 2)}\n`;
  if (/docs-dev-/iu.test(serialized) || /(?:https?|wss?):\/\//iu.test(serialized)) {
    throw new Error("acceptance report contains forbidden credential or endpoint material");
  }
  if (Buffer.byteLength(serialized) > MAX_REPORT_BYTES) {
    throw new Error("acceptance report exceeds 16 KiB");
  }
  return serialized;
}

function validateInput(input: IntegrationAcceptanceReportInput): void {
  if (new Date(input.generatedAt).toISOString() !== input.generatedAt) {
    throw new Error("generatedAt must be a canonical ISO timestamp");
  }
  if (!/^[a-f0-9]{40}(?:[a-f0-9]{24})?$/u.test(input.sourceRevision)) {
    throw new Error("sourceRevision must be a full hexadecimal revision");
  }
  if (!/^[a-f0-9]{64}$/u.test(input.sampleLockSha256)) {
    throw new Error("sampleLockSha256 must be a SHA-256 digest");
  }
  for (const [name, value] of Object.entries({
    nodeVersion: input.nodeVersion,
    playwrightVersion: input.playwrightVersion,
    chromiumRevision: input.chromiumRevision,
    chromiumVersion: input.chromiumVersion,
  })) {
    if (!/^[A-Za-z0-9._+-]{1,64}$/u.test(value)) {
      throw new Error(`${name} must be a bounded runtime identifier`);
    }
  }
  if (input.sdkPackage !== SDK.package || input.sdkVersion !== SDK.version) {
    throw new Error("installed SDK identity does not match the shared target");
  }
}

function validateReport(value: unknown): asserts value is IntegrationAcceptanceReport {
  if (!isExactRecord(value, [
    "schema",
    "generated_at",
    "harness_source",
    "target",
    "compatibility_smoke",
    "documentation_quality",
    "production_readiness",
    "publication_attestation",
  ])) {
    throw new Error("acceptance report has an invalid top-level shape");
  }
  if (
    value.schema !== INTEGRATION_ACCEPTANCE_REPORT_SCHEMA ||
    typeof value.generated_at !== "string" ||
    value.publication_attestation !== "not_issued"
  ) {
    throw new Error("acceptance report identity is invalid");
  }

  const source = value.harness_source;
  const target = value.target;
  const smoke = value.compatibility_smoke;
  const documentation = value.documentation_quality;
  const readiness = value.production_readiness;
  if (
    !isExactRecord(source, ["revision", "clean"]) ||
    typeof source.revision !== "string" ||
    typeof source.clean !== "boolean" ||
    !isExactRecord(target, ["scenario", "package_lock_sha256", "sdk", "cluster", "runtime"]) ||
    !isExactRecord(smoke, ["result", "checks"]) ||
    !isExactRecord(documentation, ["result", "checks"]) ||
    !isExactRecord(readiness, ["result", "gates"])
  ) {
    throw new Error("acceptance report sections have an invalid shape");
  }

  const sdk = target.sdk;
  const cluster = target.cluster;
  const runtime = target.runtime;
  if (
    target.scenario !== GOLDEN_SCENARIO ||
    typeof target.package_lock_sha256 !== "string" ||
    !isExactRecord(sdk, ["package", "version"]) ||
    sdk.package !== SDK.package ||
    sdk.version !== SDK.version ||
    !isExactRecord(cluster, ["source_identity"]) ||
    cluster.source_identity !== "not_assessed" ||
    !isExactRecord(runtime, ["node", "browser"]) ||
    typeof runtime.node !== "string" ||
    !isExactRecord(runtime.browser, [
      "engine",
      "playwright_package",
      "playwright_version",
      "revision",
      "browser_version",
    ]) ||
    runtime.browser.engine !== "chromium" ||
    runtime.browser.playwright_package !== "@playwright/test" ||
    typeof runtime.browser.playwright_version !== "string" ||
    typeof runtime.browser.revision !== "string" ||
    typeof runtime.browser.browser_version !== "string"
  ) {
    throw new Error("acceptance report target is invalid");
  }

  validateInput({
    generatedAt: value.generated_at,
    sourceRevision: source.revision,
    sourceClean: source.clean,
    sampleLockSha256: target.package_lock_sha256,
    nodeVersion: runtime.node,
    playwrightVersion: runtime.browser.playwright_version,
    chromiumRevision: runtime.browser.revision,
    chromiumVersion: runtime.browser.browser_version,
    sdkPackage: sdk.package,
    sdkVersion: sdk.version,
    documentationPagesIncluded: documentation.result === "passed",
  });
  if (
    smoke.result !== "passed" ||
    !hasExactResults(smoke.checks, ACCEPTANCE_CHECK_IDS, "passed") ||
    (documentation.result !== "passed" &&
      documentation.result !== "not_assessed") ||
    !hasExactResults(
      documentation.checks,
      DOCUMENTATION_QUALITY_CHECK_IDS,
      documentation.result,
    ) ||
    readiness.result !== "not_assessed" ||
    !hasExactResults(readiness.gates, PRODUCTION_GATE_IDS, "not_assessed")
  ) {
    throw new Error("acceptance report results are invalid");
  }
}

function hasExactResults(
  value: unknown,
  ids: readonly string[],
  result: string,
): boolean {
  return (
    Array.isArray(value) &&
    value.length === ids.length &&
    value.every(
      (item, index) =>
        isExactRecord(item, ["id", "result"]) &&
        item.id === ids[index] &&
        item.result === result,
    )
  );
}

function isExactRecord(
  value: unknown,
  keys: readonly string[],
): value is Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return false;
  const actual = Object.keys(value);
  return actual.length === keys.length && keys.every((key) => actual.includes(key));
}
