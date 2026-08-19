import type {LanguageClient} from 'vscode-languageclient/node';
import type * as vscode from 'vscode';

export interface WorkspaceClientEntry {
  key: string;
  folder: vscode.WorkspaceFolder;
  client: LanguageClient;
  dispose(): Promise<void>;
}

export interface ClientState {
  clientForUri(uri: vscode.Uri): LanguageClient | undefined;
  entryForUri(uri: vscode.Uri): WorkspaceClientEntry | undefined;
  resolveClient(resource?: vscode.Uri, title?: string): Promise<LanguageClient | undefined>;
  resolveEntry(resource?: vscode.Uri, title?: string): Promise<WorkspaceClientEntry | undefined>;
  resolveWorkspaceFolder(
    resource?: vscode.Uri,
    runningOnly?: boolean,
    title?: string,
  ): Promise<vscode.WorkspaceFolder | undefined>;
  runningEntries(): WorkspaceClientEntry[];
}
