#!/usr/bin/env bash
# One-time setup for the e2e suite: fetches jellyfin-web (the real,
# separately-licensed client this project targets wire compatibility
# with — see e2e/README.md) into e2e/.jellyfin-web, installs its
# dependencies, installs this suite's own dependencies, and downloads the
# Playwright browser binary. Safe to re-run.
set -euo pipefail
cd "$(dirname "$0")"

if [ ! -d .jellyfin-web ]; then
	git clone --depth 1 https://github.com/jellyfin/jellyfin-web.git .jellyfin-web
fi
(cd .jellyfin-web && npm install)

npm install
npx playwright install --with-deps chromium
