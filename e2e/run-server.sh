#!/usr/bin/env bash
# Seeds a fresh, throwaway sqlite database with the synthetic e2e fixtures
# (cmd/e2eseed) and starts a real tinyfin server against it. Run from the
# repo root (Playwright's webServer config sets cwd accordingly) so
# testdata/e2e/'s relative paths, as stored by e2eseed, resolve.
set -euo pipefail

DB="$(mktemp -d)/e2e.db"
go run ./cmd/e2eseed -db "$DB"
exec go run ./cmd/tinyfin -data "$DB" -addr :8096
