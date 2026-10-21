import { defineConfig } from "@playwright/test";
// @ts-ignore - plain ESM helper shared with the scratch scripts
import { chromiumPath } from "./lib/chromium.mjs";

// TARGET=ref|tinyfin|both picks the projects; the URLs come from run.sh (REF_URL / TINYFIN_URL),
// or BASE_URL when a single already-running server is targeted by hand.
const target = process.env.TARGET ?? "both";
const urls: Record<string, string | undefined> = {
  ref: process.env.REF_URL ?? (target === "ref" ? process.env.BASE_URL : undefined),
  tinyfin: process.env.TINYFIN_URL ?? (target === "tinyfin" ? process.env.BASE_URL : undefined),
};
const names = target === "both" ? ["ref", "tinyfin"] : [target];
for (const n of names) {
  if (!urls[n]) throw new Error(`no base URL for target "${n}" (set ${n === "ref" ? "REF_URL" : "TINYFIN_URL"} or BASE_URL)`);
}

export default defineConfig({
  testDir: "./specs",
  // One shared server state per target and a single admin session: keep it serial.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  timeout: 60_000,
  expect: { timeout: 15_000 },
  reporter: [["list"], ["json", { outputFile: "results.json" }]],
  use: {
    locale: "en-US",
    viewport: { width: 1280, height: 800 },
    launchOptions: { executablePath: chromiumPath(), args: ["--no-sandbox"] },
    trace: "retain-on-failure",
  },
  projects: names.map((n) => ({
    name: n,
    use: { baseURL: urls[n] },
    metadata: { target: n },
  })),
});
