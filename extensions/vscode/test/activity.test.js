'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { fresh, readRow, reviewRow, label, createMarkers } = require('../src/activity');
const { parseState } = require('../src/bridge');

test('read activity validates paths, ranges, session and freshness', () => {
  const now = new Date().toISOString();
  const state = { version: 1, session: 's', sequence: 0, active: true, updatedAt: now, files: [],
    activity: { session: 's', id: 'read-1', agent: 'codex', path: 'a.go', line: 4, endLine: 8, updatedAt: now } };
  assert.deepEqual(parseState(JSON.stringify(state)), state);
  assert.equal(readRow({ state }).file.ranges[0].end, 8);
  assert.match(label(readRow({ state })), /Read lines 4–8/);
  assert.equal(fresh(now, Date.parse(now) + 15000), false);
  assert.equal(fresh(now, Date.parse(now) - 1), false);
  for (const change of [{ path: '../other' }, { path: '/other' }, { line: 0 }, { endLine: 3 },
    { session: 'different' }, { id: '' }, { updatedAt: 'bad' }]) {
    assert.throws(() => parseState(JSON.stringify({ ...state, activity: { ...state.activity, ...change } })));
  }
  state.activity.updatedAt = new Date(Date.now() - 16000).toISOString();
  assert.equal(readRow({ state }), undefined);
});

test('review focus becomes a persistent source marker', () => {
  const repo = { review: { sequence: 5, updatedAt: new Date().toISOString(),
    focus: { source: 'branch', path: 'a.go', line: 3, endLine: 6 } } };
  const row = reviewRow(repo);
  assert.equal(row.kind, 'review');
  assert.equal(row.file.ranges[0].end, 6);
  assert.match(label(row), /Review in Stvena · branch · lines 3–6/);
});

function harness(t) {
  let now = Date.parse('2026-09-24T12:00:00Z');
  t.mock.method(Date, 'now', () => now);
  const calls = [];
  function makeEditor(uri = 'file:a.go', document) {
    const paints = new Map();
    const editor = { document: document || { isDirty: false, version: 1, uri: { toString: () => uri },
      lineCount: 10, lineAt: () => ({ text: 'text' }) }, paints,
      setDecorations(type, options) {
        calls.push({ editor, kind: type.kind, options });
        paints.set(type.kind, options);
      } };
    return editor;
  }
  const editor = makeEditor();
  let next = 0;
  const vscode = {
    window: { visibleTextEditors: [editor], createTextEditorDecorationType: () => ({ kind: ['read', 'edit', 'review'][next++] }) },
    DecorationRangeBehavior: { ClosedClosed: 1 }, OverviewRulerLane: { Right: 1 },
    ThemeColor: class { constructor(id) { this.id = id; } }, Uri: { file: value => value },
    Range: class { constructor(start, col, end, endCol) { Object.assign(this, { start, end, col, endCol }); } },
  };
  const markers = createMarkers(vscode, { subscriptions: [], asAbsolutePath: value => value });
  return { markers, vscode, editor, calls, makeEditor,
    advance: ms => { now += ms; },
    row: (kind = 'read', extra = {}) => ({ repo: { root: '/repo' }, kind, agent: 'codex', source: 'branch',
      at: new Date(now).toISOString(), file: { path: 'a.go', line: 4, ranges: [{ start: 4, end: 8 }] }, ...extra }),
    // Reconcile the complete poll snapshot, using freshly allocated rows.
    poll(reviews, isLive = () => true, replacement) {
      markers.reconcile(reviews.map(({ row, uri }) => ({ row: structuredClone(row), uri })), isLive,
        replacement && { row: structuredClone(replacement.row), uri: replacement.uri });
    },
  };
}

const sequence = h => h.calls.map(({ kind, options }) => [kind, options.length]);

test('one poll reconciles changed reviews and a fresh replacement before any paint', t => {
  const h = harness(t);
  const uri = h.editor.document.uri;
  h.markers.show(h.row('read', { id: 'old' }), uri);
  h.poll([{ row: h.row('review'), uri }]);
  h.advance(15000);
  const replacement = { row: h.row('read', { id: 'new' }), uri };
  const reviews = [{ row: h.row('review', { file: { line: 1, ranges: [{ start: 1, end: 2 }] } }), uri }];
  h.poll(reviews, row => row.id === 'new' || row.kind === 'review', replacement);
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['review', 1]]);
  assert.equal(h.editor.paints.get('review')[0].range.start, 0);
  h.poll(reviews, row => row.id === 'new' || row.kind === 'review');
  assert.equal(h.calls.length, 3, 'the replacement was not retained as the live current row');
});

test('equivalent activity polls leave read, edit and persistent review markers untouched', t => {
  const h = harness(t);
  const uri = h.editor.document.uri;
  const review = h.row('review', { at: new Date(Date.now() - 60000).toISOString() });
  for (const kind of ['read', 'edit']) {
    const row = h.row(kind);
    h.markers.show(row, uri);
    h.poll([{ row: review, uri }]);
    const before = h.calls.length;
    for (let i = 0; i < 5; i++) {
      h.advance(700);
      h.poll([{ row: { ...review, id: String(i), at: new Date(Date.now()).toISOString() }, uri }]);
      h.markers.show(structuredClone(row), uri);
    }
    assert.equal(h.calls.length, before, 'unchanged polling reapplied decorations');
    assert.equal(h.editor.paints.get(kind).length, 1);
    assert.equal(h.editor.paints.get('review').length, 1);
  }
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['read', 0], ['edit', 1]]);
});

test('range and label replacements apply once without clearing the same decoration type', t => {
  const h = harness(t);
  const uri = h.editor.document.uri;
  h.markers.show(h.row('read', { file: { line: 4, ranges: [{ start: 4, end: 100 }] } }), uri);
  assert.equal(h.editor.paints.get('read')[0].range.start, 3);
  assert.equal(h.editor.paints.get('read')[0].range.end, 9);
  h.markers.show(h.row('read', { file: { line: 2, ranges: [{ start: 2, end: 3 }] } }), uri);
  assert.equal(h.editor.paints.get('read')[0].range.start, 1);
  assert.equal(h.editor.paints.get('read')[0].range.end, 2);
  h.markers.show(h.row('read', { agent: 'claude', file: { line: 2, ranges: [{ start: 2, end: 3 }] } }), uri);
  assert.match(h.editor.paints.get('read')[0].renderOptions.after.contentText, /claude/);
  h.markers.show(h.row('edit', { file: { line: 2, ranges: [{ start: 2, end: 3 }, { start: 8, end: 8 }] } }), uri);
  assert.equal(h.editor.paints.get('edit')[1].range.start, 7);
  h.markers.setReviews([{ row: h.row('review'), uri }]);
  h.markers.setReviews([{ row: h.row('review', { file: { line: 1, ranges: [{ start: 1, end: 2 }] } }), uri }]);
  assert.equal(h.editor.paints.get('review')[0].range.start, 0);
  assert.deepEqual(sequence(h), [['read', 1], ['read', 1], ['read', 1], ['read', 0], ['edit', 2], ['review', 1], ['review', 1]]);
});

test('dirty buffers clear markers once and eligible clean buffers restore them', t => {
  const h = harness(t);
  const { document } = h.editor;
  h.markers.show(h.row(), document.uri);
  const reviews = [{ row: h.row('review'), uri: document.uri }];
  h.poll(reviews);
  document.isDirty = true;
  document.version++;
  h.markers.refresh(() => true);
  assert.deepEqual(h.editor.paints.get('read'), []);
  assert.deepEqual(h.editor.paints.get('review'), []);
  for (let i = 0; i < 3; i++) {
    document.version++;
    h.poll(reviews);
  }
  assert.equal(document.isDirty, true);
  assert.equal(h.calls.length, 4, 'already empty decorations were cleared again');
  document.isDirty = false;
  h.markers.refresh(() => true);
  h.poll(reviews);
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['read', 0], ['review', 0], ['read', 1], ['review', 1]]);
});

test('document versions and identity restore tracked ranges even at unchanged coordinates', t => {
  const h = harness(t);
  const { document } = h.editor;
  h.markers.show(h.row(), document.uri);
  // A clean reload can move VS Code's tracked ranges without changing the
  // marker descriptor, line count or line lengths.
  document.version++;
  h.markers.refresh(() => true);
  h.markers.refresh(() => true);
  assert.deepEqual(sequence(h), [['read', 1], ['read', 1]]);
  assert.deepEqual(h.calls[0].options, h.calls[1].options);
  h.editor.document = { ...document };
  h.markers.refresh(() => true);
  assert.equal(h.calls.length, 3, 'a replacement document reused stale decorations');
  h.editor.document.lineCount = 5;
  h.editor.document.lineAt = () => ({ text: 'longer text' });
  h.editor.document.version++;
  h.markers.refresh(() => true);
  assert.equal(h.editor.paints.get('read')[0].range.end, 4);
  assert.equal(h.editor.paints.get('read')[0].range.endCol, 11);
  assert.deepEqual(sequence(h), [['read', 1], ['read', 1], ['read', 1], ['read', 1]]);
});

test('expiry and disconnect remove obsolete activity while live review markers persist', t => {
  const h = harness(t);
  const uri = h.editor.document.uri;
  const read = h.row();
  const reviews = [{ row: h.row('review', { at: new Date(Date.now() - 60000).toISOString() }), uri }];
  h.markers.show(read, uri);
  h.poll(reviews);
  h.advance(14999);
  h.poll(reviews);
  assert.equal(h.calls.length, 2);
  h.advance(1);
  h.poll(reviews);
  h.poll(reviews);
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['read', 0]]);
  assert.equal(h.editor.paints.get('review').length, 1);
  h.markers.show(h.row('edit'), uri);
  h.markers.refresh(row => row.kind === 'review');
  assert.equal(h.editor.paints.get('review').length, 1);
  h.markers.refresh(() => false);
  h.markers.refresh(() => false);
  h.markers.show(read, uri); // Showing expired activity must not resurrect it.
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['read', 0], ['edit', 1], ['edit', 0], ['review', 0]]);
});

test('clear removes only applied markers and following can resume with identical rows', t => {
  const h = harness(t);
  const uri = h.editor.document.uri;
  const read = h.row();
  const reviews = [{ row: h.row('review'), uri }];
  h.markers.show(read, uri);
  h.poll(reviews);
  h.markers.clear();
  h.markers.clear();
  h.markers.refresh(() => true);
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['read', 0], ['review', 0]]);
  h.markers.show(structuredClone(read), uri);
  h.poll(reviews);
  h.markers.setReviews([]);
  h.markers.setReviews([]);
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1], ['read', 0], ['review', 0], ['read', 1], ['review', 1], ['review', 0]]);
});

test('newly visible and split editors receive independent decorations', t => {
  const h = harness(t);
  const uri = h.editor.document.uri;
  const split = h.makeEditor(undefined, h.editor.document);
  const other = h.makeEditor('file:b.go');
  const unrelated = h.makeEditor('file:unrelated.go');
  h.vscode.window.visibleTextEditors = [];
  h.markers.show(h.row(), uri);
  h.poll([{ row: h.row('review'), uri }, { row: h.row('review'), uri: other.document.uri }]);
  assert.equal(h.calls.length, 0);
  h.vscode.window.visibleTextEditors = [h.editor];
  h.markers.refresh(() => true);
  assert.deepEqual(sequence(h), [['read', 1], ['review', 1]]);
  h.vscode.window.visibleTextEditors = [h.editor, split, other, unrelated];
  h.markers.refresh(() => true);
  assert.deepEqual(h.calls.slice(2).map(call => [call.editor, call.kind, call.options.length]),
    [[split, 'read', 1], [split, 'review', 1], [other, 'review', 1]]);
  // Moving activity clears only the old location and applies the new one.
  h.markers.show(h.row('read', { file: { path: 'b.go', line: 2, ranges: [] } }), other.document.uri);
  assert.deepEqual(h.calls.slice(5).map(call => [call.editor, call.kind, call.options.length]),
    [[h.editor, 'read', 0], [split, 'read', 0], [other, 'read', 1]]);
  assert.equal(other.paints.get('read')[0].range.start, 1);
  assert.equal(other.paints.get('read')[0].range.end, 1);
  assert.match(other.paints.get('read')[0].hoverMessage, /range unavailable/);
  h.markers.refresh(() => true);
  assert.equal(h.calls.length, 8);
  // Hidden editors can return after their review marker disappeared.
  h.vscode.window.visibleTextEditors = [other];
  h.markers.setReviews([]);
  h.vscode.window.visibleTextEditors = [h.editor, split, other];
  h.markers.refresh(() => true);
  assert.deepEqual(h.calls.slice(8).map(call => [call.editor, call.kind, call.options.length]),
    [[other, 'review', 0], [h.editor, 'review', 0], [split, 'review', 0]]);
});
