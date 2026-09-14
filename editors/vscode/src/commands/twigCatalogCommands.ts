import * as vscode from 'vscode';
import type {ClientState} from '../clientState';

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

export function registerTwigCatalogCommands(
  context: vscode.ExtensionContext,
  clientState: ClientState,
): void {
  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.browseTwigExtensions',
    async () => {
      const languageClient = await clientState.resolveClient(
        undefined,
        'Browse Twig Extensions',
      );
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const extensions = await languageClient.sendRequest<
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Analyze Twig Template Usages',
      );
      if (!languageClient) {
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
        const entries = await languageClient.sendRequest<
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Browse Twig Components',
      );
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const components = await languageClient.sendRequest<
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
}
