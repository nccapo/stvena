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
      if (predicate()) return;
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
    console.log('STVENA_HOST_TESTS_PASSED: edits, reads, ranges, pause/resume, expiry, unsaved buffer, no diffs');
  } finally {
    await fs.rm(repo.statePath, { force: true });
  }
}

module.exports = { run };
