import { test, expect } from '@playwright/test';
import { login } from './fixtures';

test('logs in and reaches home without a crashed library drawer', async ({ page }) => {
	const consoleErrors: string[] = [];
	page.on('console', (msg) => {
		if (msg.type() === 'error') consoleErrors.push(msg.text());
	});

	await login(page);

	// Regression guard for a real bug found against a live jellyfin-web
	// build: a nil/omitted UserDto.Policy or .Configuration crashes
	// libraryMenu.js / the playback pipeline with exactly this error
	// shape (see internal/api/users.go's userPolicyDto and
	// userConfigurationDto doc comments).
	const crashed = consoleErrors.some((e) => e.includes("Cannot read properties of undefined"));
	expect(crashed, `console had a null-dereference crash:\n${consoleErrors.join('\n')}`).toBe(false);
});
