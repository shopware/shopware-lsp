import * as path from 'path';
import * as vscode from 'vscode';
import type {ClientState} from '../clientState';

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

export function registerEditorCommands(
  context: vscode.ExtensionContext,
  clientState: ClientState,
): void {
  context.subscriptions.push(
    vscode.workspace.registerTextDocumentContentProvider(
      'shopware-block',
      blockContentProvider,
    ),
  );

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
      if (!clientState.client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      const result = await clientState.client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/storefront/getPossibleSnippetFiles', {
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

      await clientState.client.sendRequest('shopware/snippet/storefront/create', {
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
    if (!clientState.client) {
      vscode.window.showErrorMessage('Shopware LSP is not running');
      return;
    }

    const extensions: { Name: string; }[] = await clientState.client.sendRequest('shopware/extension/all');

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

    const result: {code: string, message: string} | {uri: string, line: number} = await clientState.client.sendRequest('shopware/twig/extendBlock', {
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
    if (!clientState.client) {
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

      const result: { code: string; message: string } | BlockDiffResponse = await clientState.client.sendRequest('shopware/twig/getBlockDiff', {
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
    if (!clientState.client) {
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
      snippets = await clientState.client.sendRequest('shopware/snippet/admin/all');
      insertFormat = "{{ \\$tc('${label}') }}";
    } else {
      // Fetch frontend snippets
      snippets = await clientState.client.sendRequest('shopware/snippet/storefront/all');
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
      if (!clientState.client) {
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
      const result = await clientState.client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/storefront/getPossibleSnippetFiles', {
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
      await clientState.client.sendRequest('shopware/snippet/storefront/create', {
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
      if (!clientState.client) {
        vscode.window.showErrorMessage('Shopware LSP is not running');
        return;
      }

      const result = await clientState.client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/admin/getPossibleSnippetFiles', {
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

      await clientState.client.sendRequest('shopware/snippet/admin/create', {
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
      if (!clientState.client) {
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
      const result = await clientState.client.sendRequest<{paths: SnippetFile[]}>('shopware/snippet/admin/getPossibleSnippetFiles', {
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
      await clientState.client.sendRequest('shopware/snippet/admin/create', {
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
