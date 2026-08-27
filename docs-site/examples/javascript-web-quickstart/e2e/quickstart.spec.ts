import AxeBuilder from "@axe-core/playwright";
import { stat } from "node:fs/promises";
import {
  expect,
  test,
  type BrowserContext,
  type Locator,
  type Page,
  type TestInfo,
  type ViewportSize,
} from "@playwright/test";

import { acceptanceParticipantUids } from "../src/acceptance/scenario";

const MAX_FAILURE_SCREENSHOT_BYTES = 2 * 1024 * 1024;
const messageFlowParticipants = acceptanceParticipantUids(
  `${Date.now().toString(36)}-${process.pid.toString(36)}`,
);
const DEVELOPMENT_UIDS = [
  "alice",
  "bob",
  messageFlowParticipants.aliceUid,
  messageFlowParticipants.bobUid,
];

test("Alice and Bob exchange persistent messages and recover one after reconnect", async ({
  browser,
  baseURL,
}, testInfo) => {
  const origin = requireBaseUrl(baseURL);
  const { aliceUid, bobUid } = messageFlowParticipants;
  const [aliceContext, bobContext] = await Promise.all([
    browser.newContext({ baseURL: origin }),
    browser.newContext({ baseURL: origin }),
  ]);
  const messageBodies: string[] = [];

  try {
    const [alice, bob] = await Promise.all([
      openSession(
        aliceContext,
        `/session.html?uid=${encodeURIComponent(aliceUid)}&peer=${encodeURIComponent(bobUid)}`,
      ),
      openSession(
        bobContext,
        `/session.html?uid=${encodeURIComponent(bobUid)}&peer=${encodeURIComponent(aliceUid)}`,
      ),
    ]);
    await Promise.all([connect(alice), connect(bob)]);

    const aliceRealtime = `alice-live-${Date.now()}`;
    messageBodies.push(aliceRealtime);
    await sendAndAwaitAck(alice, aliceRealtime);
    await expect(
      bob.getByTestId("event-received").filter({ hasText: aliceRealtime }),
    ).toHaveCount(1);

    const bobRealtime = `bob-live-${Date.now()}`;
    messageBodies.push(bobRealtime);
    await sendAndAwaitAck(bob, bobRealtime);
    await expect(
      alice.getByTestId("event-received").filter({ hasText: bobRealtime }),
    ).toHaveCount(1);

    await activateWithKeyboard(bob, "disconnect-button");
    await expect(bob.getByTestId("connection-status")).toHaveText(
      "disconnected",
    );

    const whileBobOffline = `alice-offline-${Date.now()}`;
    messageBodies.push(whileBobOffline);
    await sendAndAwaitAck(alice, whileBobOffline);
    await expect(
      bob.getByTestId("event-received").filter({ hasText: whileBobOffline }),
    ).toHaveCount(0);

    await activateWithKeyboard(bob, "reconnect-sync-button");
    await expect(bob.getByTestId("connection-status")).toHaveText("connected");
    await expect(
      bob.getByTestId("event-synced").filter({ hasText: aliceRealtime }),
    ).toHaveCount(0);
    const recoveredOfflineMessage = bob
      .getByTestId("event-synced")
      .filter({ hasText: whileBobOffline });
    await expect(recoveredOfflineMessage).toHaveCount(1);
    await expect(recoveredOfflineMessage).toContainText("recovered");
  } catch (error) {
    const evidencePage =
      bobContext.pages().at(-1) ??
      aliceContext.pages().at(-1) ??
      (await aliceContext.newPage());
    await captureRedactedFailureScreenshot(
      evidencePage,
      testInfo,
      "message-flow",
      messageBodies,
    );
    throw error;
  } finally {
    await Promise.all([aliceContext.close(), bobContext.close()]);
  }
});

const viewports: Array<{ name: string; size: ViewportSize }> = [
  { name: "desktop", size: { width: 1440, height: 900 } },
  { name: "mobile", size: { width: 390, height: 844 } },
];

interface AccessiblePage {
  name: string;
  path: string;
}

interface DocumentationPage extends AccessiblePage {
  locale: "zh" | "en";
  canonicalPath: string;
}

const accessiblePages: Array<AccessiblePage | DocumentationPage> = [
  { name: "lab", path: "/" },
  { name: "Alice session", path: "/session.html?uid=alice&peer=bob" },
  { name: "Bob session", path: "/session.html?uid=bob&peer=alice" },
  ...documentationPages(),
];

for (const viewport of viewports) {
  test(`${viewport.name} pages have no serious accessibility findings, hidden keyboard focus, or horizontal overflow`, async ({
    browser,
    baseURL,
  }, testInfo) => {
    const context = await browser.newContext({
      baseURL: requireBaseUrl(baseURL),
      viewport: viewport.size,
      colorScheme: "light",
      reducedMotion: "reduce",
    });
    const page = await context.newPage();

    try {
      for (const entry of accessiblePages) {
        const response = await page.goto(entry.path);
        expect(response?.ok(), `${entry.name} must return a successful document`).toBe(
          true,
        );
        if ("locale" in entry) {
          await assertDocumentationPageIdentity(page, entry);
        }
        await settlePageForAccessibility(page);
        await assertNoHorizontalOverflow(page, entry.name);
        await assertNoSeriousAccessibilityViolations(page, entry.name);
        if (entry.name.startsWith("docs ")) {
          await assertPageHasVisibleKeyboardFocus(page, entry.name);
        }
      }
    } catch (error) {
      await captureRedactedFailureScreenshot(
        page,
        testInfo,
        `${viewport.name}-accessibility`,
      );
      throw error;
    } finally {
      await context.close();
    }
  });
}

async function openSession(
  context: BrowserContext,
  path: string,
): Promise<Page> {
  const page = await context.newPage();
  await page.goto(path);
  return page;
}

async function connect(page: Page): Promise<void> {
  await activateWithKeyboard(page, "connect-button");
  await expect(page.getByTestId("connection-status")).toHaveText("connected");
  await expect(page.getByTestId("node-id")).not.toHaveText("—");
}

async function sendAndAwaitAck(page: Page, text: string): Promise<void> {
  const acknowledgements = page.getByTestId("event-sendack");
  const previousCount = await acknowledgements.count();
  await focusWithKeyboard(page, "message-input");
  await page.keyboard.type(text);
  await activateWithKeyboard(page, "send-button");
  await expect(acknowledgements).toHaveCount(previousCount + 1);
  await expect(acknowledgements.nth(previousCount)).toContainText(
    "SENDACK success",
  );
}

async function activateWithKeyboard(page: Page, testId: string): Promise<void> {
  await focusWithKeyboard(page, testId);
  await page.keyboard.press("Enter");
}

async function focusWithKeyboard(page: Page, testId: string): Promise<void> {
  const target = page.getByTestId(testId);
  for (let step = 0; step < 12; step += 1) {
    if (await target.evaluate((element) => element === document.activeElement)) {
      await assertVisibleFocusIndicator(target, testId);
      return;
    }
    await page.keyboard.press("Tab");
  }
  await expect(target).toBeFocused();
  await assertVisibleFocusIndicator(target, testId);
}

async function assertPageHasVisibleKeyboardFocus(
  page: Page,
  pageName: string,
): Promise<void> {
  await page.evaluate(() => {
    if (document.activeElement instanceof HTMLElement) {
      document.activeElement.blur();
    }
  });

  const observations: string[] = [];
  for (let step = 0; step < 24; step += 1) {
    await page.keyboard.press("Tab");
    const active = page.locator(":focus");
    if ((await active.count()) !== 1) {
      observations.push("no unique active element");
      continue;
    }

    const result = await visibleFocusIndicator(active);
    observations.push(result.summary);
    if (result.visible) return;
  }

  throw new Error(
    `${pageName} exposes no visible :focus-visible indicator after keyboard navigation: ${observations.join("; ")}`,
  );
}

async function assertVisibleFocusIndicator(
  target: Locator,
  label: string,
): Promise<void> {
  const result = await visibleFocusIndicator(target);
  expect(
    result.visible,
    `${label} must expose a visible :focus-visible indicator (${result.summary})`,
  ).toBe(true);
}

async function visibleFocusIndicator(
  target: Locator,
): Promise<{ visible: boolean; summary: string }> {
  return target.evaluate((element) => {
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    const outlineWidth = Number.parseFloat(style.outlineWidth) || 0;
    const outlineColor = style.outlineColor.toLowerCase();
    const hasOutline =
      outlineWidth > 0 &&
      style.outlineStyle !== "none" &&
      outlineColor !== "transparent" &&
      !outlineColor.endsWith(", 0)");
    const hasBoxShadow =
      style.boxShadow !== "none" &&
      !style.boxShadow.includes("rgba(0, 0, 0, 0)");
    const inViewport =
      rect.width > 0 &&
      rect.height > 0 &&
      rect.bottom > 0 &&
      rect.right > 0 &&
      rect.top < innerHeight &&
      rect.left < innerWidth;
    const focusVisible = element.matches(":focus-visible");
    const visible =
      focusVisible &&
      inViewport &&
      style.visibility !== "hidden" &&
      style.display !== "none" &&
      (hasOutline || hasBoxShadow);

    return {
      visible,
      summary: `${element.tagName.toLowerCase()} focus-visible=${focusVisible} in-viewport=${inViewport} outline=${style.outlineStyle}/${style.outlineWidth}/${style.outlineColor} shadow=${style.boxShadow}`,
    };
  });
}

async function assertNoHorizontalOverflow(
  page: Page,
  pageName: string,
): Promise<void> {
  const dimensions = await page.evaluate(() => ({
    clientWidth: document.documentElement.clientWidth,
    scrollWidth: document.documentElement.scrollWidth,
  }));
  expect(
    dimensions.scrollWidth,
    `${pageName} must fit within ${dimensions.clientWidth}px`,
  ).toBeLessThanOrEqual(dimensions.clientWidth);
}

async function settlePageForAccessibility(page: Page): Promise<void> {
  await page.evaluate(async () => {
    await document.fonts.ready;
    await new Promise<void>((resolve) => {
      requestAnimationFrame(() => requestAnimationFrame(() => resolve()));
    });
  });
}

async function assertNoSeriousAccessibilityViolations(
  page: Page,
  pageName: string,
): Promise<void> {
  const results = await new AxeBuilder({ page }).analyze();
  const violations = results.violations
    .filter(({ impact }) => impact === "critical" || impact === "serious")
    .map(({ id, impact, nodes }) => ({
      id,
      impact,
      nodes: nodes.slice(0, 5).map(({ target, failureSummary, any }) => ({
        target,
        failureSummary,
        details: any.map(({ message, data }) => ({ message, data })),
      })),
    }));
  expect(violations, `${pageName} has serious axe findings`).toEqual([]);
}

async function captureRedactedFailureScreenshot(
  page: Page,
  testInfo: TestInfo,
  label: string,
  messageBodies: string[] = [],
): Promise<void> {
  await redactFailurePage(page, messageBodies);
  await assertFailurePageIsRedacted(page, messageBodies);

  const currentViewport = page.viewportSize();
  if (currentViewport) {
    await page.setViewportSize({
      width: Math.min(currentViewport.width, 1024),
      height: Math.min(currentViewport.height, 768),
    });
  }

  const screenshotPath = testInfo.outputPath(`redacted-${label}.png`);
  await page.screenshot({
    path: screenshotPath,
    animations: "disabled",
    fullPage: false,
    type: "png",
  });
  const screenshot = await stat(screenshotPath);
  expect(
    screenshot.size,
    `redacted failure screenshot must be at most ${MAX_FAILURE_SCREENSHOT_BYTES} bytes`,
  ).toBeLessThanOrEqual(MAX_FAILURE_SCREENSHOT_BYTES);
}

async function redactFailurePage(
  page: Page,
  messageBodies: string[],
): Promise<void> {
  await page.evaluate(
    ({ developmentUids, sensitiveMessages }) => {
      const replacements = [...developmentUids, ...sensitiveMessages].filter(
        (value) => value.length > 0,
      );
      const redact = (value: string): string => {
        let redacted = value.replace(
          /docs-dev-[a-z0-9._~-]+/giu,
          "[redacted token]",
        );
        for (const replacement of replacements) {
          redacted = redacted.replaceAll(replacement, "[redacted]");
          redacted = redacted.replaceAll(
            replacement.toUpperCase(),
            "[redacted]",
          );
          redacted = redacted.replaceAll(
            replacement[0].toUpperCase() + replacement.slice(1),
            "[redacted]",
          );
        }
        return redacted;
      };

      document.title = "Redacted failure evidence";
      for (const frame of document.querySelectorAll("iframe")) {
        const replacement = document.createElement("div");
        replacement.textContent = "Session frame redacted before capture.";
        replacement.setAttribute("role", "note");
        frame.replaceWith(replacement);
      }
      for (const input of document.querySelectorAll<
        HTMLInputElement | HTMLTextAreaElement
      >("input, textarea")) {
        input.value = "";
        input.removeAttribute("value");
        input.setAttribute("placeholder", "[redacted]");
      }
      for (const identity of document.querySelectorAll(
        '[data-testid="identity"], [data-testid="peer"]',
      )) {
        identity.textContent = "[redacted uid]";
      }
      for (const log of document.querySelectorAll('[data-testid="event-log"]')) {
        log.replaceChildren();
        const item = document.createElement("li");
        item.textContent = "Event details redacted before capture.";
        log.append(item);
      }
      for (const error of document.querySelectorAll('[data-testid="ui-error"]')) {
        error.textContent = "Diagnostic details redacted before capture.";
      }

      const walker = document.createTreeWalker(
        document.documentElement,
        NodeFilter.SHOW_TEXT,
      );
      let textNode = walker.nextNode();
      while (textNode) {
        textNode.nodeValue = redact(textNode.nodeValue ?? "");
        textNode = walker.nextNode();
      }
      for (const element of document.querySelectorAll("*")) {
        for (const attribute of Array.from(element.attributes)) {
          const redacted = redact(attribute.value);
          if (redacted !== attribute.value) {
            element.setAttribute(attribute.name, redacted);
          }
        }
      }
    },
    { developmentUids: DEVELOPMENT_UIDS, sensitiveMessages: messageBodies },
  );
}

async function assertFailurePageIsRedacted(
  page: Page,
  messageBodies: string[],
): Promise<void> {
  const dom = await page.evaluate(() => ({
    html: document.documentElement.outerHTML,
    inputValues: Array.from(
      document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>(
        "input, textarea",
      ),
      ({ value }) => value,
    ),
    eventLogs: Array.from(
      document.querySelectorAll('[data-testid="event-log"]'),
      ({ textContent }) => textContent ?? "",
    ),
  }));
  expect(dom.html, "failure DOM must not contain a development token").not.toMatch(
    /docs-dev-/iu,
  );
  expect(dom.html, "failure DOM must not contain development UIDs").not.toMatch(
    /\b(?:alice|bob)\b/iu,
  );
  for (const messageBody of messageBodies) {
    expect(dom.html, "failure DOM must not contain a message body").not.toContain(
      messageBody,
    );
  }
  expect(dom.inputValues, "failure inputs must be empty").toEqual(
    dom.inputValues.map(() => ""),
  );
  expect(dom.eventLogs, "failure event logs must be replaced before capture").toEqual(
    dom.eventLogs.map(() => "Event details redacted before capture."),
  );
}

function requireBaseUrl(baseURL: string | undefined): string {
  if (baseURL === undefined) {
    throw new Error("Playwright baseURL is required");
  }
  return baseURL;
}

function documentationPages(): DocumentationPage[] {
  const raw = process.env.WK_DOCS_SITE_E2E_URL;
  if (!raw) return [];

  const url = new URL(raw);
  const hostname = url.hostname === "[::1]" ? "::1" : url.hostname;
  if (
    url.protocol !== "http:" ||
    (hostname !== "127.0.0.1" && hostname !== "localhost" && hostname !== "::1")
  ) {
    throw new Error("WK_DOCS_SITE_E2E_URL must be a loopback http:// URL");
  }

  const routes = [
    "",
    "sdk/javascript/quickstart/",
    "sdk/javascript/platform-capabilities/",
    "guide/integration/acceptance/",
    "api/product-http/",
    "api/product-http/users/",
    "api/product-http/messages/",
    "api/product-http/routing/",
    "api/product-http/errors/",
  ];
  return (["zh", "en"] as const).flatMap((locale) =>
    routes.map((route) => {
      const canonicalPath = `/${locale}/${route}`;
      return {
        name: `docs ${locale}/${route || "home"}`,
        path: new URL(canonicalPath, url.origin).toString(),
        locale,
        canonicalPath,
      };
    }),
  );
}

async function assertDocumentationPageIdentity(
  page: Page,
  entry: DocumentationPage,
): Promise<void> {
  const identity = await page.evaluate(() => ({
    language: document.documentElement.lang,
    canonical: document.querySelector<HTMLLinkElement>('link[rel="canonical"]')
      ?.href,
  }));

  expect(identity.language, `${entry.name} must retain its requested locale`).toBe(
    entry.locale,
  );
  expect(identity.canonical, `${entry.name} must expose a canonical identity`).toBeTruthy();
  expect(new URL(identity.canonical ?? "about:blank").pathname).toBe(
    entry.canonicalPath,
  );
}
