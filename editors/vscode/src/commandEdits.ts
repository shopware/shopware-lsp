import type * as vscode from 'vscode';
import type {LanguageClient, WorkspaceEdit} from 'vscode-languageclient/node';

type EditClient = Pick<LanguageClient, 'protocol2CodeConverter'>;
type EditWorkspace = Pick<typeof vscode.workspace, 'applyEdit' | 'textDocuments'>;

export async function applyCommandEdit(
  client: EditClient,
  workspace: EditWorkspace,
  result: {edit?: WorkspaceEdit} | null,
): Promise<void> {
  // Older custom servers apply these commands themselves and return no edit.
  if (!result?.edit) return;
  const edit = await client.protocol2CodeConverter.asWorkspaceEdit(result.edit);
  // Conversion can yield to editor changes. Check versions immediately before
  // applying: VS Code's WorkspaceEdit does not retain LSP document versions.
  for (const change of result.edit.documentChanges ?? []) {
    if (!('textDocument' in change) || change.textDocument.version === null) continue;
    const current = workspace.textDocuments.find(document => document.uri.toString() === change.textDocument.uri);
    if (!current || current.version !== change.textDocument.version) {
      throw new Error('The target document changed; run the command again');
    }
  }
  if (!await workspace.applyEdit(edit)) throw new Error('The workspace edit could not be applied');
}
