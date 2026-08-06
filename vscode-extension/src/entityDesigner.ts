import * as path from 'path';
import * as vscode from 'vscode';
import type {LanguageClient} from 'vscode-languageclient/node';
import type {
  EntityApplyResponse,
  EntityBootstrap,
  EntityDecision,
  EntityPreview,
  EntityRelationTarget,
  EntitySpec,
} from './entityDesignerTypes';

let activePanel: vscode.WebviewPanel | undefined;

export async function openEntityDesigner(
  extensionContext: vscode.ExtensionContext,
  client: LanguageClient,
  directoryUri: vscode.Uri,
): Promise<void> {
  if (activePanel) {
    activePanel.reveal(vscode.ViewColumn.Active);
    activePanel.webview.postMessage({type: 'directory', directoryUri: directoryUri.toString()});
    return;
  }
  const panel = vscode.window.createWebviewPanel(
    'shopwareEntityDesigner',
    'Shopware Entity Designer',
    vscode.ViewColumn.Active,
    {enableScripts: true, retainContextWhenHidden: true, localResourceRoots: [vscode.Uri.joinPath(extensionContext.extensionUri, 'dist')]},
  );
  activePanel = panel;
  panel.onDidDispose(() => { activePanel = undefined; });
  panel.webview.html = designerHTML(panel.webview, extensionContext.extensionUri);

  let bootstrap: EntityBootstrap | undefined;
  let preview: EntityPreview | undefined;
  let lastRequest: {spec: EntitySpec; decisions: EntityDecision[]; driftDecision?: string} | undefined;

  const loadBootstrap = async (uri: string): Promise<void> => {
    bootstrap = await client.sendRequest<EntityBootstrap>('shopware/entity-schema/bootstrap', {directoryUri: uri});
    panel.webview.postMessage({type: 'bootstrap', value: bootstrap});
  };

  panel.webview.onDidReceiveMessage(async message => {
    try {
      switch (message.type) {
        case 'ready':
          await loadBootstrap(directoryUri.toString());
          break;
        case 'directory':
          await loadBootstrap(String(message.directoryUri));
          break;
        case 'preview': {
          lastRequest = {
            spec: message.spec as EntitySpec,
            decisions: (message.decisions ?? []) as EntityDecision[],
            driftDecision: message.driftDecision as string | undefined,
          };
          preview = await client.sendRequest<EntityPreview>('shopware/entity-schema/preview', {
            ...lastRequest,
            documents: openEntityDocuments(lastRequest.spec),
          });
          if (preview.migrationTimestamp && lastRequest) {
            lastRequest.spec.migrationTimestamp = preview.migrationTimestamp;
          }
          panel.webview.postMessage({type: 'preview', requestId: message.requestId, value: preview});
          break;
        }
        case 'apply': {
          if (!preview || !lastRequest) {
            throw new Error('Preview the entity before applying it');
          }
          const result = await client.sendRequest<EntityApplyResponse>('shopware/entity-schema/apply', {
            ...lastRequest,
            documents: openEntityDocuments(lastRequest.spec),
            revision: preview.revision,
            allowDestructive: Boolean(message.allowDestructive),
          });
          const edit = await client.protocol2CodeConverter.asWorkspaceEdit(result.edit);
          if (!await vscode.workspace.applyEdit(edit)) {
            throw new Error('VS Code rejected the entity workspace edit');
          }
          const document = await vscode.workspace.openTextDocument(vscode.Uri.parse(result.primaryFileUri));
          await vscode.window.showTextDocument(document, {preview: false});
          vscode.window.showInformationMessage(`Applied entity schema ${result.snapshotId.slice(0, 12)}`);
          panel.webview.postMessage({type: 'applied', snapshotId: result.snapshotId});
          break;
        }
        case 'search': {
          const results = await client.sendRequest<EntityRelationTarget[]>('shopware/entity-schema/search', {query: String(message.query ?? ''), limit: 100});
          panel.webview.postMessage({type: 'search', requestId: message.requestId, value: results});
          break;
        }
        case 'load': {
          const fileUri = message.fileUri ? String(message.fileUri) : undefined;
          const spec = await client.sendRequest<EntitySpec>('shopware/entity-schema/load', {
            definitionClass: String(message.definitionClass),
            fileUri,
            documents: openDocuments(fileUri ? [fileUri] : []),
          });
          panel.webview.postMessage({type: 'loaded', value: spec});
          break;
        }
        case 'reconcile': {
          const result = await client.sendRequest<EntityApplyResponse>('shopware/entity-schema/reconcile', {
            directoryUri: bootstrap?.spec.directoryUri,
            selectedLeaf: message.selectedLeaf,
          });
          const edit = await client.protocol2CodeConverter.asWorkspaceEdit(result.edit);
          if (!await vscode.workspace.applyEdit(edit)) {
            throw new Error('VS Code rejected the reconciliation snapshot');
          }
          await loadBootstrap(bootstrap!.spec.directoryUri);
          break;
        }
      }
    } catch (error) {
      const detail = error instanceof Error ? error.message : String(error);
      panel.webview.postMessage({type: 'error', operation: message.type, requestId: message.requestId, message: detail});
      vscode.window.showErrorMessage(`Entity Designer: ${detail}`);
    }
  });
}

function openEntityDocuments(spec: EntitySpec): Record<string, {text: string; version: number}> {
  const relevant = new Set([
    spec.definitionUri,
    spec.entityUri,
    spec.collectionUri,
    spec.serviceUri,
  ].filter((value): value is string => Boolean(value)));
  const result = openDocuments(relevant);
  for (const document of vscode.workspace.textDocuments) {
    if (document.uri.scheme === 'file' && document.isDirty && document.uri.fsPath.startsWith(vscode.Uri.parse(spec.pluginRootUri).fsPath + path.sep)) {
      result[document.uri.toString()] = {text: document.getText(), version: document.version};
    }
  }
  return result;
}

function openDocuments(uris: Iterable<string>): Record<string, {text: string; version: number}> {
  const result: Record<string, {text: string; version: number}> = {};
  const relevant = new Set(uris);
  for (const document of vscode.workspace.textDocuments) {
    if (document.uri.scheme === 'file' && relevant.has(document.uri.toString())) {
      result[document.uri.toString()] = {text: document.getText(), version: document.version};
    }
  }
  return result;
}

function designerHTML(webview: vscode.Webview, extensionUri: vscode.Uri): string {
  const nonce = Math.random().toString(36).slice(2);
  const script = webview.asWebviewUri(vscode.Uri.joinPath(extensionUri, 'dist', 'entityDesignerWebview.js'));
  return `<!doctype html>
<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src ${webview.cspSource} 'unsafe-inline'; script-src 'nonce-${nonce}';">
<style>
body{font-family:var(--vscode-font-family);color:var(--vscode-foreground);background:var(--vscode-editor-background);padding:18px;max-width:1500px;margin:auto}
button,input,select{font:inherit;color:inherit;background:var(--vscode-input-background);border:1px solid var(--vscode-input-border,transparent);border-radius:2px;padding:6px 8px}
button{background:var(--vscode-button-background);color:var(--vscode-button-foreground);cursor:pointer}
button.secondary{background:var(--vscode-button-secondaryBackground);color:var(--vscode-button-secondaryForeground)}
button:hover:not(:disabled){filter:brightness(1.08)}button:disabled{opacity:.55;cursor:default}
button:focus-visible,input:focus-visible,select:focus-visible{outline:1px solid var(--vscode-focusBorder);outline-offset:1px}
.grid{display:grid;grid-template-columns:repeat(3,minmax(180px,1fr));gap:10px}.grid>label{display:grid;gap:4px}.grid>label>input,.grid>label>select{box-sizing:border-box;width:100%}
.card{border:1px solid var(--vscode-panel-border);padding:14px;margin:12px 0}.toolbar,.row{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.field-list{overflow-x:auto}.field{display:grid;grid-template-columns:32px minmax(140px,170px) minmax(150px,1fr) minmax(260px,1.4fr) 70px 70px 54px 138px;gap:10px;align-items:center;padding:8px 6px;border-bottom:1px solid var(--vscode-panel-border);border-left:3px solid transparent}
.field-header{padding-top:2px;padding-bottom:6px}.field-header span:nth-child(n+5):nth-child(-n+7){font-size:12px;text-align:center}.field.locked{opacity:.75}.field.selected{border-left-color:var(--vscode-focusBorder);background:var(--vscode-list-activeSelectionBackground)}.field.invalid{border-left-color:var(--vscode-errorForeground)}
.field>input:not([type=checkbox]),.field>select{min-width:0;width:100%;box-sizing:border-box}.field>input[type=checkbox]{width:16px;height:16px;margin:0;justify-self:center}
.field-storage{display:grid;grid-template-columns:repeat(auto-fit,minmax(130px,1fr));gap:8px;min-width:0}.field-storage>input,.field-storage>button{box-sizing:border-box;min-width:0;width:100%}.field-storage>button{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.field-actions{display:flex;gap:6px;justify-content:flex-end}.field-actions button{box-sizing:border-box;width:30px;min-width:30px;padding:6px 0}.field.invalid .field-actions button:first-child{color:var(--vscode-errorForeground);font-weight:700}.field-note{grid-column:3/-1;padding:4px 0}
.inspector{margin-top:14px;padding:12px;border:1px solid var(--vscode-panel-border);border-radius:4px;background:var(--vscode-sideBar-background)}.inspector h3{margin:0}.inspector-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(210px,1fr));gap:12px 14px;margin-top:10px}.inspector-grid>label,.inspector-grid>.detail-control{display:grid;gap:4px;min-width:0}.inspector-grid>label>span,.inspector-grid>.detail-control>span{font-size:12px;color:var(--vscode-descriptionForeground)}.inspector-grid input:not([type=checkbox]),.inspector-grid select,.inspector-grid button{box-sizing:border-box;min-width:0;width:100%}.inspector-grid .compact{display:flex;align-items:center;gap:7px;min-height:32px;align-self:end}.inspector-grid .compact input{flex:0 0 auto;width:16px;height:16px;margin:0}.inspector-grid .compact span{font-size:inherit}.inspector-grid .span-two{grid-column:span 2}
.index-row{display:grid;grid-template-columns:minmax(180px,1fr) 110px minmax(260px,2fr) auto;gap:10px;align-items:start;padding:9px 0;border-bottom:1px solid var(--vscode-panel-border)}.index-row>input,.index-row>select{box-sizing:border-box;width:100%}.column-picker{display:flex;gap:7px 14px;align-items:center;flex-wrap:wrap;min-height:30px}.column-picker label{white-space:nowrap}
.issue{margin:6px 0}.issue button.link{border:0;padding:0;background:transparent;color:var(--vscode-textLink-foreground);text-decoration:underline}.success{border-left:3px solid var(--vscode-testing-iconPassed);padding-left:10px}.muted{color:var(--vscode-descriptionForeground)}.error{color:var(--vscode-errorForeground)}.warning{color:var(--vscode-editorWarning-foreground)}
.change-summary{margin-top:12px;padding:10px 12px;background:var(--vscode-textCodeBlock-background)}.change-summary h3{margin:0 0 6px}.change-summary ul{margin:0;padding-left:22px}.change-summary li{margin:3px 0}.change-summary .added::marker{color:var(--vscode-gitDecoration-addedResourceForeground)}.change-summary .removed::marker{color:var(--vscode-gitDecoration-deletedResourceForeground)}.change-summary .changed::marker{color:var(--vscode-gitDecoration-modifiedResourceForeground)}
pre{max-height:420px;overflow:auto;padding:10px;background:var(--vscode-textCodeBlock-background);white-space:pre-wrap}.badge{border-radius:10px;padding:2px 7px;background:var(--vscode-badge-background);color:var(--vscode-badge-foreground)}
dialog{color:inherit;background:var(--vscode-editor-background);border:1px solid var(--vscode-panel-border);width:min(800px,90vw)}dialog::backdrop{background:rgba(0,0,0,.35)}.relation-result{display:block;width:100%;text-align:left;margin:5px 0}
#status{position:sticky;top:0;z-index:2;background:var(--vscode-editor-background);padding:6px 0}.danger{border-left:3px solid var(--vscode-errorForeground);padding-left:10px}
@media(max-width:900px){.grid{grid-template-columns:1fr}.field{min-width:990px}.index-row{grid-template-columns:1fr 100px}.index-row .column-picker{grid-column:1/-1}.inspector-grid .span-two{grid-column:auto}}
</style></head><body><div id="app">Loading entity designer…</div><script nonce="${nonce}" src="${script}"></script></body></html>`;
}
