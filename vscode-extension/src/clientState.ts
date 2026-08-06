import type {LanguageClient} from 'vscode-languageclient/node';

export interface ClientState {
  client?: LanguageClient;
}
