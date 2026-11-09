//! Computed server capabilities: a capability is advertised only when a
//! provider is registered and its feature is enabled.

use std::collections::BTreeSet;

use serde_json::{Map, Value, json};
use swlsp_lsp_protocol as protocol;

use crate::server::{Registry, Server, read};

impl Server {
    pub(crate) fn server_capabilities(&self) -> Value {
        let registry = self.registry();
        let mut capabilities = Map::new();
        capabilities.insert(
            "textDocumentSync".into(),
            json!({"change": 1, "openClose": true}),
        );
        capabilities.insert(
            "experimental".into(),
            json!({"shopwareLSP": self.negotiated_client_state(true, "")}),
        );
        let session = read(&self.session).clone();
        let diagnostics_enabled = self.diagnostics_enabled();
        let has_inspections = !registry.inspections.is_empty();
        if session.pull_diagnostics && has_inspections && diagnostics_enabled {
            capabilities.insert(
                "diagnosticProvider".into(),
                json!({"interFileDependencies": true, "workspaceDiagnostics": true}),
            );
        }
        if !registry.completion.is_empty() && self.feature_enabled("completion") {
            capabilities.insert(
                "completionProvider".into(),
                json!({"triggerCharacters": collect_trigger_characters(&registry)}),
            );
        }
        let flags: [(&str, usize, &str); 16] = [
            (
                "definitionProvider",
                registry.definition.len(),
                "definition",
            ),
            (
                "implementationProvider",
                registry.implementation.len(),
                "implementation",
            ),
            (
                "typeHierarchyProvider",
                registry.type_hierarchy.len(),
                "typeHierarchy",
            ),
            (
                "callHierarchyProvider",
                registry.call_hierarchy.len(),
                "callHierarchy",
            ),
            (
                "referencesProvider",
                registry.references.len(),
                "references",
            ),
            ("renameProvider", registry.rename.len(), "rename"),
            (
                "workspaceSymbolProvider",
                registry.workspace_symbols.len(),
                "workspaceSymbols",
            ),
            (
                "documentSymbolProvider",
                registry.document_symbols.len(),
                "documentSymbols",
            ),
            (
                "documentHighlightProvider",
                registry.document_highlights.len(),
                "documentHighlights",
            ),
            (
                "linkedEditingRangeProvider",
                registry.linked_editing.len(),
                "linkedEditing",
            ),
            (
                "foldingRangeProvider",
                registry.folding_ranges.len(),
                "foldingRanges",
            ),
            (
                "documentFormattingProvider",
                registry.formatting.len(),
                "formatting",
            ),
            (
                "selectionRangeProvider",
                registry.selection_ranges.len(),
                "selectionRanges",
            ),
            ("colorProvider", registry.colors.len(), "documentColors"),
            ("hoverProvider", registry.hover.len(), "hover"),
            (
                "inlayHintProvider",
                registry.inlay_hints.len(),
                "inlayHints",
            ),
        ];
        for (name, count, feature) in flags {
            if count > 0 && self.feature_enabled(feature) {
                capabilities.insert(name.into(), Value::Bool(true));
            }
        }
        if !registry.document_links.is_empty() && self.feature_enabled("documentLinks") {
            capabilities.insert(
                "documentLinkProvider".into(),
                json!({"resolveProvider": false}),
            );
        }
        if !registry.semantic_tokens.is_empty() && self.feature_enabled("semanticTokens") {
            capabilities.insert(
                "semanticTokensProvider".into(),
                json!({
                    "full": true,
                    // Both legend arrays must serialize as arrays even when
                    // empty: strict clients reject null.
                    "legend": protocol::SemanticTokensLegend::shopware(),
                    "range": false,
                }),
            );
        }
        if !registry.signature_help.is_empty() && self.feature_enabled("signatureHelp") {
            capabilities.insert(
                "signatureHelpProvider".into(),
                json!({"retriggerCharacters": [","], "triggerCharacters": ["(", ","]}),
            );
        }
        if !registry.code_lens.is_empty() && self.feature_enabled("codeLens") {
            capabilities.insert("codeLensProvider".into(), json!({"resolveProvider": true}));
        }
        let inspection_actions = has_inspections && diagnostics_enabled;
        if (!registry.actions.is_empty() || inspection_actions)
            && self.feature_enabled("codeActions")
        {
            capabilities.insert(
                "codeActionProvider".into(),
                json!({
                    "codeActionKinds": collect_code_action_kinds(&registry, inspection_actions),
                    "resolveProvider": session.code_action_resolve_support,
                }),
            );
        }
        if !registry.file_rename.is_empty() && self.feature_enabled("fileRename") {
            let operations = protocol::FileOperationOptions {
                will_rename: Some(protocol::FileOperationRegistrationOptions {
                    filters: vec![protocol::FileOperationFilter {
                        scheme: "file".into(),
                        pattern: protocol::FileOperationPattern {
                            glob: "**/*.twig".into(),
                            matches: "file".into(),
                            ..protocol::FileOperationPattern::default()
                        },
                    }],
                }),
                ..protocol::FileOperationOptions::default()
            };
            capabilities.insert("workspace".into(), json!({"fileOperations": operations}));
        }
        if !registry.commands.is_empty()
            && self.feature_enabled("commands")
            && !session.options.omit_execute_command_provider
        {
            capabilities.insert(
                "executeCommandProvider".into(),
                json!({"commands": registry.commands.keys().collect::<Vec<_>>()}),
            );
        }
        Value::Object(capabilities)
    }
}

fn collect_trigger_characters(registry: &Registry) -> Vec<String> {
    let unique: BTreeSet<String> = registry
        .completion
        .iter()
        .flat_map(|provider| provider.trigger_characters())
        .collect();
    unique.into_iter().collect()
}

fn collect_code_action_kinds(registry: &Registry, inspection_actions: bool) -> Vec<String> {
    let mut unique: BTreeSet<String> = registry
        .actions
        .iter()
        .flat_map(|provider| provider.code_action_kinds())
        .map(|kind| kind.as_str().to_owned())
        .collect();
    if inspection_actions {
        unique.insert(protocol::CodeActionKind::QUICKFIX.as_str().to_owned());
    }
    unique.into_iter().collect()
}
