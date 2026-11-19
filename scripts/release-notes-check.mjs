#!/usr/bin/env node
// Renders release notes through the real plugin, offline, with the options the
// release workflow uses.
//
// Why this exists: `@semantic-release/release-notes-generator` compiles a
// Handlebars template that the changelog preset provides, and the two are
// versioned apart. `conventional-changelog-conventionalcommits@10` ships
// `@conventional-changelog/template`, which needs
// `conventional-changelog-writer@9` or newer, while
// `@semantic-release/release-notes-generator@14` depends on writer 8. Every
// step of `semantic-release` succeeds on such a tree until `generateNotes`
// renders -- so `npm ci`, `semantic-release --dry-run` on a branch that
// releases nothing, and the whole test suite all pass, and the release breaks
// on the next push to `main`.
//
// The check is the render itself: the plugin's own `generateNotes`, the options
// from `.releaserc.json`, and one fake `feat:` commit. It reports the preset and
// writer versions, so a bump that lands here says what it landed on.

import { readFileSync } from 'node:fs'

const repoRoot = new URL('..', import.meta.url)

function versions() {
  const found = {}
  for (const name of [
    '@semantic-release/release-notes-generator',
    'conventional-changelog-writer',
    'conventional-changelog-conventionalcommits',
    '@conventional-changelog/template',
  ]) {
    // Read the package manifest from disk: the plugin packages do not export
    // `./package.json`, so `require` cannot resolve it.
    try {
      found[name] = JSON.parse(readFileSync(new URL(`node_modules/${name}/package.json`, repoRoot), 'utf8')).version
    } catch {
      found[name] = '(absent)'
    }
  }
  return found
}

function notesConfig() {
  const releaserc = JSON.parse(readFileSync(new URL('.releaserc.json', repoRoot), 'utf8'))
  const entry = releaserc.plugins.find(
    (plugin) => (Array.isArray(plugin) ? plugin[0] : plugin) === '@semantic-release/release-notes-generator',
  )
  if (!entry) throw new Error('.releaserc.json has no @semantic-release/release-notes-generator plugin')
  return Array.isArray(entry) ? entry[1] ?? {} : {}
}

const commit = {
  hash: '0123456789abcdef0123456789abcdef01234567',
  message: 'feat: a commit that has to appear in the notes',
  subject: 'a commit that has to appear in the notes',
  body: '',
  committerDate: '2026-01-01T00:00:00.000Z',
  author: { name: 'Check', email: 'check@example.invalid' },
  committer: { name: 'Check', email: 'check@example.invalid' },
}

const context = {
  commits: [{ ...commit }],
  lastRelease: { gitTag: 'v0.0.0', gitHead: '0000000000000000000000000000000000000000' },
  nextRelease: { version: '1.0.0', type: 'minor', gitTag: 'v1.0.0' },
  options: { repositoryUrl: 'https://github.com/rafaelvieiras/tinyfin' },
  cwd: repoRoot.pathname,
  env: {},
  branch: { name: 'main' },
  logger: { log: () => {}, error: () => {} },
}

const config = notesConfig()
const before = versions()

let notes
try {
  const { generateNotes } = await import('@semantic-release/release-notes-generator')
  notes = await generateNotes(config, context)
} catch (error) {
  console.error('release notes: FAILED to render')
  console.error(`  preset:          ${before['conventional-changelog-conventionalcommits']}`)
  console.error(`  writer:          ${before['conventional-changelog-writer']}`)
  console.error(`  notes generator: ${before['@semantic-release/release-notes-generator']}`)
  console.error(`  ${error.message}`)
  console.error('  A changelog preset and the notes generator have to agree on the writer version.')
  process.exit(1)
}

if (!notes.includes('a commit that has to appear in the notes')) {
  console.error('release notes: rendered, but the feat commit is missing from the notes')
  console.error(notes)
  process.exit(1)
}

const firstLine = notes.split('\n').find((line) => line.startsWith('##')) ?? '(no version heading)'
console.log(`release notes: ok (${firstLine.trim()})`)
console.log(
  `  preset ${before['conventional-changelog-conventionalcommits']}, ` +
    `writer ${before['conventional-changelog-writer']}, ` +
    `notes generator ${before['@semantic-release/release-notes-generator']}`,
)
