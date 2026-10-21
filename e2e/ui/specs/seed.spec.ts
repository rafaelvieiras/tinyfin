import { test, expect, loginAsAdmin, waitIdle, openItem } from "../lib/fixtures";

// Seed specs: written against the real Jellyfin web UI on the reference server first, then run
// unchanged against Tinyfin. The fixture tree is scripts/conformance-media.sh; libraries are
// "Movies" (Alpha/Bravo/Charlie Movie) and "Shows" (Test Show), created by lib/setup-libs.mjs.


test("first-run login page renders", async ({ page }) => {
  await page.goto("/web/");
  await expect(page).toHaveURL(/#\/login/);
  // The credentials form is shown directly (no public user tiles) and offers a submit button.
  await expect(page.locator("#txtManualName")).toBeVisible();
  await expect(page.locator("#txtManualPassword")).toBeVisible();
  await expect(page.locator("button.button-submit")).toBeVisible();
});

test("login as admin", async ({ page }) => {
  await loginAsAdmin(page);
  await expect(page.locator(".headerUserButton")).toHaveAttribute("title", "admin");
  const stored = await page.evaluate(() => localStorage.getItem("jellyfin_credentials"));
  expect(stored).toContain("AccessToken");
});

test("home page shows the libraries", async ({ page }) => {
  await loginAsAdmin(page);
  await expect(page.getByText("My Media", { exact: true })).toBeVisible();
  for (const lib of ["Movies", "Shows"]) {
    await expect(page.locator(".card").filter({ hasText: lib }).first()).toBeVisible();
  }
});

test("movies library lists the fixture items", async ({ page }) => {
  await loginAsAdmin(page);
  await page.locator(".card").filter({ hasText: "Movies" }).first().click();
  await expect(page).toHaveURL(/#\/(movies|list)/);
  await waitIdle(page);
  for (const title of ["Alpha Movie", "Bravo Movie", "Charlie Movie"]) {
    await expect(page.locator(".card").filter({ hasText: title }).first()).toBeVisible();
  }
});

test("movie detail page renders", async ({ page }) => {
  await loginAsAdmin(page);
  await page.locator(".card").filter({ hasText: "Movies" }).first().click();
  await openItem(page, "Alpha Movie");
  await expect(page).toHaveURL(/#\/details\?id=/);
  await expect(page.locator(".nameContainer .itemName, h1.itemName").first()).toContainText("Alpha Movie");
  await expect(page.locator("button.btnPlay").first()).toBeVisible();
  await expect(page.locator(".itemMiscInfo").first()).toContainText("2015");
});
