package lsp

import (
	"sort"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
)

func (s *Server) serverCapabilities() map[string]interface{} {
	capabilities := map[string]interface{}{
		"textDocumentSync": map[string]interface{}{
			"openClose": true,
			"change":    1,
		},
		"experimental": map[string]interface{}{
			"shopwareLSP": s.negotiatedClientState(true, ""),
		},
	}
	if len(s.inspections.byID) > 0 && s.EffectiveConfiguration().Diagnostics.Enabled {
		capabilities["diagnosticProvider"] = map[string]interface{}{
			"interFileDependencies": true,
			"workspaceDiagnostics":  false,
		}
	}
	if len(s.completionProviders) > 0 && s.featureEnabled("completion") {
		capabilities["completionProvider"] = map[string]interface{}{
			"triggerCharacters": s.collectTriggerCharacters(),
		}
	}
	setProviderCapability(capabilities, "definitionProvider", len(s.definitionProviders), s.featureEnabled("definition"))
	setProviderCapability(capabilities, "implementationProvider", len(s.implementationProviders), s.featureEnabled("implementation"))
	setProviderCapability(capabilities, "typeHierarchyProvider", len(s.typeHierarchyProviders), s.featureEnabled("typeHierarchy"))
	setProviderCapability(capabilities, "callHierarchyProvider", len(s.callHierarchyProviders), s.featureEnabled("callHierarchy"))
	setProviderCapability(capabilities, "referencesProvider", len(s.referencesProviders), s.featureEnabled("references"))
	setProviderCapability(capabilities, "renameProvider", len(s.renameProviders), s.featureEnabled("rename"))
	setProviderCapability(capabilities, "workspaceSymbolProvider", len(s.workspaceSymbolProviders), s.featureEnabled("workspaceSymbols"))
	setProviderCapability(capabilities, "documentSymbolProvider", len(s.documentSymbolProviders), s.featureEnabled("documentSymbols"))
	setProviderCapability(capabilities, "documentHighlightProvider", len(s.documentHighlightProviders), s.featureEnabled("documentHighlights"))
	setProviderCapability(capabilities, "linkedEditingRangeProvider", len(s.linkedEditingProviders), s.featureEnabled("linkedEditing"))
	setProviderCapability(capabilities, "foldingRangeProvider", len(s.foldingRangeProviders), s.featureEnabled("foldingRanges"))
	setProviderCapability(capabilities, "selectionRangeProvider", len(s.selectionRangeProviders), s.featureEnabled("selectionRanges"))
	setProviderCapability(capabilities, "colorProvider", len(s.documentColorProviders), s.featureEnabled("documentColors"))
	setProviderCapability(capabilities, "hoverProvider", len(s.hoverProviders), s.featureEnabled("hover"))
	setProviderCapability(capabilities, "inlayHintProvider", len(s.inlayHintProviders), s.featureEnabled("inlayHints"))
	if len(s.documentLinkProviders) > 0 && s.featureEnabled("documentLinks") {
		capabilities["documentLinkProvider"] = map[string]interface{}{
			"resolveProvider": false,
		}
	}
	if len(s.semanticTokensProviders) > 0 && s.featureEnabled("semanticTokens") {
		capabilities["semanticTokensProvider"] = map[string]interface{}{
			"legend": protocol.SemanticTokensLegend{
				TokenTypes: append([]string(nil), protocol.SemanticTokenTypes...),
				TokenModifiers: append(
					[]string(nil), protocol.SemanticTokenModifiers...,
				),
			},
			"full":  true,
			"range": false,
		}
	}
	if len(s.signatureProviders) > 0 && s.featureEnabled("signatureHelp") {
		capabilities["signatureHelpProvider"] = map[string]interface{}{
			"triggerCharacters":   []string{"(", ","},
			"retriggerCharacters": []string{","},
		}
	}
	if len(s.codeLensProviders) > 0 && s.featureEnabled("codeLens") {
		capabilities["codeLensProvider"] = map[string]interface{}{
			"resolveProvider": true,
		}
	}
	hasInspectionActions := len(s.inspections.byID) > 0 &&
		s.EffectiveConfiguration().Diagnostics.Enabled
	if (len(s.actionProviders) > 0 || hasInspectionActions) &&
		s.featureEnabled("codeActions") {
		capabilities["codeActionProvider"] = map[string]interface{}{
			"codeActionKinds": s.collectCodeActionKinds(),
			"resolveProvider": s.codeActionResolveSupport,
		}
	}
	if len(s.fileRenameProviders) > 0 && s.featureEnabled("fileRename") {
		capabilities["workspace"] = map[string]interface{}{
			"fileOperations": protocol.FileOperationOptions{
				WillRename: &protocol.FileOperationRegistrationOptions{
					Filters: []protocol.FileOperationFilter{{
						Scheme: "file",
						Pattern: protocol.FileOperationPattern{
							Glob: "**/*.twig", Matches: "file",
						},
					}},
				},
			},
		}
	}
	if len(s.commandMap) > 0 && s.featureEnabled("commands") {
		commands := make([]string, 0, len(s.commandMap))
		for command := range s.commandMap {
			commands = append(commands, command)
		}
		sort.Strings(commands)
		capabilities["executeCommandProvider"] = map[string]interface{}{
			"commands": commands,
		}
	}
	return capabilities
}

func setProviderCapability(
	capabilities map[string]interface{},
	name string,
	count int,
	enabled bool,
) {
	if count > 0 && enabled {
		capabilities[name] = true
	}
}
