import { test, expect, loginAsAdmin, waitIdle } from "../lib/fixtures";
import { checkFlowTrace } from "../lib/flowtrace";

// User flow: the search page (header Search link -> type -> grouped results -> open a result).

const group = (page: import("@playwright/test").Page, title: string) =>
  page.locator(".verticalSection").filter({ has: page.locator(".sectionTitle", { hasText: new RegExp(`^${title}$`) }) });

async function flow(page: import("@playwright/test").Page) {
  await page.getByRole("link", { name: "Search" }).click();
  await expect(page).toHaveURL(/#\/search/);
  const input = page.locator("#searchTextInput");
  await input.fill("test");
  await expect(group(page, "Shows").locator(".card").filter({ hasText: "Test Show" }).first()).toBeVisible();
  await expect(group(page, "Episodes").locator(".card")).toHaveCount(3);
  await expect(group(page, "Movies")).toHaveCount(0);

  await input.fill("alpha");
  await expect(group(page, "Movies").locator(".card").filter({ hasText: "Alpha Movie" })).toHaveCount(1);
  await expect(group(page, "Shows")).toHaveCount(0);

  await input.fill("zzzqqq");
  await expect(page.locator(".searchResults .card")).toHaveCount(0);

  await input.fill("alpha");
  await group(page, "Movies").locator(".card .cardText a").first().click();
  await expect(page).toHaveURL(/#\/details\?id=/);
  await expect(page.locator(".nameContainer .itemName, h1.itemName").first()).toContainText("Alpha Movie");
  await waitIdle(page);
}

test("search groups results by type and opens a result", async ({ page }) => {
  await loginAsAdmin(page);
  await flow(page);
});

test("api trace search flow matches the reference", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await flow(page);
  checkFlowTrace("flow-search", info.project.metadata.target, trace.unique(since));
});
