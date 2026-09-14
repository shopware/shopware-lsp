import * as path from 'node:path';
import * as vscode from 'vscode';
import {parse, stringify} from 'yaml';
import type {ClientState} from './clientState';
import {
  type ConfigurationCatalog,
  type DiagnosticOverride,
  isKnownDiagnosticRule,
  projectConfigurationDirectory,
  projectConfigurationPath,
  type ReloadResult,
} from './configuration';
import {
  diagnosticPattern,
  setNested,
  upsertDiagnosticOverride,
} from './configurationModel';

const configureDiagnosticCommand = 'shopwareLSP.configureDiagnostic';
const openConfigurationCommand = 'shopwareLSP.openConfiguration';
const schemaURL = 'https://raw.githubusercontent.com/shopware/shopware-lsp/main/internal/projectconfig/schema.json';

type DiagnosticTarget = 'file' | 'directory' | 'extension' | 'workspace';
type DiagnosticEffect = {target: DiagnosticTarget; all: boolean};
type ConfigurationStorage = {
  kind: 'project' | 'extension' | 'workspace' | 'user';
  root: vscode.Uri;
  label: string;
};

export function registerDiagnosticConfigurationSupport(
  context: vscode.ExtensionContext,
  clientState: ClientState,
  output: vscode.OutputChannel,
): void {
  context.subscriptions.push(
    vscode.commands.registerCommand(configureDiagnosticCommand, async (
      uri: vscode.Uri,
      code: string,
    ) => configureDiagnostic(clientState, uri, code)),
    vscode.commands.registerCommand(openConfigurationCommand, async () =>
      openConfiguration(clientState)),
    vscode.languages.registerCodeActionsProvider(
      {scheme: 'file'},
      {
        provideCodeActions(document, _range, actionContext) {
          const result: vscode.CodeAction[] = [];
          const seen = new Set<string>();
          for (const diagnostic of actionContext.diagnostics) {
            const code = diagnosticCode(diagnostic);
            if (!code || seen.has(code) || !isKnownDiagnosticRule(document.uri, code)) continue;
            seen.add(code);
            const action = new vscode.CodeAction(
              `Configure Shopware diagnostic '${code}'…`,
              vscode.CodeActionKind.QuickFix,
            );
            action.diagnostics = [diagnostic];
            action.command = {
              command: configureDiagnosticCommand,
              title: action.title,
              arguments: [document.uri, code],
            };
            result.push(action);
          }
          return result;
        },
      },
      {providedCodeActionKinds: [vscode.CodeActionKind.QuickFix]},
    ),
  );
  output.appendLine('Registered scoped Shopware diagnostic configuration actions');
}

function diagnosticCode(diagnostic: vscode.Diagnostic): string | undefined {
  if (typeof diagnostic.code === 'string' || typeof diagnostic.code === 'number') {
    return String(diagnostic.code);
  }
  if (diagnostic.code && typeof diagnostic.code.value !== 'undefined') {
    return String(diagnostic.code.value);
  }
  return undefined;
}

async function configureDiagnostic(
  clientState: ClientState,
  uri: vscode.Uri,
  code: string,
): Promise<void> {
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  if (!folder) {
    vscode.window.showErrorMessage('The diagnostic file is not inside a workspace folder');
    return;
  }
  const extensionRoot = await findExtensionConfigurationRoot(folder.uri, uri);
  const effects: Array<vscode.QuickPickItem & {value: DiagnosticEffect}> = [
    {label: `Ignore '${code}' in this file`, value: {target: 'file', all: false}},
    {label: `Ignore '${code}' in this directory`, value: {target: 'directory', all: false}},
    {label: 'Ignore all Shopware diagnostics in this file', value: {target: 'file', all: true}},
    {label: 'Ignore all Shopware diagnostics in this directory', value: {target: 'directory', all: true}},
  ];
  if (extensionRoot && extensionRoot.fsPath !== folder.uri.fsPath) {
    effects.push(
      {label: `Ignore '${code}' for this extension`, value: {target: 'extension', all: false}},
      {label: 'Ignore all Shopware diagnostics for this extension', value: {target: 'extension', all: true}},
    );
  }
  effects.push({
    label: `Disable '${code}' for the complete workspace`,
    value: {target: 'workspace', all: false},
  });
  const selectedEffect = await vscode.window.showQuickPick(effects, {
    title: `Configure ${code}`,
    placeHolder: 'Choose what should be ignored',
  });
  if (!selectedEffect) return;

  const storages: Array<vscode.QuickPickItem & {value: ConfigurationStorage}> = [];
  if (selectedEffect.value.target !== 'workspace' &&
      extensionRoot && extensionRoot.fsPath !== folder.uri.fsPath) {
    storages.push({
      label: 'Extension configuration',
      description: vscode.workspace.asRelativePath(extensionRoot, false),
      value: {kind: 'extension', root: extensionRoot, label: 'extension configuration'},
    });
  }
  storages.push(
    {
      label: 'Project configuration', description: projectConfigurationPath,
      value: {kind: 'project', root: folder.uri, label: 'project configuration'},
    },
    {
      label: 'Workspace override', description: 'Only this VS Code workspace folder',
      value: {kind: 'workspace', root: folder.uri, label: 'workspace settings'},
    },
    {
      label: 'User override', description: 'All VS Code workspaces',
      value: {kind: 'user', root: folder.uri, label: 'user settings'},
    },
  );
  const selectedStorage = await vscode.window.showQuickPick(storages, {
    title: 'Where should this diagnostic override be stored?',
  });
  if (!selectedStorage) return;

  try {
    await writeDiagnosticConfiguration(
      selectedStorage.value,
      selectedEffect.value,
      uri,
      extensionRoot,
      code,
    );
    if (selectedStorage.value.kind === 'project' || selectedStorage.value.kind === 'extension') {
      const client = clientState.clientForUri(uri);
      if (client) await client.sendRequest<ReloadResult>('shopware/configuration/reload');
    }
    vscode.window.showInformationMessage(
      `Updated Shopware LSP ${selectedStorage.value.label}`,
    );
  } catch (error) {
    vscode.window.showErrorMessage(`Failed to configure Shopware diagnostic: ${error}`);
  }
}

async function writeDiagnosticConfiguration(
  storage: ConfigurationStorage,
  effect: DiagnosticEffect,
  document: vscode.Uri,
  extensionRoot: vscode.Uri | undefined,
  code: string,
): Promise<void> {
  const globalForStorage = effect.target === 'workspace' ||
    effect.target === 'extension' && extensionRoot?.fsPath === storage.root.fsPath;
  const update = effect.all ? {enabled: false} : {
    rule: {id: code, severity: 'off'},
  };
  if (storage.kind === 'project' || storage.kind === 'extension') {
    const configUri = vscode.Uri.joinPath(storage.root, projectConfigurationPath);
    const config = await readConfiguration(configUri);
    if (globalForStorage) {
      if (effect.all) setNested(config, ['diagnostics', 'enabled'], false);
      else setNested(config, ['diagnostics', 'rules', code], 'off');
    } else {
      const pattern = configurationPattern(storage.root, effect.target, document, extensionRoot);
      const diagnostics = objectValue(config.diagnostics);
      const overrides = upsertDiagnosticOverride(
        diagnostics.overrides as DiagnosticOverride[] | undefined,
        pattern,
        update,
      );
      setNested(config, ['diagnostics', 'overrides'], overrides);
    }
    await vscode.workspace.fs.createDirectory(vscode.Uri.joinPath(storage.root, projectConfigurationDirectory));
    await vscode.workspace.fs.writeFile(
      configUri,
      new TextEncoder().encode(stringify(config)),
    );
    return;
  }

  const target = storage.kind === 'user'
    ? vscode.ConfigurationTarget.Global
    : vscode.ConfigurationTarget.WorkspaceFolder;
  const configuration = vscode.workspace.getConfiguration('shopwareLSP', storage.root);
  if (globalForStorage) {
    if (effect.all) {
      await configuration.update('diagnostics.enabled', false, target);
    } else {
      const rules = {...storedEditorValue<Record<string, string>>(
        configuration,
        'diagnostics.rules',
        storage.kind,
      )};
      rules[code] = 'off';
      await configuration.update('diagnostics.rules', rules, target);
    }
    return;
  }
  const pattern = configurationPattern(storage.root, effect.target, document, extensionRoot);
  const overrides = upsertDiagnosticOverride(
    storedEditorValue<DiagnosticOverride[]>(
      configuration,
      'diagnostics.overrides',
      storage.kind,
    ),
    pattern,
    update,
  );
  await configuration.update('diagnostics.overrides', overrides, target);
}

function configurationPattern(
  storageRoot: vscode.Uri,
  target: DiagnosticTarget,
  document: vscode.Uri,
  extensionRoot: vscode.Uri | undefined,
): string {
  let targetPath = document.fsPath;
  let recursive = false;
  if (target === 'directory') {
    targetPath = path.dirname(document.fsPath);
    recursive = true;
  } else if (target === 'extension' && extensionRoot) {
    targetPath = extensionRoot.fsPath;
    recursive = true;
  }
  const relative = path.relative(storageRoot.fsPath, targetPath).split(path.sep).join('/');
  return diagnosticPattern(relative, recursive);
}

async function readConfiguration(uri: vscode.Uri): Promise<Record<string, unknown>> {
  try {
    const source = new TextDecoder().decode(await vscode.workspace.fs.readFile(uri));
    const parsed: unknown = parse(source);
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error(`${uri.fsPath} must contain a YAML mapping`);
    }
    return parsed as Record<string, unknown>;
  } catch (error) {
    if (!(error instanceof vscode.FileSystemError && error.code === 'FileNotFound')) throw error;
    return {$schema: schemaURL, version: 1};
  }
}

function storedEditorValue<T>(
  configuration: vscode.WorkspaceConfiguration,
  key: string,
  storage: 'workspace' | 'user',
): T | undefined {
  const inspected = configuration.inspect<T>(key);
  return storage === 'user' ? inspected?.globalValue : inspected?.workspaceFolderValue;
}

function objectValue(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? value as Record<string, unknown>
    : {};
}

async function openConfiguration(clientState: ClientState): Promise<void> {
  const folder = await clientState.resolveWorkspaceFolder(
    undefined,
    false,
    'Open Shopware LSP Configuration',
  );
  if (!folder) {
    vscode.window.showErrorMessage('A workspace folder is required');
    return;
  }
  const options: Array<vscode.QuickPickItem & {root: vscode.Uri}> = [{
    label: 'Workspace configuration',
    description: projectConfigurationPath,
    root: folder.uri,
  }];
  const client = clientState.entryForUri(folder.uri)?.client;
  if (client) {
    try {
      const catalog = await client.sendRequest<ConfigurationCatalog>('shopware/configuration/catalog');
      for (const scope of catalog.scopes || []) {
        options.push({
          label: `Extension configuration: ${vscode.workspace.asRelativePath(scope.root, false)}`,
          description: vscode.workspace.asRelativePath(scope.path, false),
          root: vscode.Uri.file(scope.root),
        });
      }
    } catch {
      // The workspace configuration remains available when the server is still starting.
    }
  }
  const active = vscode.window.activeTextEditor?.document.uri;
  if (active) {
    const detected = await findExtensionConfigurationRoot(folder.uri, active);
    if (detected && detected.fsPath !== folder.uri.fsPath &&
      !options.some(option => option.root.fsPath === detected.fsPath)) {
      options.push({
        label: `Extension configuration: ${vscode.workspace.asRelativePath(detected, false)}`,
        description: 'Create a configuration for the active extension',
        root: detected,
      });
    }
  }
  const selected = options.length === 1 ? options[0] : await vscode.window.showQuickPick(options, {
    title: 'Open Shopware LSP configuration',
  });
  if (!selected) return;
  const uri = vscode.Uri.joinPath(selected.root, projectConfigurationPath);
  try {
    await vscode.workspace.fs.stat(uri);
  } catch (error) {
    if (!(error instanceof vscode.FileSystemError && error.code === 'FileNotFound')) throw error;
    await vscode.workspace.fs.createDirectory(vscode.Uri.joinPath(selected.root, projectConfigurationDirectory));
    await vscode.workspace.fs.writeFile(
      uri,
      new TextEncoder().encode(stringify({$schema: schemaURL, version: 1})),
    );
  }
  await vscode.window.showTextDocument(uri, {preview: false});
}

async function findExtensionConfigurationRoot(
  workspaceRoot: vscode.Uri,
  document: vscode.Uri,
): Promise<vscode.Uri | undefined> {
  let current = vscode.Uri.file(path.dirname(document.fsPath));
  let detected: vscode.Uri | undefined;
  while (isWithin(workspaceRoot.fsPath, current.fsPath)) {
    if (current.fsPath !== workspaceRoot.fsPath &&
      await exists(vscode.Uri.joinPath(current, projectConfigurationPath))) {
      return current;
    }
    if (!detected && current.fsPath !== workspaceRoot.fsPath && await isExtensionRoot(current)) {
      detected = current;
    }
    if (current.fsPath === workspaceRoot.fsPath) break;
    const parent = vscode.Uri.file(path.dirname(current.fsPath));
    if (parent.fsPath === current.fsPath) break;
    current = parent;
  }
  return detected;
}

async function isExtensionRoot(root: vscode.Uri): Promise<boolean> {
  if (await exists(vscode.Uri.joinPath(root, '.git')) ||
    await exists(vscode.Uri.joinPath(root, 'manifest.xml'))) return true;
  const composerUri = vscode.Uri.joinPath(root, 'composer.json');
  try {
    const composer = JSON.parse(
      new TextDecoder().decode(await vscode.workspace.fs.readFile(composerUri)),
    ) as {type?: string; extra?: {'shopware-plugin-class'?: string}};
    return composer.type === 'shopware-platform-plugin' ||
      Boolean(composer.extra?.['shopware-plugin-class']);
  } catch {
    return false;
  }
}

async function exists(uri: vscode.Uri): Promise<boolean> {
  try {
    await vscode.workspace.fs.stat(uri);
    return true;
  } catch {
    return false;
  }
}

function isWithin(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || relative !== '..' && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}
