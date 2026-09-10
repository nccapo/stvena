'use strict';

const { execFile } = require('node:child_process');
const fs = require('node:fs/promises');
const path = require('node:path');
const { promisify } = require('node:util');
const exec = promisify(execFile);
const oidPattern = /^(?:[0-9a-f]{40}|[0-9a-f]{64})$/;

async function git(root, ...args) {
  const { stdout } = await exec('git', ['--no-optional-locks', '-C', root, ...args], {
    encoding: 'buffer', timeout: 5000, maxBuffer: 16 * 1024 * 1024, windowsHide: true,
  });
  return stdout;
}

async function discover(folder) {
  const root = (await git(folder, 'rev-parse', '--show-toplevel')).toString().replace(/\n$/, '');
  const gitDir = (await git(root, 'rev-parse', '--absolute-git-dir')).toString().replace(/\n$/, '');
  return { root, statePath: path.join(gitDir, 'stvena-live.json') };
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
  try {
    // Reject oversized metadata before parsing it. Source stays in Git objects.
    const file = await fs.open(repo.statePath, 'r');
    try {
      if ((await file.stat()).size > 8 * 1024 * 1024) throw new Error('Stvena live state exceeds 8 MiB.');
      return parseState(await file.readFile('utf8'));
    } finally {
      await file.close();
    }
  } catch (error) {
    if (error.code === 'ENOENT') return undefined;
    throw error;
  }
}

function isLive(state, now = Date.now()) {
  return !!state && state.active && now - Date.parse(state.updatedAt) < 30000;
}

async function readBlob(root, oid) {
  if (typeof oid !== 'string' || !oidPattern.test(oid)) throw new Error('Invalid captured object.');
  if (/^0+$/.test(oid)) return '';
  const data = await git(root, 'cat-file', 'blob', oid);
  if (data.includes(0)) throw new Error('Binary content has no text preview.');
  return data.toString('utf8');
}

module.exports = { discover, readState, parseState, isLive, readBlob };
