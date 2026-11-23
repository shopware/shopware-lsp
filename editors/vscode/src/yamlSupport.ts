import * as vscode from 'vscode';
import {hasShopwareSchema, YAML_EXTENSION_ID} from './yamlSupportModel';

const DISMISSED_KEY = 'shopwareLSP.yamlSupportDismissed';

/**
 * The keys and fixed values of the files the server has a schema for are
 * completed and validated by the YAML extension. It is not required: this
 * suggests it once when such a file is opened without it.
 */
export function registerYamlSupportHint(context: vscode.ExtensionContext): void {
  let asked = false;
  const suggest = async (document: vscode.TextDocument) => {
    if (asked
      || document.uri.scheme !== 'file'
      || !hasShopwareSchema(document.uri.fsPath)
      || context.globalState.get<boolean>(DISMISSED_KEY)
      || vscode.extensions.getExtension(YAML_EXTENSION_ID)) {
      return;
    }
    // Once per session, whatever the answer.
    asked = true;
    const install = 'Install';
    const never = "Don't Show Again";
    const choice = await vscode.window.showInformationMessage(
      'Install the YAML extension to get completion and validation for the keys of this file. '
        + 'Shopware LSP completes the entities, properties and locales of your project either way.',
      install,
      never,
    );
    if (choice === install) {
      await vscode.commands.executeCommand(
        'workbench.extensions.installExtension',
        YAML_EXTENSION_ID,
      );
    } else if (choice === never) {
      await context.globalState.update(DISMISSED_KEY, true);
    }
  };
  context.subscriptions.push(
    vscode.workspace.onDidOpenTextDocument(document => void suggest(document)),
  );
  for (const document of vscode.workspace.textDocuments) {
    void suggest(document);
  }
}
