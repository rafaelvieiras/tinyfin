import { type Page, expect } from '@playwright/test';

// Mirrors cmd/e2eseed/main.go's constants — kept in sync by hand since a
// Go const can't be imported into a TS test. If e2eseed's values change,
// update these too.
export const USERNAME = 'e2e';
export const PASSWORD = 'E2eTest123!';
export const LIBRARY_ID = 'lib-e2e-movies';
export const ITEM_ID = 'item-e2e-sample';
export const ITEM_NAME = 'Tinyfin E2E Sample';

export const TINYFIN_URL = process.env.TINYFIN_URL || 'http://127.0.0.1:8096';

// webpack-dev-server overlays any console error as a full-screen iframe
// that intercepts clicks — a dev-server-only artifact (see
// e2e/README.md), not something a production jellyfin-web build does.
// Some of the console errors this project's own gaps produce (e.g. no
// public-user-list endpoint) are expected and harmless. It lives inside a
// shadow root (a plain `document.getElementById` from page.evaluate never
// sees it), and `{ force: true }` alone doesn't help either — Playwright
// still dispatches the click at the target's real screen coordinates, and
// the browser hands it to whatever's actually on top there. Playwright's
// own locator engine does pierce shadow roots for a plain CSS selector,
// so find it that way and remove the real node.
export async function removeDevServerOverlay(page: Page) {
	await page
		.locator('#webpack-dev-server-client-overlay')
		.evaluateAll((nodes) => nodes.forEach((n) => n.remove()));
}

// A one-shot removal isn't enough on its own: a fresh console error can
// bring the overlay right back between the removal call and whatever
// interaction follows it (Playwright's own click retries just poll — they
// never call removeDevServerOverlay themselves). Reacting to every
// console error as it happens keeps it suppressed continuously instead.
export function suppressDevServerOverlay(page: Page) {
	page.on('console', (msg) => {
		if (msg.type() === 'error') removeDevServerOverlay(page).catch(() => {});
	});
}

// Drives the exact connect -> manual-login -> home flow verified by hand
// against a real jellyfin-web build (see e2e/README.md's "Why this
// exists" section). Leaves the page at #/home, logged in as the e2eseed
// user. Each Playwright test gets a fresh, storage-free browser context,
// so this always starts from "no server known yet" — no server-mismatch
// dialog to handle here (that only happens when a *different* server
// answers at a previously-trusted URL, e.g. across manual dev restarts).
export async function login(page: Page) {
	suppressDevServerOverlay(page);

	await page.goto('/');
	await page.locator('role=link[name="Add Server"]').or(page.locator('role=link[name="Adicionar Servidor"]')).click();

	await page.locator('role=textbox[name="Host"]').or(page.locator('role=textbox[name="Servidor"]')).fill(TINYFIN_URL);
	await page.locator('role=button[name="Connect"]').or(page.locator('role=button[name="Conectar"]')).click();

	// Tinyfin has no public-user-list endpoint (docs/04), so jellyfin-web
	// always lands on this "pick a way to sign in" screen rather than a
	// list of accounts — Manual Login is always the button to click here,
	// not a maybe-present shortcut.
	await page.locator('role=button[name="Manual Login"]').or(page.locator('role=button[name="Acesso Manual"]')).click();

	await page.locator('role=textbox[name="User"]').or(page.locator('role=textbox[name="Usuário"]')).fill(USERNAME);
	await page.locator('role=textbox[name="Password"]').or(page.locator('role=textbox[name="Senha"]')).fill(PASSWORD);
	await page.locator('role=button[name="Sign In"]').or(page.locator('role=button[name="Entrar"]')).click();

	await expect(page).toHaveURL(/#\/home/, { timeout: 15_000 });
}
