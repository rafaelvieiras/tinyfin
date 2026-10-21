import type { Page } from "@playwright/test";
import { test, expect, loginAsAdmin, waitIdle, adminFetch, findItemId } from "../lib/fixtures";
import { checkFlowTrace } from "../lib/flowtrace";

// User flows around per-user item data: favorites and played/resume state on Alpha Movie.
// Every spec arranges and restores its state through the API so the specs stay order-independent.

const TICKS_PER_SECOND = 10_000_000;

async function api(baseURL: string) {
  const { call, userId } = await adminFetch(baseURL);
  const id = await findItemId(call, "Movie", "Alpha Movie");
  const data = async () => (await call("GET", `/Users/${userId}/Items/${id}`)).json.UserData;
  const reset = async () => {
    await call("DELETE", `/Users/${userId}/FavoriteItems/${id}`);
    await call("DELETE", `/Users/${userId}/PlayedItems/${id}`);
  };
  return { call, userId, id, data, reset };
}

// A hash-only navigation back to a details page already visited in this session can show the
// client's stale copy of the item, so reload to read the server state.
const openAlpha = async (page: Page, id: string) => {
  await page.goto(`/web/#/details?id=${id}`);
  await page.reload();
  await expect(page.locator(".nameContainer .itemName, h1.itemName").first()).toContainText("Alpha Movie");
};
// The details buttons carry their state in data attributes (their title changes to a label).
const fav = (page: Page, on: boolean) => page.locator(`.page:not(.hide) button.btnUserRating[data-isfavorite="${on}"]`);
const played = (page: Page, on: boolean) => page.locator(`.page:not(.hide) button.btnPlaystate[data-played="${on}"]`);

async function favoriteFlow(page: Page, id: string) {
  await openAlpha(page, id);
  await fav(page, false).click();
  await expect(fav(page, true)).toBeVisible();
  // The Favorites tab of the home page lists it under its type.
  await page.getByRole("link", { name: "Favorites" }).click();
  await expect(page).toHaveURL(/#\/home\?tab=1/);
  const movies = page.locator(".verticalSection").filter({ has: page.locator(".sectionTitle, .sectionTitleTextButton", { hasText: /^Movies$/ }) });
  await expect(movies.locator(".card").filter({ hasText: "Alpha Movie" }).first()).toBeVisible();
  await expect(page.locator(".card").filter({ hasText: "Bravo Movie" })).toHaveCount(0);
  // Un-favorite from the details page; it leaves the Favorites tab after a reload.
  await openAlpha(page, id);
  await fav(page, true).click();
  await expect(fav(page, false)).toBeVisible();
  await page.goto("/web/#/home?tab=1");
  await waitIdle(page);
  await page.reload();
  await expect(page.locator(".card").filter({ hasText: "Alpha Movie" })).toHaveCount(0);
}

test("favorite toggle on a movie shows in the Favorites tab", async ({ page, baseURL }) => {
  const a = await api(baseURL!);
  await a.reset();
  try {
    await loginAsAdmin(page);
    await favoriteFlow(page, a.id);
    expect((await a.data()).IsFavorite).toBe(false);
  } finally {
    await a.reset();
  }
});

test("favorite toggle persists server-side and from the card overlay", async ({ page, baseURL }) => {
  const a = await api(baseURL!);
  await a.reset();
  try {
    await loginAsAdmin(page);
    await openAlpha(page, a.id);
    await fav(page, false).click();
    await expect.poll(async () => (await a.data()).IsFavorite).toBe(true);
    // The card overlay in search reflects the stored state and can toggle it back.
    await page.goto("/web/#/search");
    await page.locator("#searchTextInput").fill("alpha");
    const c = page.locator(".card").filter({ hasText: "Alpha Movie" }).first();
    await c.hover();
    await c.locator('button.cardOverlayButton[data-isfavorite="true"]').click();
    await expect.poll(async () => (await a.data()).IsFavorite).toBe(false);
  } finally {
    await a.reset();
  }
});

async function playedFlow(page: Page, id: string, data: () => Promise<any>) {
  await openAlpha(page, id);
  await played(page, false).click();
  await expect(played(page, true)).toBeVisible();
  await expect.poll(async () => (await data()).Played).toBe(true);
  // Library listing shows the played indicator on that card only.
  await page.goto("/web/#/home");
  await page.locator(".card").filter({ hasText: "Movies" }).first().click();
  const alpha = page.locator(".card").filter({ hasText: "Alpha Movie" }).first();
  await expect(alpha).toBeVisible();
  await expect(alpha.locator(".playedIndicator")).toBeVisible();
  await expect(page.locator(".card").filter({ hasText: "Bravo Movie" }).first().locator(".playedIndicator")).toHaveCount(0);
  await openAlpha(page, id);
  await played(page, true).click();
  await expect(played(page, false)).toBeVisible();
  await expect.poll(async () => (await data()).Played).toBe(false);
}

test("mark played / unplayed toggles the indicator", async ({ page, baseURL }) => {
  const a = await api(baseURL!);
  await a.reset();
  try {
    await loginAsAdmin(page);
    await playedFlow(page, a.id, a.data);
  } finally {
    await a.reset();
  }
});

test("resume position shows Resume, progress bar and Continue Watching", async ({ page, baseURL }) => {
  const a = await api(baseURL!);
  await a.reset();
  try {
    // The server ignores reported progress for items shorter than its minimum resume duration,
    // so the resume point is written directly through the user-data endpoint.
    const w = await a.call("POST", `/UserItems/${a.id}/UserData?userId=${a.userId}`, { PlaybackPositionTicks: 3 * TICKS_PER_SECOND });
    expect(w.status).toBeLessThan(300);
    await expect.poll(async () => (await a.data()).PlaybackPositionTicks, { timeout: 10_000 }).toBeGreaterThan(0);

    await loginAsAdmin(page);
    const cw = page.locator(".verticalSection").filter({ has: page.locator(".sectionTitle", { hasText: /^Continue Watching$/ }) });
    await expect(cw.locator(".card").filter({ hasText: "Alpha Movie" }).first()).toBeVisible();
    await expect(cw.locator(".card").filter({ hasText: "Alpha Movie" }).first().locator(".itemProgressBar")).toBeVisible();
    await openAlpha(page, a.id);
    await expect(page.locator(".page:not(.hide) button.btnPlay").first()).toHaveAttribute("title", "Resume");
    await expect(page.locator(".page:not(.hide) button.btnReplay").first()).toBeVisible();
    // Marking it played clears the resume point.
    await played(page, false).click();
    await expect.poll(async () => (await a.data()).PlaybackPositionTicks).toBe(0);
    await expect(page.locator(".page:not(.hide) button.btnPlay").first()).toHaveAttribute("title", "Play");
    await expect(page.locator(".page:not(.hide) button.btnReplay").first()).toBeHidden();
  } finally {
    await a.reset();
  }
});

test("api trace favorite and played flows match the reference", async ({ page, baseURL, apiTrace: trace }, info) => {
  const a = await api(baseURL!);
  await a.reset();
  try {
    await loginAsAdmin(page);
    const since = trace.mark();
    await favoriteFlow(page, a.id);
    await playedFlow(page, a.id, a.data);
    checkFlowTrace("flow-userdata", info.project.metadata.target, trace.unique(since));
  } finally {
    await a.reset();
  }
});
