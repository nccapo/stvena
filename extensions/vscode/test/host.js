'use strict';

// Run only in an isolated extension development host with a disposable workspace.
const vscode = require('vscode');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { discover } = require('../src/bridge');

// scenario is the whole editor suite, run once against a Git repository and
// once against a project Stvena snapshots privately. Nothing in here touches
// Git: the extension never dereferences a captured object ID, it only validates
// the shape, so the fixture keeps its own contents and hands out synthetic IDs.
async function scenario(repo, root, session) {
  // Each pass uses its own files. An editor keeps a document for a file it has
  // opened even after the editor closes, so sharing names across passes lets a
  // stale buffer answer an assertion about what this pass just published.
  const EXAMPLE = `${session}-example.txt`, NEXT = `${session}-next.txt`;
  const READING = `${session}-reading.txt`, ADDED = `${session}-added.txt`;
  const contents = new Map();
  let objects = 0;
  const blob = text => {
    const oid = (++objects).toString(16).padStart(40, '0');
    contents.set(oid, text);
    return oid;
  };
  const before = blob('one\ntwo\nthree\nold\n');
  const after = blob('one\ntwo\nthree\nnew\n');
  let sequence = 0, value;
  async function writeState() {
    value.updatedAt = new Date().toISOString();
    await fs.writeFile(repo.statePath + '.tmp', JSON.stringify(value));
    await fs.rename(repo.statePath + '.tmp', repo.statePath);
  }
  async function writeReview(focus, extra = {}) {
    const review = { version: 1, session: value.session, sequence: ++sequence, active: true,
      updatedAt: new Date().toISOString(), changedAt: new Date().toISOString(), focus,
      unreviewedFiles: 1, unreviewedHunks: 1, newerBatches: 0, ...extra };
    await fs.writeFile(repo.reviewPath + '.tmp', JSON.stringify(review));
    await fs.rename(repo.reviewPath + '.tmp', repo.reviewPath);
  }
  const FEATURES = ['review', 'context', 'accept', 'unaccept', 'reject', 'undo-reject',
    'apply-rejections', 'next-unreviewed', 'paste'];
  const HUNK = 'a'.repeat(64);
  // reviewFile publishes per-hunk state the way Stvena does for a changed file.
  const reviewFile = (hunk = {}) => ({ features: FEATURES, tree: '1'.repeat(40),
    files: [{ path: READING, status: 'M', hunks: [{ id: HUNK, start: 3, end: 4, ...hunk }] }] });
  const lensTitles = async file => (await vscode.commands.executeCommand('vscode.executeCodeLensProvider',
    vscode.Uri.file(path.join(root, file)))).map(lens => lens.command && lens.command.title).filter(Boolean);
  const readRequest = async () => JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
  async function publish(before, after, file = EXAMPLE) {
    const target = path.join(root, file);
    if (/^0+$/.test(after)) await fs.rm(target, { force: true });
    else await fs.writeFile(target, contents.get(after));
    value = { version: 1, session, sequence: ++sequence,
      active: true, changedAt: new Date().toISOString(), files: [{ path: file,
        status: /^0+$/.test(before) ? 'A' : /^0+$/.test(after) ? 'D' : 'M',
        before, after, line: 4, ranges: [{ start: 4, end: 4 }], added: 1, deleted: 1, binary: false, truncated: false }] };
    await writeState();
  }
  async function waitFor(predicate, message) {
    const deadline = Date.now() + 12000;
    while (Date.now() < deadline) {
      if (await predicate()) return;
      await new Promise(resolve => setTimeout(resolve, 100));
    }
    const open = vscode.workspace.textDocuments.filter(doc => doc.uri.scheme === 'file')
      .map(doc => `${path.basename(doc.uri.fsPath)}${doc.isDirty ? ' (unsaved)' : ''}`);
    const shown = vscode.window.visibleTextEditors.map(editor => path.basename(editor.document.uri.fsPath));
    throw new Error(`${message} [open: ${open.join(', ') || 'none'} · visible: ${shown.join(', ') || 'none'} · descriptors: ${repo.statePath}]`);
  }
  const visible = file => vscode.window.visibleTextEditors.find(editor =>
    editor.document.uri.scheme === 'file' && editor.document.uri.fsPath === path.join(root, file));
  const noDiffs = () => assert.ok(!vscode.window.tabGroups.all.flatMap(group => group.tabs)
    .some(tab => tab.input instanceof vscode.TabInputTextDiff), 'Following opened a diff tab');
  await vscode.commands.executeCommand('workbench.action.closeAllEditors');
  await vscode.extensions.getExtension('nccapo.stvena-live').activate();
  try {
    await publish(before, after);
    await waitFor(() => visible(EXAMPLE)?.selection.active.line === 3 &&
      visible(EXAMPLE)?.document.getText() === 'one\ntwo\nthree\nnew\n',
      'Automatic source navigation did not select the changed line');
    assert.equal(visible(EXAMPLE).document.getText(), 'one\ntwo\nthree\nnew\n');
    assert.equal(visible(EXAMPLE).selection.active.line, 3);
    noDiffs();

    await vscode.commands.executeCommand('stvena.toggleFollow');
    const next = blob('one\ntwo\nthree\nnewer\n');
    await publish(after, next, NEXT);
    await new Promise(resolve => setTimeout(resolve, 1800));
    assert.ok(visible(EXAMPLE), 'Pause navigated away from the current file');
    assert.equal(visible(NEXT), undefined);
    await vscode.commands.executeCommand('stvena.toggleFollow');
    await waitFor(() => visible(NEXT)?.selection.active.line === 3,
      'Resume did not select the newest changed line');
    assert.equal(visible(NEXT).selection.active.line, 3);

    const empty = '0'.repeat(40);
    await publish(empty, next, ADDED);
    await waitFor(() => visible(ADDED), 'Addition did not open the working file');
    await publish(next, empty, ADDED);
    await new Promise(resolve => setTimeout(resolve, 1800));
    noDiffs();

    // Automatic following must not replace an unsaved buffer or move its cursor.
    await vscode.commands.executeCommand('stvena.toggleFollow');
    const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(root, EXAMPLE)));
    const editor = await vscode.window.showTextDocument(doc);
    await editor.edit(edit => edit.insert(new vscode.Position(0, 0), 'unsaved '));
    editor.selection = new vscode.Selection(0, 0, 0, 0);
    const unsaved = doc.getText();
    await publish(after, next);
    await new Promise(resolve => setTimeout(resolve, 1800));
    await vscode.commands.executeCommand('stvena.toggleFollow');
    assert.equal(doc.getText(), unsaved);
    assert.equal(editor.selection.active.line, 0);
    assert.ok(doc.isDirty);
    noDiffs();
    await vscode.commands.executeCommand('workbench.action.revertAndCloseActiveEditor');
    // Reads must navigate even when no saved file or capture sequence changes.
    await fs.writeFile(path.join(root, READING), 'a\nb\nc\nd\ne\nf\n');
    value.activity = { session: value.session, id: 'read-1', agent: 'codex', path: READING,
      line: 2, endLine: 4, updatedAt: new Date().toISOString() };
    await writeState();
    await waitFor(() => visible(READING)?.selection.active.line === 1, 'Read did not follow file/range');
    await vscode.commands.executeCommand('stvena.toggleFollow');
    value.activity = { ...value.activity, id: 'read-2', line: 5, endLine: 6, updatedAt: new Date().toISOString() };
    await writeState();
    await new Promise(resolve => setTimeout(resolve, 1800));
    assert.equal(visible(READING).selection.active.line, 1, 'Paused read moved cursor');
    await vscode.commands.executeCommand('stvena.toggleFollow');
    await waitFor(() => visible(READING)?.selection.active.line === 4, 'Resume did not follow read');
    delete value.activity;
    await writeState();
    await new Promise(resolve => setTimeout(resolve, 1800));
    assert.equal(visible(READING).selection.active.line, 4, 'Read expiry navigated to an old edit');
    noDiffs();
    // TUI review follows the ordinary source file and editor selections travel
    // back through the local request descriptor, never through a native diff.
    await writeReview({ tree: '1'.repeat(40), source: 'session', path: READING, line: 3, endLine: 4 });
    await waitFor(() => visible(READING)?.selection.active.line === 2, 'TUI review focus did not follow source');
    const reviewEditor = visible(READING);
    reviewEditor.selection = new vscode.Selection(2, 0, 3, 1);
    await vscode.commands.executeCommand('stvena.addSelectionToContext');
    await waitFor(() => fs.access(repo.requestPath).then(() => true, () => false), 'Context request was not written');
    let request = JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
    assert.equal(request.action, 'context');
    assert.equal(request.path, READING);
    assert.equal(request.line, 3);
    assert.equal(request.endLine, 4);
    const contextID = request.id;
    await vscode.commands.executeCommand('stvena.reviewInStvena');
    await waitFor(async () => JSON.parse(await fs.readFile(repo.requestPath, 'utf8')).id !== contextID,
      'Review request did not replace the context request');
    request = JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
    assert.equal(request.action, 'review');
    reviewEditor.selection = new vscode.Selection(2, 0, 2, 0);
    noDiffs();

    // Accept and reject live above the change block, in the ordinary file.
    await writeReview(undefined, reviewFile());
    await waitFor(async () => (await lensTitles(READING)).includes('✓ Accept'),
      'Accept/Reject actions did not appear above the change block');
    assert.deepEqual(await lensTitles(READING), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
    noDiffs();

    // A selection offers Drag+b above its first line, beside the review actions.
    const beforePaste = (await readRequest()).id;
    visible(READING).selection = new vscode.Selection(1, 2, 2, 0);
    await waitFor(async () => (await lensTitles(READING)).includes('⤴ Paste to agent'),
      'Paste to agent did not appear above the selection');
    const selectionLens = (await vscode.commands.executeCommand('vscode.executeCodeLensProvider',
      vscode.Uri.file(path.join(root, READING)))).find(lens => lens.command?.title === '⤴ Paste to agent');
    assert.equal(selectionLens.range.start.line, 1);
    await vscode.commands.executeCommand(selectionLens.command.command, ...(selectionLens.command.arguments || []));
    await waitFor(async () => (await readRequest()).id !== beforePaste, 'Paste request was not written');
    request = await readRequest();
    assert.deepEqual([request.action, request.path, request.line, request.endLine], ['paste', READING, 2, 2]);
    visible(READING).selection = new vscode.Selection(1, 0, 1, 0);
    await waitFor(async () => (await lensTitles(READING)).length === 3, 'Paste to agent outlived the selection');

    // A decision must show at once, because Stvena is polled rather than pushed.
    const beforeAccept = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.acceptHunk',
      { root: repo.root, path: READING, hunkId: HUNK, start: 3, end: 4 });
    assert.deepEqual(await lensTitles(READING), ['✓ Accepted …', 'Undo'],
      'Accepting did not update the editor before Stvena confirmed it');
    await waitFor(async () => (await readRequest()).id !== beforeAccept, 'Accept request was not written');
    request = await readRequest();
    assert.equal(request.action, 'accept');
    assert.equal(request.hunkId, HUNK);
    assert.equal(request.path, READING);

    // Once Stvena agrees, the pending marker clears.
    await writeReview(undefined, reviewFile({ reviewed: true }));
    await waitFor(async () => (await lensTitles(READING)).includes('✓ Accepted'),
      'Confirmed acceptance never settled');
    assert.deepEqual(await lensTitles(READING), ['✓ Accepted', 'Undo']);

    // A rejection Stvena refuses must roll back rather than linger.
    await writeReview(undefined, reviewFile());
    await waitFor(async () => (await lensTitles(READING)).includes('✓ Accept'), 'Review state did not reset');
    const beforeReject = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.rejectHunk',
      { root: repo.root, path: READING, hunkId: HUNK, start: 3, end: 4 });
    assert.deepEqual(await lensTitles(READING), ['✗ Rejected …', 'Undo']);
    await waitFor(async () => (await readRequest()).id !== beforeReject, 'Reject request was not written');
    const refused = await readRequest();
    assert.equal(refused.action, 'reject');
    await writeReview(undefined, { ...reviewFile(), lastRequest: { id: refused.id, action: 'reject',
      status: 'refused', message: 'that change block has changed since your editor drew it',
      at: new Date().toISOString() } });
    await waitFor(async () => (await lensTitles(READING)).includes('✓ Accept'),
      'A refused rejection stayed on screen');

    // A whole-file rejection offers an explicit, token-bound Undo action.
    const wholeFile = reviewFile({ rejected: true });
    wholeFile.files[0].rejected = true;
    await writeReview(undefined, wholeFile);
    await waitFor(async () => (await lensTitles(READING)).includes('Undo file rejection'),
      'Whole-file Undo did not describe its scope');
    const beforeUndoFile = (await readRequest()).id;
    const fileLenses = await vscode.commands.executeCommand('vscode.executeCodeLensProvider',
      vscode.Uri.file(path.join(root, READING)));
    const undoFile = fileLenses.find(lens => lens.command?.title === 'Undo file rejection').command;
    await vscode.commands.executeCommand(undoFile.command, ...undoFile.arguments);
    await waitFor(async () => (await readRequest()).id !== beforeUndoFile, 'Whole-file Undo was not sent');
    assert.equal((await readRequest()).action, 'undo-reject');
    assert.equal((await readRequest()).hunkId, HUNK);

    const addition = reviewFile();
    addition.files[0].status = 'A';
    await writeReview(undefined, addition);
    await waitFor(async () => (await lensTitles(READING)).includes('✗ Reject file'),
      'Added-file rejection did not describe its scope');

    // A queued rejection can be applied from the editor.
    await writeReview(undefined, { ...reviewFile({ rejected: true }),
      pendingRejections: { count: 1, appliesAt: 'turn-end', reason: 'claude 1 is running' } });
    await waitFor(async () => (await lensTitles(READING)).includes('✗ Rejected'),
      'Queued rejection was not shown');
    const beforeApply = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.applyRejections');
    await waitFor(async () => (await readRequest()).id !== beforeApply, 'Apply request was not written');
    assert.equal((await readRequest()).action, 'apply-rejections');
    noDiffs();

    // Actions must disappear while a buffer no longer matches the capture.
    const dirtyDoc = await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(root, READING)));
    const dirtyEditor = await vscode.window.showTextDocument(dirtyDoc);
    await dirtyEditor.edit(edit => edit.insert(new vscode.Position(0, 0), 'unsaved '));
    assert.deepEqual(await lensTitles(READING), [], 'Actions stayed on an unsaved buffer');
    await vscode.commands.executeCommand('workbench.action.revertAndCloseActiveEditor');

    // An older Stvena advertises no features; the new actions must refuse.
    await writeReview(undefined, { tree: '1'.repeat(40),
      files: [{ path: READING, status: 'M', hunks: [{ id: HUNK, start: 3, end: 4 }] }] });
    await waitFor(async () => (await lensTitles(READING)).includes('✓ Accept'), 'Lens did not return');
    const beforeSkew = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.acceptHunk',
      { root: repo.root, path: READING, hunkId: HUNK, start: 3, end: 4 });
    await new Promise(resolve => setTimeout(resolve, 1500));
    assert.equal((await readRequest()).id, beforeSkew, 'Wrote an action an older Stvena cannot read');
    assert.deepEqual(await lensTitles(READING), ['✓ Accept', '✗ Reject', 'Reject with reason…'],
      'A refused send was not rolled back');
    noDiffs();
  } finally {
    await fs.rm(repo.statePath, { force: true });
    await fs.rm(repo.reviewPath, { force: true });
    await fs.rm(repo.requestPath, { force: true });
    await fs.rm(repo.presencePath, { force: true });
  }
}

// gitPass reviews the disposable workspace as an ordinary repository.
async function gitPass(root) {
  execFileSync('git', ['-C', root, 'init', '-q'], { encoding: 'utf8' });
  const repo = await discover(root);
  assert.equal(repo.mode, 'git', 'Git pass did not resolve through Git');
  try {
    await scenario(repo, root, 'editor-host-git');
  } finally {
    await fs.rm(path.join(root, '.git'), { recursive: true, force: true });
  }
}

// shadowPass reviews the same workspace with no repository in it at all, the
// way Stvena does for a folder that was never initialized: the descriptors live
// outside the project and the extension finds them through the bridge registry.
async function shadowPass(root) {
  const home = process.env.STVENA_HOME;
  assert.ok(home, 'Set STVENA_HOME on the editor launch command so the fixture never writes into a real home');
  const dir = path.join(home, 'descriptors');
  await fs.mkdir(dir, { recursive: true });
  await fs.mkdir(path.join(home, 'bridges'), { recursive: true });
  const real = await fs.realpath(root);
  const entry = { version: 1, root: real, realRoot: real, mode: 'shadow', dir,
    gitDir: path.join(dir, 'shadow.git'), updatedAt: new Date().toISOString() };
  const file = path.join(home, 'bridges', 'host.json');
  await fs.writeFile(file, JSON.stringify(entry));
  try {
    const repo = await discover(root);
    assert.equal(repo && repo.mode, 'shadow', 'Shadow pass did not resolve through the bridge registry');
    // The extension rediscovers every five seconds. Wait for it to announce
    // itself in the private directory rather than for a fixed delay: that
    // proves it resolved this project through the registry before the suite
    // starts publishing state it is supposed to react to.
    const deadline = Date.now() + 20000;
    for (;;) {
      try {
        await fs.stat(repo.presencePath);
        break;
      } catch {
        if (Date.now() > deadline) throw new Error(`extension never found the project through the registry (${repo.presencePath})`);
        await new Promise(resolve => setTimeout(resolve, 200));
      }
    }
    // Let the window settle after the previous pass before publishing state
    // the extension is expected to react to.
    await vscode.commands.executeCommand('workbench.action.closeAllEditors');
    await new Promise(resolve => setTimeout(resolve, 1500));
    await scenario(repo, root, 'editor-host-shadow');
  } finally {
    await fs.rm(file, { force: true });
    await fs.rm(dir, { recursive: true, force: true });
  }
}

async function run() {
  const root = await fs.realpath(vscode.workspace.workspaceFolders[0].uri.fsPath);
  // Avoid destructive fixture writes if someone launches the test in their repo.
  assert.equal(path.basename(root), 'stvena-editor-project');
  const only = process.env.STVENA_HOST_MODE;
  const passes = [];
  if (only !== 'shadow') passes.push(['git mode', gitPass]);
  if (only !== 'git') passes.push(['shadow mode', shadowPass]);
  for (const [name, pass] of passes) {
    try {
      await pass(root);
    } catch (error) {
      // Name the pass: a shadow-only failure otherwise reads exactly like the
      // known Git-mode flakes, and gets triaged as one.
      error.message = `${name}: ${error.message}`;
      throw error;
    }
  }
  console.log('STVENA_HOST_TESTS_PASSED: edits, reads, TUI review, editor requests, accept/reject lens, ' +
    'optimistic rollback, apply rejections, version skew, ranges, pause/resume, expiry, unsaved buffer, no diffs · ' +
    passes.map(([name]) => name).join(' · '));
}

module.exports = { run };
