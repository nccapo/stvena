'use strict';

// A selection changes on every step of a drag. The actions wait until it has
// stopped, so they do not appear and push lines down mid-drag.
const settleMs = 400;

const actions = [
  { title: '⤴ Paste to agent', command: 'stvena.pasteSelection', tooltip: 'Paste the selected lines into the agent\'s input. You send them.' },
  { title: 'Ask…', command: 'stvena.askAgent', tooltip: 'Paste the selected lines with a question' },
  { title: 'Add to context', command: 'stvena.addSelectionToContext', tooltip: 'Collect the selected lines in Stvena\'s context tray' },
];

// createSelectionActions is the editor's Drag+b: a settled selection in a file
// Stvena reviews gets "⤴ Paste to agent · Ask… · Add to context" above its
// first line, drawn like the Accept and Reject actions. locate(editor) returns
// the request target, or undefined when the selection cannot be sent. The
// commands read the editor's selection when clicked, so the lens only says
// where the actions are, not what they send.
function createSelectionActions(vscode, context, locate) {
  const changed = new vscode.EventEmitter();
  let timer, settling = false, shown, available = false; // shown: { document, line }

  function enabled() {
    return vscode.workspace.getConfiguration('stvena').get('showSelectionCodeLens', true);
  }

  function setAvailable(value) {
    if (value === available) return;
    available = value;
    void vscode.commands.executeCommand('setContext', 'stvena.canPasteSelection', value);
  }

  function show(next) {
    if (shown?.document === next?.document && shown?.line === next?.line) return;
    shown = next;
    changed.fire();
  }

  function draw(editor) {
    const target = editor && !editor.selection.isEmpty ? locate(editor) : undefined;
    setAvailable(!!target);
    show(target && enabled() ? { document: editor.document, line: target.line - 1 } : undefined);
  }

  // update follows the active selection. A lens that is already up stays put
  // while the selection moves, because removing and re-adding it would shift
  // the text under the mouse; it moves once the selection settles.
  function update(immediate = false) {
    clearTimeout(timer);
    settling = false;
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.selection.isEmpty) {
      setAvailable(false);
      show(undefined);
      return;
    }
    if (immediate) {
      draw(editor);
      return;
    }
    settling = true;
    timer = setTimeout(() => {
      settling = false;
      draw(vscode.window.activeTextEditor);
    }, settleMs);
  }

  const provider = {
    onDidChangeCodeLenses: changed.event,
    provideCodeLenses(document) {
      if (!shown || shown.document !== document || shown.line >= document.lineCount) return [];
      const range = new vscode.Range(shown.line, 0, shown.line, 0);
      return actions.map(({ title, command, tooltip }) =>
        new vscode.CodeLens(range, { title, command, tooltip }));
    },
  };

  context.subscriptions.push(changed,
    vscode.languages.registerCodeLensProvider({ scheme: 'file' }, provider),
    vscode.window.onDidChangeTextEditorSelection(() => update()),
    vscode.window.onDidChangeActiveTextEditor(() => update(true)),
    vscode.workspace.onDidChangeTextDocument(event => {
      // An unsaved buffer no longer matches the capture, so it cannot be sent.
      if (event.document === vscode.window.activeTextEditor?.document) update(true);
    }),
    vscode.workspace.onDidSaveTextDocument(() => update(true)),
    vscode.workspace.onDidChangeConfiguration(event => {
      if (event.affectsConfiguration('stvena.showSelectionCodeLens')) update(true);
    }),
    { dispose() { clearTimeout(timer); } });

  return {
    // refresh re-checks the selection after review state changed, which can
    // make a file sendable (Stvena started) or not (it stopped).
    refresh() {
      if (settling) return;
      const editor = vscode.window.activeTextEditor;
      if (editor && !editor.selection.isEmpty && !!locate(editor) !== available) draw(editor);
    },
  };
}

module.exports = { createSelectionActions, settleMs };
