import * as vscode from 'vscode';
import type {ClientState} from '../clientState';
import {type ClearReport, describeClearReport} from '../cacheReport';

type Scope = 'workspace' | 'stale' | 'all';

/**
 * "Clear Index Cache…": rebuilds the index of this workspace, or deletes the
 * caches of other projects. The server does the work; its own cache is open,
 * so that one is rebuilt in place instead of deleted.
 */
export function registerCacheCommands(context: vscode.ExtensionContext, manager: ClientState): void {
  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.clearCache', async () => {
    const entry = await manager.resolveEntry(undefined, 'Clear Shopware Index Cache');
    if (!entry) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }
    const picked = await vscode.window.showQuickPick<vscode.QuickPickItem & {scope: Scope}>([
      {
        scope: 'workspace',
        label: 'This workspace',
        description: `Rebuild the index of ${entry.folder.name} from scratch`,
      },
      {
        scope: 'stale',
        label: 'Stale caches of other projects',
        description: 'Projects that no longer exist, and outdated caches unused for a week',
      },
      {
        scope: 'all',
        label: 'All caches of other projects',
        description: 'They are rebuilt the next time each project is opened',
      },
    ], {title: 'Clear Shopware Index Cache', placeHolder: 'Which cache should be cleared?'});
    if (!picked) return;
    try {
      if (picked.scope === 'workspace') {
        await entry.client.sendRequest('shopware/forceReindex');
        vscode.window.showInformationMessage(`Shopware LSP: Rebuilding the index of ${entry.folder.name}`);
        return;
      }
      const report = await entry.client.sendRequest<ClearReport>('shopware/cache/clear', {scope: picked.scope});
      vscode.window.showInformationMessage(describeClearReport(report));
    } catch (error) {
      vscode.window.showErrorMessage(`Failed to clear the Shopware index cache: ${error}`);
    }
  }));
}
