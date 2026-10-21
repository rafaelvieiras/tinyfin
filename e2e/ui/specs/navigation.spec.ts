import type { Page } from "@playwright/test";
import { test, expect, loginAsAdmin, waitIdle, openItem, adminFetch } from "../lib/fixtures";
import { checkFlowTrace } from "../lib/flowtrace";

// User flow: Shows -> series -> season -> episode detail (fixture: Test Show, S01 with 2 episodes, S02 with 1).

const idOf = (page: Page) => new URL(page.url().replace("#/", "")).searchParams.get("id")!;

async function flow(page: Page) {
  await page.getByRole("link", { name: "Shows" }).first().click();
  await expect(page).toHaveURL(/#\/tv/);
  await waitIdle(page);
  await openItem(page, "Test Show");
  await expect(page).toHaveURL(/#\/details\?id=/);
  await expect(page.locator(".nameContainer .itemName, h1.itemName").first()).toContainText("Test Show");
  const seasons = page.locator(".page:not(.hide) .card").filter({ hasText: /Season [12]/ });
  await expect(seasons).toHaveCount(2);
  const seriesId = idOf(page);

  await page.locator(".page:not(.hide) .card .cardText a", { hasText: "Season 1" }).first().click();
  await expect(page.locator(".page:not(.hide) .nameContainer").filter({ hasText: "Season 1" }).first()).toBeVisible();
  const eps = page.locator(".page:not(.hide) .listItem");
  await expect(eps).toHaveCount(2);
  await expect(eps.nth(1)).toContainText("2.");
  const seasonId = idOf(page);
  expect(seasonId).not.toBe(seriesId);

  await eps.nth(1).getByRole("button", { name: "Info" }).click();
  await expect(page).not.toHaveURL(new RegExp(`id=${seasonId}`));
  await expect(page.locator(".page:not(.hide) button.btnPlay").first()).toBeVisible();
  const episodeId = idOf(page);
  return { seriesId, seasonId, episodeId };
}

test("Shows -> series -> season -> episode detail", async ({ page, baseURL }) => {
  await loginAsAdmin(page);
  const ids = await flow(page);
  const { call } = await adminFetch(baseURL!);
  const ep = (await call("GET", `/Items/${ids.episodeId}`)).json;
  expect(ep.Type).toBe("Episode");
  expect(ep.IndexNumber).toBe(2);
  expect(ep.ParentIndexNumber).toBe(1);
  expect(ep.SeriesId).toBe(ids.seriesId);
  expect(ep.SeasonId).toBe(ids.seasonId);
  // Browser back retraces season and series pages.
  await page.goBack();
  await expect(page.locator(".page:not(.hide) .nameContainer").filter({ hasText: "Season 1" }).first()).toBeVisible();
  await page.goBack();
  await expect(page.locator(".page:not(.hide) .card").filter({ hasText: "Season 2" }).first()).toBeVisible();
});

test("season 2 lists its single episode", async ({ page }) => {
  await loginAsAdmin(page);
  await page.getByRole("link", { name: "Shows" }).first().click();
  await openItem(page, "Test Show");
  await page.locator(".page:not(.hide) .card .cardText a", { hasText: "Season 2" }).first().click();
  await expect(page.locator(".page:not(.hide) .nameContainer").filter({ hasText: "Season 2" }).first()).toBeVisible();
  await expect(page.locator(".page:not(.hide) .listItem")).toHaveCount(1);
});

test("api trace Shows -> episode flow matches the reference", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const since = trace.mark();
  await flow(page);
  await waitIdle(page);
  checkFlowTrace("flow-navigation", info.project.metadata.target, trace.unique(since));
});
