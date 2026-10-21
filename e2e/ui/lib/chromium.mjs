// Picks a host Chromium from the Playwright cache (any revision), so the
// battery does not depend on the exact build the pinned @playwright/test expects.
import { existsSync, readdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

export function chromiumPath() {
  if (process.env.UI_CHROMIUM && existsSync(process.env.UI_CHROMIUM)) return process.env.UI_CHROMIUM;
  const root = process.env.PLAYWRIGHT_BROWSERS_PATH || join(homedir(), ".cache", "ms-playwright");
  if (!existsSync(root)) return undefined;
  const revs = readdirSync(root)
    .filter((d) => /^chromium-\d+$/.test(d))
    .sort((a, b) => Number(b.split("-")[1]) - Number(a.split("-")[1]));
  for (const r of revs) {
    for (const sub of ["chrome-linux64/chrome", "chrome-linux/chrome"]) {
      const p = join(root, r, sub);
      if (existsSync(p)) return p;
    }
  }
  return undefined;
}
