import * as vscode from 'vscode';
import type {ClientState} from '../clientState';

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

export function registerSymfonyGenerationCommands(
  context: vscode.ExtensionContext,
  clientState: ClientState,
): void {
  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.generateService',
    async (fileUri: string, className: string) => {
      const languageClient = clientState.clientForUri(vscode.Uri.parse(fileUri));
      if (!languageClient) {
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
        const result = await languageClient.sendRequest<SymfonyServiceGeneration>(
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
      const languageClient = await clientState.resolveClient(
        undefined,
        'Generate Symfony Service Definitions',
      );
      if (!languageClient) {
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
        const result = await languageClient.sendRequest<
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
      const languageClient = clientState.clientForUri(vscode.Uri.parse(bundleUri));
      if (!languageClient) {
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
        const result = await languageClient.sendRequest<CompilerPassCreation>(
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
      const languageClient = clientState.clientForUri(vscode.Uri.parse(fileUri));
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      try {
        const uri = vscode.Uri.parse(fileUri);
        let document = await vscode.workspace.openTextDocument(uri);
        const candidates = await languageClient.sendRequest<FormFieldCandidates>(
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
        const generated = await languageClient.sendRequest<FormFieldGeneration>(
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
      const languageClient = clientState.clientForUri(vscode.Uri.parse(fileUri));
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      try {
        const candidates = await languageClient.sendRequest<TwigFormFieldCandidates>(
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
        const generated = await languageClient.sendRequest<FormFieldGeneration>(
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
      const languageClient = clientState.clientForUri(vscode.Uri.parse(fileUri));
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        const document = await vscode.workspace.openTextDocument(uri);
        const candidates = await languageClient.sendRequest<TwigTemplateCandidates>(
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
        const generated = await languageClient.sendRequest<FormFieldGeneration>(
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
      const languageClient = clientState.clientForUri(vscode.Uri.parse(fileUri));
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        const document = await vscode.workspace.openTextDocument(uri);
        const candidates = await languageClient.sendRequest<TwigBlockCandidates>(
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
        const generated = await languageClient.sendRequest<FormFieldGeneration>(
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
      const languageClient = clientState.clientForUri(vscode.Uri.parse(fileUri));
      if (!languageClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        let document = await vscode.workspace.openTextDocument(uri);
        const prepared = await languageClient.sendRequest<TwigTranslationExtractionPreparation>(
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
        const generated = await languageClient.sendRequest<TwigTranslationExtractionEdits>(
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
}
