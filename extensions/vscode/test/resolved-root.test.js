'use strict';

// A project Stvena recorded under two spellings: the path it was started in
// (`root`) and the same path with symlinks resolved (`realRoot`), the way
// /tmp/p and /private/tmp/p differ on macOS. VS Code has the project open
// through the resolved spelling, so every document it reports lives under
// `realRoot`. The selection actions must still reach Stvena.

const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const fsp = require('node:fs/promises');
const os = require('node:os');
const path = require('node:path');
const vm = require('node:vm');
const { createRequire } = require('node:module');
const realBridge = require('../src/bridge');

const ROOT = path.join(path.sep, 'tmp', 'p');
const REAL_ROOT = path.join(path.sep, 'private', 'tmp', 'p');

const flush = () => new Promise(resolve => setImmediate(resolve));
const disposable = () => ({ dispose() {} });

// Run the real activate() with only VS Code and the bridge's descriptor reads
// mocked. bridge.writeRequest is the production one, writing into a private
// directory, so the request a command produces is validated exactly as it is
// in the editor.
async function harness(t, files) {
  const dir = await fsp.mkdtemp(path.join(os.tmpdir(), 'stvena-resolved-'));
  t.after(() => fsp.rm(dir, { recursive: true, force: true }));
  const requestPath = path.join(dir, 'stvena-request.json');
  const repo = { root: ROOT, realRoot: REAL_ROOT, mode: 'shadow', dir,
    gitDir: path.join(dir, 'shadow.git'),
    statePath: path.join(dir, 'stvena-live.json'), reviewPath: path.join(dir, 'stvena-review.json'),
    requestPath, presencePath: path.join(dir, 'stvena-ide.json') };

  const events = new Map(), commands = new Map(), errorsShown = [], logs = [];
  const on = name => listener => {
    if (!events.has(name)) events.set(name, []);
    events.get(name).push(listener);
    return disposable();
  };
  const uri = file => ({ scheme: 'file', fsPath: file, toString: () => `file:${file}`, path: file });
  const editors = new Map(files.map(file => {
    const document = { uri: uri(file), version: 1, isDirty: false, lineCount: 50,
      lineAt: () => ({ text: 'source' }), getText: () => 'source' };
    const editor = { document,
      // Lines 12 to 14 of the file, the way VS Code reports a dragged selection.
      selection: { isEmpty: false, active: { line: 11 },
        start: { line: 11, character: 0 }, end: { line: 13, character: 5 } },
      setDecorations() {}, revealRange() {} };
    return [file, editor];
  }));
  const active = editors.get(files[0]);
  const vscode = {
    workspace: {
      isTrusted: true, workspaceFolders: [{ name: 'p', uri: uri(REAL_ROOT) }],
      getConfiguration: () => ({ get: (_name, fallback) => fallback }),
      onDidChangeTextDocument: on('edit'), onDidSaveTextDocument: on('save'),
      onDidChangeConfiguration: on('config'), onDidChangeWorkspaceFolders: on('folders'),
      async openTextDocument(value) { return editors.get(value.fsPath).document; },
    },
    window: {
      activeTextEditor: active, visibleTextEditors: [...editors.values()],
      onDidChangeTextEditorSelection: on('selection'), onDidChangeActiveTextEditor: on('active'),
      onDidChangeVisibleTextEditors: on('visible'),
      createTextEditorDecorationType: options => ({ ...disposable(),
        kind: options.backgroundColor?.id.replace(/^stvena\.|Background$/g, '') }),
      createOutputChannel: () => ({ ...disposable(), appendLine: text => logs.push(text) }),
      createTreeView: () => disposable(),
      createStatusBarItem: () => ({ ...disposable(), show() {}, hide() {} }),
      setStatusBarMessage: () => {}, registerFileDecorationProvider: disposable,
      showErrorMessage: message => { errorsShown.push(message); },
      showInformationMessage: () => {}, showWarningMessage: () => {},
      showInputBox: async () => 'why does this need a lock?',
      async showTextDocument(document) { return editors.get(document.uri.fsPath); },
    },
    commands: { registerCommand: (name, handler) => { commands.set(name, handler); return disposable(); },
      executeCommand: () => {} },
    languages: { registerCodeLensProvider: disposable, registerHoverProvider: disposable },
    env: { appName: 'VS Code' },
    extensions: { getExtension: () => ({ packageJSON: { version: '0.0.0-test' } }) },
    Uri: { file: uri }, ThemeColor: class { constructor(id) { this.id = id; } },
    ThemeIcon: class { constructor(id) { this.id = id; } },
    TreeItem: class { constructor(label) { this.label = label; } },
    EventEmitter: class { constructor() { this.event = () => disposable(); } fire() {} dispose() {} },
    Range: class {
      constructor(start, character, end, endCharacter) {
        this.start = { line: start, character }; this.end = { line: end, character: endCharacter };
      }
    },
    CodeLens: class { constructor(range, command) { this.range = range; this.command = command; } },
    DecorationRangeBehavior: { ClosedClosed: 1 }, OverviewRulerLane: { Left: 1, Right: 2 },
    StatusBarAlignment: { Left: 1 }, TextEditorRevealType: { InCenterIfOutsideViewport: 1 },
  };

  const now = () => new Date().toISOString();
  const features = ['review', 'context', 'accept', 'unaccept', 'reject', 'undo-reject',
    'apply-rejections', 'next-unreviewed', 'prompt', 'paste', 'accept-all'];
  const bridge = { ...realBridge,
    registryStamp: async () => 'stamp',
    discover: async () => ({ ...repo }),
    lookup: async () => ({ ...repo }),
    announce: async () => {},
    readState: async () => ({ version: 1, session: 's', sequence: 1, active: true,
      updatedAt: now(), files: [] }),
    readReviewState: async () => ({ version: 1, session: 's', sequence: 1, active: true,
      updatedAt: now(), unreviewedFiles: 0, unreviewedHunks: 0, newerBatches: 0, features, files: [] }),
  };

  let timer;
  const filename = path.join(__dirname, '../src/extension.js');
  const localRequire = createRequire(filename);
  const module = { exports: {} };
  vm.runInNewContext(fs.readFileSync(filename, 'utf8'), {
    module, process, Date, console, Buffer, URL,
    setTimeout, clearTimeout, setImmediate,
    require: name => name === 'vscode' ? vscode : name === './bridge' ? bridge : name === './queue' ?
      { createRequestQueue: ({ write }) => (root, request) => write(root, request) } : localRequire(name),
    setInterval: callback => { timer = callback; return 1; }, clearInterval: () => { timer = undefined; },
  }, { filename });
  const context = { subscriptions: [], asAbsolutePath: file => path.join(__dirname, '..', file) };
  t.after(() => { for (const value of context.subscriptions.splice(0).reverse()) value.dispose(); });
  module.exports.activate(context);
  await flush();
  timer();
  for (let i = 0; i < 5; i++) await flush();

  return {
    errorsShown, logs, requestPath,
    setActive: file => { vscode.window.activeTextEditor = editors.get(file); },
    async run(name) {
      errorsShown.length = 0;
      await fsp.rm(requestPath, { force: true });
      await commands.get(name)();
      await flush();
    },
    async request() {
      try {
        return JSON.parse(await fsp.readFile(requestPath, 'utf8'));
      } catch (error) {
        if (error.code === 'ENOENT') return undefined;
        throw error;
      }
    },
  };
}

const ACTIONS = [
  ['stvena.pasteSelection', 'paste'],
  ['stvena.askAgent', 'prompt'],
  ['stvena.addSelectionToContext', 'context'],
  ['stvena.reviewInStvena', 'review'],
];

test('selection actions reach Stvena when the editor opens the project through its resolved path', async t => {
  const file = path.join(REAL_ROOT, 'a.go');
  const h = await harness(t, [file]);
  for (const [command, action] of ACTIONS) {
    await h.run(command);
    const request = await h.request();
    assert.deepEqual(h.errorsShown, [], `${command} reported an error instead of sending the selection`);
    assert.ok(request, `${command} wrote no request for a document under the project's resolved path`);
    assert.deepEqual([request.action, request.path, request.line, request.endLine],
      [action, 'a.go', 12, 14], `${command} did not send the project-relative path`);
  }
});

test('a file in a subdirectory of the resolved project path keeps its project-relative path', async t => {
  const file = path.join(REAL_ROOT, 'sub', 'a.go');
  const h = await harness(t, [file]);
  await h.run('stvena.pasteSelection');
  const request = await h.request();
  assert.deepEqual(h.errorsShown, []);
  assert.ok(request, 'paste wrote no request for a nested document under the resolved path');
  assert.equal(request.path, 'sub/a.go');
});
