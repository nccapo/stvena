'use strict';

const vscode = require('vscode');
const path = require('node:path');
const bridge = require('./bridge');
const activity = require('./activity');
const reviewUI = require('./review');

function activate(context) {
  if (!vscode.workspace.isTrusted) return;
  let repos = [], rows = [], latest, following = true, disposed = false, busy = false;
  let discoveryAt = 0, generation = 0, displayed;
  const seen = new Map();
  const errors = new Map();
  const markers = activity.createMarkers(vscode, context);
  const changes = new vscode.EventEmitter();
  const output = vscode.window.createOutputChannel('Stvena Live');
  const tree = vscode.window.createTreeView('stvena.changes', { treeDataProvider: {
    onDidChangeTreeData: changes.event,
    getChildren: () => rows,
    getTreeItem: row => {
      const item = new vscode.TreeItem(row.file.path);
      item.description = row.kind === 'edit' ? `${row.file.status} +${row.file.added} −${row.file.deleted} · ${path.basename(row.repo.root)}` : activity.label(row);
      item.tooltip = `${row.file.oldPath ? row.file.oldPath + ' → ' : ''}${row.file.path}\nLine ${row.file.line} · ${activity.label(row)}`;
      item.iconPath = new vscode.ThemeIcon(row.kind === 'read' ? 'eye' : row.kind === 'review' ? 'inspect' : row.file.binary ? 'file-binary' : 'file-code');
      item.command = { command: 'stvena.openChange', title: 'Open edit', arguments: [row] };
      return item;
    },
  } });
  const decisions = reviewUI.createReviewUI(vscode, context, (target, request) => {
    const repo = repos.find(candidate => candidate.root === target.root);
    if (!repo) throw new Error('No active Stvena review owns this file.');
    return bridge.writeRequest(repo, request);
  });
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 10);
  status.command = 'stvena.toggleFollow';
  status.show();

  function report(key, error) {
    const message = error.message || String(error);
    if (errors.get(key) !== message) output.appendLine(`${key}: ${message}`);
    errors.set(key, message);
  }

  function updateStatus() {
    const liveEdits = repos.filter(repo => bridge.isLive(repo.state));
    const liveReviews = repos.filter(repo => bridge.isLive(repo.review));
    const live = new Set([...liveEdits, ...liveReviews]);
    const unreviewed = liveReviews.reduce((sum, repo) => sum + repo.review.unreviewedFiles, 0);
    const failed = liveEdits.some(repo => repo.state.error) || errors.size > 0;
    const queued = decisions.pending(repos);
    const suffix = (unreviewed ? ` · ${unreviewed} to review` : '') +
      (queued ? ` · ${queued.count} rejected${queued.appliesAt === 'now' ? '' : ' pending'}` : '');
    status.text = `$(pulse) Stvena: ${!following ? 'Paused' : failed ? 'Waiting for capture' : live.size ? 'Following' : 'Waiting'}${suffix}`;
    status.command = queued && queued.appliesAt !== 'now' ? 'stvena.applyRejections' : 'stvena.toggleFollow';
    const latestVisible = latest && rows.some(row => row.repo.root === latest.repo.root && row.kind === latest.kind &&
      row.file.path === latest.file.path && row.id === latest.id && row.at === latest.at) &&
      (latest.kind === 'review' || activity.fresh(latest.at));
    status.tooltip = `${latestVisible ? `${activity.label(latest)} · ${latest.file.path}\n` : ''}` +
      `${queued && queued.reason ? `${queued.reason}\nClick to apply rejections now.\n` : 'Click to pause/resume following.\n'}` +
      'Run stvena in the project terminal. Open Stvena Live output for connection errors.';
    tree.message = failed ? 'Capture unavailable. See Stvena Live output.' :
      !live.size ? 'Waiting for stvena in this workspace.' :
      !following ? 'Following paused. Select any captured edit to inspect it.' :
      latestVisible ? `${activity.label(latest)} · ${latest.file.path}` :
      'Following Stvena review, reads, and edits · waiting for activity';
  }

  async function show(row, automatic = false) {
    if (!row || disposed) return;
    if (row.kind !== 'review' && (row.file.binary || row.file.truncated)) {
      if (!automatic) await vscode.window.showInformationMessage('This captured edit has no complete text preview (binary or size limit).');
      return;
    }
    if (row.kind !== 'review' && /^0+$/.test(row.file.after)) {
      if (!automatic) await vscode.window.showInformationMessage('This file was deleted; there is no working file to follow.');
      return;
    }
    const ticket = ++generation;
    try {
      const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(path.join(row.repo.root, row.file.path)));
      if (disposed || ticket !== generation || (automatic && !following)) return;
      // An unsaved buffer may no longer match the captured line. Leave the user's
      // edits and cursor alone until they save or explicitly choose this file.
      if (automatic && doc.isDirty) return;
      const line = Math.min(row.file.line - 1, doc.lineCount - 1);
      const selection = new vscode.Range(line, 0, line, 0);
      const editor = await vscode.window.showTextDocument(doc, {
        preview: true, preserveFocus: automatic, selection,
      });
      if (disposed || ticket !== generation || (automatic && !following)) return;
      editor.revealRange(selection, vscode.TextEditorRevealType.InCenterIfOutsideViewport);
      displayed = row;
      markers.show(row, doc.uri);
      errors.delete('preview');
    } catch (error) {
      report('preview', error);
      if (!automatic) await vscode.window.showErrorMessage(`Stvena: ${error.message}`);
    }
  }

  function activeEditorTarget() {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.document.uri.scheme !== 'file') throw new Error('Open a repository file first.');
    if (editor.document.isDirty) throw new Error('Save the file before sending its captured range to Stvena.');
    const repo = repos.find(candidate => {
      const relative = path.relative(candidate.root, editor.document.uri.fsPath);
      return relative && relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative) && bridge.isLive(candidate.review);
    });
    if (!repo) throw new Error('No active Stvena review owns this file.');
    const relative = path.relative(repo.root, editor.document.uri.fsPath).split(path.sep).join('/');
    const selection = editor.selection;
    const line = selection.start.line + 1;
    let endLine = selection.end.line + 1;
    if (!selection.isEmpty && selection.end.character === 0 && endLine > line) endLine--;
    return { repo, path: relative, line, endLine: Math.max(line, endLine) };
  }

  async function fileDecision(action) {
    try {
      const target = activeEditorTarget();
      await decisions.decide(action, { root: target.repo.root, path: target.path, start: 1, end: 1 });
    } catch (error) {
      void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
    }
  }

  async function sendEditorRequest(action) {
    try {
      const target = activeEditorTarget();
      await bridge.writeRequest(target.repo, { action, path: target.path, line: target.line, endLine: target.endLine });
      void vscode.window.showInformationMessage(action === 'review' ?
        `Sent ${target.path}:${target.line} to Stvena review.` :
        `Sent ${target.path}:${target.line}–${target.endLine} to Stvena context.`);
    } catch (error) {
      void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
    }
  }

  async function poll() {
    if (busy || disposed) return;
    busy = true;
    try {
      if (Date.now() >= discoveryAt) {
        discoveryAt = Date.now() + 5000;
        const found = new Map();
        for (const folder of vscode.workspace.workspaceFolders || []) {
          if (folder.uri.scheme !== 'file') continue;
          try {
            const repo = await bridge.discover(folder.uri.fsPath);
            found.set(repo.root, repos.find(old => old.root === repo.root) || repo);
            errors.delete(folder.name);
          } catch (error) {
            // Non-Git folders are ordinary workspace members.
            if (!String(error.stderr).includes('not a git repository')) report(folder.name, error);
            else errors.delete(folder.name);
          }
        }
        repos = [...found.values()];
      }
      let follow;
      for (const repo of repos) {
        const reviewErrorKey = `${repo.root} review`;
        try {
          try {
          // Tell Stvena an extension is connected, so it can offer IDE mode.
          await bridge.announce(repo, vscode.env.appName || 'VS Code',
            vscode.extensions.getExtension('nccapo.stvena-live')?.packageJSON?.version || '');
          errors.delete(`${repo.root} presence`);
        } catch (error) {
          report(`${repo.root} presence`, error);
        }
        repo.state = await bridge.readState(repo);
          if (!repo.state?.error) errors.delete(repo.root);
        } catch (error) {
          repo.state = undefined;
          report(repo.root, error);
        }
        try {
          repo.review = await bridge.readReviewState(repo);
          errors.delete(reviewErrorKey);
        } catch (error) {
          repo.review = undefined;
          report(reviewErrorKey, error);
        }
        const liveState = bridge.isLive(repo.state) && !repo.state.error;
        const liveReview = bridge.isLive(repo.review);
        if (repo.state?.error) report(repo.root, new Error(repo.state.error));
        const read = liveState ? activity.readRow(repo) : undefined;
        const key = liveState ? `${repo.state.session}:${repo.state.sequence}` : undefined;
        const readID = liveState ? repo.state.activity?.id : undefined;
        const review = liveReview ? activity.reviewRow(repo) : undefined;
        const reviewKey = liveReview ? `${repo.review.session}:${repo.review.sequence}` : undefined;
        const previous = seen.get(repo.root);
        let candidate;
        if (liveState && (previous?.edit !== key || (read && previous?.read !== readID))) {
          const files = activity.editRows(repo);
          const followable = files.filter(row => !row.file.binary && !row.file.truncated && !/^0+$/.test(row.file.after));
          const edit = followable.find(row =>
            displayed?.repo.root === repo.root && displayed?.file.path === row.file.path) || followable[0];
          candidate = previous?.edit !== key ? edit : undefined;
          if (read && previous?.read !== readID && (!candidate || Date.parse(read.at) >= (Date.parse(candidate.at) || 0))) candidate = read;
        }
        if (review && previous?.review !== reviewKey && (!candidate || Date.parse(review.at) >= (Date.parse(candidate.at) || 0))) candidate = review;
        if (candidate && (!follow || Date.parse(candidate.at) >= (Date.parse(follow.at) || 0))) follow = candidate;
        seen.set(repo.root, { edit: key, read: readID, review: reviewKey });
      }
      if (disposed) return;
      rows = repos.flatMap(repo => [
        ...(bridge.isLive(repo.review) ? [activity.reviewRow(repo)] : []),
        ...(bridge.isLive(repo.state) && !repo.state.error ? [activity.readRow(repo), ...activity.editRows(repo)] : []),
      ].filter(Boolean));
      if (follow) latest = follow;
      // Read expiry must clear the marker without jumping back to an old edit.
      if (follow && follow.kind !== 'review' && !activity.fresh(follow.at) && follow.at) follow = undefined;
      markers.setReviews(following ? rows.filter(row => row.kind === 'review').map(row => ({ row,
        uri: vscode.Uri.file(path.join(row.repo.root, row.file.path)) })) : []);
      markers.refresh(row => rows.some(current => current.repo.root === row.repo.root &&
        current.kind === row.kind && current.file.path === row.file.path && current.at === row.at && current.id === row.id));
      decisions.update(repos);
      changes.fire();
      updateStatus();
      if (following && follow) await show(follow, true);
    } finally {
      busy = false;
    }
  }

  const timer = setInterval(() => void poll().catch(error => report('connection', error)), 700);
  context.subscriptions.push(changes, output, tree, status,
    vscode.window.onDidChangeVisibleTextEditors(() => markers.refresh(() => true)),
    vscode.workspace.onDidChangeTextDocument(() => markers.refresh(() => true)),
    vscode.workspace.onDidChangeWorkspaceFolders(() => { discoveryAt = 0; }),
    vscode.commands.registerCommand('stvena.openChange', row => show(row)),
    vscode.commands.registerCommand('stvena.acceptHunk', target => decisions.decide('accept', target)),
    vscode.commands.registerCommand('stvena.unacceptHunk', target => decisions.decide('unaccept', target)),
    vscode.commands.registerCommand('stvena.rejectHunk', target => decisions.decide('reject', target)),
    vscode.commands.registerCommand('stvena.undoRejectHunk', target => decisions.decide('undo-reject', target)),
    vscode.commands.registerCommand('stvena.rejectHunkWithReason', async target => {
      const reason = await vscode.window.showInputBox({
        title: 'Reject this change',
        prompt: 'Why? This is sent to the agent so it does not write the same thing again.',
        placeHolder: 'e.g. this breaks the existing session contract',
      });
      // An empty box still rejects; only Escape cancels.
      if (reason === undefined) return;
      await decisions.decide('reject', target, reason);
    }),
    vscode.commands.registerCommand('stvena.askAgent', async () => {
      let target;
      try {
        target = activeEditorTarget();
      } catch (error) {
        void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
        return;
      }
      const range = target.line === target.endLine ? `line ${target.line}` : `lines ${target.line}–${target.endLine}`;
      const question = await vscode.window.showInputBox({
        title: `Ask about ${path.basename(target.path)} ${range}`,
        prompt: 'Your question is placed in the agent\'s input with the selected code. You press Enter to send it.',
        placeHolder: 'e.g. why does this need a lock?',
      });
      if (!question || !question.trim()) return;
      try {
        await bridge.writeRequest(target.repo, { action: 'prompt', path: target.path,
          line: target.line, endLine: target.endLine, text: question });
        void vscode.window.showInformationMessage('Stvena: question ready in the agent · press Enter there to send it.');
      } catch (error) {
        void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
      }
    }),
    vscode.commands.registerCommand('stvena.acceptFile', () => fileDecision('accept')),
    vscode.commands.registerCommand('stvena.rejectFile', () => fileDecision('reject')),
    vscode.commands.registerCommand('stvena.applyRejections', async () => {
      const owner = repos.find(repo => repo.review?.pendingRejections?.count);
      if (!owner) {
        void vscode.window.showInformationMessage('Stvena: no rejections are waiting to be applied.');
        return;
      }
      try {
        await bridge.writeRequest(owner, { action: 'apply-rejections' });
        void vscode.window.showInformationMessage('Stvena: applying rejected changes.');
      } catch (error) {
        void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
      }
    }),
    vscode.commands.registerCommand('stvena.reviewInStvena', () => sendEditorRequest('review')),
    vscode.commands.registerCommand('stvena.addSelectionToContext', () => sendEditorRequest('context')),
    vscode.commands.registerCommand('stvena.showLatest', async () => {
      if (latest && rows.some(row => row.repo.root === latest.repo.root &&
        row.kind === latest.kind && row.id === latest.id && row.at === latest.at &&
        row.file.path === latest.file.path && (row.kind === 'review' || row.file.after === latest.file.after))) await show(latest);
      else if (rows.length) await show(rows[0]);
      else await vscode.window.showInformationMessage('Run stvena in this workspace and wait for a read or captured edit.');
    }),
    vscode.commands.registerCommand('stvena.toggleFollow', () => {
      following = !following;
      generation++;
      if (!following) markers.clear();
      updateStatus();
      if (following && rows.length) return show(rows.find(row =>
        row.repo.root === latest?.repo.root && row.kind === latest?.kind && row.id === latest?.id &&
        row.file.path === latest?.file.path) || rows[0], true);
    }),
    { dispose() { disposed = true; generation++; clearInterval(timer); decisions.clear(); } });
  void poll().catch(error => report('connection', error));
}

module.exports = { activate };
