import * as vscode from 'vscode';
import {createTwigWordPattern} from './languageConfigurationModel';

export function registerTwigLanguageConfiguration(
  context: vscode.ExtensionContext,
): void {
  context.subscriptions.push(
    vscode.languages.setLanguageConfiguration('twig', {
      wordPattern: createTwigWordPattern(),
    }),
  );
}
