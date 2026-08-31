import { defineConfig } from '@playwright/test';
import path from 'node:path';

// jellyfin-web is a separate, GPL-licensed client repo — not vendored
// into this repo (see README.md). e2e/setup.sh clones it into
// e2e/.jellyfin-web by default; JELLYFIN_WEB_DIR overrides that if it's
// checked out elsewhere.
const jellyfinWebDir = process.env.JELLYFIN_WEB_DIR || path.join(__dirname, '.jellyfin-web');
const repoRoot = path.resolve(__dirname, '..');

export default defineConfig({
	testDir: './tests',
	timeout: 30_000,
	fullyParallel: false,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	reporter: 'list',
	use: {
		baseURL: 'http://127.0.0.1:8080',
		trace: 'retain-on-failure',
		video: 'retain-on-failure',
	},
	// Both servers are the exact same processes/ports used for the manual
	// real-client verification this suite replaces: a native tinyfin
	// server on :8096 (not Docker — the read-only-mount concern that
	// motivated Docker for that manual pass doesn't apply to a
	// repo-owned, throwaway test fixture) and jellyfin-web's own dev
	// server on :8080.
	webServer: [
		{
			command: `bash ${path.join(__dirname, 'run-server.sh')}`,
			cwd: repoRoot,
			port: 8096,
			timeout: 60_000,
			reuseExistingServer: !process.env.CI,
		},
		{
			command: 'npm start',
			cwd: jellyfinWebDir,
			port: 8080,
			timeout: 180_000,
			reuseExistingServer: !process.env.CI,
		},
	],
});
