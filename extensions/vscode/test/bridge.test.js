'use strict';

const assert = require('node:assert/strict');
const { test } = require('node:test');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { discover, parseState, readState, isLive, readBlob } = require('../src/bridge');

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
  assert.equal(await readState(repo), undefined);
  const value = state();
  value.files[0].before = oid;
  await fs.writeFile(repo.statePath, JSON.stringify(value));
  assert.deepEqual(await readState(repo), value);
  await fs.writeFile(path.join(root, 'nested/file.txt'), 'after\n');
  assert.equal(await readBlob(repo.root, oid), 'before\n');
  assert.equal(await readBlob(repo.root, '0'.repeat(40)), '');
  await assert.rejects(readBlob(repo.root, '--help'));
  await fs.writeFile(path.join(root, 'binary'), Buffer.from([0, 1]));
  const binary = git('hash-object', '-w', 'binary');
  await assert.rejects(readBlob(repo.root, binary), /Binary/);
  git('worktree', 'add', '-qb', 'test-worktree', path.join(root, 'worktree'));
  const worktree = await discover(path.join(root, 'worktree'));
  assert.notEqual(worktree.statePath, repo.statePath);
  assert.equal(await readState(worktree), undefined);
});
