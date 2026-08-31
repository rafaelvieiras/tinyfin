import { test, expect } from '@playwright/test';
import { login, LIBRARY_ID, ITEM_NAME } from './fixtures';

test('shows the seeded movie with a loaded poster image', async ({ page }) => {
	await login(page);

	await page.goto(`/#/movies?topParentId=${LIBRARY_ID}&collectionType=movies`);

	const card = page.getByRole('link', { name: ITEM_NAME }).first();
	await expect(card).toBeVisible();

	// Regression guard: BaseItemDto.ImageTags being empty meant
	// jellyfin-web never even requested the poster (see internal/api/
	// dto.go's ImageTags doc comment) — a passing card-visible assertion
	// alone wouldn't have caught that, since the fallback icon renders
	// too. The card's <img> is a sibling of the title link in the DOM,
	// not a descendant of it, so this scopes to the page's <main> instead
	// — safe because this library has exactly one seeded item, so
	// there's only one card/poster on the page at all.
	const poster = page.locator('main img').first();
	await expect(poster).toBeVisible();
	await expect
		.poll(() => poster.evaluate((el: HTMLImageElement) => el.naturalWidth))
		.toBeGreaterThan(0);
});
