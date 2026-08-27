import {
  buildIntegrationAcceptanceReport,
  type IntegrationAcceptanceReport,
  type IntegrationAcceptanceReportInput,
} from "./report";

export type IntegrationAcceptanceStep = "sample-check" | "real-e2e";

export interface IntegrationAcceptanceVerificationDependencies {
  removeStaleReport(): Promise<void>;
  runStep(step: IntegrationAcceptanceStep): Promise<void>;
  collectInput(): Promise<IntegrationAcceptanceReportInput>;
  writeReport(report: IntegrationAcceptanceReport): Promise<void>;
}

/** Runs acceptance gates in order and writes evidence only after every gate passes. */
export async function runIntegrationAcceptanceVerification(
  dependencies: IntegrationAcceptanceVerificationDependencies,
): Promise<IntegrationAcceptanceReport> {
  await dependencies.removeStaleReport();
  await dependencies.runStep("sample-check");
  await dependencies.runStep("real-e2e");
  const report = buildIntegrationAcceptanceReport(await dependencies.collectInput());
  await dependencies.writeReport(report);
  return report;
}
