'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { createReviewUI, optimisticLifetimeMs } = require('../src/review');

const HUNK = 'a'.repeat(64);
const OTHER = 'b'.repeat(64);

function harness(options = {}) {
  const painted = new Map();
  const warnings = [];
  const errors = [];
  const document = {
    isDirty: !!options.dirty,
    uri: { scheme: 'file', fsPath: path.join('/repo', 'a.go') },
    lineCount: 50,
    lineAt: () => ({ text: 'source' }),
  };
  let kinds = 0;
  const vscode = {
    workspace: {
      getConfiguration: () => ({ get: (_name, fallback) => options.codeLens === false ? false : fallback }),
      onDidSaveTextDocument: () => ({ dispose() {} }),
      onDidChangeTextDocument: () => ({ dispose() {} }),
    },
    window: {
      visibleTextEditors: [{ document, setDecorations: (type, list) => painted.set(type.kind, list) }],
      createTextEditorDecorationType: () => ({ kind: `type${kinds++}` }),
      onDidChangeVisibleTextEditors: () => ({ dispose() {} }),
      registerFileDecorationProvider: provider => { vscode.badges = provider; return { dispose() {} }; },
      showErrorMessage: message => { errors.push(message); },
      showWarningMessage: message => { warnings.push(message); },
    },
    languages: { registerCodeLensProvider: (_selector, provider) => { vscode.lenses = provider; return { dispose() {} }; } },
    EventEmitter: class { constructor() { this.event = () => ({ dispose() {} }); } fire() {} dispose() {} },
    Range: class { constructor(a, b, c, d) { Object.assign(this, { a, b, c, d }); } },
    CodeLens: class { constructor(range, command) { Object.assign(this, { range, command }); } },
    FileDecoration: class { constructor(badge, tooltip, color) { Object.assign(this, { badge, tooltip, color }); } },
    ThemeColor: class { constructor(id) { this.id = id; } },
    OverviewRulerLane: { Left: 1 },
  };
  const sent = [];
  const ui = createReviewUI(vscode, { subscriptions: [] }, (target, request) => {
    sent.push({ target, request });
    if (options.sendFails) return Promise.reject(new Error('does not support "reject"'));
    return Promise.resolve({ id: `req-${sent.length}` });
  });
  return { ui, vscode, sent, painted, warnings, errors, document };
}

function repos(hunk = {}, extra = {}) {
  return [{
    root: '/repo',
    review: {
      session: 's', active: true, updatedAt: new Date().toISOString(),
      files: [{ path: 'a.go', status: 'M', hunks: [{ id: HUNK, start: 4, end: 9, ...hunk }] }],
      ...extra,
    },
  }];
}

function titles(h) {
  return h.vscode.lenses.provideCodeLenses(h.document).map(lens => lens.command.title);
}

test('a change block offers accept and reject, and reflects what Stvena published', () => {
  const h = harness();
  h.ui.update(repos());
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);

  h.ui.update(repos({ reviewed: true }));
  assert.deepEqual(titles(h), ['✓ Accepted', 'Undo']);

  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(titles(h), ['✗ Rejected', 'Undo']);
});

test('a decision shows immediately and is reconciled when Stvena agrees', async () => {
  const h = harness();
  h.ui.update(repos());
  const target = { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 };

  await h.ui.decide('reject', target, 'breaks the contract');
  // The editor must not wait a poll cycle to respond.
  assert.deepEqual(titles(h), ['✗ Rejected …', 'Undo']);
  assert.equal(h.sent[0].request.action, 'reject');
  assert.equal(h.sent[0].request.text, 'breaks the contract');
  assert.equal(h.sent[0].request.hunkId, HUNK);

  // Stvena has not caught up yet: the optimistic state must survive.
  h.ui.update(repos());
  assert.deepEqual(titles(h), ['✗ Rejected …', 'Undo']);

  // Once it agrees, the optimistic entry is dropped and the "…" goes away.
  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(titles(h), ['✗ Rejected', 'Undo']);
});

test('a refused decision is rolled back and reported', async () => {
  const h = harness();
  h.ui.update(repos());
  await h.ui.decide('accept', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(titles(h), ['✓ Accepted …', 'Undo']);

  h.ui.update(repos({}, { lastRequest: { id: 'req-1', action: 'accept', status: 'refused',
    message: 'that change block has changed since your editor drew it', at: new Date().toISOString() } }));
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
  assert.match(h.warnings[0], /changed since your editor drew it/);
});

test('a decision Stvena never acknowledges expires instead of sticking', () => {
  const h = harness();
  h.ui.update(repos());
  void h.ui.decide('reject', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(titles(h), ['✗ Rejected …', 'Undo']);

  const later = Date.now() + optimisticLifetimeMs + 1;
  const original = Date.now;
  Date.now = () => later;
  try {
    h.ui.update(repos());
  } finally {
    Date.now = original;
  }
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
});

test('a send that the running Stvena cannot do is rolled back at once', async () => {
  const h = harness({ sendFails: true });
  h.ui.update(repos());
  await h.ui.decide('reject', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
  assert.match(h.errors[0], /does not support/);
});

test('changed lines are painted by state, and never onto an unsaved buffer', () => {
  const h = harness();
  h.ui.update(repos());
  const buckets = () => [...h.painted.values()].map(list => list.length);
  assert.deepEqual(buckets(), [1, 0, 0]);

  h.ui.update(repos({ reviewed: true }));
  assert.deepEqual(buckets(), [0, 1, 0]);

  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(buckets(), [0, 0, 1]);

  // An edited buffer no longer matches the captured lines.
  h.document.isDirty = true;
  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(buckets(), [0, 0, 0]);
  assert.deepEqual(h.vscode.lenses.provideCodeLenses(h.document), []);
});

test('Explorer badges count unreviewed blocks and mark rejections', () => {
  const h = harness();
  const uri = { scheme: 'file', fsPath: path.join('/repo', 'a.go') };
  h.ui.update(repos());
  assert.equal(h.vscode.badges.provideFileDecoration(uri).badge, '1');

  h.ui.update(repos({ reviewed: true }));
  assert.equal(h.vscode.badges.provideFileDecoration(uri).badge, '✓');

  h.ui.update(repos({ rejected: true }));
  assert.equal(h.vscode.badges.provideFileDecoration(uri).badge, '✗');

  // Files Stvena has not published are not Stvena's to decorate.
  assert.equal(h.vscode.badges.provideFileDecoration({ scheme: 'file', fsPath: '/repo/other.go' }), undefined);
});

test('the status bar summarises the queue and why it is waiting', () => {
  const h = harness();
  assert.equal(h.ui.pending(repos()), undefined);
  const waiting = repos({}, { pendingRejections: { count: 2, appliesAt: 'turn-end', reason: 'claude 1 is running' } });
  assert.deepEqual(h.ui.pending(waiting), { count: 2, appliesAt: 'turn-end', reason: 'claude 1 is running' });
  const ready = repos({}, { pendingRejections: { count: 1, appliesAt: 'now' } });
  assert.deepEqual(h.ui.pending(ready), { count: 1, appliesAt: 'now', reason: '' });
});

test('the lens can be turned off without affecting decorations', () => {
  const h = harness({ codeLens: false });
  h.ui.update(repos());
  assert.deepEqual(h.vscode.lenses.provideCodeLenses(h.document), []);
  assert.deepEqual([...h.painted.values()].map(list => list.length), [1, 0, 0]);
});

test('decisions on other hunks do not leak into this one', async () => {
  const h = harness();
  h.ui.update(repos());
  await h.ui.decide('reject', { root: '/repo', path: 'a.go', hunkId: OTHER, start: 20, end: 21 });
  // The published hunk is untouched; only the other one was rejected.
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
});
