import { test, expect } from '@playwright/test';
import { login, ITEM_ID } from './fixtures';

test('Direct Plays the seeded movie end to end', async ({ page }) => {
	await login(page);

	await page.goto(`/#/details?id=${ITEM_ID}`);

	// Regression guard: BaseItemDto.MediaType being empty meant
	// playbackManager.canPlay(item) was always false, so jellyfin-web
	// never rendered a Play button at all for a real, playable item (see
	// internal/api/dto.go's MediaType doc comment).
	const playButton = page.locator('.btnPlay').first();
	await expect(playButton).toBeVisible({ timeout: 15_000 });

	// Regression guard: the actual PlaybackInfo -> Direct Play -> stream
	// pipeline (internal/playback, handleVideoStream in internal/api/
	// playback.go) has to produce a real, byte-serving response — not
	// just a 200 on PlaybackInfo — for the reference client to actually
	// play anything. A Range request answered with 206 Partial Content
	// is what a real browser <video> element sends and expects; a plain
	// 200 (no Range honored) is also acceptable here since this fixture
	// is small enough to fetch whole.
	const streamResponse = page.waitForResponse(
		(res) => /\/Videos\/.*\/stream/.test(res.url()) && [200, 206].includes(res.status()),
		{ timeout: 15_000 }
	);
	await playButton.click();
	const response = await streamResponse;
	expect([200, 206]).toContain(response.status());
});
