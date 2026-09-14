import * as vscode from 'vscode';
import {createMcpProcessDefinition} from './mcpServerModel';
import {resolveServerExecutable} from './serverExecutable';
import {projectConfigurationPath, readEditorConfiguration} from './configuration';
import {
  decideActivation,
  normalizeActivationMode,
  ProjectDetector,
} from './projectDetection';
import {selectOutermostWorkspaceRoots} from './workspaceRoots';

export const shopwareMcpProviderId = 'shopwareLSP.mcp';

export interface McpProviderRegistration {
  refresh(): void;
}

export function registerMcpServerDefinitionProvider(
  context: vscode.ExtensionContext,
  outputChannel: vscode.OutputChannel,
  projectDetector: ProjectDetector,
): McpProviderRegistration {
  const changed = new vscode.EventEmitter<void>();
  const projectConfigurationWatcher = vscode.workspace.createFileSystemWatcher(
    `**/${projectConfigurationPath}`,
  );
  projectConfigurationWatcher.onDidCreate(() => changed.fire());
  projectConfigurationWatcher.onDidChange(() => changed.fire());
  projectConfigurationWatcher.onDidDelete(() => changed.fire());
  context.subscriptions.push(changed, projectConfigurationWatcher);

  const provider: vscode.McpServerDefinitionProvider<vscode.McpStdioServerDefinition> = {
    onDidChangeMcpServerDefinitions: changed.event,
    provideMcpServerDefinitions: async token => {
      const folders = vscode.workspace.workspaceFolders ?? [];
      const candidates = await Promise.all(folders.map(async folder => {
        if (token.isCancellationRequested) {
          return undefined;
        }
        const configuration = vscode.workspace.getConfiguration('shopwareLSP', folder.uri);
        if (!configuration.get<boolean>('mcp.enabled', true)) {
          return undefined;
        }
        const activationMode = normalizeActivationMode(
          configuration.get<string>('activationMode', 'auto'),
        );
        if (activationMode === 'never') {
          return undefined;
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
          return undefined;
        }
        let decision = decideActivation(activationMode);
        if (activationMode === 'auto') {
          try {
            decision = decideActivation(
              activationMode,
              await projectDetector.detect(serverPath, folder.uri.fsPath),
            );
          } catch (error) {
            outputChannel.appendLine(
              `Skipping Shopware MCP for ${folder.uri.fsPath}: project detection failed: ${error}`,
            );
            return undefined;
          }
        }
        if (!decision.enabled || token.isCancellationRequested) {
          return undefined;
        }

        return {
          key: folder.uri.toString(),
          fsPath: folder.uri.fsPath,
          enabled: true,
          folder,
          serverPath,
          configuration,
          decision,
        };
      }));
      const selected = selectOutermostWorkspaceRoots(candidates.filter(
        (candidate): candidate is NonNullable<typeof candidate> => candidate !== undefined,
      ));
      return selected.map(candidate => {
        const process = createMcpProcessDefinition({
          serverPath: candidate.serverPath,
          workspaceRoot: candidate.folder.uri.fsPath,
          label: selected.length > 1
            ? `Shopware LSP (${candidate.folder.name})`
            : 'Shopware LSP',
          version: String(context.extension.packageJSON.version ?? 'dev'),
          memoryLimitMiB: candidate.configuration.get<number>('memoryLimitMiB', 0),
          editorConfiguration: readEditorConfiguration(candidate.folder.uri),
          allowUnsupportedProject: candidate.decision.allowUnsupportedProject,
        });
        const definition = new vscode.McpStdioServerDefinition(
          process.label,
          process.command,
          process.args,
          process.env,
          process.version,
        );
        definition.cwd = candidate.folder.uri;
        return definition;
      });
    },
  };

  context.subscriptions.push(
    vscode.lm.registerMcpServerDefinitionProvider(shopwareMcpProviderId, provider),
    vscode.workspace.onDidChangeWorkspaceFolders(() => changed.fire()),
    vscode.workspace.onDidChangeConfiguration(event => {
      if (
        event.affectsConfiguration('shopwareLSP.mcp.enabled') ||
        event.affectsConfiguration('shopwareLSP.activationMode') ||
        event.affectsConfiguration('shopwareLSP.serverPath') ||
        event.affectsConfiguration('shopwareLSP.memoryLimitMiB') ||
        event.affectsConfiguration('shopwareLSP.phpExtensions') ||
        event.affectsConfiguration('shopwareLSP.disabledPhpExtensions') ||
        event.affectsConfiguration('shopwareLSP.shopwareTargetVersion') ||
        event.affectsConfiguration('shopwareLSP.features') ||
        event.affectsConfiguration('shopwareLSP.mcp.tools') ||
        event.affectsConfiguration('shopwareLSP.indexing.enabled') ||
        event.affectsConfiguration('shopwareLSP.indexing.exclude') ||
        event.affectsConfiguration('shopwareLSP.indexing.maxFileSizeMiB') ||
        event.affectsConfiguration('shopwareLSP.domains') ||
        event.affectsConfiguration('shopwareLSP.diagnostics')
      ) {
        changed.fire();
      }
    }),
  );
  return {refresh: () => changed.fire()};
}
