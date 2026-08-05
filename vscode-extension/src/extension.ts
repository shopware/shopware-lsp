import * as path from 'path';
import * as fs from 'fs';
import * as crypto from 'crypto';
import * as vscode from 'vscode';
import type {WorkspaceEdit as ProtocolWorkspaceEdit} from 'vscode-languageserver-protocol';
import {
  LanguageClient,
  LanguageClientOptions,
  ServerOptions,
  TransportKind,
  RevealOutputChannelOn
} from 'vscode-languageclient/node';
import {registerTwigVariableCommands} from './twigVariables';

let client: LanguageClient | undefined;
// Add a status bar item for indexing status
let indexingStatusBarItem: vscode.StatusBarItem;

class BlockContentProvider implements vscode.TextDocumentContentProvider {
  private contents = new Map<string, string>();

  provideTextDocumentContent(uri: vscode.Uri): string {
    return this.contents.get(uri.toString()) || '';
  }

  setContent(uri: vscode.Uri, content: string): void {
    this.contents.set(uri.toString(), content);
  }
}

const blockContentProvider = new BlockContentProvider();

interface SnippetFile {
  path: string;
  name: string;
  value: string;
}

interface SymfonyServiceGeneration {
  content: string;
  language: string;
}

interface SymfonyServiceParameterSuggestion {
  method: string;
  parameter: string;
  type?: string;
  services: string[];
}

interface SymfonyServiceDefinitionCollectionEntry {
  className: string;
  content?: string;
  language?: string;
  suggestions?: SymfonyServiceParameterSuggestion[];
  error?: string;
}

interface SymfonyServiceDefinitionCollection {
  definitions: SymfonyServiceDefinitionCollectionEntry[];
}

interface CompilerPassCreation {
  fileUri: string;
  fileContent: string;
  bundleContent: string;
}

interface FormFieldCandidate {
  name: string;
  phpType: string;
  suggestedType: string;
}

interface FormFieldCandidates {
  dataClass: string;
  fields: FormFieldCandidate[];
}

interface FormFieldGeneration {
  content: string;
}

interface TwigFormCandidate {
  variable: string;
  formType: string;
  fields: string[];
}

interface TwigFormFieldCandidates {
  forms: TwigFormCandidate[];
}

interface TwigTemplateCandidates {
  templates: string[];
}

interface TwigBlockCandidates {
  parent: string;
  blocks: string[];
}

interface LspPosition {
  line: number;
  character: number;
}

interface LspRange {
  start: LspPosition;
  end: LspPosition;
}

interface TwigTranslationExtractionPreparation {
  text: string;
  range: LspRange;
  defaultKey?: string;
  defaultDomain: string;
  domains: string[];
}

interface TwigTranslationExtractionTarget {
  fileUri: string;
  file: string;
  locale?: string;
  format: string;
  line: number;
  character: number;
  newText: string;
}

interface TwigTranslationExtractionEdits {
  replacement: string;
  range: LspRange;
  targets: TwigTranslationExtractionTarget[];
}

interface SymfonyConsoleCatalogInput {
  name: string;
  shortcut?: string;
  mode?: string;
  description?: string;
  default?: string;
}

interface SymfonyConsoleCatalogEntry {
  name: string;
  canonical?: string;
  description?: string;
  class?: string;
  method?: string;
  fileUri?: string;
  filePath?: string;
  arguments?: SymfonyConsoleCatalogInput[];
  options?: SymfonyConsoleCatalogInput[];
}

interface SymfonyRouteCatalogEntry {
  name: string;
  path?: string;
  methods?: string[];
  controller?: string;
  resolvedController?: string;
  sourceUri?: string;
  sourceLine?: number;
  controllerUri?: string;
  controllerLine?: number;
  templates?: string[];
}

interface SymfonyProfilerRuntimeTwigComponent {
  name: string;
  class?: string;
  template?: string;
  renderCount?: number;
  fileUri?: string;
  sourceLine?: number;
}

interface SymfonyProfilerRequestCatalogEntry {
  hash: string;
  method?: string;
  url: string;
  statusCode?: number;
  timestamp?: number;
  profilerUrl: string;
  controller?: string;
  controllerFileUri?: string;
  controllerLine?: number;
  route?: string;
  entryView?: string;
  staticTemplates?: string[];
  renderedTemplates?: string[];
  formTypes?: string[];
  mailMessages?: {
    title: string;
    panel: string;
  }[];
  twigComponents?: SymfonyProfilerRuntimeTwigComponent[];
  indexFileUri: string;
}

interface DoctrineEntityCatalogEntry {
  class: string;
  parent?: string;
  repository?: string;
  table?: string;
  kind: string;
  source: string;
  fileUri?: string;
  sourceLine?: number;
  fieldCount: number;
}

interface DoctrineFieldCatalogEntry {
  name: string;
  column?: string;
  type?: string;
  relation?: string;
  relationType?: string;
  enumType?: string;
  phpType?: string;
  propertyTypes?: string[];
  embeddedClass?: string;
  columnPrefix?: string;
  declaringClass?: string;
  fileUri?: string;
  sourceLine?: number;
}

interface SymfonyFormTypeCatalogEntry {
  name: string;
  className: string;
  aliases?: string[];
  parent?: string;
  dataClass?: string;
  fileUri?: string;
  sourceLine?: number;
  optionCount: number;
  fieldCount: number;
  viewVarCount: number;
}

interface SymfonyFormOptionCatalogEntry {
  name: string;
  kinds: string[];
  allowedTypes?: string[];
  default?: string;
  sourceClass?: string;
  fileUri?: string;
  sourceLine?: number;
}

interface SymfonyServiceDefinitionSource {
  source: 'explicit' | 'prototype' | 'compiled';
  fileUri?: string;
  sourceLine?: number;
  endLine?: number;
  preview?: string;
}

interface SymfonyServiceLocatorEntry {
  id: string;
  className?: string;
  resolvedClass?: string;
  aliasTarget?: string;
  decorates?: string;
  parent?: string;
  autowire?: boolean;
  autowireConfigured?: boolean;
  deprecated?: boolean;
  deprecation?: string;
  tags?: string[];
  classFileUri?: string;
  classLine?: number;
  definitions: SymfonyServiceDefinitionSource[];
}

interface TwigExtensionCatalogParameter {
  name: string;
  type?: string;
  optional?: boolean;
}

interface TwigExtensionCatalogEntry {
  type: 'filter' | 'function' | 'test' | 'tag';
  name: string;
  className?: string;
  methodName?: string;
  callable?: string;
  usage?: string;
  parameters?: TwigExtensionCatalogParameter[];
  fileUri?: string;
  sourceLine?: number;
  deprecated?: boolean;
  deprecation?: string;
}

interface TwigTemplateSourceLocation {
  fileUri: string;
  line?: number;
}

interface TwigTemplateRouteEntry {
  name: string;
  path?: string;
  methods?: string[];
}

interface TwigTemplateControllerUsage {
  controller: string;
  fileUri?: string;
  line?: number;
  routes?: TwigTemplateRouteEntry[];
}

interface TwigTemplateReferenceUsage {
  fileUri: string;
  line?: number;
}

interface TwigTemplateComponentUsage {
  component: string;
  syntax?: string;
  fileUri: string;
  line?: number;
}

interface TwigTemplateUsageCatalogEntry {
  template: string;
  files?: TwigTemplateSourceLocation[];
  controllers?: TwigTemplateControllerUsage[];
  includes?: TwigTemplateReferenceUsage[];
  embeds?: TwigTemplateReferenceUsage[];
  extends?: TwigTemplateReferenceUsage[];
  imports?: TwigTemplateReferenceUsage[];
  uses?: TwigTemplateReferenceUsage[];
  formThemes?: TwigTemplateReferenceUsage[];
  components?: TwigTemplateComponentUsage[];
}

interface TwigComponentLocation {
  fileUri?: string;
  sourceLine?: number;
}

interface TwigComponentDeclarationEntry extends TwigComponentLocation {
  class?: string;
  template?: string;
  templateFromMethod?: string;
  source: string;
  live?: boolean;
  exposePublicProps?: boolean;
}

interface TwigComponentTemplateEntry {
  template?: string;
  fileUri: string;
}

interface TwigComponentPropEntry extends TwigComponentLocation {
  name: string;
  type?: string;
  defaultValue?: string;
  description?: string;
  class?: string;
  member?: string;
  live?: boolean;
  writable?: boolean;
}

interface TwigComponentBlockEntry {
  name: string;
  fileUri?: string;
  line?: number;
  print: string;
  compose: string;
}

interface TwigComponentUsageEntry {
  syntax: string;
  fileUri: string;
  line?: number;
}

interface TwigComponentCatalogEntry {
  name: string;
  declarations?: TwigComponentDeclarationEntry[];
  templates?: TwigComponentTemplateEntry[];
  props?: TwigComponentPropEntry[];
  computed?: TwigComponentPropEntry[];
  blocks?: TwigComponentBlockEntry[];
  usages?: TwigComponentUsageEntry[];
  syntax: {
    htmlTag: string;
    function: string;
    composition: string;
  };
}

interface SymfonyScaffoldCreation {
  fileUri: string;
  content: string;
  language: string;
  className?: string;
  namespace?: string;
}

interface ShopwareScaffoldCreation {
  edit: ProtocolWorkspaceEdit;
  primaryFileUri: string;
  shopwareVersion?: string;
}

interface ShopwareScaffoldRequest {
  kind: string;
  directoryUri: string;
  name: string;
  options?: Record<string, string | number | boolean>;
}

async function applyShopwareScaffoldCreation(
  request: ShopwareScaffoldRequest,
  label: string,
): Promise<void> {
  if (!client) {
    throw new Error('Shopware LSP is not running');
  }
  const result = await client.sendRequest<ShopwareScaffoldCreation>(
    'shopware/scaffold/create',
    request,
  );
  const edit = await client.protocol2CodeConverter.asWorkspaceEdit(result.edit);
  if (!await vscode.workspace.applyEdit(edit)) {
    throw new Error(`Could not create ${label}`);
  }
  const primaryUri = vscode.Uri.parse(result.primaryFileUri);
  const document = await vscode.workspace.openTextDocument(primaryUri);
  await vscode.window.showTextDocument(document, {
    preview: false,
    preserveFocus: false,
  });
  vscode.window.showInformationMessage(`Created ${label}`);
}

async function chooseShopwareTargetDirectory(
  title: string,
  sourceUri?: string,
): Promise<vscode.Uri | undefined> {
  let defaultUri = vscode.workspace.workspaceFolders?.[0]?.uri;
  if (sourceUri) {
    try {
      const source = vscode.Uri.parse(sourceUri);
      defaultUri = vscode.workspace.getWorkspaceFolder(source)?.uri ?? defaultUri;
    } catch {
      // Keep the first workspace folder as the default.
    }
  }
  const selected = await vscode.window.showOpenDialog({
    title,
    defaultUri,
    canSelectFiles: false,
    canSelectFolders: true,
    canSelectMany: false,
    openLabel: 'Use Directory',
  });
  return selected?.[0];
}

async function createAdminComponentExtension(
  component: string,
  sourceUri?: string,
  method?: string,
  methodGroup?: string,
  parameters?: string,
): Promise<void> {
  const mode = await vscode.window.showQuickPick([
    {
      label: 'Extend',
      description: 'Register a new component derived from the selected component',
      value: 'extend',
    },
    {
      label: 'Override',
      description: 'Override the selected component in place',
      value: 'override',
    },
  ], {
    title: method
      ? `Override ${component}.${method}()`
      : `Extend or override ${component}`,
  });
  if (!mode) {
    return;
  }
  const directory = await chooseShopwareTargetDirectory(
    'Select the Administration src directory',
    sourceUri,
  );
  if (!directory) {
    return;
  }
  let name = component;
  if (mode.value === 'extend') {
    const selectedName = await vscode.window.showInputBox({
      title: 'New Administration Component',
      prompt: 'Name of the derived component',
      value: `custom-${component}`,
      validateInput: value => /^[A-Za-z][A-Za-z0-9_-]*$/.test(value.trim())
        ? null
        : 'Start with a letter and use letters, digits, dashes, or underscores',
    });
    if (!selectedName) {
      return;
    }
    name = selectedName.trim();
  }
  const options: Record<string, string | number | boolean> = {
    mode: mode.value,
    target: component,
    generateTwig: false,
    generateScss: false,
  };
  if (method) {
    options.method = method;
    options.methodGroup = methodGroup ?? '';
    options.parameters = parameters ?? '';
  }
  await applyShopwareScaffoldCreation({
    kind: 'admin-component',
    directoryUri: directory.toString(),
    name,
    options,
  }, method ? `${component}.${method}() override` : `${component} extension`);
}

/**
 * Shows a multi-step input to collect translations for all snippet files.
 * Returns the snippet files with values filled in, or undefined if cancelled.
 * @param initialValue - Optional initial value to pre-fill the first input (e.g., selected text)
 */
async function collectSnippetTranslations(
  snippetFiles: SnippetFile[],
  snippetKey: string,
  title: string,
  initialValue?: string
): Promise<SnippetFile[] | undefined> {
  const totalSteps = snippetFiles.length;
  let currentStep = 0;
  let previousValue = initialValue || '';

  for (const snippetFile of snippetFiles) {
    currentStep++;
    
    // Extract locale from filename (e.g., "en-GB.json" -> "English (GB)", "de-DE.json" -> "German (DE)")
    const locale = snippetFile.name.replace('.json', '');
    const localeName = getLocaleName(locale);
    
    const result = await vscode.window.showInputBox({
      title: `${title} (${currentStep}/${totalSteps})`,
      prompt: `Enter ${localeName} translation for "${snippetKey}"`,
      placeHolder: `Translation in ${localeName}`,
      value: previousValue,
      validateInput: (value) => {
        if (!value || value.trim() === '') {
          return 'Translation cannot be empty';
        }
        return null;
      }
    });

    if (result === undefined) {
      return undefined; // User cancelled
    }

    snippetFile.value = result;
    previousValue = result; // Pre-fill next input with same value
  }

  return snippetFiles;
}

/**
 * Convert locale code to human-readable name
 */
function getLocaleName(locale: string): string {
  const localeMap: Record<string, string> = {
    'en-GB': 'English (GB)',
    'en-US': 'English (US)',
    'en': 'English',
    'de-DE': 'German (DE)',
    'de': 'German',
    'nl-NL': 'Dutch (NL)',
    'nl': 'Dutch',
    'fr-FR': 'French (FR)',
    'fr': 'French',
    'it-IT': 'Italian (IT)',
    'it': 'Italian',
    'es-ES': 'Spanish (ES)',
    'es': 'Spanish',
    'pt-PT': 'Portuguese (PT)',
    'pt-BR': 'Portuguese (BR)',
    'pt': 'Portuguese',
    'pl-PL': 'Polish (PL)',
    'pl': 'Polish',
    'cs-CZ': 'Czech (CZ)',
    'cs': 'Czech',
    'sv-SE': 'Swedish (SE)',
    'sv': 'Swedish',
    'da-DK': 'Danish (DK)',
    'da': 'Danish',
    'fi-FI': 'Finnish (FI)',
    'fi': 'Finnish',
    'nb-NO': 'Norwegian (NO)',
    'nb': 'Norwegian',
    'ru-RU': 'Russian (RU)',
    'ru': 'Russian',
    'zh-CN': 'Chinese (Simplified)',
    'zh-TW': 'Chinese (Traditional)',
    'ja-JP': 'Japanese (JP)',
    'ja': 'Japanese',
    'ko-KR': 'Korean (KR)',
    'ko': 'Korean',
  };
  return localeMap[locale] || locale;
}

export async function activate(context: vscode.ExtensionContext): Promise<void> {
  // Create output channel for the language server
  const outputChannel = vscode.window.createOutputChannel("Shopware LSP");
  context.subscriptions.push(outputChannel);
  
  // Create status bar item for indexing status
  indexingStatusBarItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 100);
  context.subscriptions.push(indexingStatusBarItem);

  context.subscriptions.push(
    vscode.workspace.registerTextDocumentContentProvider('shopware-block', blockContentProvider)
  );

  async function startClient() {
    if (client) {
      await client.stop();
      client = undefined;
    }

    // Clear the output channel when restarting
    outputChannel.clear();

    // Get the server path from settings or use default
    let serverPath = vscode.workspace.getConfiguration('shopwareLSP').get<string>('serverPath', '');
    
    // If no custom path is provided, use the bundled server
    if (!serverPath) {
      // For development, we'll look for the server in the parent directory
      const workspaceRoot = getOuterMostWorkspaceFolder()?.uri.fsPath || '';
      const possiblePaths = [
        // When installed as extension
        context.asAbsolutePath(path.join('.', 'shopware-lsp')),
        // When installed as extension in the parent directory
        context.asAbsolutePath(path.join('..', 'shopware-lsp')),
        // When running from source
        path.join(workspaceRoot, '..', 'shopware-lsp'),
        // When in the same directory
        path.join(workspaceRoot, 'shopware-lsp')
      ];

      for (const p of possiblePaths) {
        if (fs.existsSync(p)) {
          serverPath = p;
          break;
        }
      }
    }

    if (!serverPath) {
      vscode.window.showErrorMessage('Could not find Symfony Service LSP server. Please set the path in settings.');
      return;
    }

    const memoryLimitMiB = vscode.workspace
      .getConfiguration('shopwareLSP')
      .get<number>('memoryLimitMiB', 0);
    const memoryLimit = Number.isFinite(memoryLimitMiB)
      ? Math.max(0, Math.floor(memoryLimitMiB))
      : 0;

    // Define server options
    const serverOptions: ServerOptions = {
      command: serverPath,
      // vscode-languageclient appends --stdio for this transport. Use the
      // explicit CLI entry point so editor startup and one-shot CLI commands
      // share the same binary without relying on implicit command selection.
      args: ['serve'],
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
        phpExtensions: vscode.workspace
          .getConfiguration('shopwareLSP')
          .get<string[]>('phpExtensions', []),
        disabledPhpExtensions: vscode.workspace
          .getConfiguration('shopwareLSP')
          .get<string[]>('disabledPhpExtensions', []),
        shopwareTargetVersion: vscode.workspace
          .getConfiguration('shopwareLSP')
          .get<string>('shopwareTargetVersion', '')
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
    client = new LanguageClient(
      'shopwareLSP',
      'Shopware Language Server',
      serverOptions,
      clientOptions
    );

    // Register notification handlers
    client.start().then(() => {
      // Handler for indexing started
      client!.onNotification('shopware/indexingStarted', () => {
        outputChannel.appendLine('Shopware indexing started');
        indexingStatusBarItem.text = '$(sync~spin) Shopware: Indexing...';
        indexingStatusBarItem.tooltip = 'Shopware language server is currently indexing';
        indexingStatusBarItem.show();
      });
      
      // Handler for indexing completed
      client!.onNotification('shopware/indexingCompleted', (params: { timeInSeconds: number }) => {
        indexingStatusBarItem.text = `$(check) Shopware: Indexed`;
        indexingStatusBarItem.tooltip = `Indexing completed in ${params.timeInSeconds} seconds`;
        
        // Hide the status bar message after 10 seconds
        setTimeout(() => {
          indexingStatusBarItem.hide();
        }, 10000);
      });
    }).catch((err: Error) => {
      outputChannel.appendLine(`Error registering notification handler: ${err}`);
    });
  }

  // Start client on activation and await it
  await startClient();

  // Register restart command
  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.restart', async () => {
    await startClient();
    vscode.window.showInformationMessage('Shopware LSP restarted');
  }));

  // Register force reindex command
  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.forceReindex', async () => {
    if (!client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }
    
    try {
      await client.sendRequest('shopware/forceReindex');
      vscode.window.showInformationMessage('Shopware LSP: Force reindexing started');
    } catch (error) {
      vscode.window.showErrorMessage(`Failed to trigger force reindexing: ${error}`);
    }
  }));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.runConsoleCommand',
    async (commandName: string, resourceUri?: string) => {
      const command = typeof commandName === 'string'
        ? commandName.trim()
        : '';
      if (!command || /[\u0000-\u001f\u007f]/.test(command)) {
        vscode.window.showErrorMessage(
          'Cannot run the Symfony command because its name is invalid.',
        );
        return;
      }

      let resource: vscode.Uri | undefined;
      if (resourceUri) {
        try {
          resource = vscode.Uri.parse(resourceUri);
        } catch {
          resource = undefined;
        }
      }
      const activeResource = vscode.window.activeTextEditor?.document.uri;
      const workspaceFolder = resource
        ? vscode.workspace.getWorkspaceFolder(resource)
        : activeResource
          ? vscode.workspace.getWorkspaceFolder(activeResource)
          : undefined;
      const folder = workspaceFolder ?? vscode.workspace.workspaceFolders?.[0];
      if (!folder) {
        vscode.window.showErrorMessage(
          'Open a Symfony workspace before running a console command.',
        );
        return;
      }

      const consolePath = path.join(folder.uri.fsPath, 'bin', 'console');
      let consoleExists = false;
      try {
        consoleExists = fs.statSync(consolePath).isFile();
      } catch {
        consoleExists = false;
      }
      if (!consoleExists) {
        vscode.window.showErrorMessage(
          `Symfony console not found at ${consolePath}.`,
        );
        return;
      }

      const phpExecutable = vscode.workspace
        .getConfiguration('shopwareLSP', folder.uri)
        .get<string>('phpExecutable', 'php')
        .trim() || 'php';
      const execution = new vscode.ProcessExecution(
        phpExecutable,
        [consolePath, command],
        { cwd: folder.uri.fsPath },
      );
      const task = new vscode.Task(
        {
          type: 'shopware-symfony-console',
          command,
        },
        folder,
        `Symfony: ${command}`,
        'Shopware',
        execution,
        [],
      );
      task.presentationOptions = {
        reveal: vscode.TaskRevealKind.Always,
        panel: vscode.TaskPanelKind.Dedicated,
        clear: false,
      };
      await vscode.tasks.executeTask(task);
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.runConsoleCommandPicker',
    async (resource?: vscode.Uri) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const commands = await client.sendRequest<SymfonyConsoleCatalogEntry[]>(
          'shopware/symfony/console/commands',
          {},
        );
        if (commands.length === 0) {
          vscode.window.showInformationMessage(
            'No indexed Symfony console commands were found.',
          );
          return;
        }
        const items = commands.map(command => {
          const className = command.class?.split('\\').pop() ?? '';
          const target = command.method
            ? `${className}::${command.method}`
            : className;
          const alias = command.canonical &&
            command.canonical !== command.name
            ? `alias of ${command.canonical}`
            : '';
          const argumentNames = (command.arguments ?? [])
            .map(argument => argument.name)
            .join(' ');
          const optionNames = (command.options ?? [])
            .map(option => `--${option.name}`)
            .join(' ');
          const signature = [argumentNames, optionNames]
            .filter(Boolean)
            .join(' ');
          return {
            label: `$(terminal) ${command.name}`,
            description: [alias, target].filter(Boolean).join(' · '),
            detail: [command.description, signature]
              .filter(Boolean)
              .join(' · '),
            command,
          };
        });
        const selected = await vscode.window.showQuickPick(items, {
          title: 'Run Symfony Console Command',
          placeHolder: 'Select an indexed command',
          matchOnDescription: true,
          matchOnDetail: true,
        });
        if (!selected) {
          return;
        }
        const commandResource = resource ??
          vscode.window.activeTextEditor?.document.uri ??
          vscode.workspace.workspaceFolders?.[0]?.uri;
        await vscode.commands.executeCommand(
          'shopware.symfony.runConsoleCommand',
          selected.command.name,
          commandResource?.toString(),
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load Symfony console commands: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseRoutes',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const routes = await client.sendRequest<SymfonyRouteCatalogEntry[]>(
          'shopware/symfony/analytics/routes',
          {},
        );
        if (routes.length === 0) {
          vscode.window.showInformationMessage(
            'No indexed Symfony routes were found.',
          );
          return;
        }
        const items = routes.map(route => {
          const methods = route.methods?.length
            ? route.methods.join('|')
            : 'ANY';
          return {
            label: `$(link) ${route.name}`,
            description: `${methods} ${route.path ?? ''}`.trim(),
            detail: route.resolvedController || route.controller || '',
            route,
          };
        });
        const selected = await vscode.window.showQuickPick(items, {
          title: 'Browse Symfony Routes',
          placeHolder: 'Search by route, URL, method, or controller',
          matchOnDescription: true,
          matchOnDetail: true,
        });
        if (!selected) {
          return;
        }
        const targetUri = selected.route.controllerUri ??
          selected.route.sourceUri;
        if (!targetUri) {
          vscode.window.showInformationMessage(
            `No source location is available for ${selected.route.name}.`,
          );
          return;
        }
        const line = Math.max(
          0,
          (selected.route.controllerLine ??
            selected.route.sourceLine ??
            1) - 1,
        );
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(targetUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(line, 0);
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load Symfony routes: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseProfilerRequests',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const requests = await client.sendRequest<
          SymfonyProfilerRequestCatalogEntry[]
        >(
          'shopware/symfony/analytics/profiler/requests',
          {},
        );
        if (requests.length === 0) {
          vscode.window.showInformationMessage(
            'No matching local Symfony profiler requests were found.',
          );
          return;
        }
        const requestItems = requests.map(request => ({
          label: `$(pulse) ${request.method ?? 'ANY'} ${request.url}`,
          description: [
            request.statusCode ? String(request.statusCode) : '',
            request.route,
            request.hash,
          ].filter(Boolean).join(' · '),
          detail: [
            request.controller,
            request.entryView
              ? `view ${request.entryView}`
              : '',
            request.formTypes?.length
              ? `form ${request.formTypes.join('|')}`
              : '',
            request.mailMessages?.length
              ? `${request.mailMessages.length} mail`
              : '',
            request.twigComponents?.length
              ? `${request.twigComponents.length} components`
              : '',
          ].filter(Boolean).join(' · '),
          request,
          mail: undefined as { title: string; panel: string } | undefined,
          component: undefined as
            SymfonyProfilerRuntimeTwigComponent | undefined,
        }));
        const mailItems = requests.flatMap(request =>
          (request.mailMessages ?? []).map(mail => ({
            label: `$(mail) ${mail.title}`,
            description: [
              request.hash,
              `${request.method ?? 'ANY'} ${request.url}`,
            ].join(' · '),
            detail: 'Open the Symfony profiler mail panel',
            request,
            mail,
            component: undefined,
          })),
        );
        const componentItems = requests.flatMap(request =>
          (request.twigComponents ?? []).map(component => ({
            label: `$(symbol-structure) ${component.name}`,
            description: [
              component.renderCount
                ? `${component.renderCount} renders`
                : '',
              request.hash,
            ].filter(Boolean).join(' · '),
            detail: component.class ?? component.template ??
              'Runtime Twig component',
            request,
            mail: undefined,
            component,
          })),
        );
        const items = [...requestItems, ...mailItems, ...componentItems];
        const selected = await vscode.window.showQuickPick(items, {
          title: 'Browse Local Symfony Profiler Requests',
          placeHolder:
            'Search by URL, hash, route, controller, template, or mail subject',
          matchOnDescription: true,
          matchOnDetail: true,
        });
        if (!selected) {
          return;
        }
        if (selected.component) {
          if (!selected.component.fileUri) {
            vscode.window.showInformationMessage(
              `No indexed source was found for ${selected.component.name}.`,
            );
            return;
          }
          const document = await vscode.workspace.openTextDocument(
            vscode.Uri.parse(selected.component.fileUri),
          );
          const editor = await vscode.window.showTextDocument(document, {
            preview: false,
          });
          const position = new vscode.Position(
            Math.max(0, (selected.component.sourceLine ?? 1) - 1),
            0,
          );
          editor.selection = new vscode.Selection(position, position);
          editor.revealRange(
            new vscode.Range(position, position),
            vscode.TextEditorRevealType.InCenterIfOutsideViewport,
          );
          return;
        }
        if (selected.mail) {
          const profilerUri = vscode.Uri.parse(selected.request.profilerUrl);
          if (profilerUri.scheme !== 'http' &&
              profilerUri.scheme !== 'https') {
            vscode.window.showErrorMessage(
              'Configure an absolute profiler base URL to open mail panels.',
            );
            return;
          }
          const separator = selected.request.profilerUrl.includes('?')
            ? '&'
            : '?';
          await vscode.env.openExternal(vscode.Uri.parse(
            `${selected.request.profilerUrl}${separator}panel=${
              encodeURIComponent(selected.mail.panel)
            }`,
          ));
          return;
        }
        const targetUri = selected.request.controllerFileUri ??
          selected.request.indexFileUri;
        if (!targetUri) {
          return;
        }
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(targetUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(
          Math.max(0, (selected.request.controllerLine ?? 1) - 1),
          0,
        );
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load local Symfony profiler requests: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseDoctrineEntities',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const entities = await client.sendRequest<
          DoctrineEntityCatalogEntry[]
        >(
          'shopware/symfony/analytics/doctrine/entities',
          {},
        );
        if (entities.length === 0) {
          vscode.window.showInformationMessage(
            'No indexed Doctrine entities were found.',
          );
          return;
        }
        const entityItems = entities.map(entity => ({
          label: `$(database) ${entity.class}`,
          description: [
            entity.kind,
            entity.table ? `table ${entity.table}` : '',
          ].filter(Boolean).join(' · '),
          detail: [
            entity.repository,
            `${entity.fieldCount} mapped field${
              entity.fieldCount === 1 ? '' : 's'
            }`,
          ].filter(Boolean).join(' · '),
          entity,
        }));
        const selectedEntity = await vscode.window.showQuickPick(
          entityItems,
          {
            title: 'Browse Doctrine Entities',
            placeHolder: 'Search by class, table, repository, or kind',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedEntity) {
          return;
        }
        const fields = await client.sendRequest<
          DoctrineFieldCatalogEntry[]
        >(
          'shopware/symfony/analytics/doctrine/entityFields',
          {className: selectedEntity.entity.class},
        );
        const fieldItems = fields.map(field => {
          const mappedType = field.relation
            ? `${field.relationType ?? 'relation'} → ${field.relation}`
            : field.type ?? field.phpType ?? '';
          return {
            label: `$(symbol-field) ${field.name}`,
            description: mappedType,
            detail: [
              field.column ? `column ${field.column}` : '',
              field.enumType ? `enum ${field.enumType}` : '',
              field.declaringClass &&
                field.declaringClass !== selectedEntity.entity.class
                ? `declared by ${field.declaringClass}`
                : '',
            ].filter(Boolean).join(' · '),
            field,
          };
        });
        const selectedField = await vscode.window.showQuickPick(
          fieldItems,
          {
            title: selectedEntity.entity.class,
            placeHolder: 'Select a mapped field',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedField) {
          return;
        }
        const targetUri = selectedField.field.fileUri ??
          selectedEntity.entity.fileUri;
        if (!targetUri) {
          vscode.window.showInformationMessage(
            `No source location is available for ${selectedField.field.name}.`,
          );
          return;
        }
        const line = Math.max(
          0,
          (selectedField.field.sourceLine ??
            selectedEntity.entity.sourceLine ??
            1) - 1,
        );
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(targetUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(line, 0);
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load Doctrine entities: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseFormTypes',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const formTypes = await client.sendRequest<
          SymfonyFormTypeCatalogEntry[]
        >(
          'shopware/symfony/analytics/forms/types',
          {},
        );
        if (formTypes.length === 0) {
          vscode.window.showInformationMessage(
            'No indexed Symfony form types were found.',
          );
          return;
        }
        const typeItems = formTypes.map(formType => ({
          label: `$(symbol-class) ${formType.name}`,
          description: formType.name === formType.className
            ? ''
            : formType.className,
          detail: [
            formType.parent ? `parent ${formType.parent}` : '',
            formType.dataClass ? `data ${formType.dataClass}` : '',
            `${formType.optionCount} options`,
            `${formType.fieldCount} fields`,
            `${formType.viewVarCount} view vars`,
          ].filter(Boolean).join(' · '),
          formType,
        }));
        const selectedType = await vscode.window.showQuickPick(typeItems, {
          title: 'Browse Symfony Form Types',
          placeHolder: 'Search by alias, class, parent, or data class',
          matchOnDescription: true,
          matchOnDetail: true,
        });
        if (!selectedType) {
          return;
        }

        let targetUri = selectedType.formType.fileUri;
        let targetLine = selectedType.formType.sourceLine;
        if (selectedType.formType.optionCount > 0) {
          const options = await client.sendRequest<
            SymfonyFormOptionCatalogEntry[]
          >(
            'shopware/symfony/analytics/forms/typeOptions',
            {formType: selectedType.formType.name},
          );
          const optionItems = options.map(option => ({
            label: `$(symbol-property) ${option.name}`,
            description: option.kinds.join('|'),
            detail: [
              option.allowedTypes?.length
                ? `types ${option.allowedTypes.join('|')}`
                : '',
              option.default !== undefined
                ? `default ${option.default}`
                : '',
              option.sourceClass,
            ].filter(Boolean).join(' · '),
            option,
          }));
          const selectedOption = await vscode.window.showQuickPick(
            optionItems,
            {
              title: selectedType.formType.name,
              placeHolder: 'Select an effective form option',
              matchOnDescription: true,
              matchOnDetail: true,
            },
          );
          if (!selectedOption) {
            return;
          }
          targetUri = selectedOption.option.fileUri ?? targetUri;
          targetLine = selectedOption.option.sourceLine ?? targetLine;
        }
        if (!targetUri) {
          vscode.window.showInformationMessage(
            `No source location is available for ${selectedType.formType.name}.`,
          );
          return;
        }
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(targetUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(
          Math.max(0, (targetLine ?? 1) - 1),
          0,
        );
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load Symfony form types: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.locateService',
    async (initialIdentifier?: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      const identifier = initialIdentifier?.trim() ||
        await vscode.window.showInputBox({
          title: 'Locate Symfony Service',
          prompt: 'Enter a service ID or fully-qualified PHP class',
          placeHolder: 'App\\Service\\CatalogService',
          ignoreFocusOut: true,
        });
      if (!identifier?.trim()) {
        return;
      }
      try {
        const services = await client.sendRequest<
          SymfonyServiceLocatorEntry[]
        >(
          'shopware/symfony/analytics/services/locate',
          {identifier: identifier.trim()},
        );
        const serviceItems = services.map(service => ({
          label: `$(symbol-interface) ${service.id}`,
          description: service.aliasTarget
            ? `alias → ${service.aliasTarget}`
            : service.resolvedClass ?? service.className ?? '',
          detail: [
            service.autowire ? 'autowired' : '',
            service.decorates ? `decorates ${service.decorates}` : '',
            service.parent ? `parent ${service.parent}` : '',
            service.deprecated ? 'deprecated' : '',
            service.tags?.length ? service.tags.join(', ') : '',
          ].filter(Boolean).join(' · '),
          service,
        }));
        const selectedService = serviceItems.length === 1
          ? serviceItems[0]
          : await vscode.window.showQuickPick(serviceItems, {
            title: `Services for ${identifier.trim()}`,
            placeHolder: 'Select a service definition',
            matchOnDescription: true,
            matchOnDetail: true,
          });
        if (!selectedService) {
          return;
        }

        const sourceItems = selectedService.service.definitions.map(
          definition => ({
            label: definition.fileUri
              ? `$(file-code) ${definition.source}`
              : `$(database) ${definition.source}`,
            description: definition.fileUri
              ? vscode.workspace.asRelativePath(
                vscode.Uri.parse(definition.fileUri),
              )
              : selectedService.service.resolvedClass ?? '',
            detail: definition.preview
              ?.replace(/\s+/g, ' ')
              .slice(0, 180),
            definition,
          }),
        );
        const selectedSource = sourceItems.length === 1
          ? sourceItems[0]
          : await vscode.window.showQuickPick(sourceItems, {
            title: selectedService.service.id,
            placeHolder: 'Select an explicit or prototype source',
            matchOnDescription: true,
            matchOnDetail: true,
          });
        if (!selectedSource) {
          return;
        }
        const targetUri = selectedSource.definition.fileUri ??
          selectedService.service.classFileUri;
        const targetLine = selectedSource.definition.sourceLine ??
          selectedService.service.classLine;
        if (!targetUri) {
          vscode.window.showInformationMessage(
            `No source location is available for ${selectedService.service.id}.`,
          );
          return;
        }
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(targetUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(
          Math.max(0, (targetLine ?? 1) - 1),
          0,
        );
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to locate Symfony service: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseTwigExtensions',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const extensions = await client.sendRequest<
          TwigExtensionCatalogEntry[]
        >(
          'shopware/symfony/analytics/twig/extensions',
          {},
        );
        if (extensions.length === 0) {
          vscode.window.showInformationMessage(
            'No indexed Twig extensions were found.',
          );
          return;
        }
        const icon = (type: TwigExtensionCatalogEntry['type']): string => {
          switch (type) {
            case 'filter':
              return 'filter';
            case 'function':
              return 'symbol-function';
            case 'test':
              return 'beaker';
            default:
              return 'symbol-keyword';
          }
        };
        const items = extensions.map(extension => ({
          label: `$(${icon(extension.type)}) ${extension.name}`,
          description: [
            extension.type,
            extension.deprecated ? 'deprecated' : '',
          ].filter(Boolean).join(' · '),
          detail: [
            extension.usage,
            extension.className && extension.methodName
              ? `${extension.className}::${extension.methodName}`
              : extension.className ?? extension.callable,
            extension.deprecation,
          ].filter(Boolean).join(' · '),
          extension,
        }));
        const selected = await vscode.window.showQuickPick(items, {
          title: 'Browse Twig Extensions',
          placeHolder: 'Search functions, filters, tests, and tags',
          matchOnDescription: true,
          matchOnDetail: true,
        });
        if (!selected) {
          return;
        }
        if (!selected.extension.fileUri) {
          vscode.window.showInformationMessage(
            `No source location is available for ${selected.extension.name}.`,
          );
          return;
        }
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(selected.extension.fileUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const line = Math.max(
          0,
          (selected.extension.sourceLine ?? 1) - 1,
        );
        const position = new vscode.Position(line, 0);
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load Twig extensions: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.analyzeTwigTemplateUsages',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      const template = await vscode.window.showInputBox({
        title: 'Analyze Twig Template Usages',
        prompt: 'Enter a logical template name or project-relative path',
        placeHolder: 'templates/home/index.html.twig',
      });
      if (!template?.trim()) {
        return;
      }
      try {
        const entries = await client.sendRequest<
          TwigTemplateUsageCatalogEntry[]
        >(
          'shopware/symfony/analytics/twig/templateUsages',
          {template: template.trim()},
        );
        if (entries.length === 0) {
          vscode.window.showInformationMessage(
            `No indexed Twig templates matched ${template.trim()}.`,
          );
          return;
        }
        let selectedEntry = entries[0];
        if (entries.length > 1) {
          const selected = await vscode.window.showQuickPick(
            entries.map(entry => ({
              label: `$(file-code) ${entry.template}`,
              description: `${entry.files?.length ?? 0} declaration${
                entry.files?.length === 1 ? '' : 's'
              }`,
              entry,
            })),
            {
              title: 'Select Twig Template',
              matchOnDescription: true,
            },
          );
          if (!selected) {
            return;
          }
          selectedEntry = selected.entry;
        }
        const locations: Array<{
          label: string;
          description: string;
          detail: string;
          fileUri: string;
          line: number;
        }> = [];
        for (const location of selectedEntry.files ?? []) {
          locations.push({
            label: `$(file-code) ${selectedEntry.template}`,
            description: 'declaration',
            detail: location.fileUri,
            fileUri: location.fileUri,
            line: location.line ?? 1,
          });
        }
        for (const controller of selectedEntry.controllers ?? []) {
          if (!controller.fileUri) {
            continue;
          }
          const routes = (controller.routes ?? [])
            .map(route => `${route.name} ${route.path ?? ''}`.trim())
            .join(', ');
          locations.push({
            label: `$(symbol-method) ${controller.controller}`,
            description: 'controller',
            detail: routes || controller.fileUri,
            fileUri: controller.fileUri,
            line: controller.line ?? 1,
          });
        }
        const appendReferences = (
          kind: string,
          icon: string,
          usages: TwigTemplateReferenceUsage[] | undefined,
        ): void => {
          for (const usage of usages ?? []) {
            locations.push({
              label: `$(${icon}) ${kind}`,
              description: selectedEntry.template,
              detail: usage.fileUri,
              fileUri: usage.fileUri,
              line: usage.line ?? 1,
            });
          }
        };
        appendReferences('include', 'references', selectedEntry.includes);
        appendReferences('embed', 'references', selectedEntry.embeds);
        appendReferences('extends', 'arrow-up', selectedEntry.extends);
        appendReferences('import', 'package', selectedEntry.imports);
        appendReferences('use', 'symbol-interface', selectedEntry.uses);
        appendReferences(
          'form theme',
          'symbol-color',
          selectedEntry.formThemes,
        );
        for (const usage of selectedEntry.components ?? []) {
          locations.push({
            label: `$(symbol-structure) ${usage.component}`,
            description: usage.syntax ?? 'component',
            detail: usage.fileUri,
            fileUri: usage.fileUri,
            line: usage.line ?? 1,
          });
        }
        if (locations.length === 0) {
          vscode.window.showInformationMessage(
            `No source locations are available for ${selectedEntry.template}.`,
          );
          return;
        }
        const selectedLocation = await vscode.window.showQuickPick(
          locations,
          {
            title: `Usages of ${selectedEntry.template}`,
            placeHolder: 'Select a declaration or usage',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedLocation) {
          return;
        }
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(selectedLocation.fileUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(
          Math.max(0, selectedLocation.line - 1),
          0,
        );
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to analyze Twig template usages: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseTwigComponents',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const components = await client.sendRequest<
          TwigComponentCatalogEntry[]
        >(
          'shopware/symfony/analytics/twig/components',
          {},
        );
        if (components.length === 0) {
          vscode.window.showInformationMessage(
            'No indexed Twig components were found.',
          );
          return;
        }
        const selectedComponent = await vscode.window.showQuickPick(
          components.map(component => ({
            label: `$(symbol-structure) ${component.name}`,
            description: [
              component.declarations?.some(item => item.live)
                ? 'live'
                : '',
              `${component.props?.length ?? 0} props`,
              `${component.blocks?.length ?? 0} blocks`,
            ].filter(Boolean).join(' · '),
            detail: [
              component.declarations
                ?.map(item => item.class || item.source)
                .filter(Boolean)
                .join(', '),
              component.syntax.htmlTag,
            ].filter(Boolean).join(' · '),
            component,
          })),
          {
            title: 'Browse Twig Components',
            placeHolder: 'Search by component, class, prop, or syntax',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedComponent) {
          return;
        }
        const locations: Array<{
          label: string;
          description: string;
          detail: string;
          fileUri: string;
          line: number;
        }> = [];
        for (const declaration of
          selectedComponent.component.declarations ?? []) {
          if (!declaration.fileUri) {
            continue;
          }
          locations.push({
            label: `$(symbol-class) ${
              declaration.class || selectedComponent.component.name
            }`,
            description: declaration.source,
            detail: declaration.template ?? declaration.templateFromMethod ?? '',
            fileUri: declaration.fileUri,
            line: declaration.sourceLine ?? 1,
          });
        }
        for (const template of selectedComponent.component.templates ?? []) {
          locations.push({
            label: `$(file-code) ${
              template.template || selectedComponent.component.name
            }`,
            description: 'template',
            detail: template.fileUri,
            fileUri: template.fileUri,
            line: 1,
          });
        }
        const appendProps = (
          kind: string,
          props: TwigComponentPropEntry[] | undefined,
        ): void => {
          for (const prop of props ?? []) {
            if (!prop.fileUri) {
              continue;
            }
            locations.push({
              label: `$(symbol-property) ${prop.name}`,
              description: [kind, prop.type].filter(Boolean).join(' · '),
              detail: [
                prop.writable ? 'writable' : '',
                prop.defaultValue,
                prop.description,
              ].filter(Boolean).join(' · '),
              fileUri: prop.fileUri,
              line: prop.sourceLine ?? 1,
            });
          }
        };
        appendProps('prop', selectedComponent.component.props);
        appendProps('computed', selectedComponent.component.computed);
        for (const block of selectedComponent.component.blocks ?? []) {
          if (!block.fileUri) {
            continue;
          }
          locations.push({
            label: `$(symbol-event) ${block.name}`,
            description: 'block',
            detail: block.print,
            fileUri: block.fileUri,
            line: block.line ?? 1,
          });
        }
        for (const usage of selectedComponent.component.usages ?? []) {
          locations.push({
            label: `$(references) ${usage.syntax}`,
            description: 'usage',
            detail: usage.fileUri,
            fileUri: usage.fileUri,
            line: usage.line ?? 1,
          });
        }
        if (locations.length === 0) {
          vscode.window.showInformationMessage(
            `No source locations are available for ${
              selectedComponent.component.name
            }.`,
          );
          return;
        }
        const selectedLocation = await vscode.window.showQuickPick(
          locations,
          {
            title: selectedComponent.component.name,
            placeHolder: 'Select a declaration, prop, block, or usage',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedLocation) {
          return;
        }
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(selectedLocation.fileUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
        });
        const position = new vscode.Position(
          Math.max(0, selectedLocation.line - 1),
          0,
        );
        editor.selection = new vscode.Selection(position, position);
        editor.revealRange(
          new vscode.Range(position, position),
          vscode.TextEditorRevealType.InCenterIfOutsideViewport,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to load Twig components: ${error}`,
        );
      }
    },
  ));

  registerTwigVariableCommands(context, () => client);

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.createScaffold',
    async (
      resource?: vscode.Uri,
      selectedResources?: vscode.Uri[],
    ) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      const scaffold = await vscode.window.showQuickPick([
        {
          label: 'Command',
          description: 'Symfony Console command',
          scaffoldKind: 'command',
          placeHolder: 'CacheWarm',
        },
        {
          label: 'Controller',
          description: 'Controller with an index route',
          scaffoldKind: 'controller',
          placeHolder: 'Product',
        },
        {
          label: 'Form Type',
          description: 'Symfony form type',
          scaffoldKind: 'form',
          placeHolder: 'ProductType',
        },
        {
          label: 'Twig Extension',
          description: 'Twig functions and filters',
          scaffoldKind: 'twig-extension',
          placeHolder: 'PriceExtension',
        },
        {
          label: 'Compiler Pass',
          description: 'Dependency-injection compiler pass',
          scaffoldKind: 'compiler-pass',
          placeHolder: 'CollectServicesPass',
        },
        {
          label: 'Kernel Test',
          description: 'KernelTestCase integration test',
          scaffoldKind: 'kernel-test',
          placeHolder: 'Container',
        },
        {
          label: 'Web Test',
          description: 'WebTestCase functional test',
          scaffoldKind: 'web-test',
          placeHolder: 'Storefront',
        },
        {
          label: 'YAML Service Configuration',
          description: 'Autowiring service prototype',
          scaffoldKind: 'services-yaml',
          placeHolder: 'services',
        },
        {
          label: 'XML Service Configuration',
          description: 'Autowiring service prototype',
          scaffoldKind: 'services-xml',
          placeHolder: 'services',
        },
        {
          label: 'PHP Service Configuration',
          description: 'Fluent service configurator',
          scaffoldKind: 'services-php',
          placeHolder: 'services',
        },
      ], {
        title: 'Symfony: New File',
        placeHolder: 'Select a Symfony file type',
        matchOnDescription: true,
      });
      if (!scaffold) {
        return;
      }

      const candidate = selectedResources?.[0] ?? resource;
      let directoryUri: vscode.Uri | undefined;
      if (candidate?.scheme === 'file') {
        try {
          if (fs.statSync(candidate.fsPath).isDirectory()) {
            directoryUri = candidate;
          }
        } catch {
          // Fall through to the directory picker.
        }
      }
      if (!directoryUri) {
        let defaultUri: vscode.Uri | undefined;
        const activeUri = vscode.window.activeTextEditor?.document.uri;
        if (activeUri?.scheme === 'file') {
          defaultUri = vscode.Uri.file(path.dirname(activeUri.fsPath));
        } else {
          defaultUri = vscode.workspace.workspaceFolders?.[0]?.uri;
        }
        const selected = await vscode.window.showOpenDialog({
          title: `Select the directory for the new ${scaffold.label}`,
          defaultUri,
          canSelectFiles: false,
          canSelectFolders: true,
          canSelectMany: false,
          openLabel: 'Use Directory',
        });
        directoryUri = selected?.[0];
      }
      if (!directoryUri) {
        return;
      }

      const serviceConfiguration = scaffold.scaffoldKind.startsWith(
        'services-',
      );
      const name = await vscode.window.showInputBox({
        title: `Symfony: New ${scaffold.label}`,
        prompt: serviceConfiguration
          ? 'Configuration file name (without the format extension)'
          : 'PHP class name (without namespace or generated suffix)',
        placeHolder: scaffold.placeHolder,
        value: serviceConfiguration ? 'services' : undefined,
        validateInput: value => {
          const normalized = value.trim();
          if (normalized === '') {
            return serviceConfiguration
              ? 'File name cannot be empty'
              : 'Class name cannot be empty';
          }
          if (serviceConfiguration) {
            return /^[A-Za-z0-9_.-]+$/.test(normalized)
              ? null
              : 'Use only letters, digits, dots, dashes, and underscores';
          }
          return /^[A-Za-z_\u0080-\uFFFF][A-Za-z0-9_\u0080-\uFFFF]*$/.test(
            normalized,
          )
            ? null
            : 'Enter a valid PHP class name without a namespace';
        },
      });
      if (!name) {
        return;
      }

      try {
        const result = await client.sendRequest<SymfonyScaffoldCreation>(
          'shopware/symfony/scaffold/create',
          {
            kind: scaffold.scaffoldKind,
            directoryUri: directoryUri.toString(),
            name: name.trim(),
          },
        );
        const fileUri = vscode.Uri.parse(result.fileUri);
        const edit = new vscode.WorkspaceEdit();
        edit.createFile(fileUri, {
          ignoreIfExists: false,
          overwrite: false,
        });
        edit.insert(fileUri, new vscode.Position(0, 0), result.content);
        if (!await vscode.workspace.applyEdit(edit)) {
          vscode.window.showErrorMessage(
            `Could not create ${path.basename(fileUri.fsPath)}`,
          );
          return;
        }
        const document = await vscode.workspace.openTextDocument(fileUri);
        await vscode.window.showTextDocument(document, {
          preview: false,
          preserveFocus: false,
        });
        vscode.window.showInformationMessage(
          `Created ${path.basename(fileUri.fsPath)}`,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to create Symfony file: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.createScaffold',
    async (
      resource?: vscode.Uri,
      selectedResources?: vscode.Uri[],
    ) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      const scaffold = await vscode.window.showQuickPick([
        {
          label: 'System Configuration',
          description: 'Resources/config/config.xml',
          scaffoldKind: 'system-config',
          placeHolder: 'configuration',
        },
        {
          label: 'Scheduled Task',
          description: 'Task and handler PHP classes',
          scaffoldKind: 'scheduled-task',
          placeHolder: 'Cleanup',
        },
        {
          label: 'Migration',
          description: 'Timestamped Shopware migration',
          scaffoldKind: 'migration',
          placeHolder: 'AddProductIndex',
        },
        {
          label: 'App Custom Entities',
          description: 'Resources/entities.xml',
          scaffoldKind: 'app-custom-entities',
          placeHolder: 'catalog-entry',
        },
        {
          label: 'App CMS Configuration',
          description: 'Resources/cms.xml',
          scaffoldKind: 'app-cms',
          placeHolder: 'cms',
        },
        {
          label: 'App Script',
          description: 'Twig script hook',
          scaffoldKind: 'app-script',
          placeHolder: 'product-page-loaded',
        },
        {
          label: 'Administration Component',
          description: 'JavaScript, Twig, and SCSS component files',
          scaffoldKind: 'admin-component',
          placeHolder: 'sw-example-card',
        },
        {
          label: 'Administration Module',
          description: 'Module registration and translations',
          scaffoldKind: 'admin-module',
          placeHolder: 'sw-example',
        },
        {
          label: 'CMS Block',
          description: 'Administration block and preview components',
          scaffoldKind: 'cms-block',
          placeHolder: 'example-text',
        },
        {
          label: 'CMS Element',
          description: 'Administration element components',
          scaffoldKind: 'cms-element',
          placeHolder: 'example-media',
        },
        {
          label: 'Plugin Skeleton',
          description: 'Minimal composer package and plugin class',
          scaffoldKind: 'plugin',
          placeHolder: 'AcmeExample',
        },
        {
          label: 'App Manifest',
          description: 'Minimal Shopware app manifest',
          scaffoldKind: 'app',
          placeHolder: 'acme-example',
        },
      ], {
        title: 'Shopware: New File or Feature',
        placeHolder: 'Select a Shopware artifact',
        matchOnDescription: true,
      });
      if (!scaffold) {
        return;
      }

      const candidate = selectedResources?.[0] ?? resource;
      let directoryUri: vscode.Uri | undefined;
      if (candidate?.scheme === 'file') {
        try {
          if (fs.statSync(candidate.fsPath).isDirectory()) {
            directoryUri = candidate;
          }
        } catch {
          // Fall through to the directory picker.
        }
      }
      if (!directoryUri) {
        const activeUri = vscode.window.activeTextEditor?.document.uri;
        const defaultUri = activeUri?.scheme === 'file'
          ? vscode.Uri.file(path.dirname(activeUri.fsPath))
          : vscode.workspace.workspaceFolders?.[0]?.uri;
        const selected = await vscode.window.showOpenDialog({
          title: `Select the directory for the new ${scaffold.label}`,
          defaultUri,
          canSelectFiles: false,
          canSelectFolders: true,
          canSelectMany: false,
          openLabel: 'Use Directory',
        });
        directoryUri = selected?.[0];
      }
      if (!directoryUri) {
        return;
      }

      const name = await vscode.window.showInputBox({
        title: `Shopware: New ${scaffold.label}`,
        prompt: 'Artifact name (letters, digits, dashes, and underscores)',
        placeHolder: scaffold.placeHolder,
        value: scaffold.scaffoldKind === 'system-config' ? 'configuration' :
          scaffold.scaffoldKind === 'app-cms' ? 'cms' : undefined,
        validateInput: value => /^[A-Za-z][A-Za-z0-9_-]*$/.test(value.trim())
          ? null
          : 'Start with a letter and use letters, digits, dashes, or underscores',
      });
      if (!name) {
        return;
      }

      const options: Record<string, string | number | boolean> = {};
      if (scaffold.scaffoldKind === 'scheduled-task') {
        const interval = await vscode.window.showInputBox({
          title: 'Scheduled Task Interval',
          prompt: 'Default interval in seconds',
          value: '300',
          validateInput: value => /^\d+$/.test(value) && Number(value) > 0
            ? null
            : 'Enter a positive number of seconds',
        });
        if (!interval) {
          return;
        }
        options.interval = Number(interval);
      }
      if (scaffold.scaffoldKind === 'app-script') {
        const hook = await vscode.window.showInputBox({
          title: 'App Script Hook',
          prompt: 'Hook name exposed by Shopware',
          value: name.trim(),
        });
        if (!hook?.trim()) {
          return;
        }
        options.hook = hook.trim();
      }
      if (scaffold.scaffoldKind === 'cms-block') {
        const category = await vscode.window.showInputBox({
          title: 'CMS Block Category',
          prompt: 'Administration CMS category',
          value: 'text',
        });
        if (!category?.trim()) {
          return;
        }
        options.category = category.trim();
      }

      try {
        const result = await client.sendRequest<ShopwareScaffoldCreation>(
          'shopware/scaffold/create',
          {
            kind: scaffold.scaffoldKind,
            directoryUri: directoryUri.toString(),
            name: name.trim(),
            options,
          },
        );
        const edit = await client.protocol2CodeConverter.asWorkspaceEdit(
          result.edit,
        );
        if (!await vscode.workspace.applyEdit(edit)) {
          vscode.window.showErrorMessage(
            `Could not create ${scaffold.label}`,
          );
          return;
        }
        const primaryUri = vscode.Uri.parse(result.primaryFileUri);
        const document = await vscode.workspace.openTextDocument(primaryUri);
        await vscode.window.showTextDocument(document, {
          preview: false,
          preserveFocus: false,
        });
        const version = result.shopwareVersion
          ? ` for Shopware ${result.shopwareVersion}`
          : '';
        vscode.window.showInformationMessage(
          `Created ${scaffold.label}${version}`,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to create Shopware scaffold: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.insertUuid',
    async () => {
      const editor = vscode.window.activeTextEditor;
      if (!editor) {
        return;
      }
      await editor.edit(builder => {
        for (const selection of editor.selections) {
          const uuid = crypto.randomUUID().replace(/-/g, '');
          builder.replace(selection, uuid);
        }
      });
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.admin.extendComponent',
    async (component: string, sourceUri?: string) => {
      try {
        await createAdminComponentExtension(component, sourceUri);
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to extend Administration component: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.admin.overrideMethod',
    async (
      component: string,
      method: string,
      methodGroup: string,
      parameters: string,
      sourceUri?: string,
    ) => {
      try {
        await createAdminComponentExtension(
          component,
          sourceUri,
          method,
          methodGroup,
          parameters,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to override Administration method: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.createEventListener',
    async (
      eventClass: string,
      suggestedName: string,
      sourceUri?: string,
    ) => {
      const directory = await chooseShopwareTargetDirectory(
        'Select the EventListener directory',
        sourceUri,
      );
      if (!directory) {
        return;
      }
      const name = await vscode.window.showInputBox({
        title: 'Create Shopware Event Listener',
        prompt: `Listener class for ${eventClass}`,
        value: suggestedName,
        validateInput: value => /^[A-Za-z_][A-Za-z0-9_]*$/.test(value.trim())
          ? null
          : 'Enter a valid PHP class name',
      });
      if (!name) {
        return;
      }
      try {
        await applyShopwareScaffoldCreation({
          kind: 'event-listener',
          directoryUri: directory.toString(),
          name: name.trim(),
          options: {event: eventClass},
        }, name.trim());
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to create event listener: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.copySnippetUsage',
    async (key: string) => {
      const target = await vscode.window.showQuickPick([
        {
          label: 'Administration Twig',
          code: `{{ $tc('${key}') }}`,
        },
        {
          label: 'Administration JavaScript',
          code: `this.$tc('${key}')`,
        },
        {
          label: 'Storefront Twig',
          code: `{{ '${key}'|trans }}`,
        },
      ], {
        title: `Copy usage for ${key}`,
      });
      if (!target) {
        return;
      }
      await vscode.env.clipboard.writeText(target.code);
      vscode.window.showInformationMessage(
        `Copied ${target.label} snippet usage`,
      );
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateService',
    async (fileUri: string, className: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      const output = await vscode.window.showQuickPick([
        {label: 'YAML', value: 'yaml', language: 'yaml'},
        {label: 'XML', value: 'xml', language: 'xml'},
        {label: 'PHP Fluent Configurator', value: 'fluent', language: 'php'},
        {label: 'PHP Array', value: 'php-array', language: 'php'},
      ], {
        title: 'Generate Symfony service',
        placeHolder: 'Select the service-definition format',
      });
      if (!output) {
        return;
      }

      const idStyle = await vscode.window.showQuickPick([
        {
          label: 'Class name',
          description: className,
          classAsId: true,
        },
        {
          label: 'Custom service ID',
          description: 'Choose a container service ID',
          classAsId: false,
        },
      ], {
        title: 'Generate Symfony service',
        placeHolder: 'Select the service ID style',
      });
      if (!idStyle) {
        return;
      }

      let serviceId = className;
      if (!idStyle.classAsId) {
        serviceId = await vscode.window.showInputBox({
          title: 'Generate Symfony service',
          prompt: `Service ID for ${className}`,
          value: className.toLowerCase().replace(/\\/g, '_'),
          validateInput: value => value.trim() === ''
            ? 'Service ID cannot be empty'
            : null,
        }) ?? '';
        if (serviceId === '') {
          return;
        }
      }

      try {
        const sourceDocument = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(fileUri),
        );
        const result = await client.sendRequest<SymfonyServiceGeneration>(
          'shopware/symfony/service/generate',
          {
            className,
            output: output.value,
            classAsId: idStyle.classAsId,
            serviceId,
            fileUri,
            source: sourceDocument.getText(),
            version: sourceDocument.version,
          },
        );
        const document = await vscode.workspace.openTextDocument({
          language: result.language || output.language,
          content: result.content,
        });
        await vscode.window.showTextDocument(document, {
          preview: true,
          preserveFocus: false,
        });
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to generate Symfony service: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateServiceDefinitions',
    async () => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      const classNames = await vscode.window.showInputBox({
        title: 'Generate Symfony Service Definitions',
        prompt: 'Enter one or more comma-separated PHP classes',
        placeHolder: 'App\\Service\\Catalog, App\\Service\\Checkout',
        ignoreFocusOut: true,
        validateInput: value => value.split(',')
          .map(item => item.trim())
          .filter(Boolean)
          .length === 0
          ? 'Enter at least one PHP class'
          : null,
      });
      if (!classNames) {
        return;
      }
      const output = await vscode.window.showQuickPick([
        {label: 'YAML', value: 'yaml', language: 'yaml'},
        {label: 'XML', value: 'xml', language: 'xml'},
        {label: 'PHP Fluent Configurator', value: 'fluent', language: 'php'},
        {label: 'PHP Array', value: 'php-array', language: 'php'},
      ], {
        title: 'Generate Symfony Service Definitions',
        placeHolder: 'Select the service-definition format',
      });
      if (!output) {
        return;
      }
      const idStyle = await vscode.window.showQuickPick([
        {
          label: 'Class names as IDs',
          description: 'Recommended for modern Symfony projects',
          classAsId: true,
        },
        {
          label: 'Generated string IDs',
          description: 'Generate lowercase underscore-separated IDs',
          classAsId: false,
        },
      ], {
        title: 'Generate Symfony Service Definitions',
        placeHolder: 'Select the service ID style',
      });
      if (!idStyle) {
        return;
      }
      try {
        const result = await client.sendRequest<
          SymfonyServiceDefinitionCollection
        >(
          'shopware/symfony/analytics/services/generateDefinitions',
          {
            classNames,
            output: output.value,
            classAsId: idStyle.classAsId,
          },
        );
        const generated = result.definitions.filter(
          definition => Boolean(definition.content),
        );
        const failed = result.definitions.filter(
          definition => Boolean(definition.error),
        );
        if (failed.length > 0) {
          vscode.window.showWarningMessage(
            `Could not generate ${failed.map(
              definition => definition.className,
            ).join(', ')}: ${failed.map(
              definition => definition.error,
            ).join('; ')}`,
          );
        }
        if (generated.length === 0) {
          return;
        }
        const separator = output.value === 'xml'
          ? '\n\n<!-- --- -->\n\n'
          : output.language === 'php'
            ? '\n\n// ---\n\n'
            : '\n\n# ---\n\n';
        const document = await vscode.workspace.openTextDocument({
          language: generated[0].language || output.language,
          content: generated
            .map(definition => definition.content ?? '')
            .join(separator),
        });
        await vscode.window.showTextDocument(document, {
          preview: true,
          preserveFocus: false,
        });
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to generate Symfony service definitions: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.createCompilerPass',
    async (bundleUri: string, bundleClass: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      const className = await vscode.window.showInputBox({
        title: 'Symfony: Create CompilerPass',
        prompt: 'Class name for the compiler pass (without namespace)',
        placeHolder: 'CollectTaggedServicesPass',
        validateInput: value => {
          if (value.trim() === '') {
            return 'Class name cannot be empty';
          }
          if (!/^[A-Za-z_\u0080-\uFFFF][A-Za-z0-9_\u0080-\uFFFF]*$/.test(value)) {
            return 'Enter a valid PHP class name without a namespace';
          }
          return null;
        },
      });
      if (!className) {
        return;
      }

      try {
        const uri = vscode.Uri.parse(bundleUri);
        const bundleDocument = await vscode.workspace.openTextDocument(uri);
        const result = await client.sendRequest<CompilerPassCreation>(
          'shopware/symfony/compilerPass/create',
          {
            bundleUri,
            bundleClass,
            className,
            source: bundleDocument.getText(),
            version: bundleDocument.version,
          },
        );

        const compilerPassUri = vscode.Uri.parse(result.fileUri);
        const edit = new vscode.WorkspaceEdit();
        edit.createFile(compilerPassUri, {
          ignoreIfExists: false,
          overwrite: false,
        });
        edit.insert(
          compilerPassUri,
          new vscode.Position(0, 0),
          result.fileContent,
        );
        edit.replace(
          bundleDocument.uri,
          new vscode.Range(
            new vscode.Position(0, 0),
            bundleDocument.positionAt(bundleDocument.getText().length),
          ),
          result.bundleContent,
        );

        if (!await vscode.workspace.applyEdit(edit)) {
          vscode.window.showErrorMessage(
            'Could not apply the compiler-pass workspace edit',
          );
          return;
        }
        const compilerPassDocument = await vscode.workspace.openTextDocument(
          compilerPassUri,
        );
        await vscode.window.showTextDocument(compilerPassDocument);
        vscode.window.showInformationMessage(
          `Created Symfony compiler pass ${className}`,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to create Symfony compiler pass: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateFormFields',
    async (fileUri: string, className: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      try {
        const uri = vscode.Uri.parse(fileUri);
        let document = await vscode.workspace.openTextDocument(uri);
        const candidates = await client.sendRequest<FormFieldCandidates>(
          'shopware/symfony/form/fields/candidates',
          {
            fileUri,
            className,
            source: document.getText(),
            version: document.version,
          },
        );
        if (candidates.fields.length === 0) {
          vscode.window.showInformationMessage(
            `No missing writable fields found on ${candidates.dataClass}`,
          );
          return;
        }

        const selected = await vscode.window.showQuickPick(
          candidates.fields.map(field => ({
            label: field.name,
            description: field.phpType,
            detail: field.suggestedType
              ? `Generate ${field.suggestedType}`
              : 'Generate using Symfony type inference',
            field,
          })),
          {
            title: `Symfony: Select fields for ${candidates.dataClass}`,
            placeHolder: 'Select one or more form fields',
            canPickMany: true,
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selected || selected.length === 0) {
          return;
        }

        document = await vscode.workspace.openTextDocument(uri);
        const generated = await client.sendRequest<FormFieldGeneration>(
          'shopware/symfony/form/fields/generate',
          {
            fileUri,
            className,
            source: document.getText(),
            version: document.version,
            selectedFields: selected.map(item => item.field.name),
          },
        );
        const edit = new vscode.WorkspaceEdit();
        edit.replace(
          uri,
          new vscode.Range(
            new vscode.Position(0, 0),
            document.positionAt(document.getText().length),
          ),
          generated.content,
        );
        if (!await vscode.workspace.applyEdit(edit)) {
          vscode.window.showErrorMessage(
            'Could not apply the generated form fields',
          );
          return;
        }
        vscode.window.showInformationMessage(
          `Generated ${selected.length} Symfony form field${selected.length === 1 ? '' : 's'}`,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to generate Symfony form fields: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateTwigFormFields',
    async (fileUri: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      try {
        const candidates = await client.sendRequest<TwigFormFieldCandidates>(
          'shopware/symfony/twig/form/fields/candidates',
          {fileUri},
        );
        if (candidates.forms.length === 0) {
          vscode.window.showInformationMessage(
            'No controller-backed Twig form variables were found',
          );
          return;
        }

        let selectedForm = candidates.forms[0];
        if (candidates.forms.length > 1) {
          const selection = await vscode.window.showQuickPick(
            candidates.forms.map(form => ({
              label: form.variable,
              description: form.formType,
              detail: `${form.fields.length} form fields`,
              form,
            })),
            {
              title: 'Symfony: Select Twig form',
              placeHolder: 'Select a template variable and FormType',
              matchOnDescription: true,
              matchOnDetail: true,
            },
          );
          if (!selection) {
            return;
          }
          selectedForm = selection.form;
        }

        const fields = await vscode.window.showQuickPick(
          selectedForm.fields.map(field => ({label: field})),
          {
            title: `Symfony: Select fields for ${selectedForm.variable}`,
            placeHolder: 'Select one or more form rows',
            canPickMany: true,
          },
        );
        if (!fields || fields.length === 0) {
          return;
        }
        const generated = await client.sendRequest<FormFieldGeneration>(
          'shopware/symfony/twig/form/fields/generate',
          {
            fileUri,
            variable: selectedForm.variable,
            formType: selectedForm.formType,
            selectedFields: fields.map(field => field.label),
          },
        );
        const document = await vscode.workspace.openTextDocument(
          vscode.Uri.parse(fileUri),
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
          preserveFocus: false,
        });
        await editor.insertSnippet(
          new vscode.SnippetString(generated.content),
          editor.selection.active,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to generate Twig form rows: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateTwigExtends',
    async (fileUri: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        const document = await vscode.workspace.openTextDocument(uri);
        const candidates = await client.sendRequest<TwigTemplateCandidates>(
          'shopware/symfony/twig/extends/candidates',
          {
            fileUri,
            source: document.getText(),
          },
        );
        const selected = await vscode.window.showQuickPick(
          candidates.templates.map(template => ({label: template})),
          {
            title: 'Symfony: Twig Extends',
            placeHolder: 'Select a parent template',
          },
        );
        if (!selected) {
          return;
        }
        const generated = await client.sendRequest<FormFieldGeneration>(
          'shopware/symfony/twig/extends/generate',
          {
            fileUri,
            source: document.getText(),
            template: selected.label,
          },
        );
        const edit = new vscode.WorkspaceEdit();
        edit.insert(uri, new vscode.Position(0, 0), generated.content);
        if (!await vscode.workspace.applyEdit(edit)) {
          vscode.window.showErrorMessage(
            'Could not insert the Twig extends declaration',
          );
        }
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to generate Twig extends: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateTwigBlocks',
    async (fileUri: string) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        const document = await vscode.workspace.openTextDocument(uri);
        const candidates = await client.sendRequest<TwigBlockCandidates>(
          'shopware/symfony/twig/blocks/candidates',
          {
            fileUri,
            source: document.getText(),
          },
        );
        const selected = await vscode.window.showQuickPick(
          candidates.blocks.map(block => ({label: block})),
          {
            title: `Symfony: Twig Blocks from ${candidates.parent}`,
            placeHolder: 'Select one or more blocks to override',
            canPickMany: true,
          },
        );
        if (!selected || selected.length === 0) {
          return;
        }
        const generated = await client.sendRequest<FormFieldGeneration>(
          'shopware/symfony/twig/blocks/generate',
          {
            fileUri,
            source: document.getText(),
            selectedBlocks: selected.map(block => block.label),
          },
        );
        const editor = await vscode.window.showTextDocument(document, {
          preview: false,
          preserveFocus: false,
        });
        await editor.insertSnippet(
          new vscode.SnippetString(generated.content),
          editor.selection.active,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to generate Twig block overrides: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.extractTwigTranslation',
    async (fileUri: string, selectedRange: LspRange) => {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        let document = await vscode.workspace.openTextDocument(uri);
        const prepared = await client.sendRequest<TwigTranslationExtractionPreparation>(
          'shopware/symfony/translation/extract/prepare',
          {
            fileUri,
            source: document.getText(),
            range: selectedRange,
          },
        );
        const key = await vscode.window.showInputBox({
          title: 'Symfony: Extract Twig Translation',
          prompt: `Translation key for “${prepared.text}”`,
          value: prepared.defaultKey ?? '',
          placeHolder: 'page.section.label',
          validateInput: value => {
            if (value.trim() === '') {
              return 'Translation key cannot be empty';
            }
            if (/[\r\n\u0000]/.test(value)) {
              return 'Translation key must stay on one line';
            }
            return null;
          },
        });
        if (!key) {
          return;
        }
        const domains = prepared.domains
          .map(domain => ({
            label: domain,
            description: domain.toLowerCase() === prepared.defaultDomain.toLowerCase()
              ? 'active Twig domain'
              : undefined,
          }))
          .sort((left, right) => {
            if (left.description && !right.description) {
              return -1;
            }
            if (!left.description && right.description) {
              return 1;
            }
            return left.label.localeCompare(right.label);
          });
        const selectedDomain = await vscode.window.showQuickPick(domains, {
          title: 'Symfony: Translation Domain',
          placeHolder: 'Select the translation domain',
          matchOnDescription: true,
        });
        if (!selectedDomain) {
          return;
        }

        document = await vscode.workspace.openTextDocument(uri);
        const generated = await client.sendRequest<TwigTranslationExtractionEdits>(
          'shopware/symfony/translation/extract/generate',
          {
            fileUri,
            source: document.getText(),
            range: prepared.range,
            key: key.trim(),
            domain: selectedDomain.label,
          },
        );
        const targetItems = generated.targets.map(target => ({
          label: target.locale
            ? `${target.locale} — ${target.file}`
            : target.file,
          description: target.format.toUpperCase(),
          detail: vscode.workspace.asRelativePath(
            vscode.Uri.parse(target.fileUri),
            false,
          ),
          picked: true,
          target,
        }));
        const selectedTargets = await vscode.window.showQuickPick(
          targetItems,
          {
            title: 'Symfony: Translation Files',
            placeHolder: 'Select one or more locale files to update',
            canPickMany: true,
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedTargets || selectedTargets.length === 0) {
          return;
        }

        const edit = new vscode.WorkspaceEdit();
        edit.replace(
          uri,
          new vscode.Range(
            generated.range.start.line,
            generated.range.start.character,
            generated.range.end.line,
            generated.range.end.character,
          ),
          generated.replacement,
        );
        for (const item of selectedTargets) {
          const target = item.target;
          edit.insert(
            vscode.Uri.parse(target.fileUri),
            new vscode.Position(target.line, target.character),
            target.newText,
          );
        }
        if (!await vscode.workspace.applyEdit(edit)) {
          vscode.window.showErrorMessage(
            'Could not apply the Twig translation extraction',
          );
          return;
        }
        vscode.window.showInformationMessage(
          `Extracted Twig translation “${key.trim()}”`,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to extract Twig translation: ${error}`,
        );
      }
    },
  ));

  // Register open references command
  context.subscriptions.push(vscode.commands.registerCommand('shopware.openReferences', async (references: string[]) => {
    if (!references || references.length === 0) {
      vscode.window.showInformationMessage('No references found');
      return;
    }

    // Create quick pick items from references
    const items = references.map(ref => {
      // Parse the URI and line number from the reference (format: file:///path/to/file.twig#lineNumber)
      const [uri, lineStr] = ref.split('#');
      const line = parseInt(lineStr, 10) - 1; // Convert to 0-based line number
      const filePath = uri.replace('file://', '');
      
      // Extract relative path from workspace root if possible
      let displayPath = filePath;
      const workspaceFolders = vscode.workspace.workspaceFolders;
      if (workspaceFolders && workspaceFolders.length > 0) {
        const workspaceRoot = workspaceFolders[0].uri.fsPath;
        if (filePath.startsWith(workspaceRoot)) {
          displayPath = filePath.substring(workspaceRoot.length + 1); // +1 to remove the leading slash
        }
      }
      
      return {
        label: `$(file) ${path.basename(filePath)}`,
        description: displayPath,
        detail: `Line ${line + 1}`,
        uri,
        line
      };
    });

    // If there's only one reference, directly open it without showing the quick pick
    if (items.length === 1) {
      const item = items[0];
      const document = await vscode.workspace.openTextDocument(vscode.Uri.parse(item.uri));
      const editor = await vscode.window.showTextDocument(document);
      
      // Position at the specified line
      const position = new vscode.Position(item.line, 0);
      editor.selection = new vscode.Selection(position, position);
      editor.revealRange(
        new vscode.Range(position, position),
        vscode.TextEditorRevealType.InCenter
      );
      return;
    }

    // Show quick pick with references when there are multiple
    const selected = await vscode.window.showQuickPick(items, {
      placeHolder: 'Select a reference to open',
      matchOnDescription: true,
      matchOnDetail: true
    });

    if (selected) {
      // Open the selected file and position at the specified line
      const document = await vscode.workspace.openTextDocument(vscode.Uri.parse(selected.uri));
      const editor = await vscode.window.showTextDocument(document);
      
      // Position at the specified line
      const position = new vscode.Position(selected.line, 0);
      editor.selection = new vscode.Selection(position, position);
      editor.revealRange(
        new vscode.Range(position, position),
        vscode.TextEditorRevealType.InCenter
      );
    }
  }));
  
  // Register create snippet command handler
  context.subscriptions.push(vscode.commands.registerCommand('shopware.createSnippet', async (snippetKey: string, fileUri: string) => {
    try {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      
      const result = await client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/storefront/getPossibleSnippetFiles', {
        fileUri,
      });

      if (!result || !result.paths || result.paths.length === 0) {
        vscode.window.showErrorMessage('No snippet files found');
        return;
      }

      const snippetsWithValues = await collectSnippetTranslations(
        result.paths,
        snippetKey,
        'Create Storefront Snippet'
      );

      if (!snippetsWithValues) {
        return; // User cancelled
      }

      await client.sendRequest('shopware/snippet/storefront/create', {
        fileUri,
        snippetKey,
        snippets: snippetsWithValues
      });

      vscode.window.showInformationMessage(`Snippet ${snippetKey} created successfully`);
    } catch (error) {
      vscode.window.showErrorMessage(`Error creating snippet: ${error}`);
    }
  }));

  context.subscriptions.push(vscode.commands.registerCommand('shopware.twig.extendBlock', async (textUri: string, blockName: string) => {
    if (!client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }

    const extensions: { Name: string; }[] = await client.sendRequest('shopware/extension/all');

    if (!extensions || extensions.length === 0) {
      vscode.window.showErrorMessage('No extensions found');
      return;
    }

    const items = extensions.map(ext => ({
      label: ext.Name,
      description: `Extend block in ${ext.Name}`,
      detail: `Block name: ${blockName}`,
    }));
    const selected = await vscode.window.showQuickPick(items, {
      placeHolder: 'Select an extension to extend the block',
      matchOnDescription: true,
      matchOnDetail: true
    });
    if (!selected) {
      vscode.window.showErrorMessage('No extension selected');
      return;
    }

    const result: {code: string, message: string} | {uri: string, line: number} = await client.sendRequest('shopware/twig/extendBlock', {
      textUri,
      blockName,
      extension: selected.label,
    });

    if ('code' in result) {
      vscode.window.showErrorMessage(`Error extending block: ${result.message}`);
      return;
    }

    if ('uri' in result) {
      const document = await vscode.workspace.openTextDocument(vscode.Uri.parse(result.uri));
      const editor = await vscode.window.showTextDocument(document);

      const position = new vscode.Position(result.line, 0);
      editor.selection = new vscode.Selection(position, position);

      vscode.window.showInformationMessage(`Block ${blockName} extended successfully in ${selected.label}`);
    }
  }));

  context.subscriptions.push(vscode.commands.registerCommand('shopware.twig.showBlockDiff', async (textUri: string, blockName: string) => {
    if (!client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }

    try {
      type BlockDiffResponse = {
        blockName: string;
        originalContent: string;
        originalVersion: string;
        currentContent: string;
        currentVersion: string;
      };

      const result: { code: string; message: string } | BlockDiffResponse = await client.sendRequest('shopware/twig/getBlockDiff', {
        textUri,
        blockName,
      });

      if ('code' in result) {
        vscode.window.showErrorMessage(`Block diff error: ${result.message}`);
        return;
      }

      if (!result.originalContent || !result.currentContent) {
        vscode.window.showErrorMessage('Block diff: Missing content in response');
        return;
      }

      const originalUri = vscode.Uri.parse(`shopware-block:original/${blockName}.twig?version=${result.originalVersion}`);
      const currentUri = vscode.Uri.parse(`shopware-block:current/${blockName}.twig?version=${result.currentVersion}`);

      blockContentProvider.setContent(originalUri, result.originalContent);
      blockContentProvider.setContent(currentUri, result.currentContent);

      const title = `Block "${blockName}": ${result.originalVersion} ↔ ${result.currentVersion}`;
      await vscode.commands.executeCommand('vscode.diff', originalUri, currentUri, title);
    } catch (error: unknown) {
      const errorMessage = error instanceof Error ? error.message : String(error);
      vscode.window.showErrorMessage(`Block diff failed: ${errorMessage}`);
    }
  }));

  context.subscriptions.push(vscode.commands.registerCommand('shopware.insertSnippet', async () => {
    if (!client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }

    const editor = vscode.window.activeTextEditor;
    if (!editor) {
      vscode.window.showErrorMessage('No active editor');
      return;
    }

    // Check if we're in an admin twig file
    const filePath = editor.document.uri.fsPath;
    const isAdminFile = filePath.includes('/Resources/app/administration/');

    let snippets: {key: string, text: string, file: string}[];
    let insertFormat: string;

    if (isAdminFile) {
      // Fetch admin snippets
      snippets = await client.sendRequest('shopware/snippet/admin/all');
      insertFormat = "{{ \\$tc('${label}') }}";
    } else {
      // Fetch frontend snippets
      snippets = await client.sendRequest('shopware/snippet/storefront/all');
      insertFormat = "{{ '${label}'|trans }}";
    }

    if (!snippets || snippets.length === 0) {
      vscode.window.showErrorMessage('No snippets found');
      return;
    }

    const items = snippets.map(snippet => ({
      label: snippet.key,
      description: snippet.text,
    }));

    const selected = await vscode.window.showQuickPick(items, {
      placeHolder: 'Select a snippet to insert',
      matchOnDescription: true,
      matchOnDetail: true
    });
    if (!selected) {
      return;
    }

    const text = insertFormat.replace('${label}', selected.label);

    editor.insertSnippet(new vscode.SnippetString(text));
  }));

  // Register programmatic snippet insertion command (used by code actions for cursor positioning)
  context.subscriptions.push(vscode.commands.registerCommand('shopware.editor.insertSnippetAtPosition', async (fileUri: string, line: number, character: number, snippetText: string) => {
    try {
      const document = await vscode.workspace.openTextDocument(vscode.Uri.parse(fileUri));
      const editor = await vscode.window.showTextDocument(document);
      
      // Position the cursor at the insert position
      const position = new vscode.Position(line, character);
      editor.selection = new vscode.Selection(position, position);
      
      // Insert as snippet to support $0 cursor positioning
      await editor.insertSnippet(new vscode.SnippetString(snippetText), position);
    } catch (error) {
      vscode.window.showErrorMessage(`Error adding prop: ${error}`);
    }
  }));

  context.subscriptions.push(vscode.commands.registerCommand('shopware.createSnippetFromSelection', async (fileUri: string, selectedText: string) => {
    try {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      // Ask for snippet key
      const snippetKey = await vscode.window.showInputBox({
        prompt: 'Enter a key for the snippet',
        placeHolder: 'e.g. my-component.title',
        validateInput: (value: string) => {
          if (!value || value.trim() === '') {
            return 'Snippet key cannot be empty';
          }
          return null;
        }
      });

      if (!snippetKey) {
        return; // User cancelled
      }

      // Get possible snippet files
      const result = await client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/storefront/getPossibleSnippetFiles', {
        fileUri,
      });

      if (!result || !result.paths || result.paths.length === 0) {
        vscode.window.showErrorMessage('No snippet files found');
        return;
      }

      // Collect translations with selected text pre-filled
      const snippetsWithValues = await collectSnippetTranslations(
        result.paths,
        snippetKey,
        'Create Storefront Snippet',
        selectedText
      );

      if (!snippetsWithValues) {
        return; // User cancelled
      }

      // Create the snippet
      await client.sendRequest('shopware/snippet/storefront/create', {
        fileUri,
        snippetKey,
        snippets: snippetsWithValues
      });

      // Get the editor
      const editor = vscode.window.activeTextEditor;
      if (editor) {
        // Replace the selected text with the snippet reference
        const snippetReference = `{{ '${snippetKey}'|trans }}`;
        editor.edit(editBuilder => {
          const selection = editor.selection;
          editBuilder.replace(selection, snippetReference);
        });
      }

      vscode.window.showInformationMessage(`Snippet ${snippetKey} created successfully`);
    } catch (error) {
      vscode.window.showErrorMessage(`Error creating snippet from selection: ${error}`);
    }
  }));

  // Register create admin snippet command handler
  context.subscriptions.push(vscode.commands.registerCommand('shopware.createAdminSnippet', async (snippetKey: string, fileUri: string) => {
    try {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      
      const result = await client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/admin/getPossibleSnippetFiles', {
        fileUri,
      });

      if (!result || !result.paths || result.paths.length === 0) {
        vscode.window.showErrorMessage('No admin snippet files found');
        return;
      }

      const snippetsWithValues = await collectSnippetTranslations(
        result.paths,
        snippetKey,
        'Create Admin Snippet'
      );

      if (!snippetsWithValues) {
        return; // User cancelled
      }

      await client.sendRequest('shopware/snippet/admin/create', {
        fileUri,
        snippetKey,
        snippets: snippetsWithValues
      });

      vscode.window.showInformationMessage(`Admin snippet ${snippetKey} created successfully`);
    } catch (error) {
      vscode.window.showErrorMessage(`Error creating admin snippet: ${error}`);
    }
  }));

  // Register create admin snippet from selection command handler
  context.subscriptions.push(vscode.commands.registerCommand('shopware.createAdminSnippetFromSelection', async (fileUri: string, selectedText: string) => {
    try {
      if (!client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      // Ask for snippet key
      const snippetKey = await vscode.window.showInputBox({
        prompt: 'Enter a key for the admin snippet',
        placeHolder: 'e.g. my-module.component.title',
        validateInput: (value: string) => {
          if (!value || value.trim() === '') {
            return 'Snippet key cannot be empty';
          }
          return null;
        }
      });

      if (!snippetKey) {
        return; // User cancelled
      }

      // Get possible admin snippet files
      const result = await client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/admin/getPossibleSnippetFiles', {
        fileUri,
      });

      if (!result || !result.paths || result.paths.length === 0) {
        vscode.window.showErrorMessage('No admin snippet files found');
        return;
      }

      // Collect translations with selected text pre-filled
      const snippetsWithValues = await collectSnippetTranslations(
        result.paths,
        snippetKey,
        'Create Admin Snippet',
        selectedText
      );

      if (!snippetsWithValues) {
        return; // User cancelled
      }

      // Create the snippet
      await client.sendRequest('shopware/snippet/admin/create', {
        fileUri,
        snippetKey,
        snippets: snippetsWithValues
      });

      // Get the editor
      const editor = vscode.window.activeTextEditor;
      if (editor) {
        // Replace the selected text with the admin snippet reference
        const snippetReference = `{{ $tc('${snippetKey}') }}`;
        editor.edit(editBuilder => {
          const selection = editor.selection;
          editBuilder.replace(selection, snippetReference);
        });
      }

      vscode.window.showInformationMessage(`Admin snippet ${snippetKey} created successfully`);
    } catch (error) {
      vscode.window.showErrorMessage(`Error creating admin snippet from selection: ${error}`);
    }
  }));
}

export function deactivate(): Thenable<void> | undefined {
  if (!client) {
    return undefined;
  }
  
  // Add a timeout to ensure the server has time to respond
  return new Promise<void>((resolve) => {
    // Try to stop the client gracefully
    const stopPromise = client!.stop();
    
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
