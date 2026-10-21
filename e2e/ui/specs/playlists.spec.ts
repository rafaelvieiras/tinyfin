import type { Page } from "@playwright/test";
import { test, expect, loginAsAdmin, waitIdle, adminFetch } from "../lib/fixtures";
import { checkFlowTrace } from "../lib/flowtrace";

// User flows: create a playlist / collection through the "Add to ..." dialog, open it, remove an item.
// Created containers are deleted through the API (before and after) so the specs are order-independent.

const PL = "UI2 Playlist";
const COLL = "UI2 Collection";

async function purge(baseURL: string, type: "Playlist" | "BoxSet", name: string) {
  const { call } = await adminFetch(baseURL);
  const r = await call("GET", `/Items?Recursive=true&IncludeItemTypes=${type}&SearchTerm=${encodeURIComponent(name)}`);
  for (const it of r.json?.Items ?? []) await call("DELETE", `/Items/${it.Id}`);
}

const searchFor = async (page: Page, q: string) => {
  await page.goto("/web/#/search");
  await page.locator("#searchTextInput").fill(q);
};

/** Opens the card menu of a movie found by search and picks an entry. */
async function cardMenu(page: Page, movie: string, entry: string) {
  await searchFor(page, movie);
  const c = page.locator(".page:not(.hide) .card").filter({ hasText: movie }).first();
  await expect(c).toBeVisible();
  await c.hover();
  await c.getByRole("button", { name: "More" }).click();
  await page.locator(".actionSheet button", { hasText: entry }).first().click();
}

async function addToNew(page: Page, movie: string, entry: string, selectId: string, nameId: string, name: string) {
  await cardMenu(page, movie, entry);
  const dlg = page.locator(".dialog");
  await expect(dlg.locator(`#${selectId}`)).toHaveValue("");
  await dlg.locator(`#${nameId}`).fill(name);
  await dlg.locator("button.btnSubmit").click();
  await expect(dlg).toHaveCount(0);
}

async function addToExisting(page: Page, movie: string, entry: string, selectId: string, label: string) {
  await cardMenu(page, movie, entry);
  const dlg = page.locator(".dialog");
  await dlg.locator(`#${selectId}`).selectOption({ label });
  await dlg.locator("button.btnSubmit").click();
  await expect(dlg).toHaveCount(0);
}

async function openFromSearch(page: Page, group: string, name: string) {
  await searchFor(page, name);
  const sec = page.locator(".page:not(.hide) .verticalSection").filter({ has: page.locator(".sectionTitle", { hasText: new RegExp(`^${group}$`) }) });
  await sec.locator(".card .cardText a", { hasText: name }).first().click();
  await expect(page).toHaveURL(/#\/details\?id=/);
}

const rows = (page: Page) => page.locator(".page:not(.hide) .listItem");

async function playlistFlow(page: Page) {
  await addToNew(page, "Alpha Movie", "Add to playlist", "selectPlaylistToAddTo", "txtNewPlaylistName", PL);
  await addToExisting(page, "Bravo Movie", "Add to playlist", "selectPlaylistToAddTo", PL);
  await openFromSearch(page, "Playlists", PL);
  await expect(page.locator(".page:not(.hide) .nameContainer").filter({ hasText: PL }).first()).toBeVisible();
  await expect(rows(page)).toHaveCount(2);
  await expect(rows(page).first()).toContainText("Alpha Movie");
  await expect(rows(page).nth(1)).toContainText("Bravo Movie");
  // Remove the second entry through its row menu.
  await rows(page).nth(1).getByRole("button", { name: "More" }).click();
  await page.locator(".actionSheet button", { hasText: "Remove from playlist" }).first().click();
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText("Alpha Movie");
}

test("playlist: create via UI, add an item, open it, remove an item", async ({ page, baseURL }) => {
  await purge(baseURL!, "Playlist", PL);
  try {
    await loginAsAdmin(page);
    await playlistFlow(page);
    // The removal is stored server-side.
    const { call } = await adminFetch(baseURL!);
    const pl = (await call("GET", `/Items?Recursive=true&IncludeItemTypes=Playlist&SearchTerm=${encodeURIComponent(PL)}`)).json.Items[0];
    const items = (await call("GET", `/Playlists/${pl.Id}/Items`)).json;
    expect(items.Items.map((i: any) => i.Name.replace(/ \(\d+\)$/, ""))).toEqual(["Alpha Movie"]);
  } finally {
    await purge(baseURL!, "Playlist", PL);
  }
});

test("collection: create via UI and open it", async ({ page, baseURL }) => {
  await purge(baseURL!, "BoxSet", COLL);
  try {
    await loginAsAdmin(page);
    await addToNew(page, "Alpha Movie", "Add to collection", "selectCollectionToAddTo", "txtNewCollectionName", COLL);
    await openFromSearch(page, "Collections", COLL);
    await expect(page.locator(".page:not(.hide) .nameContainer").filter({ hasText: COLL }).first()).toBeVisible();
    await expect(page.locator(".page:not(.hide) .card").filter({ hasText: "Alpha Movie" }).first()).toBeVisible();
    const { call } = await adminFetch(baseURL!);
    const bs = (await call("GET", `/Items?Recursive=true&IncludeItemTypes=BoxSet&SearchTerm=${encodeURIComponent(COLL)}`)).json.Items[0];
    const kids = (await call("GET", `/Items?ParentId=${bs.Id}`)).json.Items;
    expect(kids).toHaveLength(1);
  } finally {
    await purge(baseURL!, "BoxSet", COLL);
  }
});

test("api trace playlist flow matches the reference", async ({ page, baseURL, apiTrace: trace }, info) => {
  await purge(baseURL!, "Playlist", PL);
  try {
    await loginAsAdmin(page);
    const since = trace.mark();
    await playlistFlow(page);
    await waitIdle(page);
    checkFlowTrace("flow-playlist", info.project.metadata.target, trace.unique(since));
  } finally {
    await purge(baseURL!, "Playlist", PL);
  }
});
