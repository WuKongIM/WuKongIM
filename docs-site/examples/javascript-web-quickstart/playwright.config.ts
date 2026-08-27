import { defineConfig } from "@playwright/test";

const uiUrl = new URL(
  process.env.WK_DOCS_QUICKSTART_E2E_UI_URL ?? "http://127.0.0.1:5173",
);
const productHttpUrl =
  process.env.WK_DOCS_QUICKSTART_E2E_PRODUCT_HTTP_URL ??
  "http://127.0.0.1:5001";
const uiHost = uiUrl.hostname === "[::1]" ? "::1" : uiUrl.hostname;

if (
  uiUrl.protocol !== "http:" ||
  (uiHost !== "127.0.0.1" && uiHost !== "localhost" && uiHost !== "::1")
) {
  throw new Error(
    "WK_DOCS_QUICKSTART_E2E_UI_URL must be a loopback http:// URL",
  );
}

const serverPort = uiUrl.port || "80";

export default defineConfig({
  testDir: "./e2e",
  outputDir:
    process.env.WK_DOCS_QUICKSTART_E2E_OUTPUT_DIR ?? "test-results",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  fullyParallel: false,
  workers: 1,
  reporter: "line",
  use: {
    baseURL: uiUrl.origin,
    browserName: "chromium",
    // Failure evidence is captured only after the page has been redacted and
    // checked by the E2E suite. Playwright's automatic capture is not safe for
    // the development token, UIDs, or message bodies used by this laboratory.
    screenshot: "off",
    trace: "off",
    video: "off",
  },
  webServer: {
    command: "npm run dev",
    url: uiUrl.origin,
    timeout: 120_000,
    reuseExistingServer: false,
    env: {
      WK_DOCS_QUICKSTART_HOST: uiHost,
      WK_DOCS_QUICKSTART_PORT: serverPort,
      WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL: productHttpUrl,
    },
  },
});
