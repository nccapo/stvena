'use strict';

const vscode = require('vscode');
const path = require('node:path');
const bridge = require('./bridge');
const activity = require('./activity');
const reviewUI = require('./review');
const selectionUI = require('./selection');
const { createRequestQueue } = require('./queue');

// alt shows the Alt key the way the platform labels it, for hints that name a shortcut.
const alt = process.platform === 'darwin' ? '⌥' : 'Alt+';

function activate(context) {
  if (!vscode.workspace.isTrusted) return;
  let repos = [], rows = [], latest, following = true, disposed = false, busy = false;
  let discoveryAt = 0, generation = 0, displayed;
  // Folders the last full discovery could not resolve, and the registry state
  // they were last checked against. A project without Git only becomes
  // findable once Stvena has started and written its registry entry, which is
  // usually a moment after the editor last looked; waiting for the next full
  // discovery would miss everything the agent does in the meantime.
  let unresolved = [], unresolvedStamp;
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
  // Every request goes through one queue per project, so a decision made while
  // Stvena has not yet read the previous one cannot overwrite it.
  const sendRequest = createRequestQueue({
    write: (root, request) => {
      const repo = repos.find(candidate => candidate.root === root);
      if (!repo) throw new Error('No active Stvena review owns this file.');
      return bridge.writeRequest(repo, request);
    },
    acknowledged: (root, id) => repos.find(candidate => candidate.root === root)?.review?.lastRequest?.id === id,
  });
  const decisions = reviewUI.createReviewUI(vscode, context, (target, request) => sendRequest(target.root, request),
    (repo, oid) => bridge.readBlob(repo, oid));
  // Handoffs to the agent are pasted by Stvena after it reads the request, so
  // whether one arrived is only known from its acknowledgement.
  const handoffs = new Map(); // request id -> sent at
  const selection = selectionUI.createSelectionActions(vscode, context, editor => {
    try {
      const target = activeEditorTarget(editor);
      return bridge.supports(target.repo.review, 'paste') ? target : undefined;
    } catch {
      return undefined;
    }
  });
  const status = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 10);
  status.command = 'stvena.toggleFollow';
  status.show();
  // What is left to review, beside the connection status. Clicking it goes to
  // the next change, the same as the keyboard shortcut.
  const remainingStatus = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Left, 9);
  remainingStatus.command = 'stvena.nextChange';
  const contextKeys = new Map();

  function setContext(name, value) {
    if (contextKeys.get(name) === value) return;
    contextKeys.set(name, value);
    void vscode.commands.executeCommand('setContext', name, value);
  }

  // syncCursor tells keybindings whether the cursor sits on a change to review.
  function syncCursor() {
    const editor = vscode.window.activeTextEditor;
    setContext('stvena.changeAtCursor', !!(editor && decisions.at(editor.document, editor.selection.active.line)));
  }

  // syncReview redraws the remaining count and the keybinding context. It runs
  // after every poll and every local decision, which changes the count at once.
  function syncReview() {
    const live = repos.filter(repo => bridge.isLive(repo.review));
    let changes = 0, files = 0;
    for (const repo of live) {
      const left = decisions.remaining(repo.root);
      changes += left.changes;
      files += left.files;
    }
    const more = live.some(repo => repo.review.truncated) ? '+' : '';
    if (changes) {
      remainingStatus.text = `$(checklist) ${changes}${more} change${changes === 1 ? '' : 's'} in ${files} file${files === 1 ? '' : 's'}`;
      remainingStatus.tooltip = `Stvena: go to the next change to review (${alt}])\n` +
        `${alt}Y accepts and ${alt}N rejects the change at the cursor.`;
      remainingStatus.show();
    } else {
      remainingStatus.hide();
    }
    setContext('stvena.hasChanges', !!decisions.step(undefined, 0, 1));
    syncCursor();
  }

  // hint reports a keyboard action that did nothing, without a notification
  // that would take the keyboard away from the editor.
  function hint(message) {
    vscode.window.setStatusBarMessage(`$(info) Stvena: ${message}`, 3000);
  }

  async function goTo(target) {
    if (!target) return false;
    const doc = await vscode.workspace.openTextDocument(vscode.Uri.file(target.fsPath));
    const line = Math.max(0, Math.min(target.line - 1, doc.lineCount - 1));
    const selection = new vscode.Range(line, 0, line, 0);
    const editor = await vscode.window.showTextDocument(doc, { preview: true, selection });
    editor.revealRange(selection, vscode.TextEditorRevealType.InCenterIfOutsideViewport);
    return true;
  }

  // step moves from the cursor to the next or previous change to review,
  // crossing into other files and wrapping around.
  async function step(direction) {
    const editor = vscode.window.activeTextEditor;
    const target = decisions.step(editor?.document.uri, editor ? editor.selection.active.line : 0, direction);
    if (!target) {
      hint('nothing left to review');
      return;
    }
    try {
      await goTo(target);
    } catch (error) {
      void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
    }
  }

  // decide applies a decision and redraws the count now rather than on the
  // next poll; Stvena confirms it a moment later.
  function decide(kind, target, text) {
    const done = decisions.decide(kind, target, text);
    syncReview();
    return done.finally(syncReview);
  }

  // decideAtCursor accepts or rejects the change under the cursor, then moves
  // on to the next one so a review can be done without the mouse.
  async function decideAtCursor(kind, withReason = false) {
    const editor = vscode.window.activeTextEditor;
    const target = editor && decisions.at(editor.document, editor.selection.active.line);
    if (!target) {
      hint(editor?.document.isDirty ? 'save the file to review its changes' :
        `no change to review at the cursor · ${alt}] goes to the next one`);
      return;
    }
    // Rejecting part of a new file rejects the file, which deletes it.
    if (kind === 'reject' && target.status === 'A') {
      const choice = await vscode.window.showWarningMessage(`Reject the new file ${path.basename(target.path)}?`,
        { modal: true, detail: 'The agent created it, so rejecting it deletes the file when the agent finishes its turn.' },
        'Reject File');
      if (choice !== 'Reject File') return;
    }
    let text;
    if (withReason) {
      text = await vscode.window.showInputBox({
        title: 'Reject this change',
        prompt: 'Why? This is sent to the agent so it does not write the same thing again.',
        placeHolder: 'e.g. this breaks the existing session contract',
      });
      if (text === undefined) return;
    }
    const line = editor.selection.active.line;
    void decide(kind, target, text);
    if (vscode.workspace.getConfiguration('stvena').get('revealNextChange', true)) {
      const next = decisions.step(editor.document.uri, line, 1);
      if (next) await goTo(next).catch(error => void vscode.window.showErrorMessage(`Stvena: ${error.message}`));
      else hint('that was the last change to review');
    }
  }

  async function acceptAll() {
    const owners = repos.filter(repo => bridge.isLive(repo.review))
      .map(repo => ({ repo, ...decisions.remaining(repo.root) })).filter(owner => owner.changes);
    if (!owners.length) {
      hint('nothing left to review');
      return;
    }
    if (owners.some(owner => !bridge.supports(owner.repo.review, 'accept-all'))) {
      void vscode.window.showErrorMessage('Stvena: this Stvena version cannot accept all changes from the editor. Update the stvena binary.');
      return;
    }
    const changes = owners.reduce((sum, owner) => sum + owner.changes, 0);
    const files = owners.reduce((sum, owner) => sum + owner.files, 0);
    const choice = await vscode.window.showWarningMessage(
      `Accept ${changes} change${changes === 1 ? '' : 's'} in ${files} file${files === 1 ? '' : 's'}?`,
      { modal: true, detail: 'They stay exactly as the agent wrote them. Rejections you have queued are not affected. ' +
        'If the agent writes anything before Stvena receives this, nothing is accepted and you can review the new changes first.' },
      'Accept All');
    if (choice !== 'Accept All') return;
    for (const owner of owners) void decisions.acceptAll(owner.repo).finally(syncReview);
    syncReview();
  }

  function report(key, error) {
    const message = error.message || String(error);
    if (errors.get(key) !== message) output.appendLine(`${key}: ${message}`);
    errors.set(key, message);
  }

  function updateStatus() {
    const liveEdits = repos.filter(repo => bridge.isLive(repo.state));
    const liveReviews = repos.filter(repo => bridge.isLive(repo.review));
    const live = new Set([...liveEdits, ...liveReviews]);
    const failed = liveEdits.some(repo => repo.state.error) || errors.size > 0;
    const queued = decisions.pending(repos);
    // What is left to review has its own item; this one says how Stvena is.
    const suffix = queued ? ` · ${queued.count} rejected${queued.appliesAt === 'now' ? '' : ' pending'}` : '';
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

  function activeEditorTarget(editor = vscode.window.activeTextEditor) {
    if (!editor || editor.document.uri.scheme !== 'file') throw new Error('Open a repository file first.');
    if (editor.document.isDirty) throw new Error('Save the file before sending its captured range to Stvena.');
    const owns = (root, file) => {
      if (!root) return false;
      const relative = path.relative(root, file);
      return relative && relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
    };
    const repo = repos.find(candidate => bridge.isLive(candidate.review) &&
      (owns(candidate.root, editor.document.uri.fsPath) || owns(candidate.realRoot, editor.document.uri.fsPath)));
    if (!repo) throw new Error('No active Stvena review owns this file.');
    const relative = path.relative(repo.root, editor.document.uri.fsPath).split(path.sep).join('/');
    const selection = editor.selection;
    const line = selection.start.line + 1;
    let endLine = selection.end.line + 1;
    if (!selection.isEmpty && selection.end.character === 0 && endLine > line) endLine--;
    return { repo, path: relative, line, endLine: Math.max(line, endLine) };
  }

  // handOff writes a request that ends in the agent's input and reports what
  // Stvena did with it once the acknowledgement arrives.
  async function handOff(request) {
    const target = activeEditorTarget();
    const written = await sendRequest(target.repo.root, { ...request, path: target.path,
      line: target.line, endLine: target.endLine });
    handoffs.set(written.id, Date.now());
  }

  function reportHandoffs() {
    for (const repo of repos) {
      const last = repo.review?.lastRequest;
      if (!last || !handoffs.has(last.id)) continue;
      handoffs.delete(last.id);
      if (last.status === 'refused') void vscode.window.showWarningMessage(`Stvena: ${last.message || 'the selection was not sent.'}`);
      else void vscode.window.showInformationMessage(`Stvena: ${last.message || 'the selection is in the agent\'s input.'}`);
    }
    for (const [id, at] of handoffs) {
      if (Date.now() - at > 10000) handoffs.delete(id);
    }
  }

  async function fileDecision(action) {
    try {
      const target = activeEditorTarget();
      await decide(action, { root: target.repo.root, path: target.path, start: 1, end: 1 });
    } catch (error) {
      void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
    }
  }

  async function sendEditorRequest(action) {
    try {
      const target = activeEditorTarget();
      await sendRequest(target.repo.root, { action, path: target.path, line: target.line, endLine: target.endLine });
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
        const missing = [];
        unresolvedStamp = await bridge.registryStamp();
        for (const folder of vscode.workspace.workspaceFolders || []) {
          if (folder.uri.scheme !== 'file') continue;
          try {
            const repo = await bridge.discover(folder.uri.fsPath);
            // A folder Stvena is not reviewing is an ordinary workspace member.
            // Keeping the existing entry preserves its polled state, but only
            // while it still points at the same descriptors: a project that has
            // since gained Git writes them somewhere else.
            if (repo) found.set(repo.root, repos.find(old => old.root === repo.root && old.dir === repo.dir) || repo);
            else missing.push(folder);
            errors.delete(folder.name);
          } catch (error) {
            report(folder.name, error);
          }
        }
        repos = [...found.values()];
        unresolved = missing;
      } else if (unresolved.length) {
        // Between full discoveries, look again only when the registry changed,
        // and only through the registry: no git process runs on this path.
        const stamp = await bridge.registryStamp();
        if (stamp !== unresolvedStamp) {
          unresolvedStamp = stamp;
          const still = [];
          for (const folder of unresolved) {
            try {
              const repo = await bridge.lookup(folder.uri.fsPath);
              if (repo && !repos.some(old => old.root === repo.root && old.dir === repo.dir)) repos = [...repos, repo];
              else if (!repo) still.push(folder);
            } catch (error) {
              report(folder.name, error);
              still.push(folder);
            }
          }
          unresolved = still;
        }
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
      syncReview();
      reportHandoffs();
      selection.refresh();
      changes.fire();
      updateStatus();
      if (following && follow) await show(follow, true);
    } finally {
      busy = false;
    }
  }

  const timer = setInterval(() => void poll().catch(error => report('connection', error)), 700);
  context.subscriptions.push(changes, output, tree, status, remainingStatus,
    vscode.window.onDidChangeTextEditorSelection(() => syncCursor()),
    vscode.window.onDidChangeActiveTextEditor(() => syncCursor()),
    vscode.window.onDidChangeVisibleTextEditors(() => markers.refresh(() => true)),
    vscode.workspace.onDidChangeTextDocument(() => markers.refresh(() => true)),
    vscode.workspace.onDidChangeWorkspaceFolders(() => { discoveryAt = 0; }),
    vscode.commands.registerCommand('stvena.openChange', row => show(row)),
    vscode.commands.registerCommand('stvena.acceptHunk', target => decide('accept', target)),
    vscode.commands.registerCommand('stvena.unacceptHunk', target => decide('unaccept', target)),
    vscode.commands.registerCommand('stvena.rejectHunk', target => decide('reject', target)),
    vscode.commands.registerCommand('stvena.undoRejectHunk', target => decide('undo-reject', target)),
    vscode.commands.registerCommand('stvena.acceptChangeAtCursor', () => decideAtCursor('accept')),
    vscode.commands.registerCommand('stvena.rejectChangeAtCursor', () => decideAtCursor('reject')),
    vscode.commands.registerCommand('stvena.rejectChangeAtCursorWithReason', () => decideAtCursor('reject', true)),
    vscode.commands.registerCommand('stvena.nextChange', () => step(1)),
    vscode.commands.registerCommand('stvena.previousChange', () => step(-1)),
    vscode.commands.registerCommand('stvena.acceptAll', () => acceptAll()),
    vscode.commands.registerCommand('stvena.rejectHunkWithReason', async target => {
      const reason = await vscode.window.showInputBox({
        title: 'Reject this change',
        prompt: 'Why? This is sent to the agent so it does not write the same thing again.',
        placeHolder: 'e.g. this breaks the existing session contract',
      });
      // An empty box still rejects; only Escape cancels.
      if (reason === undefined) return;
      await decide('reject', target, reason);
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
        await handOff({ action: 'prompt', text: question });
      } catch (error) {
        void vscode.window.showErrorMessage(`Stvena: ${error.message}`);
      }
    }),
    vscode.commands.registerCommand('stvena.pasteSelection', async () => {
      try {
        await handOff({ action: 'paste' });
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
        await decisions.decide('apply-rejections', { root: owner.root });
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
