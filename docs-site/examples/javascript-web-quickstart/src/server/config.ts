export interface QuickstartServerConfig {
  host: "127.0.0.1" | "localhost" | "::1";
  port: number;
  productHttpUrl: string;
}

type Environment = Record<string, string | undefined>;

/** Reads the intentionally small, localhost-only development configuration. */
export function readServerConfig(env: Environment): QuickstartServerConfig {
  const host = env.WK_DOCS_QUICKSTART_HOST ?? "127.0.0.1";
  if (host !== "127.0.0.1" && host !== "localhost" && host !== "::1") {
    throw new Error(
      "WK_DOCS_QUICKSTART_HOST must bind to 127.0.0.1, localhost, or ::1",
    );
  }

  const rawPort = env.WK_DOCS_QUICKSTART_PORT ?? "5173";
  const port = Number(rawPort);
  if (!Number.isInteger(port) || port < 1 || port > 65_535) {
    throw new Error("WK_DOCS_QUICKSTART_PORT must be an integer from 1 to 65535");
  }

  const productUrl = new URL(
    env.WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL ?? "http://127.0.0.1:5001",
  );
  if (productUrl.protocol !== "http:" && productUrl.protocol !== "https:") {
    throw new Error(
      "WK_DOCS_QUICKSTART_PRODUCT_HTTP_URL must use http or https",
    );
  }

  return {
    host,
    port,
    productHttpUrl: productUrl.href.replace(/\/$/, ""),
  };
}
