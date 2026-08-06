import * as vscode from 'vscode';
import type {LanguageClient} from 'vscode-languageclient/node';
import type {ClientState} from './clientState';
import {setNested} from './configurationModel';

export const projectConfigurationPath = '.config/shopware-lsp/config.json';

type Severity = 'off' | 'hint' | 'information' | 'warning' | 'error';

export interface PartialConfiguration {
  php?: {extensions?: string[]; disabledExtensions?: string[]};
  shopware?: {targetVersion?: string};
  features?: Record<string, boolean>;
  indexing?: {enabled?: boolean};
  domains?: Record<string, boolean>;
  diagnostics?: {
    enabled?: boolean;
    inspections?: Record<string, boolean>;
    rules?: Record<string, Severity>;
  };
}

interface CatalogEntry {
  id: string;
  label: string;
  description?: string;
  parent?: string;
  dependsOn?: string[];
}

interface DiagnosticEntry {
  id: string;
  source: string;
  defaultSeverity: Severity;
}

interface InspectionEntry {
  id: string;
  languages: string[];
  rules: DiagnosticEntry[];
}

interface EffectiveConfiguration {
  features: Record<string, boolean>;
  indexing: {enabled: boolean};
  domains: Record<string, boolean>;
  diagnostics: {
    enabled: boolean;
    inspections: Record<string, boolean>;
    rules: Record<string, Severity>;
  };
  disabledDomainReasons?: Record<string, string>;
  origins?: Record<string, string>;
}

export interface ConfigurationCatalog {
  path: string;
  effective: EffectiveConfiguration;
  features: CatalogEntry[];
  domains: CatalogEntry[];
  inspections: InspectionEntry[];
  error?: string;
}

interface ReloadResult {
  applied: boolean;
  restartRequired: boolean;
  error?: string;
}

type ConfigurableKind = 'feature' | 'domain' | 'inspection' | 'rule' | 'diagnostics' | 'indexing';

interface ConfigurableItem extends vscode.QuickPickItem {
  configKind: ConfigurableKind;
  id: string;
  value: boolean | Severity;
}

export function readEditorConfiguration(): PartialConfiguration {
  const configuration = vscode.workspace.getConfiguration('shopwareLSP');
  const result: PartialConfiguration = {};

  const phpExtensions = explicitValue<string[]>(configuration, 'phpExtensions');
  const disabledPhpExtensions = explicitValue<string[]>(configuration, 'disabledPhpExtensions');
  if (phpExtensions !== undefined || disabledPhpExtensions !== undefined) {
    result.php = {};
    if (phpExtensions !== undefined) result.php.extensions = phpExtensions;
    if (disabledPhpExtensions !== undefined) result.php.disabledExtensions = disabledPhpExtensions;
  }
  const targetVersion = explicitValue<string>(configuration, 'shopwareTargetVersion');
  if (targetVersion !== undefined) result.shopware = {targetVersion};

  const features = explicitValue<Record<string, boolean>>(configuration, 'features');
  if (features !== undefined) result.features = features;
  const domains = explicitValue<Record<string, boolean>>(configuration, 'domains');
  if (domains !== undefined) result.domains = domains;
  const indexing = explicitValue<boolean>(configuration, 'indexing.enabled');
  if (indexing !== undefined) result.indexing = {enabled: indexing};

  const diagnosticsEnabled = explicitValue<boolean>(configuration, 'diagnostics.enabled');
  const inspections = explicitValue<Record<string, boolean>>(configuration, 'diagnostics.inspections');
  const rules = explicitValue<Record<string, Severity>>(configuration, 'diagnostics.rules');
  if (diagnosticsEnabled !== undefined || inspections !== undefined || rules !== undefined) {
    result.diagnostics = {};
    if (diagnosticsEnabled !== undefined) result.diagnostics.enabled = diagnosticsEnabled;
    if (inspections !== undefined) result.diagnostics.inspections = inspections;
    if (rules !== undefined) result.diagnostics.rules = rules;
  }
  return result;
}

function explicitValue<T>(configuration: vscode.WorkspaceConfiguration, key: string): T | undefined {
  const inspected = configuration.inspect<T>(key);
  if (!inspected) return undefined;
  const configured = inspected.workspaceFolderLanguageValue ?? inspected.workspaceLanguageValue ??
    inspected.globalLanguageValue ?? inspected.workspaceFolderValue ?? inspected.workspaceValue ??
    inspected.globalValue;
  return configured === undefined ? undefined : configuration.get<T>(key);
}

export function registerConfigurationSupport(
  context: vscode.ExtensionContext,
  clientState: ClientState,
  output: vscode.OutputChannel,
  restart: () => Promise<void>,
): void {
  const diagnostics = vscode.languages.createDiagnosticCollection('shopware-lsp-configuration');
  context.subscriptions.push(diagnostics);

  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.configure', async () => {
    const client = clientState.client;
    if (!client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }
    await showConfigurationPicker(client);
  }));

  context.subscriptions.push(vscode.workspace.onDidChangeConfiguration(async event => {
    if (!event.affectsConfiguration('shopwareLSP')) return;
    const client = clientState.client;
    if (!client) return;
    await client.sendNotification('workspace/didChangeConfiguration', {
      settings: readEditorConfiguration(),
    });
  }));

  const folder = outermostWorkspaceFolder();
  if (folder) {
    const watcher = vscode.workspace.createFileSystemWatcher(
      new vscode.RelativePattern(folder, projectConfigurationPath),
    );
    const reload = async () => {
      const client = clientState.client;
      if (!client) return;
      try {
        const result = await client.sendRequest<ReloadResult>('shopware/configuration/reload');
        updateConfigurationError(diagnostics, folder, result.error);
        if (result.error) output.appendLine(`Configuration error: ${result.error}`);
      } catch (error) {
        output.appendLine(`Failed to reload Shopware LSP configuration: ${error}`);
      }
    };
    watcher.onDidCreate(reload, undefined, context.subscriptions);
    watcher.onDidChange(reload, undefined, context.subscriptions);
    watcher.onDidDelete(reload, undefined, context.subscriptions);
    context.subscriptions.push(watcher);
  }

  context.subscriptions.push({dispose: () => { restartPromptVisible = false; }});
  configurationDiagnostics = diagnostics;
  configurationOutput = output;
  configurationRestart = restart;
}

let configurationDiagnostics: vscode.DiagnosticCollection | undefined;
let configurationOutput: vscode.OutputChannel | undefined;
let configurationRestart: (() => Promise<void>) | undefined;
let restartPromptVisible = false;

export function attachConfigurationClient(client: LanguageClient): void {
  client.onNotification('shopware/configurationRestartRequired', async (params: {message?: string}) => {
    if (restartPromptVisible) return;
    restartPromptVisible = true;
    const selected = await vscode.window.showInformationMessage(
      params.message || 'Shopware LSP configuration changes require a restart.',
      'Restart',
      'Later',
    );
    restartPromptVisible = false;
    if (selected === 'Restart') await configurationRestart?.();
  });
  void client.sendRequest<ConfigurationCatalog>('shopware/configuration/catalog').then(catalog => {
    const folder = outermostWorkspaceFolder();
    if (folder) updateConfigurationError(configurationDiagnostics, folder, catalog.error);
    if (catalog.error) configurationOutput?.appendLine(`Configuration error: ${catalog.error}`);
  }, error => configurationOutput?.appendLine(`Failed to read configuration catalog: ${error}`));
}

async function showConfigurationPicker(client: LanguageClient): Promise<void> {
  const catalog = await client.sendRequest<ConfigurationCatalog>('shopware/configuration/catalog');
  const items = configurableItems(catalog);
  const selected = await vscode.window.showQuickPick(items, {
    title: 'Shopware Language Server Configuration',
    placeHolder: 'Search features, domains, inspections, and diagnostic rules',
    matchOnDescription: true,
    matchOnDetail: true,
  });
  if (!selected) return;

  const value = await chooseValue(selected);
  if (value === undefined) return;
  const scope = await chooseScope();
  if (!scope) return;
  if (scope === 'project') {
    await updateProjectConfiguration(selected, value);
    await client.sendRequest<ReloadResult>('shopware/configuration/reload');
  } else {
    await updateEditorSetting(selected, value, scope);
  }
}

function configurableItems(catalog: ConfigurationCatalog): ConfigurableItem[] {
  const result: ConfigurableItem[] = [
    {
      configKind: 'diagnostics', id: 'diagnostics.enabled', label: 'Diagnostics',
      description: catalog.effective.diagnostics.enabled ? 'Enabled' : 'Disabled',
      detail: `All diagnostics · ${configurationOrigin(catalog, 'diagnostics.enabled')}`,
      value: catalog.effective.diagnostics.enabled,
    },
    {
      configKind: 'indexing', id: 'indexing.enabled', label: 'Indexing',
      description: catalog.effective.indexing.enabled ? 'Enabled' : 'Disabled',
      detail: `Structural setting · ${configurationOrigin(catalog, 'indexing.enabled')} · changing it requires a restart`,
      value: catalog.effective.indexing.enabled,
    },
  ];
  for (const feature of catalog.features) {
    const enabled = catalog.effective.features[feature.id] !== false;
    result.push({
      configKind: 'feature', id: feature.id, label: feature.label,
      description: enabled ? 'Enabled' : 'Disabled',
      detail: `Feature · ${feature.id} · ${configurationOrigin(catalog, `features.${feature.id}`)}`,
      value: enabled,
    });
  }
  for (const domain of catalog.domains) {
    const enabled = catalog.effective.domains[domain.id] !== false;
    const reason = catalog.effective.disabledDomainReasons?.[domain.id];
    result.push({
      configKind: 'domain', id: domain.id, label: domain.label,
      description: enabled ? 'Enabled' : `Disabled${reason ? ` · ${reason}` : ''}`,
      detail: `Domain · ${domain.id} · ${reason ? 'dependency' : configurationOrigin(catalog, `domains.${domain.id}`)} · changing it requires a restart`,
      value: enabled,
    });
  }
  for (const inspection of catalog.inspections) {
    const enabled = catalog.effective.diagnostics.inspections[inspection.id] !== false;
    result.push({
      configKind: 'inspection', id: inspection.id, label: inspection.id,
      description: enabled ? 'Enabled' : 'Disabled',
      detail: `Inspection · ${inspection.languages.join(', ')} · ${configurationOrigin(catalog, `diagnostics.inspections.${inspection.id}`)}`,
      value: enabled,
    });
    for (const rule of inspection.rules) {
      const value = catalog.effective.diagnostics.rules[rule.id] ?? rule.defaultSeverity;
      result.push({
        configKind: 'rule', id: rule.id, label: rule.id, description: value,
        detail: `Diagnostic · ${inspection.id} · ${rule.source} · ${configurationOrigin(catalog, `diagnostics.rules.${rule.id}`)}`,
        value,
      });
    }
  }
  return result;
}

function configurationOrigin(catalog: ConfigurationCatalog, path: string): string {
  return catalog.effective.origins?.[path] || 'default';
}

async function chooseValue(item: ConfigurableItem): Promise<boolean | Severity | null | undefined> {
  if (item.configKind === 'rule') {
    const values: Array<vscode.QuickPickItem & {value: Severity | null}> = [
      {label: 'Inherit default', description: 'Remove this override', value: null},
      {label: 'Off', value: 'off'}, {label: 'Hint', value: 'hint'},
      {label: 'Information', value: 'information'}, {label: 'Warning', value: 'warning'},
      {label: 'Error', value: 'error'},
    ];
    return (await vscode.window.showQuickPick(values, {title: item.label}))?.value;
  }
  const values: Array<vscode.QuickPickItem & {value: boolean | null}> = [
    {label: 'Inherit default', description: 'Remove this override', value: null},
    {label: 'Enabled', value: true}, {label: 'Disabled', value: false},
  ];
  return (await vscode.window.showQuickPick(values, {title: item.label}))?.value;
}

async function chooseScope(): Promise<'project' | 'workspace' | 'user' | undefined> {
  const values: Array<vscode.QuickPickItem & {value: 'project' | 'workspace' | 'user'}> = [
    {label: 'Project configuration', description: projectConfigurationPath, value: 'project'},
    {label: 'Workspace override', description: 'Only this VS Code workspace', value: 'workspace'},
    {label: 'User override', description: 'All VS Code workspaces', value: 'user'},
  ];
  return (await vscode.window.showQuickPick(values, {title: 'Where should this setting be stored?'}))?.value;
}

async function updateEditorSetting(
  item: ConfigurableItem,
  value: boolean | Severity | null,
  scope: 'workspace' | 'user',
): Promise<void> {
  const target = scope === 'user' ? vscode.ConfigurationTarget.Global : vscode.ConfigurationTarget.WorkspaceFolder;
  const configuration = vscode.workspace.getConfiguration('shopwareLSP', outermostWorkspaceFolder()?.uri);
  if (item.configKind === 'diagnostics' || item.configKind === 'indexing') {
    await configuration.update(item.id, value === null ? undefined : value, target);
    return;
  }
  const key = item.configKind === 'feature' ? 'features' : item.configKind === 'domain' ? 'domains' :
    item.configKind === 'inspection' ? 'diagnostics.inspections' : 'diagnostics.rules';
  const current = {...(explicitValue<Record<string, boolean | Severity>>(configuration, key) || {})};
  if (value === null) delete current[item.id];
  else current[item.id] = value;
  await configuration.update(key, Object.keys(current).length ? current : undefined, target);
}

async function updateProjectConfiguration(
  item: ConfigurableItem,
  value: boolean | Severity | null,
): Promise<void> {
  const folder = outermostWorkspaceFolder();
  if (!folder) throw new Error('A workspace folder is required for project configuration');
  const uri = vscode.Uri.joinPath(folder.uri, projectConfigurationPath);
  let document: Record<string, unknown> = {
    $schema: 'https://raw.githubusercontent.com/shopwareLabs/shopware-lsp/main/internal/projectconfig/schema.json',
    version: 1,
  };
  try {
    document = JSON.parse(new TextDecoder().decode(await vscode.workspace.fs.readFile(uri)));
  } catch (error) {
    if (!(error instanceof vscode.FileSystemError && error.code === 'FileNotFound')) throw error;
  }
  const path = item.configKind === 'feature' ? ['features', item.id] :
    item.configKind === 'domain' ? ['domains', item.id] :
    item.configKind === 'inspection' ? ['diagnostics', 'inspections', item.id] :
    item.configKind === 'rule' ? ['diagnostics', 'rules', item.id] : item.id.split('.');
  setNested(document, path, value);
  await vscode.workspace.fs.createDirectory(vscode.Uri.joinPath(folder.uri, '.config/shopware-lsp'));
  await vscode.workspace.fs.writeFile(uri, new TextEncoder().encode(`${JSON.stringify(document, null, 2)}\n`));
  await vscode.window.showTextDocument(uri, {preview: false});
}

function updateConfigurationError(
  collection: vscode.DiagnosticCollection | undefined,
  folder: vscode.WorkspaceFolder,
  message?: string,
): void {
  if (!collection) return;
  const uri = vscode.Uri.joinPath(folder.uri, projectConfigurationPath);
  if (!message) {
    collection.delete(uri);
    return;
  }
  const diagnostic = new vscode.Diagnostic(
    new vscode.Range(0, 0, 0, 1), message, vscode.DiagnosticSeverity.Error,
  );
  diagnostic.source = 'shopware-lsp';
  diagnostic.code = 'shopware.config.invalid';
  collection.set(uri, [diagnostic]);
}

function outermostWorkspaceFolder(): vscode.WorkspaceFolder | undefined {
  const folders = [...(vscode.workspace.workspaceFolders || [])]
    .sort((left, right) => left.uri.toString().length - right.uri.toString().length);
  return folders[0];
}
