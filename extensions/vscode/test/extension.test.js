'use strict';

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { createRequire } = require('node:module');
const realBridge = require('../src/bridge');

const flush = () => new Promise(resolve => setImmediate(resolve));
const disposable = () => ({ dispose() {} });

function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}

// Run activate() and its real timer-driven poll, with only the bridge's I/O
// and VS Code host mocked. No copy of polling or marker logic lives here.
async function harness(t) {
  let now = Date.parse('2026-09-24T12:00:00Z'), timer, nextOpen, nextShow;
  t.mock.method(Date, 'now', () => now);
  const events = new Map(), commands = new Map(), calls = [], shown = [], revealed = [], logs = [];
  const on = name => listener => {
    if (!events.has(name)) events.set(name, []);
    events.get(name).push(listener);
    return disposable();
  };
  const emit = (name, value) => { for (const listener of events.get(name) || []) listener(value); };
  const uri = file => ({ scheme: 'file', fsPath: file, toString: () => `file:${file}` });
  const editors = new Map(['a.go', 'b.go'].map(file => {
    const document = { uri: uri(path.join('/repo', file)), version: 1, isDirty: false,
      lineCount: 50, lineAt: () => ({ text: 'source' }) };
    const editor = { document, selection: { isEmpty: true, active: { line: 0 } },
      setDecorations(type, options) {
        if (['read', 'edit', 'review'].includes(type.kind)) calls.push({ file, kind: type.kind, options });
      },
      revealRange(range) { revealed.push({ file, range }); },
    };
    return [document.uri.fsPath, editor];
  }));
  let treeProvider;
  const vscode = {
    workspace: {
      isTrusted: true, workspaceFolders: [{ name: 'repo', uri: uri('/repo') }],
      getConfiguration: () => ({ get: (_name, fallback) => fallback }),
      onDidChangeTextDocument: on('edit'), onDidSaveTextDocument: on('save'),
      onDidChangeConfiguration: on('config'), onDidChangeWorkspaceFolders: on('folders'),
      async openTextDocument(uri) {
        const gate = nextOpen;
        nextOpen = undefined;
        if (gate) await gate.promise;
        const editor = editors.get(uri.fsPath);
        if (!editor) throw new Error(`missing ${uri.fsPath}`);
        return editor.document;
      },
    },
    window: {
      visibleTextEditors: [...editors.values()],
      onDidChangeTextEditorSelection: on('selection'), onDidChangeActiveTextEditor: on('active'),
      onDidChangeVisibleTextEditors: on('visible'),
      createTextEditorDecorationType: options => ({ ...disposable(),
        kind: options.backgroundColor?.id.replace(/^stvena\.|Background$/g, '') }),
      createOutputChannel: () => ({ ...disposable(), appendLine: text => logs.push(text) }),
      createTreeView: (_name, options) => { treeProvider = options.treeDataProvider; return disposable(); },
      createStatusBarItem: () => ({ ...disposable(), show() {}, hide() {} }),
      registerFileDecorationProvider: disposable,
      showErrorMessage: message => logs.push(message), showInformationMessage: () => {}, showWarningMessage: () => {},
      async showTextDocument(document, options) {
        shown.push({ document, options });
        const gate = nextShow;
        nextShow = undefined;
        if (gate) await gate.promise;
        emit('visible', vscode.window.visibleTextEditors);
        return editors.get(document.uri.fsPath);
      },
    },
    commands: { registerCommand: (name, handler) => { commands.set(name, handler); return disposable(); },
      executeCommand: () => {} },
    languages: { registerCodeLensProvider: disposable, registerHoverProvider: disposable },
    env: { appName: 'VS Code' }, extensions: { getExtension: () => ({ packageJSON: { version: '0.5.1' } }) },
    Uri: { file: uri }, ThemeColor: class { constructor(id) { this.id = id; } },
    EventEmitter: class { constructor() { this.event = () => disposable(); } fire() {} dispose() {} },
    Range: class {
      constructor(start, character, end, endCharacter) {
        this.start = { line: start, character }; this.end = { line: end, character: endCharacter };
      }
    },
    DecorationRangeBehavior: { ClosedClosed: 1 }, OverviewRulerLane: { Left: 1, Right: 2 },
    StatusBarAlignment: { Left: 1 }, TextEditorRevealType: { InCenterIfOutsideViewport: 1 },
  };
  const timestamp = () => new Date(now).toISOString();
  const state = { session: 's', active: true, sequence: 1, files: [] };
  let review = { session: 's', active: true, sequence: 1, changedAt: new Date(now - 1000).toISOString(), files: [],
    focus: { path: 'a.go', line: 12, endLine: 14, source: 'branch' } };
  const publishRead = (id, extra = {}) => {
    state.activity = { session: 's', id, agent: 'codex', path: 'a.go', line: 4, endLine: 8, updatedAt: timestamp(), ...extra };
  };
  publishRead('read-1');
  const bridge = { ...realBridge,
    registryStamp: async () => 'stamp', discover: async () => ({ root: '/repo', dir: '/repo/.git' }),
    announce: async () => {},
    readState: async () => structuredClone({ ...state, updatedAt: timestamp() }),
    readReviewState: async () => review && structuredClone({ ...review, updatedAt: timestamp() }),
  };
  const filename = path.join(__dirname, '../src/extension.js');
  const localRequire = createRequire(filename);
  const module = { exports: {} };
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), {
    module, process, Date,
    require: name => name === 'vscode' ? vscode : name === './bridge' ? bridge : localRequire(name),
    setInterval: callback => { timer = callback; return 1; }, clearInterval: () => { timer = undefined; },
  }, { filename });
  const context = { subscriptions: [], asAbsolutePath: file => path.join(__dirname, '..', file) };
  const dispose = () => { for (const value of context.subscriptions.splice(0).reverse()) value.dispose(); };
  t.after(dispose);
  module.exports.activate(context);
  await flush();
  assert.deepEqual(logs, []);
  assert.deepEqual(calls.map(({ kind, options }) => [kind, options.length]).sort(), [['read', 1], ['review', 1]]);
  calls.length = 0;
  return { calls, shown, revealed, logs, state, vscode, publishRead, dispose,
    document: editors.get('/repo/a.go').document,
    advance: ms => { now += ms; },
    setReview: value => { review = value; },
    rows: () => treeProvider.getChildren(),
    holdOpen: () => (nextOpen = deferred()), holdShow: () => (nextShow = deferred()),
    emit: name => emit(name, name === 'edit' ? { document: editors.get('/repo/a.go').document } : undefined),
    command: (name, ...args) => commands.get(name)(...args),
    async poll(expectedLogs = []) { timer(); await flush(); assert.deepEqual(logs, expectedLogs); },
  };
}

const sequence = h => h.calls.map(({ file, kind, options }) => [file, kind, options.length]);

test('production polls keep equivalent new read IDs and timestamps stable through async opening', async t => {
  const h = await harness(t);
  for (let i = 2; i < 5; i++) {
    h.advance(700);
    h.publishRead(`read-${i}`);
    const gate = h.holdOpen();
    await h.poll();
    assert.deepEqual(h.calls, [], 'poll cleared the predecessor before opening its equivalent replacement');
    h.emit('visible');
    gate.resolve();
    await flush();
    assert.deepEqual(h.calls, [], 'completing navigation repainted equivalent markers');
    await h.poll();
    assert.deepEqual(h.calls, []);
  }
});

test('changed read ranges paint one final nonempty value before navigation finishes', async t => {
  const h = await harness(t);
  h.advance(700);
  h.publishRead('read-2', { line: 6, endLine: 10 });
  const gate = h.holdOpen();
  await h.poll();
  assert.deepEqual(sequence(h), [['a.go', 'read', 1]]);
  assert.equal(h.calls[0].options[0].range.start.line, 5);
  assert.equal(h.calls[0].options[0].range.end.line, 9);
  gate.resolve();
  await flush();
  assert.deepEqual(sequence(h), [['a.go', 'read', 1]]);
});

test('a fresh replacement stays uninterrupted when its predecessor expires, then expires normally', async t => {
  for (const age of [14700, 15000]) {
    await t.test(`predecessor age ${age}ms`, async t => {
      const h = await harness(t);
      h.advance(age);
      h.publishRead('read-2');
      const gate = h.holdOpen();
      await h.poll();
      assert.deepEqual(h.calls, [], 'updating persistent reviews painted the expired predecessor');
      h.advance(700);
      h.emit('visible');
      gate.resolve();
      await flush();
      assert.deepEqual(h.calls, []);
      h.advance(14300);
      await h.poll();
      assert.deepEqual(sequence(h), [['a.go', 'read', 0]]);
      await h.poll();
      assert.deepEqual(sequence(h), [['a.go', 'read', 0]], 'persistent review flickered during expiry cleanup');
    });
  }
});

test('obsolete markers clear when no eligible replacement exists', async t => {
  for (const reason of ['missing', 'expired', 'binary', 'truncated', 'deleted', 'inactive', 'error']) {
    await t.test(reason, async t => {
      const h = await harness(t);
      h.advance(700);
      delete h.state.activity;
      h.state.sequence++;
      h.state.changedAt = new Date(Date.now()).toISOString();
      if (reason === 'expired') h.publishRead('stale', { updatedAt: new Date(Date.now() - 15000).toISOString() });
      if (reason === 'inactive') h.state.active = false;
      if (reason === 'error') h.state.error = 'capture failed';
      if (['binary', 'truncated', 'deleted'].includes(reason)) h.state.files = [{ path: 'a.go', line: 1,
        after: reason === 'deleted' ? '0'.repeat(40) : '1'.repeat(40),
        binary: reason === 'binary', truncated: reason === 'truncated' }];
      await h.poll(reason === 'error' ? ['/repo: capture failed'] : []);
      assert.deepEqual(sequence(h), [['a.go', 'read', 0]]);
      assert.equal(h.shown.length, 1, 'an ineligible replacement was navigated to');
    });
  }
});

test('disconnect clears activity and persistent reviews once', async t => {
  const h = await harness(t);
  h.state.active = false;
  h.setReview(undefined);
  await h.poll();
  await h.poll();
  assert.deepEqual(sequence(h), [['a.go', 'read', 0], ['a.go', 'review', 0]]);
});

test('dirty buffers remain untouched while a replacement waits for document opening', async t => {
  const h = await harness(t);
  h.advance(700);
  h.publishRead('read-2', { line: 6, endLine: 10 });
  const gate = h.holdOpen();
  await h.poll();
  h.document.isDirty = true;
  h.document.version++;
  h.emit('edit');
  gate.resolve();
  await flush();
  assert.equal(h.document.isDirty, true);
  assert.equal(h.shown.length, 1, 'automatic navigation disturbed a dirty buffer');
  assert.deepEqual(sequence(h), [['a.go', 'read', 1], ['a.go', 'read', 0], ['a.go', 'review', 0]]);
  h.document.isDirty = false;
  h.document.version++;
  h.emit('edit');
  assert.deepEqual(sequence(h).slice(3), [['a.go', 'read', 1], ['a.go', 'review', 1]]);
  assert.equal(h.calls[3].options[0].range.start.line, 5);
});

test('pausing cancels navigation and prevents a pending replacement from returning', async t => {
  const h = await harness(t);
  h.advance(700);
  h.publishRead('read-2');
  const gate = h.holdOpen();
  await h.poll();
  h.command('stvena.toggleFollow');
  gate.resolve();
  await flush();
  assert.equal(h.shown.length, 1);
  assert.deepEqual(sequence(h), [['a.go', 'read', 0], ['a.go', 'review', 0]]);
  await h.poll();
  assert.equal(h.calls.length, 2);
  await h.command('stvena.toggleFollow');
  await h.poll();
  assert.deepEqual(sequence(h).slice(2), [['a.go', 'read', 1], ['a.go', 'review', 1]]);
});

test('explicit navigation supersedes a pending automatic document display', async t => {
  const h = await harness(t);
  h.advance(700);
  h.publishRead('read-2');
  const gate = h.holdShow();
  await h.poll();
  const row = h.rows().find(row => row.kind === 'read');
  await h.command('stvena.openChange', { ...row, file: { ...row.file, path: 'b.go' } });
  assert.deepEqual(sequence(h), [['a.go', 'read', 0], ['b.go', 'read', 1]]);
  const revealed = h.revealed.length;
  gate.resolve();
  await flush();
  assert.equal(h.revealed.length, revealed, 'cancelled navigation revealed the old target');
  assert.deepEqual(sequence(h), [['a.go', 'read', 0], ['b.go', 'read', 1]]);
});

test('disposal cancels an automatic document open without restoring markers', async t => {
  const h = await harness(t);
  h.advance(700);
  h.publishRead('read-2');
  const gate = h.holdOpen();
  await h.poll();
  h.dispose();
  gate.resolve();
  await flush();
  assert.equal(h.shown.length, 1);
  assert.equal(h.revealed.length, 1);
  assert.deepEqual(h.calls, []);
});
