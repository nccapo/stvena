'use strict';

const assert = require('node:assert/strict');
const { test } = require('node:test');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { discover, lookup, registryStamp, parseState, readState, parseReviewState, readReviewState, isLive, writeRequest, readBlob } = require('../src/bridge');

const state = () => ({ version: 1, session: 'test-session', sequence: 1, active: true,
  updatedAt: new Date().toISOString(), files: [{ path: 'nested/file.txt', status: 'M',
    before: '1'.repeat(40), after: '2'.repeat(40), line: 4, added: 1, deleted: 1,
    binary: false, truncated: false }] });

test('state validation and heartbeat expiry', () => {
  const value = state();
  assert.deepEqual(parseState(JSON.stringify(value)), value);
  assert.equal(isLive(value), true);
  assert.equal(isLive(value, Date.parse(value.updatedAt) + 30001), false);
  assert.equal(isLive({ ...value, active: false }), false);
  for (const change of [{ version: 2 }, { sequence: -1 }, { updatedAt: 'invalid' }, { files: null }]) {
    assert.throws(() => parseState(JSON.stringify({ ...value, ...change })));
  }
  for (const change of [{ path: '../outside' }, { path: '/outside' }, { path: 'C:\\outside' },
    { oldPath: '../outside' }, { before: '--help' }, { line: 0 }, { binary: 'false' }]) {
    assert.throws(() => parseState(JSON.stringify({ ...value, files: [{ ...value.files[0], ...change }] })));
  }
});

test('review state validation', () => {
  const now = new Date().toISOString();
  const value = { version: 1, session: 'review-session', sequence: 3, active: true,
    updatedAt: now, changedAt: now, unreviewedFiles: 2, unreviewedHunks: 4, newerBatches: 1,
    focus: { tree: '1'.repeat(40), source: 'session', path: 'nested/file.txt', line: 4, endLine: 7 } };
  assert.deepEqual(parseReviewState(JSON.stringify(value)), value);
  for (const change of [{ unreviewedFiles: -1 }, { newerBatches: 1.5 }, { changedAt: 'bad' },
    { focus: { ...value.focus, path: '../outside' } }, { focus: { ...value.focus, endLine: 3 } },
    { focus: { ...value.focus, source: 'unknown' } }, { focus: { ...value.focus, tree: '0'.repeat(40) } }]) {
    assert.throws(() => parseReviewState(JSON.stringify({ ...value, ...change })));
  }
});

test('discovers nested folders and worktrees; reads exact immutable blobs', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-extension-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const git = (...args) => execFileSync('git', ['-C', root, ...args], { encoding: 'utf8' }).trim();
  git('init', '-q');
  git('config', 'user.name', 'Stvena Test');
  git('config', 'user.email', 'test@example.invalid');
  await fs.mkdir(path.join(root, 'nested'));
  await fs.writeFile(path.join(root, 'nested/file.txt'), 'before\n');
  git('add', '.');
  git('commit', '-qm', 'baseline');
  const oid = git('rev-parse', 'HEAD:nested/file.txt');
  const repo = await discover(path.join(root, 'nested'));
  assert.equal(await fs.realpath(repo.root), await fs.realpath(root));
  assert.equal(repo.mode, 'git');
  assert.equal(await readState(repo), undefined);
  assert.equal(await readReviewState(repo), undefined);
  const value = state();
  value.files[0].before = oid;
  await fs.writeFile(repo.statePath, JSON.stringify(value));
  assert.deepEqual(await readState(repo), value);
  await fs.writeFile(path.join(root, 'nested/file.txt'), 'after\n');
  assert.equal(await readBlob(repo, oid), 'before\n');
  assert.equal(await readBlob(repo, '0'.repeat(40)), '');
  await assert.rejects(readBlob(repo, '--help'));
  await fs.writeFile(path.join(root, 'binary'), Buffer.from([0, 1]));
  const binary = git('hash-object', '-w', 'binary');
  await assert.rejects(readBlob(repo, binary), /Binary/);
  git('worktree', 'add', '-qb', 'test-worktree', path.join(root, 'worktree'));
  const worktree = await discover(path.join(root, 'worktree'));
  assert.notEqual(worktree.statePath, repo.statePath);
  assert.equal(await readState(worktree), undefined);
});

test('writes session-bound editor requests atomically', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-request-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  execFileSync('git', ['-C', root, 'init', '-q']);
  const repo = await discover(root);
  repo.review = { version: 1, session: 'active-session', sequence: 0, active: true,
    updatedAt: new Date().toISOString(), unreviewedFiles: 0, unreviewedHunks: 0, newerBatches: 0 };
  const written = await writeRequest(repo, { action: 'context', path: 'file.go', line: 2, endLine: 5 });
  const stored = JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
  assert.deepEqual(stored, written);
  assert.equal(stored.session, 'active-session');
  assert.ok(stored.id);
  await assert.rejects(writeRequest(repo, { action: 'context', path: '../outside', line: 1, endLine: 1 }));
  repo.review.active = false;
  await assert.rejects(writeRequest(repo, { action: 'review', path: 'file.go', line: 1, endLine: 1 }), /not active/);
});

test('per-hunk review state parses, and older descriptors without it still work', () => {
  const now = new Date().toISOString();
  const base = { version: 1, session: 'review-session', sequence: 3, active: true,
    updatedAt: now, unreviewedFiles: 2, unreviewedHunks: 4, newerBatches: 0 };
  // An older Stvena omits every field added after version 1.
  assert.deepEqual(parseReviewState(JSON.stringify(base)), base);

  const hunk = { id: 'a'.repeat(64), start: 4, end: 9, reviewed: true };
  const value = { ...base, features: ['review', 'accept', 'reject'], tree: '1'.repeat(40),
    files: [{ path: 'a.go', status: 'M', reviewed: false, hunks: [hunk] }],
    pendingRejections: { count: 2, appliesAt: 'turn-end', reason: 'claude 1 is running' },
    lastRequest: { id: 'r1', action: 'reject', status: 'queued', at: now } };
  assert.deepEqual(parseReviewState(JSON.stringify(value)), value);

  for (const change of [
    { features: 'reject' },
    { files: [{ path: '../outside', status: 'M' }] },
    { files: [{ path: 'a.go', status: 'Z' }] },
    { files: [{ path: 'a.go', status: 'M', hunks: [{ ...hunk, id: 'short' }] }] },
    { files: [{ path: 'a.go', status: 'M', hunks: [{ ...hunk, end: 1 }] }] },
    { files: [{ path: 'a.go', status: 'M', hunks: [{ ...hunk, start: 0 }] }] },
    { pendingRejections: { count: 1, appliesAt: 'whenever' } },
    { pendingRejections: { count: -1, appliesAt: 'now' } },
    { lastRequest: { id: 'r1', action: 'reject', status: 'maybe', at: now } },
  ]) {
    assert.throws(() => parseReviewState(JSON.stringify({ ...value, ...change })));
  }
});

test('requests are gated on what the running Stvena advertises', async t => {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-features-'));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const repo = { requestPath: path.join(root, 'request.json') };
  const live = { active: true, session: 's1', updatedAt: new Date().toISOString() };

  // An older Stvena advertises no features: only the original actions are safe.
  repo.review = { ...live };
  await assert.rejects(() => writeRequest(repo, { action: 'reject', path: 'a.go', line: 1, endLine: 1 }),
    /does not support/);
  await writeRequest(repo, { action: 'review', path: 'a.go', line: 1, endLine: 1 });

  repo.review = { ...live, features: ['review', 'context', 'reject', 'apply-rejections'] };
  const rejection = await writeRequest(repo, { action: 'reject', path: 'a.go', line: 4, endLine: 9,
    hunkId: 'b'.repeat(64), text: 'breaks the contract' });
  assert.equal(rejection.hunkId, 'b'.repeat(64));
  assert.equal(rejection.text, 'breaks the contract');

  // Queue-wide actions carry no location.
  const applied = await writeRequest(repo, { action: 'apply-rejections' });
  assert.equal(applied.path, '');
  assert.equal(applied.line, 0);

  await assert.rejects(() => writeRequest(repo, { action: 'reject', path: 'a.go', line: 1, endLine: 1,
    hunkId: 'nothex' }), /hunk reference/);
  await assert.rejects(() => writeRequest(repo, { action: 'reject', path: 'a.go', line: 1, endLine: 1,
    text: 'x'.repeat(4097) }), /too long/);
  await assert.rejects(() => writeRequest(repo, { action: 'reject', path: '../escape', line: 1, endLine: 1 }),
    /Invalid Stvena editor request/);
});

// registry builds an isolated Stvena home with the given bridge entries, so a
// test never reads or writes the developer's real one.
async function registry(t, entries) {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-home-'));
  t.after(() => fs.rm(home, { recursive: true, force: true }));
  const previous = process.env.STVENA_HOME;
  process.env.STVENA_HOME = home;
  t.after(() => { if (previous === undefined) delete process.env.STVENA_HOME; else process.env.STVENA_HOME = previous; });
  await fs.mkdir(path.join(home, 'bridges'), { recursive: true });
  for (const [name, entry] of Object.entries(entries)) {
    const body = typeof entry === 'string' ? entry : JSON.stringify(entry);
    await fs.writeFile(path.join(home, 'bridges', name), body);
  }
  return home;
}

function entry(root, dir, extra = {}) {
  return { version: 1, root, realRoot: root, mode: 'shadow', dir, gitDir: path.join(dir, 'shadow.git'),
    updatedAt: new Date().toISOString(), ...extra };
}

test('finds a project that is not a Git repository, and the folders inside it', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-plain-')));
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-cache-'));
  t.after(() => Promise.all([fs.rm(root, { recursive: true, force: true }), fs.rm(dir, { recursive: true, force: true })]));
  await registry(t, { 'a.json': entry(root, dir) });
  await fs.mkdir(path.join(root, 'pkg', 'inner'), { recursive: true });
  for (const folder of [root, path.join(root, 'pkg', 'inner')]) {
    const repo = await discover(folder);
    assert.equal(repo.mode, 'shadow');
    assert.equal(repo.root, root);
    assert.equal(repo.statePath, path.join(dir, 'stvena-live.json'));
    assert.equal(repo.reviewPath, path.join(dir, 'stvena-review.json'));
    assert.equal(repo.requestPath, path.join(dir, 'stvena-request.json'));
    assert.equal(repo.presencePath, path.join(dir, 'stvena-ide.json'));
  }
});

test('the nearest reviewed project owns a folder', async t => {
  const outer = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-outer-')));
  const inner = path.join(outer, 'service');
  await fs.mkdir(path.join(inner, 'cmd'), { recursive: true });
  t.after(() => fs.rm(outer, { recursive: true, force: true }));
  await registry(t, { 'outer.json': entry(outer, '/tmp/outer-cache'), 'inner.json': entry(inner, '/tmp/inner-cache') });
  const repo = await discover(path.join(inner, 'cmd'));
  assert.equal(repo.root, inner);
});

test('Git answers before the registry, so a repository never changes behaviour', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-both-')));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  execFileSync('git', ['-C', root, 'init', '-q']);
  await registry(t, { 'a.json': entry(root, '/tmp/should-not-be-used') });
  const repo = await discover(root);
  assert.equal(repo.mode, 'git');
  assert.ok(repo.statePath.includes('.git'));
});

test('unusable registry entries are ignored rather than followed or thrown', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-bad-')));
  const other = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-other-')));
  t.after(() => Promise.all([fs.rm(root, { recursive: true, force: true }), fs.rm(other, { recursive: true, force: true })]));
  await registry(t, {
    'broken.json': '{',
    'version.json': entry(root, '/tmp/cache', { version: 2 }),
    'mode.json': entry(root, '/tmp/cache', { mode: 'other' }),
    'relative.json': { version: 1, root, realRoot: root, mode: 'shadow', dir: 'cache', gitDir: 'cache', updatedAt: new Date().toISOString() },
    'stamp.json': entry(root, '/tmp/cache', { updatedAt: 'not a date' }),
    'elsewhere.json': entry(other, '/tmp/other-cache'),
    'notes.txt': 'ignored',
  });
  assert.equal(await discover(root), undefined);
});

test('a folder Stvena has never reviewed is not an error', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-none-')));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  await registry(t, {});
  assert.equal(await discover(root), undefined);
});

test('reads captured blobs from a private snapshot store', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-blob-')));
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-blob-cache-'));
  t.after(() => Promise.all([fs.rm(root, { recursive: true, force: true }), fs.rm(dir, { recursive: true, force: true })]));
  const shadow = path.join(dir, 'shadow.git');
  execFileSync('git', ['init', '--bare', '-q', shadow]);
  const oid = execFileSync('git', ['--git-dir', shadow, 'hash-object', '-w', '--stdin'],
    { input: 'captured\n', encoding: 'utf8' }).trim();
  await registry(t, { 'a.json': entry(root, dir) });
  const repo = await discover(root);
  assert.equal(await readBlob(repo, oid), 'captured\n');
});

test('lookup finds a project through the registry without asking Git', async t => {
  const root = await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-lookup-')));
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  // A git that would fail loudly proves lookup never runs one.
  const bin = await fs.mkdtemp(path.join(os.tmpdir(), 'stvena-nogit-bin-'));
  t.after(() => fs.rm(bin, { recursive: true, force: true }));
  await fs.writeFile(path.join(bin, 'git'), '#!/bin/sh\necho "git must not run" >&2\nexit 99\n', { mode: 0o755 });
  const previous = process.env.PATH;
  process.env.PATH = `${bin}${path.delimiter}${previous}`;
  t.after(() => { process.env.PATH = previous; });
  await registry(t, {});
  assert.equal(await lookup(root), undefined);
  const home = process.env.STVENA_HOME;
  await fs.writeFile(path.join(home, 'bridges', 'a.json'), JSON.stringify(entry(root, '/tmp/lookup-cache')));
  const repo = await lookup(path.join(root));
  assert.equal(repo.mode, 'shadow');
  assert.equal(repo.statePath, path.join('/tmp/lookup-cache', 'stvena-live.json'));
});

test('the registry stamp changes when Stvena writes an entry', async t => {
  const home = await registry(t, {});
  const before = await registryStamp();
  assert.notEqual(before, undefined);
  await new Promise(resolve => setTimeout(resolve, 20));
  // Stvena writes entries by atomic rename into the directory.
  const temporary = path.join(home, 'bridges', '.save-1');
  await fs.writeFile(temporary, '{}');
  await fs.rename(temporary, path.join(home, 'bridges', 'b.json'));
  assert.notEqual(await registryStamp(), before);
  await fs.rm(path.join(home, 'bridges'), { recursive: true });
  assert.equal(await registryStamp(), undefined);
});
