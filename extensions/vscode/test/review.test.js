'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const path = require('node:path');
const { createReviewUI, optimisticLifetimeMs, hoverLineLimit } = require('../src/review');

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
    lineCount: options.lineCount || 50,
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
    languages: {
      registerCodeLensProvider: (_selector, provider) => { vscode.lenses = provider; return { dispose() {} }; },
      registerHoverProvider: (_selector, provider) => { vscode.hovers = provider; return { dispose() {} }; },
    },
    MarkdownString: class {
      constructor() { this.value = ''; }
      appendMarkdown(text) { this.value += text; return this; }
    },
    Hover: class { constructor(contents) { this.contents = contents; } },
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
  const reads = [];
  const ui = createReviewUI(vscode, { subscriptions: [] }, (target, request) => {
    sent.push({ target, request });
    if (options.sendFails) return Promise.reject(new Error('does not support "reject"'));
    return Promise.resolve({ id: `req-${sent.length}` });
  }, (repo, oid) => {
    reads.push(oid);
    if (options.readFails && reads.length === 1) return Promise.reject(new Error('object not found'));
    return Promise.resolve(options.before || '');
  });
  return { ui, vscode, sent, reads, painted, warnings, errors, information, document,
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

test('a change block offers accept and reject, and leaves once decided', () => {
  const h = harness();
  h.ui.update(repos());
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);

  h.ui.update(repos({ reviewed: true }));
  assert.deepEqual(titles(h), []);

  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(titles(h), []);
});

test('a decision shows immediately and is reconciled when Stvena agrees', async () => {
  const h = harness();
  h.ui.update(repos());
  const target = { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 };

  await h.ui.decide('reject', target, 'breaks the contract');
  // The editor must not wait a poll cycle to respond: the change is gone.
  assert.deepEqual(titles(h), []);
  assert.equal(h.sent[0].request.action, 'reject');
  assert.equal(h.sent[0].request.text, 'breaks the contract');
  assert.equal(h.sent[0].request.hunkId, HUNK);

  // Stvena has not caught up yet: the change must not come back.
  h.ui.update(repos());
  assert.deepEqual(titles(h), []);

  // Once it agrees, it stays gone.
  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(titles(h), []);
});

test('a refused decision is rolled back and reported', async () => {
  const h = harness();
  h.ui.update(repos());
  await h.ui.decide('accept', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(titles(h), []);

  h.ui.update(repos({}, { lastRequest: { id: 'req-1', action: 'accept', status: 'refused',
    message: 'that change block has changed since your editor drew it', at: new Date().toISOString() } }));
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
  assert.match(h.warnings[0], /changed since your editor drew it/);
});

test('a decision Stvena never acknowledges expires instead of sticking', () => {
  const h = harness();
  h.ui.update(repos());
  void h.ui.decide('reject', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(titles(h), []);

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

test('only undecided lines are painted, and never onto an unsaved buffer', () => {
  const h = harness();
  h.ui.update(repos());
  const buckets = () => [h.painted.get('type0').length];
  assert.deepEqual(buckets(), [1]);

  h.ui.update(repos({ reviewed: true }));
  assert.deepEqual(buckets(), [0]);

  h.ui.update(repos({ rejected: true }));
  assert.deepEqual(buckets(), [0]);

  // Decided in this editor and not yet confirmed: already unpainted.
  h.ui.update(repos());
  void h.ui.decide('accept', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(buckets(), [0]);
  h.ui.clear();

  // An edited buffer no longer matches the captured lines. The edit itself
  // repaints; nothing waits for the next poll.
  h.ui.update(repos());
  assert.deepEqual(buckets(), [1]);
  h.document.isDirty = true;
  h.edit();
  assert.deepEqual(buckets(), [0]);
  assert.deepEqual(h.vscode.lenses.provideCodeLenses(h.document), []);
});

test('Explorer badges count undecided blocks only', () => {
  const h = harness();
  const uri = { scheme: 'file', fsPath: path.join('/repo', 'a.go') };
  h.ui.update(repos());
  assert.equal(h.vscode.badges.provideFileDecoration(uri).badge, '1');

  h.ui.update(repos({ reviewed: true }));
  assert.equal(h.vscode.badges.provideFileDecoration(uri), undefined);

  h.ui.update(repos({ rejected: true }));
  assert.equal(h.vscode.badges.provideFileDecoration(uri), undefined);

  const mixed = repos();
  mixed[0].review.files[0].hunks.push({ id: OTHER, start: 20, end: 21, rejected: true },
    { id: 'c'.repeat(64), start: 30, end: 31 });
  h.ui.update(mixed);
  assert.equal(h.vscode.badges.provideFileDecoration(uri).badge, '2');

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


test('a whole-file rejection clears every block of the file', () => {
  const h = harness();
  const state = repos();
  state[0].review.files[0].rejected = true;
  state[0].review.files[0].hunks.push({ id: OTHER, start: 20, end: 21 });
  h.ui.update(state);
  // Stvena marks each block rejected too, but the file mark alone is enough.
  state[0].review.files[0].hunks.forEach(hunk => { hunk.rejected = false; });
  h.ui.update(state);
  assert.deepEqual(titles(h), []);
  assert.equal(h.vscode.badges.provideFileDecoration({ scheme: 'file', fsPath: path.join('/repo', 'a.go') }), undefined);
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

// withBefore publishes one block the way a Stvena that names replaced lines does.
function withBefore(hunk, file = {}) {
  const value = repos(hunk);
  Object.assign(value[0].review.files[0], { before: 'f'.repeat(40) }, file);
  return value;
}

const BEFORE = Array.from({ length: 50 }, (_, i) => `old ${i + 1}`).join('\n') + '\n';

async function hoverAt(h, line) {
  const hover = await h.vscode.hovers.provideHover(h.document, { line });
  return hover && hover.contents.value;
}

test('hovering a changed block shows the lines it replaced', async () => {
  const h = harness({ before: BEFORE });
  h.ui.update(withBefore({ added: 3, removed: [[4, 2]] }));
  const text = await hoverAt(h, 5);
  assert.match(text, /replaced 2 lines with 3 lines/);
  assert.match(text, /Before:/);
  assert.match(text, /```diff\n- old 4\n- old 5\n```/);
  assert.deepEqual(h.reads, ['f'.repeat(40)]);

  // The blob is read once and reused for later hovers.
  await hoverAt(h, 4);
  assert.equal(h.reads.length, 1);
  // Outside the block there is nothing to say.
  assert.equal(await hoverAt(h, 20), undefined);
});

test('unchanged lines between two removals are shown as context', async () => {
  const h = harness({ before: BEFORE });
  h.ui.update(withBefore({ added: 1, removed: [[4, 1], [7, 1]] }));
  assert.match(await hoverAt(h, 4), /- old 4\n {2}old 5\n {2}old 6\n- old 7\n/);
});

test('a long replacement is cut to a count', async () => {
  const h = harness({ before: BEFORE });
  h.ui.update(withBefore({ added: 1, removed: [[1, 45]] }));
  const text = await hoverAt(h, 4);
  assert.equal((text.match(/^- old/gm) || []).length, hoverLineLimit);
  assert.match(text, /and 15 more lines/);
});

test('a block that only deleted lines is marked where they were, not tinted', async () => {
  const h = harness({ before: BEFORE });
  h.ui.update(withBefore({ start: 12, end: 12, removed: [[12, 6]] }));
  assert.equal(h.painted.get('type0').length, 0, 'an untouched neighbour line was tinted as changed');
  const [above] = h.painted.get('type1');
  assert.equal(above.range.a, 11);
  assert.equal(above.renderOptions.after.contentText, '− 6 lines removed above');
  assert.equal(h.painted.get('type2').length, 0);

  const text = await hoverAt(h, 11);
  assert.match(text, /removed 6 lines/);
  assert.match(text, /Removed:/);
  assert.match(text, /- old 12\n[\s\S]*- old 17\n```/);
  // Accept and reject still sit above it.
  assert.deepEqual(titles(h), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
});

test('lines deleted from the end of the file are marked below the last line', () => {
  const h = harness({ before: BEFORE, lineCount: 10 });
  h.ui.update(withBefore({ start: 11, end: 11, removed: [[11, 1]] }));
  assert.equal(h.painted.get('type1').length, 0);
  const [below] = h.painted.get('type2');
  assert.equal(below.range.a, 9);
  assert.equal(below.renderOptions.after.contentText, '− 1 line removed below');
});

test('an older Stvena without before-versions keeps the plain hover', async () => {
  const h = harness({ before: BEFORE });
  h.ui.update(repos());
  const text = await hoverAt(h, 5);
  assert.match(text, /Changed by the agent · not reviewed yet/);
  assert.doesNotMatch(text, /```/);
  assert.deepEqual(h.reads, []);

  const added = repos();
  added[0].review.files[0].status = 'A';
  h.ui.update(added);
  assert.match(await hoverAt(h, 5), /New file from the agent/);
});

test('no hover on a decided block or an unsaved buffer', async () => {
  const h = harness({ before: BEFORE });
  h.ui.update(withBefore({ added: 1, removed: [[4, 1]], reviewed: true }));
  assert.equal(await hoverAt(h, 4), undefined);
  h.ui.update(withBefore({ added: 1, removed: [[4, 1]] }));
  h.document.isDirty = true;
  assert.equal(await hoverAt(h, 4), undefined);
});

test('an unreadable before-version is explained, and retried on the next hover', async () => {
  const h = harness({ before: BEFORE, readFails: true });
  h.ui.update(withBefore({ added: 1, removed: [[4, 1]] }));
  assert.match(await hoverAt(h, 4), /original lines are unavailable: object not found/);
  assert.match(await hoverAt(h, 4), /- old 4/);
  assert.equal(h.reads.length, 2);
});

test('a line containing backticks cannot close the fence early', async () => {
  const h = harness({ before: 'one\nuse ```js here\n' });
  h.ui.update(withBefore({ added: 1, removed: [[2, 1]] }));
  assert.match(await hoverAt(h, 4), /````diff\n- use ```js here\n````/);
});

// review publishes several files the way Stvena does, for navigation tests.
function reviewOf(files, extra = {}) {
  return [{ root: '/repo', review: { session: 's', active: true, updatedAt: new Date().toISOString(),
    tree: '1'.repeat(40), files, ...extra } }];
}
const at = file => ({ scheme: 'file', fsPath: path.join('/repo', file) });

test('the block at the cursor is the one drawn on that line', () => {
  const h = harness();
  h.ui.update(repos());
  assert.equal(h.ui.at(h.document, 2), undefined);
  assert.deepEqual(h.ui.at(h.document, 3), { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9, status: 'M' });
  assert.equal(h.ui.at(h.document, 8).hunkId, HUNK);
  assert.equal(h.ui.at(h.document, 9), undefined);
  // Decided blocks and unsaved buffers have nothing to act on.
  h.ui.update(repos({ reviewed: true }));
  assert.equal(h.ui.at(h.document, 3), undefined);
  h.ui.update(repos());
  h.document.isDirty = true;
  assert.equal(h.ui.at(h.document, 3), undefined);
});

test('next and previous walk blocks top to bottom, across files, and wrap', () => {
  const h = harness();
  h.ui.update(reviewOf([
    { path: 'a.go', status: 'M', hunks: [{ id: 'b'.repeat(64), start: 30, end: 31 }, { id: HUNK, start: 4, end: 9 }] },
    { path: 'gone.go', status: 'D', hunks: [{ id: 'd'.repeat(64), start: 1, end: 1 }] },
    { path: 'b.go', status: 'M', hunks: [{ id: 'c'.repeat(64), start: 12, end: 12 }] },
  ]));
  const a = path.join('/repo', 'a.go'), b = path.join('/repo', 'b.go');
  assert.deepEqual(h.ui.step(at('a.go'), 0, 1), { fsPath: a, line: 4 });
  // Inside a block, next is the block after it.
  assert.deepEqual(h.ui.step(at('a.go'), 5, 1), { fsPath: a, line: 30 });
  // A deleted file has nothing to open, so it is skipped.
  assert.deepEqual(h.ui.step(at('a.go'), 30, 1), { fsPath: b, line: 12 });
  assert.deepEqual(h.ui.step(at('b.go'), 20, 1), { fsPath: a, line: 4 }, 'did not wrap to the first block');
  assert.deepEqual(h.ui.step(at('b.go'), 0, -1), { fsPath: a, line: 30 });
  assert.deepEqual(h.ui.step(at('a.go'), 3, -1), { fsPath: b, line: 12 }, 'did not wrap to the last block');
  // From a file with nothing to review, next starts at the top.
  assert.deepEqual(h.ui.step(at('other.go'), 99, 1), { fsPath: a, line: 4 });
  assert.deepEqual(h.ui.step(undefined, 0, -1), { fsPath: b, line: 12 });
});

test('a block decided in this editor is skipped at once', () => {
  const h = harness();
  h.ui.update(reviewOf([{ path: 'a.go', status: 'M', hunks: [{ id: HUNK, start: 4, end: 9 },
    { id: OTHER, start: 20, end: 21 }] }]));
  void h.ui.decide('accept', { root: '/repo', path: 'a.go', hunkId: HUNK, start: 4, end: 9 });
  assert.deepEqual(h.ui.step(at('a.go'), 0, 1), { fsPath: path.join('/repo', 'a.go'), line: 20 });
  void h.ui.decide('reject', { root: '/repo', path: 'a.go', hunkId: OTHER, start: 20, end: 21 });
  assert.equal(h.ui.step(at('a.go'), 0, 1), undefined);
  assert.deepEqual(h.ui.remaining(), { changes: 0, files: 0 });
});

test('remaining counts undecided blocks, and a binary file as one change', () => {
  const h = harness();
  h.ui.update(reviewOf([
    { path: 'a.go', status: 'M', hunks: [{ id: HUNK, start: 4, end: 9 }, { id: OTHER, start: 20, end: 21, reviewed: true }] },
    { path: 'logo.png', status: 'M', binary: true },
    { path: 'done.go', status: 'M', reviewed: true, hunks: [{ id: 'c'.repeat(64), start: 1, end: 1 }] },
  ]));
  assert.deepEqual(h.ui.remaining(), { changes: 2, files: 2 });
  assert.deepEqual(h.ui.remaining('/elsewhere'), { changes: 0, files: 0 });
});

test('accept all sends one request naming the tree, and clears everything at once', async () => {
  const h = harness();
  h.ui.update(reviewOf([
    { path: 'a.go', status: 'M', hunks: [{ id: HUNK, start: 4, end: 9 }, { id: OTHER, start: 20, end: 21, rejected: true }] },
    { path: 'logo.png', status: 'M', binary: true },
  ]));
  await h.ui.acceptAll({ root: '/repo', review: { tree: '1'.repeat(40) } });
  assert.equal(h.sent.length, 1);
  assert.deepEqual(h.sent[0].request, { action: 'accept-all', tree: '1'.repeat(40) });
  assert.deepEqual(h.ui.remaining(), { changes: 0, files: 0 });
  assert.deepEqual(titles(h), []);
});

test('a refused accept all brings every block back with one warning', async () => {
  const h = harness();
  const files = [{ path: 'a.go', status: 'M', hunks: [{ id: HUNK, start: 4, end: 9 }, { id: OTHER, start: 20, end: 21 }] }];
  h.ui.update(reviewOf(files));
  await h.ui.acceptAll({ root: '/repo', review: { tree: '1'.repeat(40) } });
  h.ui.update(reviewOf(files, { lastRequest: { id: 'req-1', action: 'accept-all', status: 'refused',
    message: 'New changes arrived before you confirmed', at: new Date().toISOString() } }));
  assert.deepEqual(h.ui.remaining(), { changes: 2, files: 1 });
  assert.deepEqual(h.warnings, ['Stvena: New changes arrived before you confirmed']);
});
