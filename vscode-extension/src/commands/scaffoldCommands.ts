import * as path from 'path';
import * as fs from 'fs';
import * as crypto from 'crypto';
import * as vscode from 'vscode';
import type {WorkspaceEdit as ProtocolWorkspaceEdit} from 'vscode-languageserver-protocol';
import {LanguageClient} from 'vscode-languageclient/node';
import type {ClientState} from '../clientState';
import {openEntityDesigner} from '../entityDesigner';

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

type NewFileBackend = 'shopware' | 'symfony' | 'designer';

interface NewFileScaffoldItem extends vscode.QuickPickItem {
  backend?: NewFileBackend;
  scaffoldKind?: string;
  placeHolder?: string;
}

const newFileScaffolds: NewFileScaffoldItem[] = [
  {label: 'Shopware', kind: vscode.QuickPickItemKind.Separator},
  {
    label: 'DAL Entity Definition',
    description: 'Visual entity, migration, service, and snapshot designer',
    backend: 'designer',
    scaffoldKind: 'entity-definition',
  },
  {
    label: 'System Configuration',
    description: 'Resources/config/config.xml',
    backend: 'shopware',
    scaffoldKind: 'system-config',
    placeHolder: 'configuration',
  },
  {
    label: 'Scheduled Task',
    description: 'Task and handler PHP classes',
    backend: 'shopware',
    scaffoldKind: 'scheduled-task',
    placeHolder: 'Cleanup',
  },
  {
    label: 'Migration',
    description: 'Timestamped Shopware migration',
    backend: 'shopware',
    scaffoldKind: 'migration',
    placeHolder: 'AddProductIndex',
  },
  {
    label: 'Plugin Skeleton',
    description: 'Minimal composer package and plugin class',
    backend: 'shopware',
    scaffoldKind: 'plugin',
    placeHolder: 'AcmeExample',
  },
  {label: 'Administration', kind: vscode.QuickPickItemKind.Separator},
  {
    label: 'Administration Component',
    description: 'JavaScript, Twig, and SCSS component files',
    backend: 'shopware',
    scaffoldKind: 'admin-component',
    placeHolder: 'sw-example-card',
  },
  {
    label: 'Administration Module',
    description: 'Module registration and translations',
    backend: 'shopware',
    scaffoldKind: 'admin-module',
    placeHolder: 'sw-example',
  },
  {
    label: 'CMS Block',
    description: 'Administration block and preview components',
    backend: 'shopware',
    scaffoldKind: 'cms-block',
    placeHolder: 'example-text',
  },
  {
    label: 'CMS Element',
    description: 'Administration element components',
    backend: 'shopware',
    scaffoldKind: 'cms-element',
    placeHolder: 'example-media',
  },
  {label: 'Apps', kind: vscode.QuickPickItemKind.Separator},
  {
    label: 'App Manifest',
    description: 'Minimal Shopware app manifest',
    backend: 'shopware',
    scaffoldKind: 'app',
    placeHolder: 'acme-example',
  },
  {
    label: 'App Custom Entities',
    description: 'Resources/entities.xml',
    backend: 'shopware',
    scaffoldKind: 'app-custom-entities',
    placeHolder: 'catalog-entry',
  },
  {
    label: 'App CMS Configuration',
    description: 'Resources/cms.xml',
    backend: 'shopware',
    scaffoldKind: 'app-cms',
    placeHolder: 'cms',
  },
  {
    label: 'App Script',
    description: 'Twig script hook',
    backend: 'shopware',
    scaffoldKind: 'app-script',
    placeHolder: 'product-page-loaded',
  },
  {label: 'Symfony', kind: vscode.QuickPickItemKind.Separator},
  {
    label: 'Command',
    description: 'Symfony Console command',
    backend: 'symfony',
    scaffoldKind: 'command',
    placeHolder: 'CacheWarm',
  },
  {
    label: 'Controller',
    description: 'Controller with an index route',
    backend: 'symfony',
    scaffoldKind: 'controller',
    placeHolder: 'Product',
  },
  {
    label: 'Form Type',
    description: 'Symfony form type',
    backend: 'symfony',
    scaffoldKind: 'form',
    placeHolder: 'ProductType',
  },
  {
    label: 'Twig Extension',
    description: 'Twig functions and filters',
    backend: 'symfony',
    scaffoldKind: 'twig-extension',
    placeHolder: 'PriceExtension',
  },
  {
    label: 'Compiler Pass',
    description: 'Dependency-injection compiler pass',
    backend: 'symfony',
    scaffoldKind: 'compiler-pass',
    placeHolder: 'CollectServicesPass',
  },
  {label: 'Symfony Tests', kind: vscode.QuickPickItemKind.Separator},
  {
    label: 'Kernel Test',
    description: 'KernelTestCase integration test',
    backend: 'symfony',
    scaffoldKind: 'kernel-test',
    placeHolder: 'Container',
  },
  {
    label: 'Web Test',
    description: 'WebTestCase functional test',
    backend: 'symfony',
    scaffoldKind: 'web-test',
    placeHolder: 'Storefront',
  },
  {label: 'Symfony Configuration', kind: vscode.QuickPickItemKind.Separator},
  {
    label: 'YAML Service Configuration',
    description: 'Autowiring service prototype',
    backend: 'symfony',
    scaffoldKind: 'services-yaml',
    placeHolder: 'services',
  },
  {
    label: 'XML Service Configuration',
    description: 'Autowiring service prototype',
    backend: 'symfony',
    scaffoldKind: 'services-xml',
    placeHolder: 'services',
  },
  {
    label: 'PHP Service Configuration',
    description: 'Fluent service configurator',
    backend: 'symfony',
    scaffoldKind: 'services-php',
    placeHolder: 'services',
  },
];

async function chooseNewFileDirectory(
  scaffold: NewFileScaffoldItem,
  resource?: vscode.Uri,
  selectedResources?: vscode.Uri[],
): Promise<vscode.Uri | undefined> {
  const candidate = selectedResources?.[0] ?? resource;
  let defaultUri: vscode.Uri | undefined;
  if (candidate?.scheme === 'file') {
    try {
      if (fs.statSync(candidate.fsPath).isDirectory()) {
        return candidate;
      }
      defaultUri = vscode.Uri.file(path.dirname(candidate.fsPath));
    } catch {
      // Fall through to the directory picker.
    }
  }
  if (!defaultUri) {
    const activeUri = vscode.window.activeTextEditor?.document.uri;
    defaultUri = activeUri?.scheme === 'file'
      ? vscode.Uri.file(path.dirname(activeUri.fsPath))
      : vscode.workspace.workspaceFolders?.[0]?.uri;
  }
  const selected = await vscode.window.showOpenDialog({
    title: `Select the directory for the new ${scaffold.label}`,
    defaultUri,
    canSelectFiles: false,
    canSelectFolders: true,
    canSelectMany: false,
    openLabel: 'Use Directory',
  });
  return selected?.[0];
}

async function createSymfonyNewFile(
  languageClient: LanguageClient,
  scaffold: NewFileScaffoldItem,
  directoryUri: vscode.Uri,
): Promise<void> {
  const scaffoldKind = scaffold.scaffoldKind!;
  const serviceConfiguration = scaffoldKind.startsWith('services-');
  const name = await vscode.window.showInputBox({
    title: `Shopware: New ${scaffold.label}`,
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

  const result = await languageClient.sendRequest<SymfonyScaffoldCreation>(
    'shopware/symfony/scaffold/create',
    {
      kind: scaffoldKind,
      directoryUri: directoryUri.toString(),
      name: name.trim(),
    },
  );
  const fileUri = vscode.Uri.parse(result.fileUri);
  const edit = new vscode.WorkspaceEdit();
  edit.createFile(fileUri, {ignoreIfExists: false, overwrite: false});
  edit.insert(fileUri, new vscode.Position(0, 0), result.content);
  if (!await vscode.workspace.applyEdit(edit)) {
    throw new Error(`Could not create ${path.basename(fileUri.fsPath)}`);
  }
  const document = await vscode.workspace.openTextDocument(fileUri);
  await vscode.window.showTextDocument(document, {
    preview: false,
    preserveFocus: false,
  });
  vscode.window.showInformationMessage(
    `Created ${path.basename(fileUri.fsPath)}`,
  );
}

async function createShopwareNewFile(
  languageClient: LanguageClient,
  scaffold: NewFileScaffoldItem,
  directoryUri: vscode.Uri,
): Promise<void> {
  const scaffoldKind = scaffold.scaffoldKind!;
  const name = await vscode.window.showInputBox({
    title: `Shopware: New ${scaffold.label}`,
    prompt: 'Artifact name (letters, digits, dashes, and underscores)',
    placeHolder: scaffold.placeHolder,
    value: scaffoldKind === 'system-config' ? 'configuration' :
      scaffoldKind === 'app-cms' ? 'cms' : undefined,
    validateInput: value => /^[A-Za-z][A-Za-z0-9_-]*$/.test(value.trim())
      ? null
      : 'Start with a letter and use letters, digits, dashes, or underscores',
  });
  if (!name) {
    return;
  }

  const options: Record<string, string | number | boolean> = {};
  if (scaffoldKind === 'scheduled-task') {
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
  if (scaffoldKind === 'app-script') {
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
  if (scaffoldKind === 'cms-block') {
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

  const result = await languageClient.sendRequest<ShopwareScaffoldCreation>(
    'shopware/scaffold/create',
    {
      kind: scaffoldKind,
      directoryUri: directoryUri.toString(),
      name: name.trim(),
      options,
    },
  );
  const edit = await languageClient.protocol2CodeConverter.asWorkspaceEdit(
    result.edit,
  );
  if (!await vscode.workspace.applyEdit(edit)) {
    throw new Error(`Could not create ${scaffold.label}`);
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
}

async function applyShopwareScaffoldCreation(
  clientState: ClientState,
  request: ShopwareScaffoldRequest,
  label: string,
): Promise<void> {
  if (!clientState.client) {
    throw new Error('Shopware LSP is not running');
  }
  const result = await clientState.client.sendRequest<ShopwareScaffoldCreation>(
    'shopware/scaffold/create',
    request,
  );
  const edit = await clientState.client.protocol2CodeConverter.asWorkspaceEdit(result.edit);
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
  clientState: ClientState,
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
  await applyShopwareScaffoldCreation(clientState, {
    kind: 'admin-component',
    directoryUri: directory.toString(),
    name,
    options,
  }, method ? `${component}.${method}() override` : `${component} extension`);
}

export function registerScaffoldCommands(
  context: vscode.ExtensionContext,
  clientState: ClientState,
): void {
  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.createFile',
    async (
      resource?: vscode.Uri,
      selectedResources?: vscode.Uri[],
    ) => {
      const languageClient = clientState.client;
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      const scaffold = await vscode.window.showQuickPick(newFileScaffolds, {
        title: 'Shopware: New File',
        placeHolder: 'Select a Shopware or Symfony artifact',
        matchOnDescription: true,
      });
      if (!scaffold?.backend || !scaffold.scaffoldKind) {
        return;
      }

      const directoryUri = await chooseNewFileDirectory(
        scaffold,
        resource,
        selectedResources,
      );
      if (!directoryUri) {
        return;
      }

      try {
        if (scaffold.backend === 'designer') {
          await openEntityDesigner(context, languageClient, directoryUri);
        } else if (scaffold.backend === 'symfony') {
          await createSymfonyNewFile(languageClient, scaffold, directoryUri);
        } else {
          await createShopwareNewFile(languageClient, scaffold, directoryUri);
        }
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to create ${scaffold.label}: ${error}`,
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
        await createAdminComponentExtension(clientState, component, sourceUri);
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
          clientState,
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
        await applyShopwareScaffoldCreation(clientState, {
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
          code: `{{ $t('${key}') }}`,
        },
        {
          label: 'Administration JavaScript',
          code: `this.$t('${key}')`,
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
}
