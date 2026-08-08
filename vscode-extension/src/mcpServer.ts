import * as vscode from 'vscode';
import {createMcpProcessDefinition} from './mcpServerModel';
import {resolveServerExecutable} from './serverExecutable';
import {readEditorConfiguration} from './configuration';

export const shopwareMcpProviderId = 'shopwareLSP.mcp';

export function registerMcpServerDefinitionProvider(
  context: vscode.ExtensionContext,
  outputChannel: vscode.OutputChannel,
): void {
  const changed = new vscode.EventEmitter<void>();
  context.subscriptions.push(changed);

  const provider: vscode.McpServerDefinitionProvider<vscode.McpStdioServerDefinition> = {
    onDidChangeMcpServerDefinitions: changed.event,
    provideMcpServerDefinitions: token => {
      const folders = vscode.workspace.workspaceFolders ?? [];
      const multiRoot = folders.length > 1;
      const definitions: vscode.McpStdioServerDefinition[] = [];

      for (const folder of folders) {
        if (token.isCancellationRequested) {
          break;
        }
        const configuration = vscode.workspace.getConfiguration('shopwareLSP', folder.uri);
        if (!configuration.get<boolean>('mcp.enabled', true)) {
          continue;
        }
        const serverPath = resolveServerExecutable({
          configuredPath: configuration.get<string>('serverPath', ''),
          extensionPath: context.extensionPath,
          workspaceRoot: folder.uri.fsPath,
        });
        if (!serverPath) {
          outputChannel.appendLine(
            `Skipping Shopware MCP for ${folder.uri.fsPath}: language-server executable not found`,
          );
          continue;
        }

        const process = createMcpProcessDefinition({
          serverPath,
          workspaceRoot: folder.uri.fsPath,
          label: multiRoot ? `Shopware LSP (${folder.name})` : 'Shopware LSP',
          version: String(context.extension.packageJSON.version ?? 'dev'),
          memoryLimitMiB: configuration.get<number>('memoryLimitMiB', 0),
          editorConfiguration: readEditorConfiguration(folder.uri),
        });
        const definition = new vscode.McpStdioServerDefinition(
          process.label,
          process.command,
          process.args,
          process.env,
          process.version,
        );
        definition.cwd = folder.uri;
        definitions.push(definition);
      }
      return definitions;
    },
  };

  context.subscriptions.push(
    vscode.lm.registerMcpServerDefinitionProvider(shopwareMcpProviderId, provider),
    vscode.workspace.onDidChangeWorkspaceFolders(() => changed.fire()),
    vscode.workspace.onDidChangeConfiguration(event => {
      if (
        event.affectsConfiguration('shopwareLSP.mcp.enabled') ||
        event.affectsConfiguration('shopwareLSP.serverPath') ||
        event.affectsConfiguration('shopwareLSP.memoryLimitMiB') ||
        event.affectsConfiguration('shopwareLSP.phpExtensions') ||
        event.affectsConfiguration('shopwareLSP.disabledPhpExtensions') ||
        event.affectsConfiguration('shopwareLSP.shopwareTargetVersion') ||
        event.affectsConfiguration('shopwareLSP.features') ||
        event.affectsConfiguration('shopwareLSP.indexing.enabled') ||
        event.affectsConfiguration('shopwareLSP.domains') ||
        event.affectsConfiguration('shopwareLSP.diagnostics')
      ) {
        changed.fire();
      }
    }),
  );
}
