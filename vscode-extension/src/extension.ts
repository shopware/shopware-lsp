import * as vscode from 'vscode';
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
  RevealOutputChannelOn,
} from 'vscode-languageclient/node';
import type {ClientState} from './clientState';
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
  normalizeActivationMode,
  ProjectDetector,
} from './projectDetection';
import {registerProjectMarkerWatchers} from './projectMarkers';
import {resolveServerExecutable} from './serverExecutable';
import {registerDiagnosticConfigurationSupport} from './diagnosticConfiguration';
import {
  attachConfigurationClient,
  readEditorConfiguration,
  registerConfigurationSupport,
} from './configuration';

const clientState: ClientState = {};
let indexingStatusBarItem: vscode.StatusBarItem;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  // Create output channel for the language server
  const outputChannel = vscode.window.createOutputChannel("Shopware LSP");
  context.subscriptions.push(outputChannel);
  
  // Create status bar item for indexing status
  indexingStatusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  context.subscriptions.push(indexingStatusBarItem);
  const projectDetector = new ProjectDetector();

  async function startClient(): Promise<boolean> {
    if (clientState.client) {
      await clientState.client.stop();
      clientState.client = undefined;
    }
    indexingStatusBarItem.hide();

    // Clear the output channel when restarting
    outputChannel.clear();

    const workspaceFolder = getOuterMostWorkspaceFolder();
    if (!workspaceFolder) {
      outputChannel.appendLine('Shopware LSP is inactive: no workspace folder is open');
      return false;
    }
    const configuration = vscode.workspace.getConfiguration(
      'shopwareLSP', workspaceFolder.uri,
    );
    const activationMode = normalizeActivationMode(
      configuration.get<string>('activationMode', 'auto'),
    );
    if (activationMode === 'never') {
      outputChannel.appendLine('Shopware LSP is inactive: activation mode is set to never');
      return false;
    }
    const serverPath = resolveServerExecutable({
      configuredPath: configuration.get<string>('serverPath', ''),
      extensionPath: context.extensionPath,
      workspaceRoot: workspaceFolder.uri.fsPath,
    });

    if (!serverPath) {
      vscode.window.showErrorMessage(
        'Could not find the Shopware LSP server. Please set shopwareLSP.serverPath.',
      );
      return false;
    }

    let decision = decideActivation(activationMode);
    if (activationMode === 'auto') {
      try {
        const project = await projectDetector.detect(serverPath, workspaceFolder.uri.fsPath);
        decision = decideActivation(activationMode, project);
        if (!decision.enabled) {
          outputChannel.appendLine(
            `Shopware LSP is inactive for ${workspaceFolder.uri.fsPath}: no Shopware or Symfony project markers were found`,
          );
          outputChannel.appendLine(
            'Add .config/shopware-lsp/config.json or set shopwareLSP.activationMode to always to opt in.',
          );
          return false;
        }
        outputChannel.appendLine(`Detected ${project.kind} project: ${workspaceFolder.uri.fsPath}`);
      } catch (error) {
        outputChannel.appendLine(`Shopware LSP project detection failed: ${error}`);
        return false;
      }
    }

    const memoryLimit = normalizeMemoryLimitMiB(
      configuration.get<number>('memoryLimitMiB', 0),
    );

    // Define server options
    const serverOptions: ServerOptions = {
      command: serverPath,
      // vscode-languageclient appends --stdio for this transport. Use the
      // explicit CLI entry point so editor startup and one-shot CLI commands
      // share the same binary without relying on implicit command selection.
      args: decision.allowUnsupportedProject
        ? ['-allow-unsupported-project', 'serve']
        : ['serve'],
      transport: TransportKind.stdio,
      ...(memoryLimit > 0
        ? {
            options: {
              env: {
                ...process.env,
                GOMEMLIMIT: `${memoryLimit}MiB`
              }
            }
          }
        : {})
    };

    // Define client options
    const clientOptions: LanguageClientOptions = {
      documentSelector: [
        { scheme: 'file', language: 'php' },
        { scheme: 'file', language: 'xml' },
        { scheme: 'file', language: 'yml' },
        { scheme: 'file', language: 'yaml' },
        { scheme: 'file', language: 'twig' },
		{ scheme: 'file', language: 'vue' },
        { scheme: 'file', language: 'json' },
        { scheme: 'file', language: 'scss' },
        { scheme: 'file', language: 'javascript' },
        { scheme: 'file', language: 'typescript' },
        { scheme: 'file', language: 'dotenv' },
        { scheme: 'file', language: 'dockerfile' },
		{ scheme: 'file', pattern: '**/*.vue' },
        { scheme: 'file', pattern: '**/.env*' },
        { scheme: 'file', pattern: '**/*.env' },
        { scheme: 'file', pattern: '**/Dockerfile*' }
      ],
      initializationOptions: {
        configuration: readEditorConfiguration(workspaceFolder.uri),
        allowUnsupportedProject: decision.allowUnsupportedProject,
      },
      // Add output configuration
      outputChannel: outputChannel,
      traceOutputChannel: outputChannel,
      revealOutputChannelOn: RevealOutputChannelOn.Error
    };

    // Show output channel on start
    outputChannel.appendLine(`Starting Shopware Language Server at ${serverPath}`);
    if (memoryLimit > 0) {
      outputChannel.appendLine(
        `Using a ${memoryLimit} MiB soft memory limit for the language server`
      );
    }

    // Create and start the client
    const client = new LanguageClient(
      'shopwareLSP',
      'Shopware Language Server',
      serverOptions,
      clientOptions
    );
    clientState.client = client;

    try {
      await client.start();
      attachConfigurationClient(client);
      // Handler for indexing started
      client.onNotification('shopware/indexingStarted', () => {
        outputChannel.appendLine('Shopware indexing started');
        indexingStatusBarItem.text = '$(sync~spin) Shopware: Indexing...';
        indexingStatusBarItem.tooltip = 'Shopware language server is currently indexing';
        indexingStatusBarItem.show();
      });
      
      // Handler for indexing completed
      client.onNotification('shopware/indexingCompleted', (params: { timeInSeconds: number }) => {
        indexingStatusBarItem.text = `$(check) Shopware: Indexed`;
        indexingStatusBarItem.tooltip = `Indexing completed in ${params.timeInSeconds} seconds`;
        
        // Hide the status bar message after 10 seconds
        setTimeout(() => {
          indexingStatusBarItem.hide();
        }, 10000);
      });
      return true;
    } catch (error) {
      if (clientState.client === client) clientState.client = undefined;
      outputChannel.appendLine(`Failed to start Shopware Language Server: ${error}`);
      vscode.window.showErrorMessage(`Failed to start Shopware Language Server: ${error}`);
      return false;
    }
  }

  let restartQueue: Promise<boolean> = Promise.resolve(false);
  const queueStart = (): Promise<boolean> => {
    restartQueue = restartQueue.catch(() => false).then(startClient);
    return restartQueue;
  };

  const mcpProvider = registerMcpServerDefinitionProvider(
    context, outputChannel, projectDetector,
  );
  let markerRestart: NodeJS.Timeout | undefined;
  registerProjectMarkerWatchers(context, event => {
    projectDetector.invalidate(event.folder.uri.fsPath);
    mcpProvider.refresh();
    const current = getOuterMostWorkspaceFolder();
    if (!current || current.uri.toString() !== event.folder.uri.toString()) return;
    if (
      event.path === '.config/shopware-lsp/config.json' &&
      event.change === 'change' &&
      clientState.client
    ) return;
    if (markerRestart) clearTimeout(markerRestart);
    markerRestart = setTimeout(() => void queueStart(), 250);
  });
  context.subscriptions.push({dispose: () => {
    if (markerRestart) clearTimeout(markerRestart);
  }});

  // Start the client on activation and await it
  await queueStart();

  // Register restart command
  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.restart', async () => {
    projectDetector.invalidate();
    const running = await queueStart();
    vscode.window.showInformationMessage(
      running ? 'Shopware LSP restarted' : 'Shopware LSP is inactive for this workspace',
    );
  }));

  registerConfigurationSupport(context, clientState, outputChannel, async () => {
    await queueStart();
  });
  registerDiagnosticConfigurationSupport(context, clientState, outputChannel);

  context.subscriptions.push(
    vscode.workspace.onDidChangeWorkspaceFolders(() => {
      projectDetector.invalidate();
      void queueStart();
    }),
    vscode.workspace.onDidChangeConfiguration(event => {
      if (
        event.affectsConfiguration('shopwareLSP.activationMode') ||
        event.affectsConfiguration('shopwareLSP.serverPath') ||
        event.affectsConfiguration('shopwareLSP.memoryLimitMiB')
      ) {
        projectDetector.invalidate();
        mcpProvider.refresh();
        void queueStart();
      }
    }),
  );

  // Register force reindex command
  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.forceReindex', async () => {
    if (!clientState.client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }
    
    try {
      await clientState.client.sendRequest('shopware/forceReindex');
      vscode.window.showInformationMessage('Shopware LSP: Force reindexing started');
    } catch (error) {
      vscode.window.showErrorMessage(`Failed to trigger force reindexing: ${error}`);
    }
  }));

  registerSymfonyCatalogCommands(context, clientState);
  registerTwigCatalogCommands(context, clientState);
  registerTwigVariableCommands(context, () => clientState.client);
  registerScaffoldCommands(context, clientState);
  registerSymfonyGenerationCommands(context, clientState);
  registerEditorCommands(context, clientState);
}

export function deactivate(): Thenable<void> | undefined {
  if (!clientState.client) {
    return undefined;
  }
  
  // Add a timeout to ensure the server has time to respond
  return new Promise<void>((resolve) => {
    // Try to stop the client gracefully
    const stopPromise = clientState.client!.stop();
    
    // Set a timeout in case the stop hangs
    const timeout = setTimeout(() => {
      console.log('Client stop timed out, forcing resolution');
      resolve();
    }, 2000); // 2 second timeout
    
    // Handle normal completion
    stopPromise.then(() => {
      clearTimeout(timeout);
      resolve();
    }).catch(error => {
      console.error('Error stopping client:', error);
      clearTimeout(timeout);
      resolve(); // Resolve anyway to prevent VSCode from hanging
    });
  });
}


function getOuterMostWorkspaceFolder(): vscode.WorkspaceFolder | undefined {
  const sorted = (vscode.workspace.workspaceFolders || [])
    .map((folder: vscode.WorkspaceFolder) => {
        let path = folder.uri.toString();
        return path.endsWith('/') ? path : path + '/';
    })
    .sort((a: string, b: string) =>  a.length - b.length);

  return sorted[0] ? vscode.workspace.getWorkspaceFolder(vscode.Uri.parse(sorted[0])) : undefined;
}
