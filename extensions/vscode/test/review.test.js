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
  const information = [];
  const document = {
    isDirty: !!options.dirty,
    uri: { scheme: 'file', fsPath: path.join('/repo', 'a.go') },
    lineCount: 50,
    lineAt: () => ({ text: 'source' }),
  };
  let kinds = 0;
  const emitters = []; // lens changes, then badge changes, in creation order
  const edited = []; // onDidChangeTextDocument listeners
  const vscode = {
    workspace: {
      getConfiguration: () => ({ get: (_name, fallback) => options.codeLens === false ? false : fallback }),
      onDidSaveTextDocument: () => ({ dispose() {} }),
      onDidChangeTextDocument: listener => { edited.push(listener); return { dispose() {} }; },
    },
    window: {
      visibleTextEditors: [{ document, setDecorations: (type, list) => painted.set(type.kind, list) }],
      createTextEditorDecorationType: () => ({ kind: `type${kinds++}` }),
      onDidChangeVisibleTextEditors: () => ({ dispose() {} }),
      registerFileDecorationProvider: provider => { vscode.badges = provider; return { dispose() {} }; },
      showErrorMessage: message => { errors.push(message); },
      showWarningMessage: message => { warnings.push(message); },
      showInformationMessage: message => { information.push(message); },
    },
    languages: { registerCodeLensProvider: (_selector, provider) => { vscode.lenses = provider; return { dispose() {} }; } },
    EventEmitter: class {
      constructor() { this.event = () => ({ dispose() {} }); emitters.push(this); this.fired = []; }
      fire(value) { this.fired.push(value); }
      dispose() {}
    },
    Uri: { file: fsPath => ({ scheme: 'file', fsPath }) },
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
  return { ui, vscode, sent, painted, warnings, errors, information, document,
    lensFires: () => emitters[0].fired, badgeFires: () => emitters[1].fired,
    edit: () => edited.forEach(listener => listener({ document })) };
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

  // An edited buffer no longer matches the captured lines. The edit itself
  // repaints; nothing waits for the next poll.
  h.document.isDirty = true;
  h.edit();
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


test('whole-file rejection lenses identify what Undo cancels and retain stale-click tokens', () => {
  const h = harness();
  const state = repos({ rejected: true });
  state[0].review.files[0].rejected = true;
  h.ui.update(state);
  assert.deepEqual(titles(h), ['✗ Rejected', 'Undo file rejection']);
  const undo = h.vscode.lenses.provideCodeLenses(h.document)[1];
  assert.equal(undo.command.arguments[0].hunkId, HUNK);
});

test('new-file lenses describe whole-file rejection and retain the captured hunk token', () => {
  const h = harness();
  const state = repos();
  state[0].review.files[0].status = 'A';
  h.ui.update(state);
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject file', 'Reject file with reason…']);
  const reject = h.vscode.lenses.provideCodeLenses(h.document)[1];
  assert.equal(reject.command.arguments[0].hunkId, HUNK);
});

test('explicit apply reports the acknowledged outcome once, even after the queue disappears', async () => {
  for (const status of ['refused', 'applied']) {
    const h = harness();
    h.ui.update(repos());
    await h.ui.decide('apply-rejections', { root: '/repo' });
    assert.equal(h.sent[0].request.action, 'apply-rejections');
    const message = status === 'refused' ? 'Could not revert; agent handoff queued' : 'Reverted 2 changes';
    const state = repos({}, { files: [], lastRequest: { id: 'req-1', action: 'apply-rejections', status, message } });
    h.ui.update(state);
    h.ui.update(state);
    assert.deepEqual(h.warnings, status === 'refused' ? [`Stvena: ${message}`] : []);
    assert.deepEqual(h.information, status === 'applied' ? [`Stvena: ${message}`] : []);
  }
});

// The extension polls every 700 ms. Telling VS Code that decorations changed
// makes it drop and re-request them, which shows: the badge and the tab colour
// blink. A poll that changes nothing must redraw nothing.
test('an unchanged poll redraws nothing, so badges and lenses do not blink', () => {
  const h = harness();
  h.ui.update(repos());
  assert.equal(h.badgeFires().length, 1);
  assert.equal(h.lensFires().length, 1);
  for (let i = 0; i < 5; i++) h.ui.update(repos());
  assert.equal(h.badgeFires().length, 1, 'an identical poll re-fired the badges');
  assert.equal(h.lensFires().length, 1, 'an identical poll re-fired the lenses');
});

test('a changed poll names only the files involved', () => {
  const h = harness();
  h.ui.update(repos());
  h.ui.update(repos({ reviewed: true }));
  assert.equal(h.badgeFires().length, 2);
  const uris = h.badgeFires()[1];
  assert.ok(Array.isArray(uris), 'a routine change invalidated every decoration');
  assert.deepEqual(uris.map(uri => uri.fsPath), [path.join('/repo', 'a.go')]);
  // A file that leaves the review is also named, so its badge is removed.
  h.ui.update([{ ...repos()[0], review: { ...repos()[0].review, files: [] } }]);
  assert.deepEqual(h.badgeFires()[2].map(uri => uri.fsPath), [path.join('/repo', 'a.go')]);
});

test('a decision Stvena confirms still redraws, even though its own state is unchanged', async () => {
  const h = harness();
  h.ui.update(repos());
  await h.ui.decide('accept', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  const before = h.badgeFires().length;
  // Stvena now agrees: the optimistic entry is dropped, which changes nothing
  // visible, but the published state did change, so a redraw is due.
  h.ui.update(repos({ reviewed: true }, { lastRequest: { id: 'req-1', action: 'accept', status: 'applied', at: new Date().toISOString() } }));
  assert.equal(h.badgeFires().length, before + 1);
  h.ui.update(repos({ reviewed: true }, { lastRequest: { id: 'req-1', action: 'accept', status: 'applied', at: new Date().toISOString() } }));
  assert.equal(h.badgeFires().length, before + 1, 'the settled state kept redrawing');
});
