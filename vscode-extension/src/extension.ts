import {createHash} from 'node:crypto';
import * as vscode from 'vscode';
import {
  LanguageClient,
  type LanguageClientOptions,
  RevealOutputChannelOn,
  type ServerOptions,
  TransportKind,
} from 'vscode-languageclient/node';
import {registerEditorCommands} from './commands/editorCommands';
import {registerScaffoldCommands} from './commands/scaffoldCommands';
import {registerSymfonyCatalogCommands} from './commands/symfonyCatalogCommands';
import {registerSymfonyGenerationCommands} from './commands/symfonyGenerationCommands';
import {registerTwigCatalogCommands} from './commands/twigCatalogCommands';
import {registerTwigVariableCommands} from './twigVariables';
import {registerMcpServerDefinitionProvider} from './mcpServer';
import {normalizeMemoryLimitMiB} from './mcpServerModel';
import {
  decideActivation,
  inactiveServerProject,
  normalizeActivationMode,
  ProjectDetector,
} from './projectDetection';
import {registerProjectMarkerWatchers} from './projectMarkers';
import {resolveServerExecutable} from './serverExecutable';
import {registerDiagnosticConfigurationSupport} from './diagnosticConfiguration';
import {
  attachConfigurationClient,
  projectConfigurationPath,
  readEditorConfiguration,
  registerConfigurationSupport,
} from './configuration';
import type {WorkspaceClientEntry} from './clientState';
import {IndexingStatus} from './indexingStatus';
import {
  WorkspaceClientManager,
  type WorkspaceClientPlan,
} from './workspaceClientManager';

let activeManager: WorkspaceClientManager | undefined;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  const coordinatorOutput = vscode.window.createOutputChannel('Shopware LSP');
  const statusItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  const indexingStatus = new IndexingStatus(statusItem);
  context.subscriptions.push(coordinatorOutput, statusItem, indexingStatus);
  const projectDetector = new ProjectDetector();

  let manager: WorkspaceClientManager;
  const prepareClient = async (
    folder: vscode.WorkspaceFolder,
  ): Promise<WorkspaceClientPlan | undefined> => {
    const configuration = vscode.workspace.getConfiguration('shopwareLSP', folder.uri);
    const activationMode = normalizeActivationMode(
      configuration.get<string>('activationMode', 'auto'),
    );
    if (activationMode === 'never') {
      coordinatorOutput.appendLine(
        `Shopware LSP is inactive for ${folder.uri.fsPath}: activation mode is never`,
      );
      return undefined;
    }
    const serverPath = resolveServerExecutable({
      configuredPath: configuration.get<string>('serverPath', ''),
      extensionPath: context.extensionPath,
      workspaceRoot: folder.uri.fsPath,
    });
    if (!serverPath) {
      coordinatorOutput.appendLine(
        `Shopware LSP is inactive for ${folder.uri.fsPath}: executable not found`,
      );
      return undefined;
    }
    let decision = decideActivation(activationMode);
    if (activationMode === 'auto') {
      const project = await projectDetector.detect(serverPath, folder.uri.fsPath);
      decision = decideActivation(activationMode, project);
      if (!decision.enabled) {
        coordinatorOutput.appendLine(
          `Shopware LSP is inactive for ${folder.uri.fsPath}: no Shopware or Symfony project markers were found`,
        );
        return undefined;
      }
      coordinatorOutput.appendLine(`Detected ${project.kind} project: ${folder.uri.fsPath}`);
    }
    const memoryLimit = normalizeMemoryLimitMiB(
      configuration.get<number>('memoryLimitMiB', 0),
    );
    const key = folder.uri.toString();
    return {
      key,
      fsPath: folder.uri.fsPath,
      enabled: decision.enabled,
      folder,
      start: () => startWorkspaceClient(
        folder,
        key,
        serverPath,
        decision.allowUnsupportedProject,
        memoryLimit,
      ),
    };
  };

  manager = new WorkspaceClientManager(prepareClient, (message, error) => {
    coordinatorOutput.appendLine(`${message}: ${error}`);
  });
  activeManager = manager;

  registerConfigurationSupport(context, manager, coordinatorOutput, async folder => {
    projectDetector.invalidate(folder.uri.fsPath);
    await manager.restartFolder(folder);
  });
  registerDiagnosticConfigurationSupport(context, manager, coordinatorOutput);

  async function startWorkspaceClient(
    folder: vscode.WorkspaceFolder,
    key: string,
    serverPath: string,
    allowUnsupportedProject: boolean,
    memoryLimit: number,
  ): Promise<WorkspaceClientEntry | undefined> {
    const output = vscode.window.createOutputChannel(`Shopware LSP: ${folder.name}`);
    const serverOptions: ServerOptions = {
      command: serverPath,
      args: allowUnsupportedProject
        ? ['-allow-unsupported-project', 'serve']
        : ['serve'],
      transport: TransportKind.stdio,
      ...(memoryLimit > 0 ? {
        options: {
          env: {...process.env, GOMEMLIMIT: `${memoryLimit}MiB`},
        },
      } : {}),
    };
    const workspacePattern = new vscode.RelativePattern(folder, '**/*');
    const languageFilters = [
      'php', 'xml', 'yml', 'yaml', 'twig', 'vue', 'json', 'scss',
      'javascript', 'typescript', 'dotenv', 'dockerfile',
    ].map(language => ({scheme: 'file', language, pattern: workspacePattern}));
    const documentSelector = [
      ...languageFilters,
      {scheme: 'file', pattern: new vscode.RelativePattern(folder, '**/*.vue')},
      {scheme: 'file', pattern: new vscode.RelativePattern(folder, '**/.env*')},
      {scheme: 'file', pattern: new vscode.RelativePattern(folder, '**/*.env')},
      {scheme: 'file', pattern: new vscode.RelativePattern(folder, '**/Dockerfile*')},
      // vscode-languageclient's protocol type still declares pattern as a
      // string, while VS Code's runtime accepts RelativePattern for precise
      // workspace-folder scoping.
    ] as unknown as LanguageClientOptions['documentSelector'];
    const clientOptions: LanguageClientOptions = {
      workspaceFolder: folder,
      documentSelector,
      initializationOptions: {
        configuration: readEditorConfiguration(folder.uri),
        allowUnsupportedProject,
        // Execute-command IDs are registered globally by vscode-languageclient
        // and would collide across clients. Interactive commands are registered
        // once by this extension and route to the owning client explicitly.
        omitExecuteCommandProvider: true,
      },
      outputChannel: output,
      traceOutputChannel: output,
      revealOutputChannelOn: RevealOutputChannelOn.Error,
      middleware: {
        workspace: {
          willRenameFiles: (event, next) => {
            const files = event.files.filter(file =>
              manager.entryForUri(file.oldUri)?.key === key);
            if (files.length === 0) return Promise.resolve(null);
            return next({...event, files});
          },
        },
      },
    };
    const suffix = createHash('sha256').update(key).digest('hex').slice(0, 12);
    const client = new LanguageClient(
      `shopwareLSP-${suffix}`,
      `Shopware Language Server (${folder.name})`,
      serverOptions,
      clientOptions,
    );
    output.appendLine(`Starting Shopware Language Server at ${serverPath}`);
    output.appendLine(`Workspace: ${folder.uri.fsPath}`);
    if (memoryLimit > 0) {
      output.appendLine(`Using a ${memoryLimit} MiB soft memory limit`);
    }
    try {
      await client.start();
      if (inactiveServerProject(client.initializeResult?.capabilities.experimental)) {
        output.appendLine('The server marked this workspace as unsupported; stopping it');
        await client.stop();
        output.dispose();
        return undefined;
      }
    } catch (error) {
      output.appendLine(`Failed to start Shopware Language Server: ${error}`);
      output.show(true);
      try {
        await client.stop();
      } catch {
        // The failed client may already be stopped.
      }
      output.dispose();
      throw error;
    }

    const configurationClient = attachConfigurationClient(
      client,
      folder,
      coordinatorOutput,
    );
    client.onNotification('shopware/indexingStarted', () => {
      output.appendLine('Shopware indexing started');
      indexingStatus.started(key, folder.name);
    });
    client.onNotification(
      'shopware/indexingCompleted',
      (params: {timeInSeconds: number}) => {
        output.appendLine(`Shopware indexing completed in ${params.timeInSeconds} seconds`);
        indexingStatus.completed(key, folder.name, params.timeInSeconds);
      },
    );
    client.onNotification('shopware/indexingFailed', (params: {message?: string}) => {
      const message = params.message || 'unknown indexing error';
      output.appendLine(`Shopware indexing failed: ${message}`);
      indexingStatus.failed(key, folder.name, message);
    });
    let disposed = false;
    return {
      key,
      folder,
      client,
      async dispose() {
        if (disposed) return;
        disposed = true;
        configurationClient.dispose();
        indexingStatus.remove(key);
        try {
          await client.stop();
        } finally {
          output.dispose();
        }
      },
    };
  }

  const mcpProvider = registerMcpServerDefinitionProvider(
    context,
    coordinatorOutput,
    projectDetector,
  );
  let reconcileTimer: NodeJS.Timeout | undefined;
  const scheduleReconcile = () => {
    if (reconcileTimer) clearTimeout(reconcileTimer);
    reconcileTimer = setTimeout(() => void manager.reconcile(), 250);
  };
  registerProjectMarkerWatchers(context, event => {
    projectDetector.invalidate(event.folder.uri.fsPath);
    mcpProvider.refresh();
    scheduleReconcile();
  });
  context.subscriptions.push({dispose: () => {
    if (reconcileTimer) clearTimeout(reconcileTimer);
  }});

  await manager.reconcile();

  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.restart', async () => {
    const folder = await manager.resolveWorkspaceFolder(
      undefined,
      false,
      'Restart Shopware Language Server',
    );
    if (!folder) {
      vscode.window.showErrorMessage('A workspace folder is required');
      return;
    }
    projectDetector.invalidate(folder.uri.fsPath);
    const running = await manager.restartFolder(folder);
    vscode.window.showInformationMessage(
      running
        ? `Shopware LSP restarted for ${running.folder.name}`
        : `Shopware LSP is inactive for ${folder.name}`,
    );
  }));

  context.subscriptions.push(
    vscode.workspace.onDidChangeWorkspaceFolders(() => {
      projectDetector.invalidate();
      mcpProvider.refresh();
      scheduleReconcile();
    }),
    vscode.workspace.onDidChangeConfiguration(event => {
      const restartSections = [
        'shopwareLSP.activationMode',
        'shopwareLSP.serverPath',
        'shopwareLSP.memoryLimitMiB',
      ];
      const affected = manager.runningEntries().filter(entry =>
        restartSections.some(section =>
          event.affectsConfiguration(section, entry.folder.uri)));
      const affectsInactive = (vscode.workspace.workspaceFolders ?? []).some(folder =>
        restartSections.some(section => event.affectsConfiguration(section, folder.uri)));
      if (affected.length === 0 && !affectsInactive) return;
      for (const entry of affected) projectDetector.invalidate(entry.folder.uri.fsPath);
      mcpProvider.refresh();
      void manager.reconcile(new Set(affected.map(entry => entry.key)));
    }),
  );

  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.forceReindex', async () => {
    const entry = await manager.resolveEntry(undefined, 'Force Reindex Shopware Workspace');
    if (!entry) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }
    try {
      await entry.client.sendRequest('shopware/forceReindex');
      vscode.window.showInformationMessage(
        `Shopware LSP: Force reindexing started for ${entry.folder.name}`,
      );
    } catch (error) {
      vscode.window.showErrorMessage(`Failed to trigger force reindexing: ${error}`);
    }
  }));

  registerSymfonyCatalogCommands(context, manager);
  registerTwigCatalogCommands(context, manager);
  registerTwigVariableCommands(context, manager);
  registerScaffoldCommands(context, manager);
  registerSymfonyGenerationCommands(context, manager);
  registerEditorCommands(context, manager);
}

export function deactivate(): Thenable<void> | undefined {
  const manager = activeManager;
  activeManager = undefined;
  return manager?.stopAll();
}
