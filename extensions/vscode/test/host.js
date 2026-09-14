'use strict';

// Run only in an isolated extension development host with a disposable workspace.
const vscode = require('vscode');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const { discover } = require('../src/bridge');

async function run() {
  const root = await fs.realpath(vscode.workspace.workspaceFolders[0].uri.fsPath);
  // Avoid destructive fixture writes if someone launches the test in their repo.
  assert.equal(path.basename(root), 'stvena-editor-project');
  const git = (...args) => execFileSync('git', ['-C', root, ...args], { encoding: 'utf8' }).trim();
  git('init', '-q');
  const repo = await discover(root);
  const blob = text => execFileSync('git', ['-C', root, 'hash-object', '-w', '--stdin'],
    { input: text, encoding: 'utf8' }).trim();
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
    'apply-rejections', 'next-unreviewed'];
  const HUNK = 'a'.repeat(64);
  // reviewFile publishes per-hunk state the way Stvena does for a changed file.
  const reviewFile = (hunk = {}) => ({ features: FEATURES, tree: '1'.repeat(40),
    files: [{ path: 'reading.txt', status: 'M', hunks: [{ id: HUNK, start: 3, end: 4, ...hunk }] }] });
  const lensTitles = async file => (await vscode.commands.executeCommand('vscode.executeCodeLensProvider',
    vscode.Uri.file(path.join(root, file)))).map(lens => lens.command && lens.command.title).filter(Boolean);
  const readRequest = async () => JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
  async function publish(before, after, file = 'example.txt') {
    const target = path.join(root, file);
    if (/^0+$/.test(after)) await fs.rm(target, { force: true });
    else await fs.writeFile(target, git('cat-file', 'blob', after) + '\n');
    value = { version: 1, session: 'editor-host-test', sequence: ++sequence,
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
    throw new Error(message);
  }
  const visible = file => vscode.window.visibleTextEditors.find(editor =>
    editor.document.uri.scheme === 'file' && editor.document.uri.fsPath === path.join(root, file));
  const noDiffs = () => assert.ok(!vscode.window.tabGroups.all.flatMap(group => group.tabs)
    .some(tab => tab.input instanceof vscode.TabInputTextDiff), 'Following opened a diff tab');
  await vscode.commands.executeCommand('workbench.action.closeAllEditors');
  await vscode.extensions.getExtension('nccapo.stvena-live').activate();
  try {
    await publish(before, after);
    await waitFor(() => visible('example.txt'), 'Automatic source navigation did not open');
    assert.equal(visible('example.txt').document.getText(), 'one\ntwo\nthree\nnew\n');
    assert.equal(visible('example.txt').selection.active.line, 3);
    noDiffs();

    await vscode.commands.executeCommand('stvena.toggleFollow');
    const next = blob('one\ntwo\nthree\nnewer\n');
    await publish(after, next, 'next.txt');
    await new Promise(resolve => setTimeout(resolve, 1800));
    assert.ok(visible('example.txt'), 'Pause navigated away from the current file');
    assert.equal(visible('next.txt'), undefined);
    await vscode.commands.executeCommand('stvena.toggleFollow');
    await waitFor(() => visible('next.txt'), 'Resume did not show the newest file');
    assert.equal(visible('next.txt').selection.active.line, 3);

    const empty = '0'.repeat(40);
    await publish(empty, next, 'added.txt');
    await waitFor(() => visible('added.txt'), 'Addition did not open the working file');
    await publish(next, empty, 'added.txt');
    await new Promise(resolve => setTimeout(resolve, 1800));
    noDiffs();

    // Automatic following must not replace an unsaved buffer or move its cursor.
    await vscode.commands.executeCommand('stvena.toggleFollow');
    const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(root, 'example.txt')));
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
    await fs.writeFile(path.join(root, 'reading.txt'), 'a\nb\nc\nd\ne\nf\n');
    value.activity = { session: value.session, id: 'read-1', agent: 'codex', path: 'reading.txt',
      line: 2, endLine: 4, updatedAt: new Date().toISOString() };
    await writeState();
    await waitFor(() => visible('reading.txt')?.selection.active.line === 1, 'Read did not follow file/range');
    await vscode.commands.executeCommand('stvena.toggleFollow');
    value.activity = { ...value.activity, id: 'read-2', line: 5, endLine: 6, updatedAt: new Date().toISOString() };
    await writeState();
    await new Promise(resolve => setTimeout(resolve, 1800));
    assert.equal(visible('reading.txt').selection.active.line, 1, 'Paused read moved cursor');
    await vscode.commands.executeCommand('stvena.toggleFollow');
    await waitFor(() => visible('reading.txt')?.selection.active.line === 4, 'Resume did not follow read');
    delete value.activity;
    await writeState();
    await new Promise(resolve => setTimeout(resolve, 1800));
    assert.equal(visible('reading.txt').selection.active.line, 4, 'Read expiry navigated to an old edit');
    noDiffs();
    // TUI review follows the ordinary source file and editor selections travel
    // back through the local request descriptor, never through a native diff.
    await writeReview({ tree: '1'.repeat(40), source: 'session', path: 'reading.txt', line: 3, endLine: 4 });
    await waitFor(() => visible('reading.txt')?.selection.active.line === 2, 'TUI review focus did not follow source');
    const reviewEditor = visible('reading.txt');
    reviewEditor.selection = new vscode.Selection(2, 0, 3, 1);
    await vscode.commands.executeCommand('stvena.addSelectionToContext');
    await waitFor(() => fs.access(repo.requestPath).then(() => true, () => false), 'Context request was not written');
    let request = JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
    assert.equal(request.action, 'context');
    assert.equal(request.path, 'reading.txt');
    assert.equal(request.line, 3);
    assert.equal(request.endLine, 4);
    const contextID = request.id;
    await vscode.commands.executeCommand('stvena.reviewInStvena');
    await waitFor(async () => JSON.parse(await fs.readFile(repo.requestPath, 'utf8')).id !== contextID,
      'Review request did not replace the context request');
    request = JSON.parse(await fs.readFile(repo.requestPath, 'utf8'));
    assert.equal(request.action, 'review');
    noDiffs();

    // Accept and reject live above the change block, in the ordinary file.
    await writeReview(undefined, reviewFile());
    await waitFor(async () => (await lensTitles('reading.txt')).includes('✓ Accept'),
      'Accept/Reject actions did not appear above the change block');
    assert.deepEqual(await lensTitles('reading.txt'), ['✓ Accept', '✗ Reject', 'Reject with reason…']);
    noDiffs();

    // A decision must show at once, because Stvena is polled rather than pushed.
    const beforeAccept = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.acceptHunk',
      { root: repo.root, path: 'reading.txt', hunkId: HUNK, start: 3, end: 4 });
    assert.deepEqual(await lensTitles('reading.txt'), ['✓ Accepted …', 'Undo'],
      'Accepting did not update the editor before Stvena confirmed it');
    await waitFor(async () => (await readRequest()).id !== beforeAccept, 'Accept request was not written');
    request = await readRequest();
    assert.equal(request.action, 'accept');
    assert.equal(request.hunkId, HUNK);
    assert.equal(request.path, 'reading.txt');

    // Once Stvena agrees, the pending marker clears.
    await writeReview(undefined, reviewFile({ reviewed: true }));
    await waitFor(async () => (await lensTitles('reading.txt')).includes('✓ Accepted'),
      'Confirmed acceptance never settled');
    assert.deepEqual(await lensTitles('reading.txt'), ['✓ Accepted', 'Undo']);

    // A rejection Stvena refuses must roll back rather than linger.
    await writeReview(undefined, reviewFile());
    await waitFor(async () => (await lensTitles('reading.txt')).includes('✓ Accept'), 'Review state did not reset');
    const beforeReject = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.rejectHunk',
      { root: repo.root, path: 'reading.txt', hunkId: HUNK, start: 3, end: 4 });
    assert.deepEqual(await lensTitles('reading.txt'), ['✗ Rejected …', 'Undo']);
    await waitFor(async () => (await readRequest()).id !== beforeReject, 'Reject request was not written');
    const refused = await readRequest();
    assert.equal(refused.action, 'reject');
    await writeReview(undefined, { ...reviewFile(), lastRequest: { id: refused.id, action: 'reject',
      status: 'refused', message: 'that change block has changed since your editor drew it',
      at: new Date().toISOString() } });
    await waitFor(async () => (await lensTitles('reading.txt')).includes('✓ Accept'),
      'A refused rejection stayed on screen');

    // A queued rejection can be applied from the editor.
    await writeReview(undefined, { ...reviewFile({ rejected: true }),
      pendingRejections: { count: 1, appliesAt: 'turn-end', reason: 'claude 1 is running' } });
    await waitFor(async () => (await lensTitles('reading.txt')).includes('✗ Rejected'),
      'Queued rejection was not shown');
    const beforeApply = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.applyRejections');
    await waitFor(async () => (await readRequest()).id !== beforeApply, 'Apply request was not written');
    assert.equal((await readRequest()).action, 'apply-rejections');
    noDiffs();

    // Actions must disappear while a buffer no longer matches the capture.
    const dirtyDoc = await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(root, 'reading.txt')));
    const dirtyEditor = await vscode.window.showTextDocument(dirtyDoc);
    await dirtyEditor.edit(edit => edit.insert(new vscode.Position(0, 0), 'unsaved '));
    assert.deepEqual(await lensTitles('reading.txt'), [], 'Actions stayed on an unsaved buffer');
    await vscode.commands.executeCommand('workbench.action.revertAndCloseActiveEditor');

    // An older Stvena advertises no features; the new actions must refuse.
    await writeReview(undefined, { tree: '1'.repeat(40),
      files: [{ path: 'reading.txt', status: 'M', hunks: [{ id: HUNK, start: 3, end: 4 }] }] });
    await waitFor(async () => (await lensTitles('reading.txt')).includes('✓ Accept'), 'Lens did not return');
    const beforeSkew = (await readRequest()).id;
    await vscode.commands.executeCommand('stvena.acceptHunk',
      { root: repo.root, path: 'reading.txt', hunkId: HUNK, start: 3, end: 4 });
    await new Promise(resolve => setTimeout(resolve, 1500));
    assert.equal((await readRequest()).id, beforeSkew, 'Wrote an action an older Stvena cannot read');
    assert.deepEqual(await lensTitles('reading.txt'), ['✓ Accept', '✗ Reject', 'Reject with reason…'],
      'A refused send was not rolled back');
    noDiffs();
    console.log('STVENA_HOST_TESTS_PASSED: edits, reads, TUI review, editor requests, accept/reject lens, optimistic rollback, apply rejections, version skew, ranges, pause/resume, expiry, unsaved buffer, no diffs');
  } finally {
    await fs.rm(repo.statePath, { force: true });
    await fs.rm(repo.reviewPath, { force: true });
    await fs.rm(repo.requestPath, { force: true });
  }
}

module.exports = { run };
