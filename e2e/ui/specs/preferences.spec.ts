import type { Locator, Page } from "@playwright/test";
import { test, expect, loginAsAdmin, waitIdle } from "../lib/fixtures";
import { checkFlowTrace } from "../lib/flowtrace";

// User flow: user menu -> Settings -> a preferences page; change one setting, save, reload, the value
// persists; the original value is restored through the same UI so the specs stay order-independent.

async function openPrefs(page: Page, link: "Display" | "Home" | "Playback" | "Subtitles") {
  await page.getByRole("button", { name: /user menu/i }).click();
  await page.getByRole("menuitem", { name: "Settings" }).or(page.getByRole("link", { name: "Settings" })).first().click();
  await expect(page).toHaveURL(/#\/mypreferencesmenu/);
  await page.locator(".page:not(.hide)").getByRole("link", { name: link, exact: true }).click();
  await waitIdle(page);
}

// Display and Home post /Users/{id}/Configuration then /DisplayPreferences/{id}; Playback and Subtitles
// only the former. Wait for the last request of the page before reloading.
const save = async (page: Page, link: string) => {
  const last = link === "Display" || link === "Home" ? /\/DisplayPreferences\// : /\/Users\/[^/]+\/Configuration/;
  const stored = page.waitForResponse((r) => r.request().method() === "POST" && last.test(r.url()));
  await page.locator(".page:not(.hide)").getByRole("button", { name: "Save", exact: true }).click();
  expect((await stored).status()).toBeLessThan(300);
  await waitIdle(page, 3_000);
};

/** Reload keeps the route, so the form is rebuilt from what the server and client stored. */
const reloadPrefs = async (page: Page, ready: Locator) => {
  await page.reload();
  await expect(ready).toBeVisible();
};

async function checkboxRoundTrip(page: Page, link: "Display" | "Home" | "Playback", name: RegExp) {
  await openPrefs(page, link);
  const box = page.locator(".page:not(.hide)").getByRole("checkbox", { name });
  await expect(box).toBeVisible();
  const original = await box.isChecked();
  try {
    await box.setChecked(!original, { force: true });
    await save(page, link);
    await reloadPrefs(page, box);
    await expect(box).toBeChecked({ checked: !original });
  } finally {
    await openPrefs(page, link);
    // Wait until the form shows the stored value, or a late load would overwrite the restore.
    await expect(box).toBeChecked({ checked: !original }).catch(() => {});
    await box.setChecked(original, { force: true });
    await save(page, link);
    await reloadPrefs(page, box);
    await expect(box).toBeChecked({ checked: original });
  }
}

async function selectRoundTrip(page: Page, link: "Home" | "Playback" | "Subtitles", selector: string, pick: (current: string, options: string[]) => string) {
  await openPrefs(page, link);
  const sel = page.locator(`.page:not(.hide) ${selector}`);
  // The select is rendered before its options and stored value are applied.
  await expect(sel).toBeVisible();
  await expect(sel.locator("option")).not.toHaveCount(0);
  await waitIdle(page, 3_000);
  const original = await sel.inputValue();
  const options = await sel.locator("option").evaluateAll((o) => o.map((x) => (x as HTMLOptionElement).value));
  const next = pick(original, options);
  expect(next).not.toBe(original);
  try {
    await sel.selectOption(next);
    await save(page, link);
    await reloadPrefs(page, sel);
    await expect(sel).toHaveValue(next);
  } finally {
    await openPrefs(page, link);
    await expect(sel).toHaveValue(next).catch(() => {});
    await sel.selectOption(original);
    await save(page, link);
    await reloadPrefs(page, sel);
    await expect(sel).toHaveValue(original);
  }
}

const other = (cur: string, opts: string[]) => opts.find((o) => o !== cur && o !== "")!;

test("preferences menu lists the settings pages", async ({ page }) => {
  await loginAsAdmin(page);
  await page.getByRole("button", { name: /user menu/i }).click();
  await page.getByRole("menuitem", { name: "Settings" }).or(page.getByRole("link", { name: "Settings" })).first().click();
  await expect(page).toHaveURL(/#\/mypreferencesmenu/);
  for (const l of ["Display", "Home", "Playback", "Subtitles"]) {
    await expect(page.getByRole("link", { name: l, exact: true })).toBeVisible();
  }
});

test("display preferences: missing episodes setting persists", async ({ page }) => {
  await loginAsAdmin(page);
  await checkboxRoundTrip(page, "Display", /Display missing episodes within seasons/);
});

test("home preferences: hide watched checkbox persists", async ({ page }) => {
  await loginAsAdmin(page);
  await checkboxRoundTrip(page, "Home", /Hide watched content from/);
});

test("home preferences: home screen section choice persists", async ({ page }) => {
  await loginAsAdmin(page);
  // Section 2 is Continue Watching (empty value) by default; swap it with Next Up.
  await selectRoundTrip(page, "Home", "#selectHomeSection2", (cur) => (cur === "nextup" ? "" : "nextup"));
});

test("playback preferences: default audio track setting persists", async ({ page }) => {
  await loginAsAdmin(page);
  await checkboxRoundTrip(page, "Playback", /Play default audio track regardless of language/);
});

test("subtitle preferences: subtitle mode persists", async ({ page }) => {
  await loginAsAdmin(page);
  await selectRoundTrip(page, "Subtitles", "#selectSubtitlePlaybackMode", other);
});

test("api trace preferences flow matches the reference", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await checkboxRoundTrip(page, "Display", /Display missing episodes within seasons/);
  await selectRoundTrip(page, "Subtitles", "#selectSubtitlePlaybackMode", other);
  checkFlowTrace("flow-preferences", info.project.metadata.target, trace.unique(since));
});
