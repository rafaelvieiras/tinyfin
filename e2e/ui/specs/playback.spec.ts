import { mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { test, expect, loginAsAdmin, type TraceEntry } from "../lib/fixtures";
import type { Page } from "@playwright/test";
import { mutApi, mediaRoot, ensureLibrary, dropLibrary, findItem, userData, resetUserData, PlayRec, videoState, type Api } from "../lib/pb";

// Playback specs: the web player is driven in Chromium against the real Jellyfin 12.2 UI.
// Fixtures come from scripts/conformance-media.sh; the folders the default Movies/Shows
// libraries do not cover are added as temporary "PB ..." libraries and removed afterwards.
// Every spec restores the played state / resume position it touched.

const TMP_LIBS: [string, string, number, ("movies" | "tvshows")?, Record<string, unknown>?][] = [
  ["PB Long", "Long", 1],
  ["PB AvTrack", "AvTrack", 2],
  // 30 s clip with chapters at 0/10/20 s; the reference extracts chapter stills and trickplay sheets for it during the scan
  // (it makes none for the 6 s chapter fixtures, and the 1500 s trickplay fixture only delays the scan)
  ["PB ChapterImg", "ChapterImg", 2, "movies", { EnableChapterImageExtraction: true, ExtractChapterImagesDuringLibraryScan: true, EnableTrickplayImageExtraction: true, ExtractTrickplayImagesDuringLibraryScan: true }],
  ["PB UpNext", "UpNext", 2, "tvshows"],
];

let api: Api;
const touched = new Set<string>();

test.beforeAll(async ({ baseURL }) => {
  test.setTimeout(300_000);
  api = await mutApi(baseURL!);
  const root = await mediaRoot(api);
  for (const [name, dir, n, kind, opts] of TMP_LIBS) await ensureLibrary(api, root, name, dir, n, kind, opts);
});

test.afterEach(async () => {
  for (const id of touched) await resetUserData(api, id);
  touched.clear();
});

test.afterAll(async () => {
  for (const [name] of TMP_LIBS) await dropLibrary(api, name);
});

let rec: PlayRec;
test.beforeEach(async ({ page, baseURL }) => {
  rec = new PlayRec(new URL(baseURL!).origin);
  rec.attach(page);
});

const seen = (id: string) => { touched.add(id); return id; };

/** Opens the details page of an item and presses its Play button. */
async function startPlayback(page: Page, id: string, button = "button.btnPlay") {
  await page.goto(`/web/#/details?id=${id}`);
  await page.locator(button).first().click();
  await expect(page.locator("video")).toBeAttached();
}

/** Waits until the video has played for at least `secs` (currentTime advancing, not paused, no error). */
async function expectPlaying(page: Page, secs = 1) {
  await expect.poll(async () => {
    const v = await videoState(page);
    return v && v.err === null && !v.paused && v.rs >= 3 ? v.t : -1;
  }, { timeout: 30_000, message: "video element should be playing" }).toBeGreaterThan(secs);
  await expect(page.locator(".dialogContainer .dialog:visible"), await page.locator(".dialogContainer .dialog:visible").allInnerTexts().then((t) => t.join("/"))).toHaveCount(0);
}

const showOsd = async (page: Page) => { await page.mouse.move(500 + Math.random() * 50, 300); };

/** Opens an OSD popup button and returns the visible action sheet entries. */
async function openMenu(page: Page, btn: string) {
  await showOsd(page);
  await page.locator(btn).first().click();
  const items = page.locator(".actionSheet .actionSheetMenuItem:visible");
  await expect(items.first()).toBeVisible();
  return items;
}

async function closePlayer(page: Page) {
  // the web client occasionally pushes an extra history entry while a stream is reloading
  for (let i = 0; i < 3; i++) {
    await page.goBack();
    if (await page.locator("video").count().then((n) => n === 0).catch(() => false)) {
      await page.waitForTimeout(300);
      if ((await page.locator("video").count()) === 0) return;
    }
    await page.waitForTimeout(1500);
    if ((await page.locator("video").count()) === 0) return;
  }
  await expect(page.locator("video")).toHaveCount(0, { timeout: 5_000 });
}

test("play: direct play starts the video element", async ({ page }) => {
  await loginAsAdmin(page);
  const it = await findItem(api, "Alpha Movie");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 1);
  const v = (await videoState(page))!;
  // h264/aac mp4 in a browser profile: the original file is served as-is
  expect(v.src.toLowerCase()).toContain(`/videos/${it.Id}/stream`);
  expect(rec.infos.at(-1)).toMatchObject({ direct: true, transcodeUrl: null, status: 200 });
  expect(rec.media.length).toBeGreaterThan(0);
  expect(rec.media.every((m) => m.status === 200 || m.status === 206)).toBe(true);
  expect(rec.count("/Sessions/Playing")).toBe(1);
});

/** Seeks by clicking the OSD position slider at a fraction of its width. */
async function seekTo(page: Page, frac: number) {
  await showOsd(page);
  const s = page.locator(".osdPositionSlider").first();
  const b = (await s.boundingBox())!;
  await page.mouse.click(b.x + b.width * frac, b.y + b.height / 2);
}

test("progress: pause, seek, resume and stop are reported and the resume position persists", async ({ page }) => {
  await loginAsAdmin(page);
  const it = await findItem(api, "Long Movie");
  const id = seen(it.Id);
  await startPlayback(page, id);
  await expectPlaying(page, 1);

  await seekTo(page, 0.5);
  await expect.poll(async () => (await videoState(page))?.t ?? 0, { timeout: 20_000 }).toBeGreaterThan(120);

  await showOsd(page);
  await page.locator(".btnPause").first().click();
  await expect.poll(async () => (await videoState(page))?.paused).toBe(true);
  // the pause is reported with IsPaused on the next progress call
  await expect.poll(() => rec.reports.some((r) => r.path === "/Sessions/Playing/Progress" && r.body?.IsPaused === true), { timeout: 15_000 }).toBe(true);
  const pausedAt = (await videoState(page))!.t;
  await page.waitForTimeout(1500);
  expect(Math.abs((await videoState(page))!.t - pausedAt)).toBeLessThan(0.5);

  await showOsd(page);
  await page.locator(".btnPause").first().click();
  await expect.poll(async () => (await videoState(page))!.t, { timeout: 15_000 }).toBeGreaterThan(pausedAt + 0.5);
  await closePlayer(page);

  await expect.poll(() => rec.count("/Sessions/Playing/Stopped"), { timeout: 15_000 }).toBe(1);
  expect(rec.count("/Sessions/Playing")).toBe(1);
  expect(rec.reports.some((r) => r.path === "/Sessions/Playing/Progress" && r.body?.IsPaused === false)).toBe(true);
  const stopped = rec.last("/Sessions/Playing/Stopped")!.body;
  expect(stopped.ItemId).toBe(id);
  expect(stopped.PositionTicks).toBeGreaterThan(100 * 1e7);

  const ud = await userData(api, id);
  expect(ud.PlaybackPositionTicks).toBeGreaterThan(100 * 1e7);
  expect(Math.abs(ud.PlaybackPositionTicks - stopped.PositionTicks)).toBeLessThan(5 * 1e7);

  // a fresh load of the details page offers to resume
  await page.goto(`/web/#/details?id=${id}`);
  await page.reload();
  const resume = page.locator('button.btnPlay[title="Resume"]').first();
  await expect(resume).toBeVisible();
  await resume.click();
  // resuming starts near the stored position rather than at 0
  await expect.poll(async () => (await videoState(page))?.t ?? 0, { timeout: 30_000 }).toBeGreaterThan(100);
  await expectPlaying(page, 100);
});

/** Per-browser quality setting the web client reads (it is what the Quality menu writes). */
async function limitBitrate(page: Page, bps: number) {
  await page.addInitScript((v) => {
    for (const net of ["true", "false"]) {
      localStorage.setItem(`maxbitrate-Video-${net}`, String(v));
      localStorage.setItem(`enableautobitratebitrate-Video-${net}`, "false");
    }
  }, bps);
}

test("quality menu: the 420 kbps preset is sent as MaxStreamingBitrate and a lower-bitrate source stays direct", async ({ page }) => {
  await loginAsAdmin(page);
  const it = await findItem(api, "Long Movie");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 1);
  expect(rec.infos.at(-1)).toMatchObject({ direct: true, transcodeUrl: null });

  await showOsd(page);
  await page.locator(".btnVideoOsdSettings").click();
  await page.locator('.actionSheetMenuItem[data-id="quality"]').click();
  // the submenu lists "Auto" and the bitrates below the source bitrate; the lowest preset is 420 kbps
  // (a React menu, unlike the action sheets used elsewhere in the OSD), above the ~110 kbps fixture
  const opts = page.getByText(/^\d+(\.\d+)? [KM]bps$/i);
  await expect(opts.first()).toBeVisible();
  await opts.last().click();
  await expect.poll(() => rec.infos.length, { timeout: 15_000 }).toBeGreaterThan(1);
  await expectPlaying(page, 1);
  expect(rec.infos.at(-1)!.body.MaxStreamingBitrate).toBe(420000);
  expect(rec.infos.at(-1)).toMatchObject({ direct: true, transcodeUrl: null });
  await closePlayer(page);
});

test("transcode: a bitrate limit below the source makes the server produce HLS", async ({ page }) => {
  await limitBitrate(page, 60_000);
  await loginAsAdmin(page);
  const it = await findItem(api, "Long Movie");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 1);
  const info = rec.infos.at(-1)!;
  expect(info.body.MaxStreamingBitrate).toBe(60000);
  expect(info.direct).toBe(false);
  expect(info.transcodeUrl).toMatch(/master\.m3u8/);
  const playlists = rec.media.filter((m) => /\.m3u8$/.test(m.path));
  const segments = rec.media.filter((m) => !/\.m3u8$/.test(m.path) && /\/hls1\//.test(m.path));
  expect(playlists.length).toBeGreaterThan(0);
  expect(segments.length).toBeGreaterThan(0);
  expect([...playlists, ...segments].every((m) => m.status === 200)).toBe(true);
  const v = (await videoState(page))!;
  expect(v.src.startsWith("blob:") || /m3u8/.test(v.src)).toBe(true);
  expect(rec.count("/Sessions/Playing")).toBe(1);
  await closePlayer(page);
  await expect.poll(() => rec.count("/Sessions/Playing/Stopped"), { timeout: 15_000 }).toBe(1);
});

/** Picks an entry of the audio / subtitle OSD menu by its label. */
async function pickTrack(page: Page, btn: string, label: RegExp) {
  const items = await openMenu(page, btn);
  await items.filter({ hasText: label }).first().click();
}

const lastProgress = (field: string) => rec.reports.filter((r) => r.path === "/Sessions/Playing/Progress" && r.body && field in r.body).at(-1)?.body[field];

test("audio: the audio menu lists both tracks and switching reloads the stream", async ({ page }) => {
  await loginAsAdmin(page);
  const it = await findItem(api, "Av Multi");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 1);
  expect(rec.infos.at(-1)).toMatchObject({ direct: true, container: "mkv" });

  const items = await openMenu(page, ".btnAudio");
  await expect(items).toHaveCount(2);
  await expect(items.nth(0)).toContainText("English");
  await expect(items.nth(0)).toContainText("Default");
  await expect(items.nth(1)).toContainText("Portuguese");
  await items.nth(1).click();
  await expect.poll(() => rec.infos.length, { timeout: 15_000 }).toBeGreaterThan(1);
  await expectPlaying(page, 1);
  // an MP3 track the browser profile cannot take from the MKV: the server must transcode that track via HLS
  expect(rec.infos.at(-1)!.direct).toBe(false);
  expect(rec.infos.at(-1)!.transcodeUrl).toMatch(/master\.m3u8.*AudioStreamIndex=2/i);
  expect(rec.media.some((m) => /AudioStreamIndex=2/i.test(m.query))).toBe(true);
  expect(rec.media.every((m) => m.status === 200 || m.status === 206)).toBe(true);
  await closePlayer(page);
});

/** Text tracks of the <video> that are currently showing, with how many cues they hold. */
const shownTracks = (page: Page) =>
  page.evaluate(() => {
    const v = document.querySelector("video") as HTMLVideoElement | null;
    return v ? [...v.textTracks].filter((t) => t.mode === "showing").map((t) => ({ kind: t.kind, cues: t.cues?.length ?? 0 })) : [];
  });

/** Pauses so the 4 s fixture cannot end while a menu is used. */
async function pausePlayback(page: Page) {
  await showOsd(page);
  await page.locator(".btnPause").first().click();
  await expect.poll(async () => (await videoState(page))?.paused).toBe(true);
}

test("subtitles: the menu lists Off and both embedded tracks; a text track is shown, then turned off", async ({ page }) => {
  await loginAsAdmin(page);
  const it = await findItem(api, "Av Multi");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 0.5);
  await pausePlayback(page);

  const items = await openMenu(page, ".btnSubtitles");
  await expect(items.filter({ hasText: /^Off$/ })).toHaveCount(1);
  await expect(items.filter({ hasText: "English - Default - SUBRIP" })).toHaveCount(1);
  await expect(items.filter({ hasText: "Portuguese - ASS" })).toHaveCount(1);
  await items.filter({ hasText: /^English/ }).first().click();
  // an embedded SubRip track is converted by the server and shown as a browser text track
  await expect.poll(async () => (await shownTracks(page)).length, { timeout: 15_000 }).toBe(1);
  expect((await shownTracks(page))[0].cues).toBeGreaterThan(0);
  expect(rec.media.some((m) => /\/Subtitles\/3\/0\/Stream\./i.test(m.path) && m.status === 200)).toBe(true);
  // the stream itself was not restarted
  expect(rec.infos).toHaveLength(1);

  await pickTrack(page, ".btnSubtitles", /^Off$/);
  await expect.poll(async () => (await shownTracks(page)).length, { timeout: 15_000 }).toBe(0);
  await closePlayer(page);
});

test("subtitles: with a transcode and \"always burn in\" the selected track is burned into the HLS stream", async ({ page }) => {
  await limitBitrate(page, 100_000);
  await page.addInitScript(() => localStorage.setItem("alwaysBurnInSubtitleWhenTranscoding", "true"));
  await loginAsAdmin(page);
  const it = await findItem(api, "Av Multi");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 0.5);
  await pausePlayback(page);
  await pickTrack(page, ".btnSubtitles", /^English/);
  await expect.poll(() => rec.infos.some((i) => /SubtitleStreamIndex=3/i.test(i.transcodeUrl ?? "")), { timeout: 15_000 }).toBe(true);
  // burned in: the track index travels in the transcoding URL and nothing is fetched for the browser
  expect(rec.infos.at(-1)!.transcodeUrl).toMatch(/master\.m3u8.*SubtitleStreamIndex=3/i);
  expect(rec.media.some((m) => /\/Subtitles\/3\//i.test(m.path))).toBe(false);
  expect(await shownTracks(page)).toHaveLength(0);
  expect(rec.media.filter((m) => /\/hls1\//.test(m.path)).every((m) => m.status === 200)).toBe(true);
  await closePlayer(page);
});

test("subtitles: without burn-in a transcoded stream keeps the subtitle as an external text track", async ({ page }) => {
  await limitBitrate(page, 100_000);
  await loginAsAdmin(page);
  const it = await findItem(api, "Av Multi");
  await startPlayback(page, seen(it.Id));
  await expectPlaying(page, 0.5);
  await pausePlayback(page);
  await pickTrack(page, ".btnSubtitles", /^English/);
  await expect.poll(async () => (await shownTracks(page)).length, { timeout: 15_000 }).toBe(1);
  expect(rec.infos.at(-1)!.direct).toBe(false);
  expect(rec.infos.at(-1)!.transcodeUrl).not.toMatch(/SubtitleStreamIndex=3/i);
  expect(rec.media.some((m) => /\/Subtitles\/3\/0\/Stream\./i.test(m.path) && m.status === 200)).toBe(true);
  await closePlayer(page);
});

test("next episode: the Up Next popup appears near the end and Start Now plays the next episode", async ({ page }) => {
  await loginAsAdmin(page);
  const e1 = await findItem(api, "Up Show (2022) S01E01", "Episode");
  const e2 = await findItem(api, "Up Show (2022) S01E02", "Episode");
  seen(e1.Id); seen(e2.Id);
  await startPlayback(page, e1.Id);
  await expectPlaying(page, 1);
  // The client shows the popup only while the OSD is hidden, the runtime is >= 10 min and 20-30 s
  // are left: jump to ~33 s before the end, move the pointer off the controls and let the OSD hide.
  await seekTo(page, 0.953);
  await page.mouse.move(640, 150);
  const popup = page.locator(".upNextContainer .btnStartNow");
  await expect(popup).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".upNextDialog-title")).toContainText("Up Show");
  await popup.click();
  await expect.poll(() => rec.reports.some((r) => r.path === "/Sessions/Playing" && r.body?.ItemId === e2.Id), { timeout: 30_000 }).toBe(true);
  await expectPlaying(page, 0.5);
  expect(rec.reports.some((r) => r.path === "/Sessions/Playing/Stopped" && r.body?.ItemId === e1.Id)).toBe(true);
  await closePlayer(page);
});

test("chapters: the details page lists the chapters and a chapter starts playback at its position", async ({ page }) => {
  test.setTimeout(120_000);
  const it = await findItem(api, "Chapter Pics");
  seen(it.Id);
  // chapter thumbnails are extracted during the library scan; the web UI hides chapters without one
  await expect.poll(async () => ((await api.get(`/Items/${it.Id}?userId=${api.userId}`)).Chapters ?? [])[0]?.ImageTag ?? "", { timeout: 40_000 }).not.toBe("");
  await loginAsAdmin(page);
  await page.goto(`/web/#/details?id=${it.Id}`);
  const cards = page.locator("#scenesContent .card");
  await expect(cards).toHaveCount(3);
  await expect(cards.nth(0)).toContainText("One");
  await expect(cards.nth(1)).toContainText("Two");
  await cards.nth(1).locator(".cardContent, .cardImageContainer").first().click();
  await expect.poll(() => rec.count("/Sessions/Playing"), { timeout: 20_000 }).toBe(1);
  // the second chapter starts at 10 s
  expect(rec.last("/Sessions/Playing")!.body.PositionTicks).toBeGreaterThanOrEqual(10 * 1e7);
  await closePlayer(page);
});

test("trickplay: hovering the position slider shows a thumbnail served from the trickplay sheets", async ({ page }) => {
  test.setTimeout(120_000);
  const it = await findItem(api, "Chapter Pics");
  seen(it.Id);
  await expect.poll(async () => Object.keys((await api.get(`/Items/${it.Id}?userId=${api.userId}`)).Trickplay ?? {}).length, { timeout: 40_000 }).toBeGreaterThan(0);
  await loginAsAdmin(page);
  await startPlayback(page, it.Id);
  await expectPlaying(page, 0.5);
  await pausePlayback(page);
  const s = page.locator(".osdPositionSlider").first();
  const b = (await s.boundingBox())!;
  await page.mouse.move(b.x + b.width * 0.5, b.y + b.height / 2);
  await expect(page.locator(".chapterThumbContainer, .chapterThumb").first()).toBeVisible({ timeout: 10_000 });
  await expect.poll(() => rec.media.some((m) => /\/Trickplay\/\d+\/\d+\.jpg/i.test(m.path)), { timeout: 10_000 }).toBe(true);
  await closePlayer(page);
});

// Golden API traces of a playback session (Play -> a few seconds -> close), same scheme as trace.spec.ts:
// golden files are written only by the reference (`run.sh --update-golden`); a later run must still
// cover every golden endpoint and Tinyfin must not fail where the reference succeeded.
const key = (e: { method: string; path: string }) => `${e.method} ${e.path}`;
function checkGolden(name: string, flow: string, got: TraceEntry[], target: string) {
  const file = join(__dirname, "..", "golden", `${name}.json`);
  if (process.env.UPDATE_GOLDEN === "1") {
    expect(target, "golden may only be generated against the reference").toBe("ref");
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, JSON.stringify({ flow, entries: got }, null, 2) + "\n");
    return;
  }
  expect(existsSync(file), "golden missing: run `run.sh --update-golden` against the reference").toBe(true);
  const golden: TraceEntry[] = JSON.parse(readFileSync(file, "utf8")).entries;
  const gotKeys = new Set(got.map(key));
  expect([...new Set(golden.map(key))].filter((k) => !gotKeys.has(k)), "endpoints the reference UI calls that this run did not").toEqual([]);
  if (target === "tinyfin") {
    const refOk = new Set(golden.filter((e) => e.status < 400).map(key));
    expect(got.filter((e) => e.status >= 400 && refOk.has(key(e))).map((e) => `${key(e)} -> ${e.status}`), "responses that fail here but succeed on the reference").toEqual([]);
    const refBad = new Set(golden.filter((e) => e.status >= 400).map(key));
    expect(got.filter((e) => e.status >= 400 && !refBad.has(key(e)) && !refOk.has(key(e))).map((e) => `${key(e)} -> ${e.status}`), "4xx/5xx on endpoints the reference never called").toEqual([]);
  }
}

/** Drops what varies with timing or carries the server address: segments (hls1/...), images, progress ticks, home-page background calls (covered by trace.spec), absolute-URL paths. */
const stable = (es: TraceEntry[]) => es.filter((e) => !/\/hls1\/|\/Images\/|\/Playing\/Progress$|^\/(SyncPlay|LiveTv)\/|^http/.test(e.path));

test("api trace: direct-play session matches the reference", async ({ page, apiTrace: trace }, info) => {
  await loginAsAdmin(page);
  const it = await findItem(api, "Alpha Movie");
  await page.goto(`/web/#/details?id=${seen(it.Id)}`);
  const since = trace.mark();
  await page.locator("button.btnPlay").first().click();
  await expectPlaying(page, 1.5);
  await page.goBack();
  await expect.poll(() => trace.unique(since).some((e) => e.path === "/Sessions/Playing/Stopped"), { timeout: 15_000 }).toBe(true);
  const end = trace.mark();
  await expect(page.locator("video")).toHaveCount(0);
  checkGolden("playback-direct", "details -> Play -> 1.5 s -> close", stable(trace.unique(since, end)), info.project.metadata.target as string);
});

test("api trace: HLS transcode session matches the reference", async ({ page, apiTrace: trace }, info) => {
  await limitBitrate(page, 60_000);
  await loginAsAdmin(page);
  const it = await findItem(api, "Long Movie");
  await page.goto(`/web/#/details?id=${seen(it.Id)}`);
  const since = trace.mark();
  await page.locator("button.btnPlay").first().click();
  await expectPlaying(page, 1.5);
  await page.goBack();
  await expect.poll(() => trace.unique(since).some((e) => e.path === "/Sessions/Playing/Stopped"), { timeout: 15_000 }).toBe(true);
  const got = stable(trace.unique(since, trace.mark()));
  await expect(page.locator("video")).toHaveCount(0);
  checkGolden("playback-hls", "details -> Play (60 kbps limit) -> 1.5 s -> close", got, info.project.metadata.target as string);
});
