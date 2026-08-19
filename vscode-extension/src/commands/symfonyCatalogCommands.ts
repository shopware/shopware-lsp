import * as path from 'path';
import * as fs from 'fs';
import * as vscode from 'vscode';
import type {ClientState} from '../clientState';

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

export function registerSymfonyCatalogCommands(
  context: vscode.ExtensionContext,
  clientState: ClientState,
): void {
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
      const folder = await clientState.resolveWorkspaceFolder(
        resource,
        true,
        'Run Symfony Console Command',
      );
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
      const entry = await clientState.resolveEntry(
        resource,
        'Run Symfony Console Command',
      );
      if (!entry) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const commands = await entry.client.sendRequest<SymfonyConsoleCatalogEntry[]>(
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
        const commandResource = resource ?? entry.folder.uri;
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Browse Symfony Routes',
      );
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const routes = await languageClient.sendRequest<SymfonyRouteCatalogEntry[]>(
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Browse Local Symfony Profiler Requests',
      );
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const requests = await languageClient.sendRequest<
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Browse Doctrine Entities',
      );
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const entities = await languageClient.sendRequest<
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
        const fields = await languageClient.sendRequest<
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Browse Symfony Form Types',
      );
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const formTypes = await languageClient.sendRequest<
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
          const options = await languageClient.sendRequest<
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Locate Symfony Service',
      );
      if (!languageClient) {
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
        const services = await languageClient.sendRequest<
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

}
