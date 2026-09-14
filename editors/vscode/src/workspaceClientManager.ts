import * as vscode from 'vscode';
import type {ClientState, WorkspaceClientEntry} from './clientState';
import {
  selectOutermostWorkspaceRoots,
  workspaceRootForPath,
  type WorkspaceRootCandidate,
} from './workspaceRoots';

export interface WorkspaceClientPlan extends WorkspaceRootCandidate {
  folder: vscode.WorkspaceFolder;
  start(): Promise<WorkspaceClientEntry | undefined>;
}

type PlanResolver = (folder: vscode.WorkspaceFolder) => Promise<WorkspaceClientPlan | undefined>;

export class WorkspaceClientManager implements ClientState, vscode.Disposable {
  private readonly entries = new Map<string, WorkspaceClientEntry>();
  private generation = 0;
  private mutationQueue: Promise<void> = Promise.resolve();
  private disposed = false;

  constructor(
    private readonly resolvePlan: PlanResolver,
    private readonly reportError: (message: string, error: unknown) => void,
  ) {}

  runningEntries(): WorkspaceClientEntry[] {
    return [...this.entries.values()].sort((left, right) =>
      left.folder.name.localeCompare(right.folder.name) || left.key.localeCompare(right.key));
  }

  entryForUri(uri: vscode.Uri): WorkspaceClientEntry | undefined {
    if (uri.scheme !== 'file') return undefined;
    return workspaceRootForPath(
      this.runningEntries().map(entry => ({
        entry,
        key: entry.key,
        fsPath: entry.folder.uri.fsPath,
        enabled: true,
      })),
      uri.fsPath,
    )?.entry;
  }

  clientForUri(uri: vscode.Uri) {
    return this.entryForUri(uri)?.client;
  }

  async resolveClient(resource?: vscode.Uri, title?: string) {
    return (await this.resolveEntry(resource, title))?.client;
  }

  async resolveEntry(
    resource?: vscode.Uri,
    title = 'Select a Shopware workspace',
  ): Promise<WorkspaceClientEntry | undefined> {
    if (resource) return this.entryForUri(resource);
    const active = vscode.window.activeTextEditor?.document.uri;
    const activeEntry = active ? this.entryForUri(active) : undefined;
    if (activeEntry) return activeEntry;
    const entries = this.runningEntries();
    if (entries.length === 1) return entries[0];
    if (entries.length === 0) return undefined;
    const selected = await vscode.window.showQuickPick(
      entries.map(entry => ({
        label: entry.folder.name,
        description: entry.folder.uri.fsPath,
        entry,
      })),
      {title, placeHolder: 'Choose the workspace for this command'},
    );
    return selected?.entry;
  }

  async reconcile(forceRestart: ReadonlySet<string> = new Set()): Promise<void> {
    if (this.disposed) return;
    const generation = ++this.generation;
    const folders = vscode.workspace.workspaceFolders ?? [];
    const resolved = await Promise.all(folders.map(async folder => {
      try {
        return await this.resolvePlan(folder);
      } catch (error) {
        this.reportError(`Failed to prepare Shopware LSP for ${folder.uri.fsPath}`, error);
        return undefined;
      }
    }));
    if (generation !== this.generation || this.disposed) return;
    const plans = selectOutermostWorkspaceRoots(
      resolved.filter((plan): plan is WorkspaceClientPlan => plan !== undefined),
    );
    const desired = new Map(plans.map(plan => [plan.key, plan]));
    this.mutationQueue = this.mutationQueue.catch(() => undefined).then(async () => {
      if (generation !== this.generation || this.disposed) return;
      const stops = [...this.entries.values()].filter(entry =>
        !desired.has(entry.key) || forceRestart.has(entry.key));
      await Promise.all(stops.map(entry => this.stopEntry(entry)));
      if (generation !== this.generation || this.disposed) return;
      await Promise.all(plans.filter(plan => !this.entries.has(plan.key)).map(async plan => {
        try {
          const entry = await plan.start();
          if (!entry) return;
          if (generation !== this.generation || this.disposed) {
            await entry.dispose();
            return;
          }
          this.entries.set(entry.key, entry);
        } catch (error) {
          this.reportError(`Failed to start Shopware LSP for ${plan.folder.uri.fsPath}`, error);
        }
      }));
    });
    await this.mutationQueue;
  }

  async restart(resource?: vscode.Uri): Promise<WorkspaceClientEntry | undefined> {
    const entry = await this.resolveEntry(resource, 'Restart Shopware Language Server');
    if (!entry) return undefined;
    await this.reconcile(new Set([entry.key]));
    return this.entries.get(entry.key);
  }

  async resolveWorkspaceFolder(
    resource?: vscode.Uri,
    runningOnly = false,
    title = 'Select a workspace folder',
  ): Promise<vscode.WorkspaceFolder | undefined> {
    if (resource) {
      if (runningOnly) return this.entryForUri(resource)?.folder;
      return vscode.workspace.getWorkspaceFolder(resource);
    }
    const active = vscode.window.activeTextEditor?.document.uri;
    if (active) {
      const folder = runningOnly
        ? this.entryForUri(active)?.folder
        : vscode.workspace.getWorkspaceFolder(active);
      if (folder) return folder;
    }
    const folders = runningOnly
      ? this.runningEntries().map(entry => entry.folder)
      : [...(vscode.workspace.workspaceFolders ?? [])];
    if (folders.length === 1) return folders[0];
    if (folders.length === 0) return undefined;
    const selected = await vscode.window.showQuickPick(
      folders.map(folder => ({
        label: folder.name,
        description: folder.uri.fsPath,
        folder,
      })),
      {title, placeHolder: 'Choose a workspace folder'},
    );
    return selected?.folder;
  }

  async restartFolder(folder: vscode.WorkspaceFolder): Promise<WorkspaceClientEntry | undefined> {
    const current = this.entryForUri(folder.uri);
    await this.reconcile(new Set([current?.key ?? folder.uri.toString()]));
    return this.entryForUri(folder.uri);
  }

  async stopAll(): Promise<void> {
    this.generation++;
    await this.mutationQueue.catch(() => undefined);
    const entries = [...this.entries.values()];
    await Promise.all(entries.map(entry => this.stopEntry(entry)));
  }

  dispose(): void {
    this.disposed = true;
    void this.stopAll();
  }

  private async stopEntry(entry: WorkspaceClientEntry): Promise<void> {
    if (this.entries.get(entry.key) === entry) this.entries.delete(entry.key);
    try {
      await entry.dispose();
    } catch (error) {
      this.reportError(`Failed to stop Shopware LSP for ${entry.folder.uri.fsPath}`, error);
    }
  }
}
