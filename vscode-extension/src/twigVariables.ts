import * as path from 'path';
import * as vscode from 'vscode';
import {LanguageClient} from 'vscode-languageclient/node';

interface TwigTemplateSourceLocation {
  fileUri: string;
  line?: number;
}

interface TwigTemplateVariableSource {
  kind: string;
  fileUri?: string;
  line?: number;
}

interface TwigTemplateVariableProperty {
  name: string;
  kind: string;
  type?: string;
  class?: string;
  fileUri?: string;
  sourceLine?: number;
  deprecated?: boolean;
}

interface TwigTemplateVariableEntry {
  name: string;
  type: string;
  types?: string[];
  properties?: TwigTemplateVariableProperty[];
  sources?: TwigTemplateVariableSource[];
}

interface TwigTemplateVariableCatalogEntry {
  template: string;
  files?: TwigTemplateSourceLocation[];
  variables?: TwigTemplateVariableEntry[];
}

interface TwigVariableExpressionItem extends vscode.QuickPickItem {
  expression: string;
  variable: TwigTemplateVariableEntry;
  property?: TwigTemplateVariableProperty;
}

type TwigVariableAction =
  | 'print'
  | 'conditional'
  | 'loop'
  | 'loopProperty'
  | 'navigate';

interface TwigVariableActionItem extends vscode.QuickPickItem {
  action: TwigVariableAction;
}

interface TwigVariableLocation extends vscode.QuickPickItem {
  fileUri: string;
  line: number;
}

const twigTemplateVariableRequest =
  'shopware/symfony/analytics/twig/templateVariables';

export function registerTwigVariableCommands(
  context: vscode.ExtensionContext,
  getClient: () => LanguageClient | undefined,
): void {
  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.analyzeTwigTemplateVariables',
    async () => {
      const activeClient = getClient();
      if (!activeClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      const template = await vscode.window.showInputBox({
        title: 'Analyze Twig Template Variables',
        prompt: 'Enter logical template names or a project-relative path',
        placeHolder: 'home/index.html.twig',
      });
      if (!template?.trim()) {
        return;
      }
      try {
        const entries = await loadTwigVariableCatalog(
          activeClient,
          template.trim(),
        );
        if (entries.length === 0) {
          vscode.window.showInformationMessage(
            `No indexed Twig templates matched ${template.trim()}.`,
          );
          return;
        }
        const selectedEntry = await selectCatalogEntry(entries);
        if (!selectedEntry) {
          return;
        }
        const selectedVariable = await vscode.window.showQuickPick(
          (selectedEntry.variables ?? []).map(variable => ({
            label: `$(symbol-variable) ${variable.name}`,
            description: variable.type,
            detail: [
              `${variable.properties?.length ?? 0} accessible members`,
              uniqueSourceKinds(variable).join(', '),
            ].filter(Boolean).join(' · '),
            variable,
          })),
          {
            title: `Variables in ${selectedEntry.template}`,
            placeHolder: 'Select a typed Twig variable',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!selectedVariable) {
          return;
        }
        const locations = [
          ...variableLocations(selectedVariable.variable),
          ...(selectedVariable.variable.properties ?? [])
            .flatMap(property => propertyLocations(property)),
        ];
        if (locations.length === 0) {
          vscode.window.showInformationMessage(
            `No source locations are available for ${
              selectedVariable.variable.name
            }.`,
          );
          return;
        }
        await navigateToTwigVariableLocation(
          locations,
          `${selectedVariable.variable.name}: ${
            selectedVariable.variable.type
          }`,
        );
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to analyze Twig template variables: ${error}`,
        );
      }
    },
  ));

  context.subscriptions.push(vscode.commands.registerCommand(
    'shopware.symfony.twigVariables',
    async (fileUri: string, variableNames?: string[]) => {
      const activeClient = getClient();
      if (!activeClient) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }
      if (typeof fileUri !== 'string' || fileUri.trim() === '') {
        vscode.window.showErrorMessage(
          'Cannot browse Twig variables without a template file.',
        );
        return;
      }
      try {
        const uri = vscode.Uri.parse(fileUri);
        const template = templatePathForUri(uri);
        const entries = await loadTwigVariableCatalog(
          activeClient,
          template,
        );
        const variables = mergeTwigVariables(entries, variableNames);
        if (variables.length === 0) {
          vscode.window.showInformationMessage(
            'No typed controller variables are indexed for this template.',
          );
          return;
        }
        const expression = await vscode.window.showQuickPick(
          twigVariableExpressionItems(variables),
          {
            title: `Twig Variables (${variables.length})`,
            placeHolder:
              'Select a variable or accessible member to insert or navigate',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!expression) {
          return;
        }
        const action = await vscode.window.showQuickPick(
          twigVariableActions(expression),
          {
            title: expression.expression,
            placeHolder: 'Choose an action',
            matchOnDescription: true,
            matchOnDetail: true,
          },
        );
        if (!action) {
          return;
        }
        if (action.action === 'navigate') {
          const locations = expression.property
            ? propertyLocations(expression.property)
            : variableLocations(expression.variable);
          await navigateToTwigVariableLocation(
            locations,
            expression.expression,
          );
          return;
        }
        const snippet = twigVariableSnippet(action.action, expression);
        await insertTwigSnippetAtCaret(uri, snippet);
      } catch (error) {
        vscode.window.showErrorMessage(
          `Failed to browse Twig template variables: ${error}`,
        );
      }
    },
  ));
}

async function loadTwigVariableCatalog(
  activeClient: LanguageClient,
  template: string,
): Promise<TwigTemplateVariableCatalogEntry[]> {
  return activeClient.sendRequest<TwigTemplateVariableCatalogEntry[]>(
    twigTemplateVariableRequest,
    {template},
  );
}

async function selectCatalogEntry(
  entries: TwigTemplateVariableCatalogEntry[],
): Promise<TwigTemplateVariableCatalogEntry | undefined> {
  if (entries.length === 1) {
    return entries[0];
  }
  const selected = await vscode.window.showQuickPick(
    entries.map(entry => ({
      label: `$(file-code) ${entry.template}`,
      description: `${entry.variables?.length ?? 0} variables`,
      entry,
    })),
    {title: 'Select Twig Template'},
  );
  return selected?.entry;
}

function templatePathForUri(uri: vscode.Uri): string {
  const folder = vscode.workspace.getWorkspaceFolder(uri);
  if (folder && uri.scheme === 'file') {
    const relative = path.relative(folder.uri.fsPath, uri.fsPath);
    if (relative !== '..' && !relative.startsWith(`..${path.sep}`)) {
      return relative.split(path.sep).join('/');
    }
  }
  return vscode.workspace.asRelativePath(uri, false);
}

function mergeTwigVariables(
  entries: TwigTemplateVariableCatalogEntry[],
  variableNames?: string[],
): TwigTemplateVariableEntry[] {
  const selectedNames = new Set(
    (variableNames ?? [])
      .filter(name => typeof name === 'string')
      .map(name => name.toLocaleLowerCase()),
  );
  const values = new Map<string, TwigTemplateVariableEntry>();
  for (const entry of entries) {
    for (const variable of entry.variables ?? []) {
      const key = variable.name.toLocaleLowerCase();
      if (selectedNames.size > 0 && !selectedNames.has(key)) {
        continue;
      }
      const current = values.get(key);
      if (!current) {
        values.set(key, {
          ...variable,
          types: [...(variable.types ?? [])],
          properties: [...(variable.properties ?? [])],
          sources: [...(variable.sources ?? [])],
        });
        continue;
      }
      current.types = uniqueStrings([
        ...(current.types ?? []),
        ...(variable.types ?? []),
      ]);
      if (current.types.length > 0) {
        current.type = current.types.join('|');
      } else if (
        current.type === 'unknown' &&
        variable.type !== 'unknown'
      ) {
        current.type = variable.type;
      }
      current.properties = mergeProperties(
        current.properties ?? [],
        variable.properties ?? [],
      );
      current.sources = mergeSources(
        current.sources ?? [],
        variable.sources ?? [],
      );
    }
  }
  return [...values.values()].sort((left, right) => {
    if (left.name === 'app') {
      return 1;
    }
    if (right.name === 'app') {
      return -1;
    }
    return left.name.localeCompare(right.name);
  });
}

function mergeProperties(
  left: TwigTemplateVariableProperty[],
  right: TwigTemplateVariableProperty[],
): TwigTemplateVariableProperty[] {
  const values = new Map<string, TwigTemplateVariableProperty>();
  for (const property of [...left, ...right]) {
    const key = property.name.toLocaleLowerCase();
    const current = values.get(key);
    if (!current || (!current.fileUri && property.fileUri)) {
      values.set(key, property);
    }
  }
  return [...values.values()].sort((a, b) => a.name.localeCompare(b.name));
}

function mergeSources(
  left: TwigTemplateVariableSource[],
  right: TwigTemplateVariableSource[],
): TwigTemplateVariableSource[] {
  const values = new Map<string, TwigTemplateVariableSource>();
  for (const source of [...left, ...right]) {
    const key = `${source.kind}\0${source.fileUri ?? ''}\0${source.line ?? 1}`;
    values.set(key, source);
  }
  return [...values.values()];
}

function uniqueStrings(values: string[]): string[] {
  return [...new Set(values.filter(Boolean))].sort((left, right) =>
    left.localeCompare(right),
  );
}

function uniqueSourceKinds(variable: TwigTemplateVariableEntry): string[] {
  return uniqueStrings((variable.sources ?? []).map(source => source.kind));
}

function twigVariableExpressionItems(
  variables: TwigTemplateVariableEntry[],
): TwigVariableExpressionItem[] {
  const items: TwigVariableExpressionItem[] = [];
  for (const variable of variables) {
    items.push({
      label: `$(symbol-variable) ${variable.name}`,
      description: variable.type || 'unknown',
      detail: [
        `${variable.properties?.length ?? 0} accessible members`,
        uniqueSourceKinds(variable).join(', '),
      ].filter(Boolean).join(' · '),
      expression: variable.name,
      variable,
    });
    for (const property of variable.properties ?? []) {
      items.push({
        label: `$(symbol-property) ${variable.name}.${property.name}`,
        description: property.type || 'unknown',
        detail: [
          property.kind,
          property.class,
          property.deprecated ? 'deprecated' : '',
        ].filter(Boolean).join(' · '),
        expression: `${variable.name}.${property.name}`,
        variable,
        property,
      });
    }
  }
  return items;
}

function twigVariableActions(
  item: TwigVariableExpressionItem,
): TwigVariableActionItem[] {
  const result: TwigVariableActionItem[] = [];
  const expressionType = item.property?.type ?? item.variable.type;
  if (item.property && isTwigListType(item.variable.type)) {
    result.push({
      label: '$(symbol-array) Insert loop with property access',
      description: `{% for ${singularizeTwigName(
        item.variable.name,
      )} in ${item.variable.name} %}`,
      detail: `Print ${singularizeTwigName(item.variable.name)}.${
        item.property.name
      } inside the loop`,
      action: 'loopProperty',
    });
  } else if (isTwigListType(expressionType)) {
    result.push({
      label: '$(symbol-array) Insert loop',
      description: `{% for ${singularizeTwigName(
        item.property?.name ?? item.variable.name,
      )} in ${item.expression} %}`,
      action: 'loop',
    });
  } else if (isTwigBooleanType(expressionType)) {
    result.push({
      label: '$(symbol-boolean) Insert conditional',
      description: `{% if ${item.expression} %}`,
      action: 'conditional',
    });
  }
  result.push({
    label: '$(add) Insert expression',
    description: `{{ ${item.expression} }}`,
    action: 'print',
  });
  const locations = item.property
    ? propertyLocations(item.property)
    : variableLocations(item.variable);
  if (locations.length > 0) {
    result.push({
      label: '$(go-to-file) Go to declaration',
      description: locations.length === 1
        ? locations[0].description
        : `${locations.length} declarations`,
      action: 'navigate',
    });
  }
  return result;
}

function twigVariableSnippet(
  action: Exclude<TwigVariableAction, 'navigate'>,
  item: TwigVariableExpressionItem,
): string {
  switch (action) {
  case 'conditional':
    return `\n{% if ${item.expression} %}\n    $0\n{% endif %}`;
  case 'loop': {
    const itemName = singularizeTwigName(
      item.property?.name ?? item.variable.name,
    );
    if (item.property) {
      return `\n{% for ${itemName} in ${item.expression} %}\n    {{ ${itemName} }}\n{% endfor %}$0`;
    }
    return `\n{% for ${itemName} in ${item.expression} %}\n    $0\n{% endfor %}`;
  }
  case 'loopProperty': {
    const itemName = singularizeTwigName(item.variable.name);
    return `\n{% for ${itemName} in ${item.variable.name} %}\n    {{ ${itemName}.${item.property?.name ?? ''} }}\n{% endfor %}$0`;
  }
  case 'print':
    return `\n{{ ${item.expression} }}$0`;
  }
}

function singularizeTwigName(value: string): string {
  const lastSegment = value.split('.').pop() ?? value;
  const word = lastSegment.replace(/[^a-zA-Z0-9_]/g, '') || 'item';
  if (word.endsWith('ies') && word.length > 3) {
    return `${word.slice(0, -3)}y`;
  }
  if (
    /(ches|shes|sses|xes|zes|oes)$/.test(word) &&
    word.length > 2
  ) {
    return word.slice(0, -2);
  }
  if (word.endsWith('s') && !word.endsWith('ss') && word.length > 1) {
    return word.slice(0, -1);
  }
  return `${word}Item`;
}

function isTwigBooleanType(value: string | undefined): boolean {
  if (!value) {
    return false;
  }
  const members = value
    .toLocaleLowerCase()
    .split('|')
    .map(member => member.trim())
    .filter(member => member !== '' && member !== 'null');
  return members.length > 0 && members.every(member =>
    member === 'bool' ||
    member === 'boolean' ||
    member === 'true' ||
    member === 'false',
  );
}

function isTwigListType(value: string | undefined): boolean {
  const members = value
    ?.toLocaleLowerCase()
    .replace(/\s+/g, '')
    .split('|') ?? [];
  return members.some(type =>
    type.endsWith('[]') ||
    type === 'array' ||
    type.startsWith('array<') ||
    type.startsWith('array{') ||
    type === 'iterable' ||
    type.startsWith('iterable<') ||
    type.startsWith('list<') ||
    type.includes('collection') ||
    type.includes('traversable') ||
    type.includes('iterator') ||
    type.includes('generator'),
  );
}

function variableLocations(
  variable: TwigTemplateVariableEntry,
): TwigVariableLocation[] {
  const result: TwigVariableLocation[] = [];
  for (const source of variable.sources ?? []) {
    if (!source.fileUri) {
      continue;
    }
    result.push({
      label: `$(symbol-variable) ${variable.name}`,
      description: source.kind,
      detail: source.fileUri,
      fileUri: source.fileUri,
      line: source.line ?? 1,
    });
  }
  return uniqueLocations(result);
}

function propertyLocations(
  property: TwigTemplateVariableProperty,
): TwigVariableLocation[] {
  if (!property.fileUri) {
    return [];
  }
  return [{
    label: `$(symbol-property) ${property.name}`,
    description: [property.kind, property.type].filter(Boolean).join(' · '),
    detail: [
      property.class,
      property.deprecated ? 'deprecated' : '',
    ].filter(Boolean).join(' · '),
    fileUri: property.fileUri,
    line: property.sourceLine ?? 1,
  }];
}

function uniqueLocations(
  locations: TwigVariableLocation[],
): TwigVariableLocation[] {
  const values = new Map<string, TwigVariableLocation>();
  for (const location of locations) {
    values.set(`${location.fileUri}\0${location.line}`, location);
  }
  return [...values.values()];
}

async function navigateToTwigVariableLocation(
  locations: TwigVariableLocation[],
  title: string,
): Promise<void> {
  if (locations.length === 0) {
    vscode.window.showInformationMessage(
      `No source location is available for ${title}.`,
    );
    return;
  }
  const selected = locations.length === 1
    ? locations[0]
    : await vscode.window.showQuickPick(locations, {
      title,
      placeHolder: 'Select a source or accessible member',
      matchOnDescription: true,
      matchOnDetail: true,
    });
  if (!selected) {
    return;
  }
  const document = await vscode.workspace.openTextDocument(
    vscode.Uri.parse(selected.fileUri),
  );
  const editor = await vscode.window.showTextDocument(document, {
    preview: false,
  });
  const position = new vscode.Position(Math.max(0, selected.line - 1), 0);
  editor.selection = new vscode.Selection(position, position);
  editor.revealRange(
    new vscode.Range(position, position),
    vscode.TextEditorRevealType.InCenterIfOutsideViewport,
  );
}

async function insertTwigSnippetAtCaret(
  uri: vscode.Uri,
  snippet: string,
): Promise<void> {
  const active = vscode.window.activeTextEditor;
  const editor = active?.document.uri.toString() === uri.toString()
    ? active
    : await vscode.window.showTextDocument(
      await vscode.workspace.openTextDocument(uri),
      {preview: false},
    );
  const position = editor.document.lineAt(editor.selection.active.line).range.end;
  editor.selection = new vscode.Selection(position, position);
  await editor.insertSnippet(new vscode.SnippetString(snippet), position);
}
