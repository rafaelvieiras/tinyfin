import { type Page, type Request } from "@playwright/test";

// Helpers for the playback specs: an admin REST client that can mutate, temporary libraries
// over the fixture folders that the default Movies/Shows libraries do not cover, and a
// recorder for the playback reporting calls the web player makes.

const ADMIN_USER = process.env.UI_ADMIN_USER ?? "admin";
const ADMIN_PASS = process.env.UI_ADMIN_PASS ?? "Conformance-Admin-1";
const AUTH = 'MediaBrowser Client="tinyfin-ui", Device="spec", DeviceId="tinyfin-ui-pb", Version="1.0"';

export interface Api {
  get(path: string): Promise<any>;
  send(method: string, path: string, body?: unknown, query?: Record<string, string>): Promise<{ status: number; json: any }>;
  userId: string;
}

export async function mutApi(baseURL: string): Promise<Api> {
  const login = await fetch(new URL("/Users/AuthenticateByName", baseURL), {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: AUTH },
    body: JSON.stringify({ Username: ADMIN_USER, Pw: ADMIN_PASS }),
  });
  if (!login.ok) throw new Error(`admin API login failed: HTTP ${login.status}`);
  const j = (await login.json()) as { AccessToken: string; User: { Id: string } };
  const h = { Authorization: `${AUTH}, Token="${j.AccessToken}"` };
  const send: Api["send"] = async (method, path, body, query) => {
    const u = new URL(path, baseURL);
    for (const [k, v] of Object.entries(query ?? {})) u.searchParams.set(k, v);
    const r = await fetch(u, {
      method,
      headers: body ? { ...h, "Content-Type": "application/json" } : h,
      body: body ? JSON.stringify(body) : undefined,
    });
    const t = await r.text();
    let json: any = null;
    try { json = t ? JSON.parse(t) : null; } catch { /* non-JSON body */ }
    return { status: r.status, json };
  };
  return { get: async (p) => (await send("GET", p)).json, send, userId: j.User.Id };
}

/** Folder on the server that holds the fixture tree (parent of the Movies library folder). */
export async function mediaRoot(api: Api): Promise<string> {
  const vf = await api.get("/Library/VirtualFolders");
  const movies = vf.find((v: any) => v.Name === "Movies");
  return String(movies.Locations[0]).replace(/\/Movies\/?$/, "");
}

async function waitFor<T>(fn: () => Promise<T | undefined | false>, nudge: () => Promise<unknown>, ms = 90_000): Promise<T> {
  const end = Date.now() + ms;
  let last = 0;
  for (;;) {
    const v = await fn();
    if (v) return v;
    if (Date.now() > end) throw new Error("timeout waiting for library scan");
    // a library created while another scan is running is sometimes never scanned: ask again
    if (Date.now() - last > 10_000) { last = Date.now(); await nudge(); }
    await new Promise((r) => setTimeout(r, 500));
  }
}

/** Creates a movie (or show) library over <root>/<dir> (idempotent) and waits until it has `count` movies/episodes. */
export async function ensureLibrary(api: Api, root: string, name: string, dir: string, count: number, kind: "movies" | "tvshows" = "movies", options: Record<string, unknown> = {}) {
  const vf = await api.get("/Library/VirtualFolders");
  if (!vf.some((v: any) => v.Name === name)) {
    const r = await api.send("POST", "/Library/VirtualFolders", { LibraryOptions: { PathInfos: [{ Path: `${root}/${dir}` }], ...options } }, {
      name, collectionType: kind, refreshLibrary: "true",
    });
    if (r.status !== 200 && r.status !== 204) throw new Error(`create library ${name}: HTTP ${r.status}`);
  }
  await waitFor(async () => {
    const items = await api.get(`/Items?IncludeItemTypes=${kind === "movies" ? "Movie" : "Episode"}&Recursive=true&Fields=Path`);
    return items.Items.filter((i: any) => String(i.Path).includes(`/${dir}/`)).length >= count;
  }, () => api.send("POST", "/Library/Refresh"));
}

export async function dropLibrary(api: Api, name: string) {
  await api.send("DELETE", "/Library/VirtualFolders", undefined, { name, refreshLibrary: "false" });
}

export async function findItem(api: Api, name: string, type = "Movie"): Promise<any> {
  const r = await api.get(`/Items?Recursive=true&IncludeItemTypes=${type}&Fields=Path,MediaStreams,MediaSources,Chapters`);
  // names may or may not carry the folder year depending on the server: match on the file path
  const it = r.Items.find((i: any) => String(i.Path).includes(`/${name}`));
  if (!it) throw new Error(`fixture item not found: ${name}`);
  return it;
}

export async function userData(api: Api, id: string) {
  return api.get(`/UserItems/${id}/UserData`);
}

/** Restores played state / resume position of an item to "unplayed, no position". */
export async function resetUserData(api: Api, id: string) {
  await api.send("DELETE", `/UserPlayedItems/${id}`);
  await api.send("POST", `/Users/${api.userId}/Items/${id}/UserData`, { PlaybackPositionTicks: 0, Played: false, PlayCount: 0 });
}

/** Records the playback reporting calls and the stream/HLS requests of a page. */
export class PlayRec {
  reports: { path: string; body: any }[] = [];
  media: { path: string; query: string; status: number }[] = [];
  /** PlaybackInfo exchanges: query + JSON body of the request and the chosen source. */
  infos: { query: URLSearchParams; body: any; direct: boolean; stream: boolean; transcodeUrl: string | null; container: string; status: number }[] = [];
  constructor(private origin: string) {}
  attach(page: Page) {
    page.on("request", (r: Request) => {
      const u = new URL(r.url());
      if (u.origin !== this.origin || r.method() !== "POST") return;
      if (/^\/Sessions\/Playing(\/Progress|\/Stopped)?$/.test(u.pathname)) {
        let body: any = null;
        try { body = r.postDataJSON(); } catch { /* none */ }
        this.reports.push({ path: u.pathname, body });
      }
    });
    page.on("response", async (r) => {
      const u = new URL(r.url());
      if (u.origin !== this.origin) return;
      if (r.request().method() === "POST" && /^\/Items\/[^/]+\/PlaybackInfo$/.test(u.pathname)) {
        const j = await r.json().catch(() => null);
        const ms = j?.MediaSources?.[0];
        let body: any = null;
        try { body = r.request().postDataJSON(); } catch { /* none */ }
        this.infos.push({ query: u.searchParams, body, direct: !!ms?.SupportsDirectPlay, stream: !!ms?.SupportsDirectStream, transcodeUrl: ms?.TranscodingUrl ?? null, container: ms?.Container ?? "", status: r.status() });
        return;
      }
      if (/^\/(videos|audio)\//i.test(u.pathname)) this.media.push({ path: u.pathname, query: u.search, status: r.status() });
    });
  }
  count(path: string) { return this.reports.filter((x) => x.path === path).length; }
  last(path: string) { return [...this.reports].reverse().find((x) => x.path === path); }
}

/** Snapshot of the <video> element. */
export const videoState = (page: Page) =>
  page.evaluate(() => {
    const v = document.querySelector("video") as HTMLVideoElement | null;
    if (!v) return null;
    return { t: v.currentTime, paused: v.paused, ended: v.ended, rs: v.readyState, err: v.error ? v.error.code : null, src: v.currentSrc, dur: v.duration, tracks: v.textTracks.length };
  });
