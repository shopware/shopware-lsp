import * as vscode from 'vscode';
import {projectConfigurationPath} from './configuration';

export type ProjectMarkerChange = 'create' | 'change' | 'delete';

export interface ProjectMarkerEvent {
  folder: vscode.WorkspaceFolder;
  path: string;
  change: ProjectMarkerChange;
}

export const projectMarkerPaths = [
  'composer.json',
  'composer.lock',
  'manifest.xml',
  'config/bundles.php',
  projectConfigurationPath,
] as const;

export function registerProjectMarkerWatchers(
  context: vscode.ExtensionContext,
  onMarker: (event: ProjectMarkerEvent) => void,
): void {
  const watchers = new Map<string, vscode.Disposable[]>();

  const remove = (folder: vscode.WorkspaceFolder) => {
    const current = watchers.get(folder.uri.toString());
    if (!current) return;
    current.forEach(disposable => disposable.dispose());
    watchers.delete(folder.uri.toString());
  };
  const add = (folder: vscode.WorkspaceFolder) => {
    remove(folder);
    const current: vscode.Disposable[] = [];
    for (const markerPath of projectMarkerPaths) {
      const watcher = vscode.workspace.createFileSystemWatcher(
        new vscode.RelativePattern(folder, markerPath),
      );
      current.push(
        watcher,
        watcher.onDidCreate(() => onMarker({folder, path: markerPath, change: 'create'})),
        watcher.onDidChange(() => onMarker({folder, path: markerPath, change: 'change'})),
        watcher.onDidDelete(() => onMarker({folder, path: markerPath, change: 'delete'})),
      );
    }
    watchers.set(folder.uri.toString(), current);
  };

  (vscode.workspace.workspaceFolders ?? []).forEach(add);
  const workspaceListener = vscode.workspace.onDidChangeWorkspaceFolders(event => {
    event.removed.forEach(remove);
    event.added.forEach(add);
  });
  context.subscriptions.push(workspaceListener, {
    dispose: () => {
      for (const disposables of watchers.values()) {
        disposables.forEach(disposable => disposable.dispose());
      }
      watchers.clear();
    },
  });
}
