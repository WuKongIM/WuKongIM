import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFile, rename, rm, writeFile, mkdir } from "node:fs/promises";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { chromium } from "@playwright/test";

import {
  serializeIntegrationAcceptanceReport,
  type IntegrationAcceptanceReportInput,
} from "../src/acceptance/report";
import { runIntegrationAcceptanceVerification } from "../src/acceptance/verification";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const repositoryRoot = path.resolve(packageRoot, "../../..");
const reportPath = path.join(packageRoot, "test-results", "integration-acceptance.json");
const temporaryReportPath = `${reportPath}.${process.pid}.tmp`;
const npmCommand = process.platform === "win32" ? "npm.cmd" : "npm";
const require = createRequire(import.meta.url);

try {
  const report = await runIntegrationAcceptanceVerification({
    removeStaleReport: async () => {
      await Promise.all([
        rm(reportPath, { force: true }),
        rm(temporaryReportPath, { force: true }),
      ]);
    },
    runStep: async (step) => {
      const script = step === "sample-check" ? "check" : "test:e2e";
      const result = spawnSync(npmCommand, ["run", script], {
        cwd: packageRoot,
        env: process.env,
        stdio: "inherit",
      });
      if (result.error) throw result.error;
      if (result.status !== 0) {
        throw new Error(`${step} failed with exit code ${result.status ?? "unknown"}`);
      }
    },
    collectInput: collectReportInput,
    writeReport: async (value) => {
      await mkdir(path.dirname(reportPath), { recursive: true });
      await writeFile(
        temporaryReportPath,
        serializeIntegrationAcceptanceReport(value),
        { encoding: "utf8", mode: 0o600 },
      );
      await rename(temporaryReportPath, reportPath);
    },
  });
  console.log(`Integration acceptance report: ${reportPath}`);
  console.log(
    `Compatibility smoke: ${report.compatibility_smoke.result}; production readiness: ${report.production_readiness.result}`,
  );
} catch (error) {
  await Promise.all([
    rm(reportPath, { force: true }),
    rm(temporaryReportPath, { force: true }),
  ]);
  console.error(
    "Integration acceptance failed; no passing report was retained:",
    error instanceof Error ? error.message : "unknown error",
  );
  process.exitCode = 1;
}

async function collectReportInput(): Promise<IntegrationAcceptanceReportInput> {
  const packageLock = await readFile(path.join(packageRoot, "package-lock.json"));
  const playwrightManifest = JSON.parse(
    await readFile(require.resolve("@playwright/test/package.json"), "utf8"),
  ) as { version?: string };
  const playwrightCoreRoot = path.dirname(require.resolve("playwright-core/package.json"));
  const browserManifest = JSON.parse(
    await readFile(path.join(playwrightCoreRoot, "browsers.json"), "utf8"),
  ) as { browsers?: Array<{ name?: string; revision?: string }> };
  const chromiumEntry = browserManifest.browsers?.find(({ name }) => name === "chromium");
  if (!playwrightManifest.version || !chromiumEntry?.revision) {
    throw new Error("locked Playwright or Chromium identity is unavailable");
  }

  const browser = await chromium.launch({ headless: true });
  let chromiumVersion: string;
  try {
    chromiumVersion = browser.version();
  } finally {
    await browser.close();
  }

  const sourceRevision = runGit(["rev-parse", "--verify", "HEAD"]);
  const sourceStatus = runGit(["status", "--porcelain=v1", "--untracked-files=all"]);
  return {
    generatedAt: new Date().toISOString(),
    sourceRevision,
    sourceClean: sourceStatus === "",
    sampleLockSha256: createHash("sha256").update(packageLock).digest("hex"),
    nodeVersion: process.versions.node,
    playwrightVersion: playwrightManifest.version,
    chromiumRevision: chromiumEntry.revision,
    chromiumVersion,
  };
}

function runGit(arguments_: string[]): string {
  const result = spawnSync("git", ["-C", repositoryRoot, ...arguments_], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "ignore"],
  });
  if (result.error) throw result.error;
  const output = result.stdout.trim();
  if (result.status !== 0) throw new Error("repository identity is unavailable");
  return output;
}
