'use strict';

const lifetime = 15000;
function fresh(time, now = Date.now()) {
  const age = now - Date.parse(time);
  return age >= 0 && age < lifetime;
}
function readRow(repo) {
  const activity = repo.state?.activity;
  if (!activity || !fresh(activity.updatedAt)) return undefined;
  return { repo, kind: 'read', at: activity.updatedAt, id: activity.id, agent: activity.agent,
    file: { path: activity.path, line: activity.line,
      ranges: activity.endLine ? [{ start: activity.line, end: activity.endLine }] : [] } };
}
function editRows(repo) {
  return repo.state.files.map(file => ({ repo, file, kind: 'edit', at: repo.state.changedAt }));
}
function label(row) {
  if (row.kind === 'read') {
    const range = row.file.ranges[0];
    return `${row.agent || 'Agent'} · Read ${range ? `lines ${range.start}–${range.end}` : 'file (range unavailable)'}`;
  }
  return 'Changed · latest captured edit';
}

// Decorations are separate from editor selection, so the activity remains visible
// with keyboard focus in the terminal. Only the currently followed location is marked.
function createMarkers(vscode, context) {
  const types = {};
  for (const [kind, color] of [['read', 'editorInfo.foreground'], ['edit', 'editorWarning.foreground']]) {
    types[kind] = vscode.window.createTextEditorDecorationType({
      isWholeLine: true, rangeBehavior: vscode.DecorationRangeBehavior.ClosedClosed,
      backgroundColor: new vscode.ThemeColor(kind === 'read' ? 'stvena.readBackground' : 'stvena.editBackground'),
      borderWidth: '0 0 0 2px', borderStyle: 'solid', borderColor: new vscode.ThemeColor(color),
      overviewRulerColor: new vscode.ThemeColor(color), overviewRulerLane: vscode.OverviewRulerLane.Right,
      gutterIconPath: vscode.Uri.file(context.asAbsolutePath(`media/${kind}.svg`)), gutterIconSize: 'contain',
    });
    context.subscriptions.push(types[kind]);
  }
  let current;
  function paint() {
    for (const editor of vscode.window.visibleTextEditors) {
      for (const type of Object.values(types)) editor.setDecorations(type, []);
      if (!current || editor.document.isDirty || editor.document.uri.toString() !== current.uri || !fresh(current.row.at)) continue;
      const row = current.row;
      // An unknown read range gets a marker at its reported starting line only.
      const ranges = row.file.ranges?.length ? row.file.ranges : [{ start: row.file.line, end: row.file.line }];
      const options = ranges.map((range, index) => {
        const start = Math.min(range.start - 1, editor.document.lineCount - 1);
        const end = Math.min(range.end - 1, editor.document.lineCount - 1);
        return { range: new vscode.Range(start, 0, end, editor.document.lineAt(end).text.length),
          hoverMessage: label(row),
          ...(index === 0 ? { renderOptions: { after: { contentText: `  ${label(row)}`, margin: '0 0 0 1em',
            color: new vscode.ThemeColor(row.kind === 'read' ? 'editorInfo.foreground' : 'editorWarning.foreground') } } } : {}) };
      });
      editor.setDecorations(types[row.kind || 'edit'], options);
    }
  }
  return {
    show(row, uri) { current = { row, uri: uri.toString() }; paint(); },
    refresh(isLive) { if (current && !isLive(current.row)) current = undefined; paint(); },
    clear() { current = undefined; paint(); },
  };
}
module.exports = { fresh, readRow, editRows, label, createMarkers };
