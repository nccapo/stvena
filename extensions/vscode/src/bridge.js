'use strict';

const { execFile } = require('node:child_process');
const fs = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const { randomUUID } = require('node:crypto');
const { promisify } = require('node:util');
const exec = promisify(execFile);
const oidPattern = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;

function stvenaHome() {
  return process.env.STVENA_HOME || path.join(os.homedir(), '.stvena');
}

async function git(root, ...args) {
  return run(['-C', root, ...args]);
}

async function gitDir(dir, ...args) {
  return run(['--git-dir', dir, ...args]);
}

async function run(args) {
  const { stdout } = await exec('git', ['--no-optional-locks', ...args], {
    encoding: 'buffer', timeout: 5000, maxBuffer: 16 * 1024 * 1024, windowsHide: true,
  });
  return stdout;
}

// discover resolves a workspace folder to the directory Stvena writes its
// descriptors in. Git answers first, so a repository behaves exactly as it
// always has. A folder that is not a repository is looked up in Stvena's bridge
// registry instead, where it records the private directory it snapshots into.
// A folder Stvena has never reviewed simply has no bridge, which is not an error.
async function discover(folder) {
  try {
    const root = (await git(folder, 'rev-parse', '--show-toplevel')).toString().replace(/\n$/, '');
    const gitDir = (await git(root, 'rev-parse', '--absolute-git-dir')).toString().replace(/\n$/, '');
    return describe({ root, realRoot: root, mode: 'git', dir: gitDir, gitDir });
  } catch (error) {
    // Git saying "not a repository" is an answer. Git failing to answer, or
    // not being installed, leaves the registry as the only way to find a
    // bridge, so both fall through rather than throwing.
    if (!String(error.stderr).includes('not a git repository') && error.code !== 'ENOENT') throw error;
  }
  const entry = await fromRegistry(folder);
  return entry && describe(entry);
}

function describe(entry) {
  return { root: entry.root, realRoot: entry.realRoot, mode: entry.mode, dir: entry.dir, gitDir: entry.gitDir,
    statePath: path.join(entry.dir, 'stvena-live.json'), reviewPath: path.join(entry.dir, 'stvena-review.json'),
    requestPath: path.join(entry.dir, 'stvena-request.json'), presencePath: path.join(entry.dir, 'stvena-ide.json') };
}

// contains reports whether path lies in root, so an entry can only ever point
// at a tree that actually holds the folder being resolved.
function contains(root, target) {
  if (!root || !target) return false;
  const from = path.resolve(root), to = path.resolve(target);
  return from === to || to.startsWith(from + path.sep);
}

function validEntry(entry) {
  return !!entry && entry.version === 1 && ['git', 'shadow'].includes(entry.mode) &&
    ['root', 'realRoot', 'dir', 'gitDir'].every(key => typeof entry[key] === 'string' &&
      entry[key].length > 0 && !entry[key].includes('\0') && path.isAbsolute(entry[key])) &&
    typeof entry.updatedAt === 'string' && Number.isFinite(Date.parse(entry.updatedAt));
}

// fromRegistry returns the entry for the project this folder belongs to. An
// entry is a map of paths and nothing more: nothing here is ever executed.
async function fromRegistry(folder) {
  let names;
  try {
    names = await fs.readdir(path.join(stvenaHome(), 'bridges'));
  } catch (error) {
    if (error.code === 'ENOENT' || error.code === 'EACCES') return undefined;
    throw error;
  }
  if (names.length > 1000) return undefined;
  let target = folder;
  try {
    target = await fs.realpath(folder);
  } catch { /* an unresolvable folder still matches on its literal path */ }
  let best;
  for (const name of names) {
    if (!name.endsWith('.json')) continue;
    let entry;
    try {
      const data = await fs.readFile(path.join(stvenaHome(), 'bridges', name), 'utf8');
      if (data.length > 8 * 1024) continue;
      entry = JSON.parse(data);
    } catch { continue; }
    if (!validEntry(entry)) continue;
    if (!contains(entry.realRoot, target) && !contains(entry.root, folder)) continue;
    // The nearest project wins, so one reviewed folder inside another resolves
    // to the one that actually holds this file.
    if (!best || entry.realRoot.length > best.realRoot.length) best = entry;
  }
  return best;
}

// announce tells Stvena an editor extension is actually watching this
// repository. Several editors report themselves as VS Code to the terminal, and
// the extension may not be installed in the one that is running, so Stvena
// waits for this rather than trusting the environment.
async function announce(repo, ide, extension) {
  const value = { version: 1, ide, extension, updatedAt: new Date().toISOString() };
  await writeAtomically(repo.presencePath, value);
  return value;
}

async function writeAtomically(target, value) {
  const temporary = `${target}.${process.pid}.${randomUUID()}.tmp`;
  try {
    await fs.writeFile(temporary, `${JSON.stringify(value)}\n`, { encoding: 'utf8', mode: 0o600, flag: 'wx' });
    await fs.rename(temporary, target);
  } catch (error) {
    await fs.rm(temporary, { force: true }).catch(() => {});
    throw error;
  }
}

function validPath(value) {
  return typeof value === 'string' && value.length > 0 && !value.includes('\0') &&
    !path.posix.isAbsolute(value) && !path.win32.isAbsolute(value) &&
    !value.split(/[\\/]/).includes('..');
}

function parseState(data) {
  const state = JSON.parse(data);
  if (state.version !== 1 || typeof state.session !== 'string' || !state.session ||
      !Number.isSafeInteger(state.sequence) || state.sequence < 0 ||
      typeof state.active !== 'boolean' || typeof state.updatedAt !== 'string' ||
      !Number.isFinite(Date.parse(state.updatedAt)) ||
      (state.error !== undefined && typeof state.error !== 'string') || !Array.isArray(state.files)) {
    throw new Error('Unsupported or invalid Stvena live state; update Stvena and the extension together.');
  }
  for (const file of state.files) {
    if (!validPath(file.path) || (file.oldPath !== undefined && !validPath(file.oldPath)) ||
        typeof file.before !== 'string' || !oidPattern.test(file.before) ||
        typeof file.after !== 'string' || !oidPattern.test(file.after) ||
        !Number.isSafeInteger(file.line) || file.line < 1 ||
        typeof file.status !== 'string' || !/^[AMDRCTU?]$/.test(file.status) ||
        !Number.isSafeInteger(file.added) || file.added < 0 ||
        !Number.isSafeInteger(file.deleted) || file.deleted < 0 ||
        typeof file.binary !== 'boolean' || typeof file.truncated !== 'boolean') {
      throw new Error('Invalid captured edit in Stvena live state.');
    }
    if (file.ranges !== undefined && (!Array.isArray(file.ranges) || file.ranges.some(range =>
      !range || !Number.isSafeInteger(range.start) || range.start < 1 ||
      !Number.isSafeInteger(range.end) || range.end < range.start))) {
      throw new Error('Invalid changed line ranges.');
    }
  }
  if (state.changedAt !== undefined && !Number.isFinite(Date.parse(state.changedAt))) throw new Error('Invalid edit timestamp.');
  const activity = state.activity;
  if (activity !== undefined && (!activity || activity.session !== state.session ||
      typeof activity.id !== 'string' || !activity.id || typeof activity.agent !== 'string' ||
      !validPath(activity.path) || !Number.isSafeInteger(activity.line) || activity.line < 1 ||
      (activity.endLine !== undefined && (!Number.isSafeInteger(activity.endLine) || activity.endLine < activity.line)) ||
      typeof activity.updatedAt !== 'string' || !Number.isFinite(Date.parse(activity.updatedAt)))) {
    throw new Error('Invalid agent read activity.');
  }
  return state;
}

async function readState(repo) {
  return readMetadata(repo.statePath, 8 * 1024 * 1024, parseState);
}

function parseReviewState(data) {
  const state = JSON.parse(data);
  if (state.version !== 1 || typeof state.session !== 'string' || !state.session ||
      !Number.isSafeInteger(state.sequence) || state.sequence < 0 || typeof state.active !== 'boolean' ||
      typeof state.updatedAt !== 'string' || !Number.isFinite(Date.parse(state.updatedAt)) ||
      (state.changedAt !== undefined && !Number.isFinite(Date.parse(state.changedAt))) ||
      !Number.isSafeInteger(state.unreviewedFiles) || state.unreviewedFiles < 0 ||
      !Number.isSafeInteger(state.unreviewedHunks) || state.unreviewedHunks < 0 ||
      !Number.isSafeInteger(state.newerBatches) || state.newerBatches < 0) {
    throw new Error('Unsupported or invalid Stvena review state; update Stvena and the extension together.');
  }
  const focus = state.focus;
  if (focus !== undefined && (!focus || !['session', 'workspace', 'project', 'branch'].includes(focus.source) ||
      typeof focus.tree !== 'string' || !oidPattern.test(focus.tree) || /^0+$/.test(focus.tree) || !validPath(focus.path) ||
      !Number.isSafeInteger(focus.line) || focus.line < 1 ||
      !Number.isSafeInteger(focus.endLine) || focus.endLine < focus.line)) {
    throw new Error('Invalid Stvena review focus.');
  }
  // Fields below were added after version 1. An older Stvena omits them, so an
  // absent value means "this build cannot do that", never a protocol error.
  if (state.features !== undefined && (!Array.isArray(state.features) ||
      state.features.some(name => typeof name !== 'string'))) {
    throw new Error('Invalid Stvena feature list.');
  }
  if (state.files !== undefined) {
    if (!Array.isArray(state.files)) throw new Error('Invalid Stvena review files.');
    for (const file of state.files) {
      if (!file || !validPath(file.path) || (file.oldPath !== undefined && !validPath(file.oldPath)) ||
          typeof file.status !== 'string' || !/^[AMDRCTU?]$/.test(file.status)) {
        throw new Error('Invalid Stvena review file.');
      }
      if (file.hunks !== undefined) {
        if (!Array.isArray(file.hunks)) throw new Error('Invalid Stvena review hunks.');
        for (const hunk of file.hunks) {
          if (!hunk || typeof hunk.id !== 'string' || !/^[0-9a-f]{64}$/.test(hunk.id) ||
              !Number.isSafeInteger(hunk.start) || hunk.start < 1 ||
              !Number.isSafeInteger(hunk.end) || hunk.end < hunk.start) {
            throw new Error('Invalid Stvena review hunk.');
          }
        }
      }
    }
  }
  const pending = state.pendingRejections;
  if (pending !== undefined && (!pending || !Number.isSafeInteger(pending.count) || pending.count < 0 ||
      !['now', 'turn-end', 'manual'].includes(pending.appliesAt))) {
    throw new Error('Invalid Stvena pending rejections.');
  }
  const last = state.lastRequest;
  if (last !== undefined && (!last || typeof last.id !== 'string' || !last.id ||
      typeof last.action !== 'string' || !['applied', 'queued', 'refused'].includes(last.status) ||
      !Number.isFinite(Date.parse(last.at)))) {
    throw new Error('Invalid Stvena request result.');
  }
  return state;
}

// supports reports whether the running Stvena accepts a request action. An
// older build advertises nothing, so only the two original actions are assumed.
function supports(review, action) {
  const features = review?.features;
  if (!Array.isArray(features)) return action === 'review' || action === 'context';
  return features.includes(action);
}

// Actions that operate on the whole queue rather than one located change.
const pathlessActions = new Set(['apply-rejections', 'next-unreviewed']);

async function readMetadata(filePath, limit, parse) {
  try {
    // Reject oversized metadata before parsing it. Source stays in Git objects.
    const file = await fs.open(filePath, 'r');
    try {
      if ((await file.stat()).size > limit) throw new Error('Stvena metadata exceeds its size limit.');
      return parse(await file.readFile('utf8'));
    } finally {
      await file.close();
    }
  } catch (error) {
    if (error.code === 'ENOENT') return undefined;
    throw error;
  }
}

async function readReviewState(repo) {
  return readMetadata(repo.reviewPath, 64 * 1024, parseReviewState);
}

function isLive(state, now = Date.now()) {
  return !!state && state.active && now - Date.parse(state.updatedAt) < 30000;
}

async function writeRequest(repo, request) {
  if (!isLive(repo.review)) throw new Error('Stvena review is not active in this repository.');
  if (!request || typeof request.action !== 'string') throw new Error('Invalid Stvena editor request.');
  if (!supports(repo.review, request.action)) {
    throw new Error(`This Stvena version does not support "${request.action}". Update the stvena binary.`);
  }
  const located = !pathlessActions.has(request.action);
  if (located && (!validPath(request.path) ||
      !Number.isSafeInteger(request.line) || request.line < 1 ||
      !Number.isSafeInteger(request.endLine) || request.endLine < request.line)) {
    throw new Error('Invalid Stvena editor request.');
  }
  if (request.hunkId !== undefined && !/^[0-9a-f]{64}$/.test(request.hunkId)) {
    throw new Error('Invalid Stvena hunk reference.');
  }
  if (request.text !== undefined && (typeof request.text !== 'string' ||
      Buffer.byteLength(request.text, 'utf8') > 4096)) {
    throw new Error('Rejection reason is too long.');
  }
  const value = { version: 1, session: repo.review.session, id: randomUUID(), action: request.action,
    path: located ? request.path : '', line: located ? request.line : 0,
    endLine: located ? request.endLine : 0, updatedAt: new Date().toISOString() };
  if (request.hunkId !== undefined) value.hunkId = request.hunkId;
  if (request.text) value.text = request.text;
  await writeAtomically(repo.requestPath, value);
  return value;
}

async function readBlob(repo, oid) {
  if (typeof oid !== 'string' || !oidPattern.test(oid)) throw new Error('Invalid captured object.');
  if (/^0+$/.test(oid)) return '';
  // Captures live in the repository's own object store, or in the private one
  // Stvena keeps for a project without Git; the workspace says which.
  const data = await gitDir(repo.gitDir, 'cat-file', 'blob', oid);
  if (data.includes(0)) throw new Error('Binary content has no text preview.');
  return data.toString('utf8');
}

module.exports = { discover, readState, parseState, readReviewState, parseReviewState, isLive, supports, writeRequest, announce, readBlob };
