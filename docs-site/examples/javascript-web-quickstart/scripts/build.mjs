import { cp, mkdir, rm } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";

import { build } from "esbuild";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const outputRoot = path.join(packageRoot, "dist");
const publicOutput = path.join(outputRoot, "public");

await rm(outputRoot, { recursive: true, force: true });
await mkdir(path.join(publicOutput, "assets"), { recursive: true });

await Promise.all([
  build({
    entryPoints: [path.join(packageRoot, "src/client/main.ts")],
    outfile: path.join(publicOutput, "assets/app.js"),
    bundle: true,
    format: "esm",
    platform: "browser",
    target: ["chrome128"],
    // SDK 1.3.5 logs decoded packets and retry payloads unconditionally.
    // Remove every browser-console call so the development lab does not leak
    // identities or message text into ordinary console capture.
    drop: ["console"],
    sourcemap: true,
  }),
  build({
    entryPoints: [path.join(packageRoot, "src/server/main.ts")],
    outfile: path.join(outputRoot, "server.mjs"),
    bundle: true,
    format: "esm",
    platform: "node",
    target: ["node20"],
    sourcemap: true,
  }),
  cp(path.join(packageRoot, "public"), publicOutput, { recursive: true }),
]);
