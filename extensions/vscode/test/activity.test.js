'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { fresh, readRow, label, createMarkers } = require('../src/activity');
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

test('read/edit markers cover reported ranges, clamp EOF, and clear on dirty, expiry and disconnect', () => {
  let paints = new Map();
  const editor = { document: { isDirty: false, uri: { toString: () => 'file:a.go' },
    lineCount: 10, lineAt: () => ({ text: 'text' }) }, setDecorations: (type, options) => paints.set(type.kind, options) };
  let next = 0;
  const vscode = {
    window: { visibleTextEditors: [editor], createTextEditorDecorationType: () => ({ kind: next++ }) },
    DecorationRangeBehavior: { ClosedClosed: 1 }, OverviewRulerLane: { Right: 1 },
    ThemeColor: class { constructor(id) { this.id = id; } }, Uri: { file: value => value },
    Range: class { constructor(start, col, end, endCol) { Object.assign(this, { start, end, col, endCol }); } },
  };
  const markers = createMarkers(vscode, { subscriptions: [], asAbsolutePath: value => value });
  const row = { kind: 'read', agent: 'codex', at: new Date().toISOString(), file: { line: 4, ranges: [{ start: 4, end: 100 }] } };
  markers.show(row, editor.document.uri);
  assert.equal(paints.get(0)[0].range.start, 3);
  assert.equal(paints.get(0)[0].range.end, 9);
  assert.match(paints.get(0)[0].renderOptions.after.contentText, /Read/);
  editor.document.isDirty = true;
  markers.refresh(() => true);
  assert.deepEqual(paints.get(0), []);
  editor.document.isDirty = false;
  markers.show({ ...row, kind: 'edit', file: { line: 2, ranges: [{ start: 2, end: 3 }, { start: 8, end: 8 }] } }, editor.document.uri);
  assert.equal(paints.get(1).length, 2);
  assert.deepEqual(paints.get(0), []);
  markers.refresh(() => false);
  assert.deepEqual(paints.get(1), []);
  markers.show({ ...row, at: new Date(Date.now() - 16000).toISOString() }, editor.document.uri);
  assert.deepEqual(paints.get(0), []);
});
