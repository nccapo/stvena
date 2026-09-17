'use strict';
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { createSelectionActions, settleMs } = require('../src/selection');

function harness(options = {}) {
  const listeners = {};
  const on = name => listener => { listeners[name] = listener; return { dispose() {} }; };
  const contextKeys = new Map();
  let enabled = options.enabled !== false;
  let fired = 0;
  const document = { lineCount: 20 };
  const editor = { document, selection: { isEmpty: false } };
  let provider;
  const vscode = {
    window: {
      activeTextEditor: editor,
      onDidChangeTextEditorSelection: on('selection'),
      onDidChangeActiveTextEditor: on('active'),
    },
    workspace: {
      getConfiguration: () => ({ get: () => enabled }),
      onDidChangeTextDocument: on('edit'),
      onDidSaveTextDocument: on('save'),
      onDidChangeConfiguration: on('config'),
    },
    languages: { registerCodeLensProvider: (_selector, value) => { provider = value; return { dispose() {} }; } },
    commands: { executeCommand: (name, key, value) => { if (name === 'setContext') contextKeys.set(key, value); } },
    EventEmitter: class {
      constructor() { this.event = () => ({ dispose() {} }); }
      fire() { fired++; }
      dispose() {}
    },
    Range: class { constructor(line) { this.line = line; } },
    CodeLens: class { constructor(range, command) { this.range = range; this.command = command; } },
  };
  let target = options.target === undefined ? { line: 3, endLine: 5 } : options.target;
  const ui = createSelectionActions(vscode, { subscriptions: [] }, () => target);
  return {
    ui, editor, document, listeners, contextKeys,
    lenses: (doc = document) => provider.provideCodeLenses(doc),
    fired: () => fired,
    setTarget: value => { target = value; },
    setEnabled: value => { enabled = value; },
  };
}

const settle = () => new Promise(resolve => setTimeout(resolve, settleMs + 50));

test('a settled selection gets the actions above its first line', async () => {
  const h = harness();
  h.listeners.selection();
  // Nothing appears while the selection may still be moving.
  assert.deepEqual(h.lenses(), []);
  await settle();
  const lenses = h.lenses();
  assert.deepEqual(lenses.map(lens => lens.command.title), ['⤴ Paste to agent', 'Ask…', 'Add to context']);
  assert.deepEqual(lenses.map(lens => lens.command.command),
    ['stvena.pasteSelection', 'stvena.askAgent', 'stvena.addSelectionToContext']);
  assert.ok(lenses.every(lens => lens.range.line === 2));
  assert.deepEqual(h.lenses({ lineCount: 20 }), [], 'actions leaked into another document');
  assert.equal(h.contextKeys.get('stvena.canPasteSelection'), true);
});

test('the actions hold still while a drag continues, and move once it settles', async () => {
  const h = harness();
  h.listeners.selection();
  await settle();
  const before = h.fired();
  h.setTarget({ line: 7, endLine: 9 });
  h.listeners.selection();
  assert.equal(h.fired(), before, 'the lens was redrawn mid-drag');
  assert.equal(h.lenses()[0].range.line, 2);
  await settle();
  assert.equal(h.lenses()[0].range.line, 6);

  // Extending a selection from the same first line does not redraw.
  const moved = h.fired();
  h.setTarget({ line: 7, endLine: 12 });
  h.listeners.selection();
  await settle();
  assert.equal(h.fired(), moved);
});

test('clearing the selection removes the actions at once', async () => {
  const h = harness();
  h.listeners.active();
  assert.equal(h.lenses().length, 3);
  h.editor.selection = { isEmpty: true };
  h.listeners.selection();
  assert.deepEqual(h.lenses(), []);
  assert.equal(h.contextKeys.get('stvena.canPasteSelection'), false);
});

test('an unsendable selection shows nothing', async () => {
  const h = harness({ target: null });
  h.listeners.selection();
  await settle();
  assert.deepEqual(h.lenses(), []);
  assert.notEqual(h.contextKeys.get('stvena.canPasteSelection'), true);
});

test('the setting hides the actions but keeps the title button', () => {
  const h = harness({ enabled: false });
  h.listeners.active();
  assert.deepEqual(h.lenses(), []);
  assert.equal(h.contextKeys.get('stvena.canPasteSelection'), true);
  h.setEnabled(true);
  h.listeners.config({ affectsConfiguration: name => name === 'stvena.showSelectionCodeLens' });
  assert.equal(h.lenses().length, 3);
});

test('refresh follows Stvena starting and stopping, but not mid-drag', async () => {
  const h = harness({ target: null });
  h.listeners.active();
  h.setTarget({ line: 1, endLine: 1 });
  h.listeners.selection();
  h.ui.refresh();
  assert.deepEqual(h.lenses(), [], 'drew while the selection was settling');
  await settle();
  assert.equal(h.lenses().length, 3);

  h.setTarget(null);
  h.ui.refresh();
  assert.deepEqual(h.lenses(), []);
  assert.equal(h.contextKeys.get('stvena.canPasteSelection'), false);
});
