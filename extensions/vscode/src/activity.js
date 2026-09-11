'use strict';

const activityLifetimeMs = 15000;
function fresh(time, now = Date.now()) {
  const age = now - Date.parse(time);
  return age >= 0 && age < activityLifetimeMs;
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
function reviewRow(repo) {
  const focus = repo.review?.focus;
  if (!focus) return undefined;
  return { repo, kind: 'review', at: repo.review.changedAt || repo.review.updatedAt,
    id: String(repo.review.sequence), source: focus.source,
    file: { path: focus.path, line: focus.line, ranges: [{ start: focus.line, end: focus.endLine }] } };
}
function label(row) {
  if (row.kind === 'read') {
    const range = row.file.ranges[0];
    return `${row.agent || 'Agent'} · Read ${range ? `lines ${range.start}–${range.end}` : 'file (range unavailable)'}`;
  }
  if (row.kind === 'review') {
    const range = row.file.ranges[0];
    return `Review in Stvena · ${row.source} · ${range.start === range.end ? `line ${range.start}` : `lines ${range.start}–${range.end}`}`;
  }
  return 'Changed · latest captured edit';
}

// Decorations are separate from editor selection, so activity remains visible
// with keyboard focus in the terminal. A persistent review range can coexist
// with the one transient read/edit location currently being followed.
function createMarkers(vscode, context) {
  const types = {};
  for (const [kind, color] of [['read', 'editorInfo.foreground'], ['edit', 'editorWarning.foreground'], ['review', 'charts.purple']]) {
    types[kind] = vscode.window.createTextEditorDecorationType({
      isWholeLine: true, rangeBehavior: vscode.DecorationRangeBehavior.ClosedClosed,
      backgroundColor: new vscode.ThemeColor(`stvena.${kind}Background`),
      borderWidth: '0 0 0 2px', borderStyle: 'solid', borderColor: new vscode.ThemeColor(color),
      overviewRulerColor: new vscode.ThemeColor(color), overviewRulerLane: vscode.OverviewRulerLane.Right,
      gutterIconPath: vscode.Uri.file(context.asAbsolutePath(`media/${kind}.svg`)), gutterIconSize: 'contain',
    });
    context.subscriptions.push(types[kind]);
  }
  let current, reviews = [];
  function paint() {
    for (const editor of vscode.window.visibleTextEditors) {
      for (const type of Object.values(types)) editor.setDecorations(type, []);
      if (editor.document.isDirty) continue;
      for (const entry of [...reviews, current].filter(Boolean)) {
        if (editor.document.uri.toString() !== entry.uri ||
            (entry.row.kind !== 'review' && !fresh(entry.row.at))) continue;
        const row = entry.row;
        // An unknown read range gets a marker at its reported starting line only.
        const ranges = row.file.ranges?.length ? row.file.ranges : [{ start: row.file.line, end: row.file.line }];
        const options = ranges.map((range, index) => {
          const start = Math.min(range.start - 1, editor.document.lineCount - 1);
          const end = Math.min(range.end - 1, editor.document.lineCount - 1);
          return { range: new vscode.Range(start, 0, end, editor.document.lineAt(end).text.length),
            hoverMessage: label(row),
            ...(index === 0 ? { renderOptions: { after: { contentText: `  ${label(row)}`, margin: '0 0 0 1em',
              color: new vscode.ThemeColor(row.kind === 'read' ? 'editorInfo.foreground' :
                row.kind === 'review' ? 'charts.purple' : 'editorWarning.foreground') } } } : {}) };
        });
        editor.setDecorations(types[row.kind || 'edit'], options);
      }
    }
  }
  return {
    show(row, uri) {
      const entry = { row, uri: uri.toString() };
      if (row.kind === 'review') {
        reviews = reviews.filter(existing => row.repo?.root ? existing.row.repo?.root !== row.repo.root : existing.uri !== entry.uri);
        reviews.push(entry);
      }
      else current = entry;
      paint();
    },
    setReviews(entries) { reviews = entries.map(entry => ({ row: entry.row, uri: entry.uri.toString() })); paint(); },
    refresh(isLive) {
      if (current && !isLive(current.row)) current = undefined;
      reviews = reviews.filter(entry => isLive(entry.row));
      paint();
    },
    clear() { current = undefined; reviews = []; paint(); },
  };
}
module.exports = { fresh, readRow, editRows, reviewRow, label, createMarkers };
