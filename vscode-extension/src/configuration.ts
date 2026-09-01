import * as vscode from 'vscode';
import {parse, stringify} from 'yaml';
import type {LanguageClient} from 'vscode-languageclient/node';
import type {ClientState} from './clientState';
import {setNested} from './configurationModel';
import {pathWithinRoot} from './workspaceRoots';

export const projectConfigurationDirectory = '.config/shopware';
export const projectConfigurationPath = `${projectConfigurationDirectory}/lsp.yaml`;

export type Severity = 'off' | 'hint' | 'information' | 'warning' | 'error';

export interface DiagnosticOverride {
  files: string[];
  enabled?: boolean;
  inspections?: Record<string, boolean>;
  rules?: Record<string, Severity>;
}

export interface PartialConfiguration {
  php?: {extensions?: string[]; disabledExtensions?: string[]};
  shopware?: {targetVersion?: string};
  features?: Record<string, boolean>;
  indexing?: {enabled?: boolean; exclude?: string[]; maxFileSizeMiB?: number};
  mcp?: {tools?: Record<string, boolean>};
  domains?: Record<string, boolean>;
  diagnostics?: {
    enabled?: boolean;
    inspections?: Record<string, boolean>;
    rules?: Record<string, Severity>;
    overrides?: DiagnosticOverride[];
  };
}

export interface ConfigurationIssue {
  path: string;
  message: string;
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
  defaultEnabled?: boolean;
}

interface InspectionEntry {
  id: string;
  languages: string[];
  rules: DiagnosticEntry[];
}

interface EffectiveConfiguration {
  features: Record<string, boolean>;
  indexing: {enabled: boolean; exclude?: string[]; maxFileSizeMiB?: number};
  mcp?: {tools: Record<string, boolean>};
  domains: Record<string, boolean>;
  diagnostics: {
    enabled: boolean;
    inspections: Record<string, boolean>;
    rules: Record<string, Severity>;
    overrides?: DiagnosticOverride[];
  };
  disabledDomainReasons?: Record<string, string>;
  origins?: Record<string, string>;
}

export interface ConfigurationCatalog {
  path: string;
  effective: EffectiveConfiguration;
  features: CatalogEntry[];
  mcpTools?: CatalogEntry[];
  domains: CatalogEntry[];
  inspections: InspectionEntry[];
  error?: string;
  errors?: ConfigurationIssue[];
  scopes?: Array<{root: string; path: string; error?: string}>;
}

export interface ReloadResult {
  applied: boolean;
  restartRequired: boolean;
  error?: string;
  errors?: ConfigurationIssue[];
}

type ConfigurableKind = 'feature' | 'mcpTool' | 'domain' | 'inspection' | 'rule' | 'diagnostics' | 'indexing';

interface ConfigurableItem extends vscode.QuickPickItem {
  configKind: ConfigurableKind;
  id: string;
  value: boolean | Severity;
}

export function readEditorConfiguration(resource?: vscode.Uri): PartialConfiguration {
  const configuration = vscode.workspace.getConfiguration('shopwareLSP', resource);
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
  const indexingExclude = explicitValue<string[]>(configuration, 'indexing.exclude');
  const indexingMaxFileSize = explicitValue<number>(configuration, 'indexing.maxFileSizeMiB');
  if (indexing !== undefined || indexingExclude !== undefined ||
    indexingMaxFileSize !== undefined) {
    result.indexing = {};
    if (indexing !== undefined) result.indexing.enabled = indexing;
    if (indexingExclude !== undefined) result.indexing.exclude = indexingExclude;
    if (indexingMaxFileSize !== undefined) {
      result.indexing.maxFileSizeMiB = indexingMaxFileSize;
    }
  }
  const mcpTools = explicitValue<Record<string, boolean>>(configuration, 'mcp.tools');
  if (mcpTools !== undefined) result.mcp = {tools: mcpTools};

  const diagnosticsEnabled = explicitValue<boolean>(configuration, 'diagnostics.enabled');
  const inspections = explicitValue<Record<string, boolean>>(configuration, 'diagnostics.inspections');
  const rules = explicitValue<Record<string, Severity>>(configuration, 'diagnostics.rules');
  const overrides = explicitValue<DiagnosticOverride[]>(configuration, 'diagnostics.overrides');
  if (diagnosticsEnabled !== undefined || inspections !== undefined || rules !== undefined ||
    overrides !== undefined) {
    result.diagnostics = {};
    if (diagnosticsEnabled !== undefined) result.diagnostics.enabled = diagnosticsEnabled;
    if (inspections !== undefined) result.diagnostics.inspections = inspections;
    if (rules !== undefined) result.diagnostics.rules = rules;
    if (overrides !== undefined) result.diagnostics.overrides = overrides;
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
  restart: (folder: vscode.WorkspaceFolder) => Promise<void>,
): void {
  const diagnostics = vscode.languages.createDiagnosticCollection('shopware-lsp-configuration');
  context.subscriptions.push(diagnostics);

  context.subscriptions.push(vscode.commands.registerCommand('shopwareLSP.configure', async () => {
    const entry = await clientState.resolveEntry(
      undefined,
      'Configure Shopware Language Server',
    );
    if (!entry) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }
    await showConfigurationPicker(entry.client, entry.folder);
  }));

  context.subscriptions.push(vscode.workspace.onDidChangeConfiguration(async event => {
    if (!event.affectsConfiguration('shopwareLSP')) return;
    await Promise.all(clientState.runningEntries().filter(entry =>
      event.affectsConfiguration('shopwareLSP', entry.folder.uri)).map(entry =>
      entry.client.sendNotification('workspace/didChangeConfiguration', {
        settings: readEditorConfiguration(entry.folder.uri),
      })));
  }));

  const watcher = vscode.workspace.createFileSystemWatcher(`**/${projectConfigurationPath}`);
  const reload = async (uri: vscode.Uri) => {
    const entry = clientState.entryForUri(uri);
    if (!entry) return;
    try {
      const result = await entry.client.sendRequest<ReloadResult>('shopware/configuration/reload');
      updateConfigurationErrors(diagnostics, entry.folder, result.errors, result.error);
      if (result.error) {
        output.appendLine(`Configuration error in ${entry.folder.name}: ${result.error}`);
      }
    } catch (error) {
      output.appendLine(`Failed to reload Shopware LSP configuration for ${entry.folder.name}: ${error}`);
    }
  };
  watcher.onDidCreate(reload, undefined, context.subscriptions);
  watcher.onDidChange(reload, undefined, context.subscriptions);
  watcher.onDidDelete(reload, undefined, context.subscriptions);
  context.subscriptions.push(watcher);

  context.subscriptions.push({dispose: () => {
    restartPromptVisible.clear();
    diagnosticRuleIDs.clear();
  }});
  configurationDiagnostics = diagnostics;
  configurationRestart = restart;
}

let configurationDiagnostics: vscode.DiagnosticCollection | undefined;
let configurationRestart: ((folder: vscode.WorkspaceFolder) => Promise<void>) | undefined;
const restartPromptVisible = new Set<string>();
const diagnosticRuleIDs = new Map<string, Set<string>>();

export function isKnownDiagnosticRule(uri: vscode.Uri, id: string): boolean {
  if (uri.scheme !== 'file') return false;
  return [...diagnosticRuleIDs.entries()]
    .map(([root, rules]) => ({root: vscode.Uri.parse(root), rules}))
    .filter(entry => entry.root.scheme === 'file' && pathWithinRoot(entry.root.fsPath, uri.fsPath))
    .sort((left, right) => right.root.fsPath.length - left.root.fsPath.length)[0]
    ?.rules.has(id) === true;
}

function rememberDiagnosticRules(folder: vscode.WorkspaceFolder, catalog: ConfigurationCatalog): void {
  diagnosticRuleIDs.set(folder.uri.toString(), new Set(
    catalog.inspections.flatMap(inspection => inspection.rules.map(rule => rule.id)),
  ));
}

export function attachConfigurationClient(
  client: LanguageClient,
  folder: vscode.WorkspaceFolder,
  output: vscode.OutputChannel,
): vscode.Disposable {
  const key = folder.uri.toString();
  let active = true;
  const restartNotification = client.onNotification(
    'shopware/configurationRestartRequired',
    async (params: {message?: string}) => {
      if (!active) return;
      if (restartPromptVisible.has(key)) return;
      restartPromptVisible.add(key);
      const selected = await vscode.window.showInformationMessage(
        `${folder.name}: ${params.message || 'Shopware LSP configuration changes require a restart.'}`,
        'Restart',
        'Later',
      );
      restartPromptVisible.delete(key);
      if (selected === 'Restart' && active) await configurationRestart?.(folder);
    },
  );
  void client.sendRequest<ConfigurationCatalog>('shopware/configuration/catalog').then(catalog => {
    if (!active) return;
    rememberDiagnosticRules(folder, catalog);
    updateConfigurationErrors(configurationDiagnostics, folder, catalog.errors, catalog.error);
    if (catalog.error) output.appendLine(`Configuration error in ${folder.name}: ${catalog.error}`);
  }, error => {
    if (active) output.appendLine(`Failed to read configuration catalog for ${folder.name}: ${error}`);
  });
  return {
    dispose() {
      active = false;
      restartNotification.dispose();
      restartPromptVisible.delete(key);
      diagnosticRuleIDs.delete(key);
      updateConfigurationErrors(configurationDiagnostics, folder);
    },
  };
}

async function showConfigurationPicker(
  client: LanguageClient,
  folder: vscode.WorkspaceFolder,
): Promise<void> {
  const catalog = await client.sendRequest<ConfigurationCatalog>('shopware/configuration/catalog');
  rememberDiagnosticRules(folder, catalog);
  const items = configurableItems(catalog);
  const selected = await vscode.window.showQuickPick(items, {
    title: 'Shopware Language Server Configuration',
    placeHolder: 'Search features, MCP tools, domains, inspections, and diagnostic rules',
    matchOnDescription: true,
    matchOnDetail: true,
  });
  if (!selected) return;

  const value = await chooseValue(selected);
  if (value === undefined) return;
  const scope = await chooseScope();
  if (!scope) return;
  if (scope === 'project') {
    await updateProjectConfiguration(folder, selected, value);
    await client.sendRequest<ReloadResult>('shopware/configuration/reload');
  } else {
    await updateEditorSetting(folder, selected, value, scope);
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
  for (const tool of catalog.mcpTools ?? []) {
    const enabled = catalog.effective.mcp?.tools[tool.id] !== false;
    result.push({
      configKind: 'mcpTool', id: tool.id, label: `MCP: ${tool.label}`,
      description: enabled ? 'Enabled' : 'Disabled',
      detail: `MCP tool · ${tool.id} · ${configurationOrigin(catalog, `mcp.tools.${tool.id}`)}`,
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
      const value = catalog.effective.diagnostics.rules[rule.id] ??
        (rule.defaultEnabled !== false ? rule.defaultSeverity : 'off');
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
  folder: vscode.WorkspaceFolder,
  item: ConfigurableItem,
  value: boolean | Severity | null,
  scope: 'workspace' | 'user',
): Promise<void> {
  const target = scope === 'user' ? vscode.ConfigurationTarget.Global : vscode.ConfigurationTarget.WorkspaceFolder;
  const configuration = vscode.workspace.getConfiguration('shopwareLSP', folder.uri);
  if (item.configKind === 'diagnostics' || item.configKind === 'indexing') {
    await configuration.update(item.id, value === null ? undefined : value, target);
    return;
  }
  const key = item.configKind === 'feature' ? 'features' : item.configKind === 'domain' ? 'domains' :
    item.configKind === 'mcpTool' ? 'mcp.tools' :
    item.configKind === 'inspection' ? 'diagnostics.inspections' : 'diagnostics.rules';
  const current = {...(explicitValue<Record<string, boolean | Severity>>(configuration, key) || {})};
  if (value === null) delete current[item.id];
  else current[item.id] = value;
  await configuration.update(key, Object.keys(current).length ? current : undefined, target);
}

async function updateProjectConfiguration(
  folder: vscode.WorkspaceFolder,
  item: ConfigurableItem,
  value: boolean | Severity | null,
): Promise<void> {
  const uri = vscode.Uri.joinPath(folder.uri, projectConfigurationPath);
  let document: Record<string, unknown> = {
    $schema: 'https://raw.githubusercontent.com/shopware/shopware-lsp/main/internal/projectconfig/schema.json',
    version: 1,
  };
  try {
    const parsed: unknown = parse(new TextDecoder().decode(await vscode.workspace.fs.readFile(uri)));
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error(`${uri.fsPath} must contain a YAML mapping`);
    }
    document = parsed as Record<string, unknown>;
  } catch (error) {
    if (!(error instanceof vscode.FileSystemError && error.code === 'FileNotFound')) throw error;
  }
  const path = item.configKind === 'feature' ? ['features', item.id] :
    item.configKind === 'mcpTool' ? ['mcp', 'tools', item.id] :
    item.configKind === 'domain' ? ['domains', item.id] :
    item.configKind === 'inspection' ? ['diagnostics', 'inspections', item.id] :
    item.configKind === 'rule' ? ['diagnostics', 'rules', item.id] : item.id.split('.');
  setNested(document, path, value);
  await vscode.workspace.fs.createDirectory(vscode.Uri.joinPath(folder.uri, projectConfigurationDirectory));
  await vscode.workspace.fs.writeFile(uri, new TextEncoder().encode(stringify(document)));
  await vscode.window.showTextDocument(uri, {preview: false});
}

function updateConfigurationErrors(
  collection: vscode.DiagnosticCollection | undefined,
  folder: vscode.WorkspaceFolder,
  issues?: ConfigurationIssue[],
  fallbackMessage?: string,
): void {
  if (!collection) return;
  collection.forEach(uri => {
    if (uri.scheme === 'file' && pathWithinRoot(folder.uri.fsPath, uri.fsPath)) {
      collection.delete(uri);
    }
  });
  const values = issues?.length ? issues : fallbackMessage ? [{
    path: vscode.Uri.joinPath(folder.uri, projectConfigurationPath).fsPath,
    message: fallbackMessage,
  }] : [];
  for (const issue of values) {
    const diagnostic = new vscode.Diagnostic(
      new vscode.Range(0, 0, 0, 1), issue.message, vscode.DiagnosticSeverity.Error,
    );
    diagnostic.source = 'shopware-lsp';
    diagnostic.code = 'shopware.config.invalid';
    collection.set(vscode.Uri.file(issue.path), [diagnostic]);
  }
}
