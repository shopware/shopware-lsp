package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
)

type ConfigurationCatalog struct {
	Path        string                       `json:"path"`
	Effective   projectconfig.Effective      `json:"effective"`
	Features    []projectconfig.CatalogEntry `json:"features"`
	Domains     []projectconfig.CatalogEntry `json:"domains"`
	Inspections []ConfigurationInspection    `json:"inspections"`
	Error       string                       `json:"error,omitempty"`
}

type ConfigurationInspection struct {
	ID        string                    `json:"id"`
	Languages []string                  `json:"languages"`
	Rules     []ConfigurationDiagnostic `json:"rules"`
}

type ConfigurationDiagnostic struct {
	ID              string `json:"id"`
	Source          string `json:"source"`
	DefaultSeverity string `json:"defaultSeverity"`
}

type ConfigurationReloadResult struct {
	Applied         bool   `json:"applied"`
	RestartRequired bool   `json:"restartRequired"`
	Error           string `json:"error,omitempty"`
}

func (s *Server) initializeConfiguration(
	root string,
	options protocol.InitializationOptions,
) error {
	project, _, loadErr := projectconfig.Load(root)
	if loadErr != nil && options.CLIMode {
		return loadErr
	}
	if loadErr != nil {
		log.Printf("Invalid Shopware LSP configuration: %v", loadErr)
		project = projectconfig.Partial{}
	}
	editor := editorConfiguration(options)
	if err := projectconfig.Validate(editor); err != nil {
		if options.CLIMode {
			return fmt.Errorf("invalid editor configuration: %w", err)
		}
		loadErr = errors.Join(loadErr, fmt.Errorf("invalid editor configuration: %w", err))
		editor = projectconfig.Partial{}
	}
	effective := projectconfig.Resolve(project, editor)
	s.configurationMu.Lock()
	s.projectConfiguration = project
	s.editorConfiguration = editor
	s.effectiveConfiguration = effective
	s.configurationErr = loadErr
	s.configurationMu.Unlock()
	return nil
}

func editorConfiguration(options protocol.InitializationOptions) projectconfig.Partial {
	var result projectconfig.Partial
	if options.Configuration != nil {
		result = clonePartial(*options.Configuration)
	}
	if result.PHP == nil {
		result.PHP = &projectconfig.PHPConfig{}
	}
	if result.PHP.Extensions == nil && options.PHPExtensions != nil {
		values := slices.Clone(options.PHPExtensions)
		result.PHP.Extensions = &values
	}
	if result.PHP.DisabledExtensions == nil && options.DisabledPHPExtensions != nil {
		values := slices.Clone(options.DisabledPHPExtensions)
		result.PHP.DisabledExtensions = &values
	}
	if result.PHP.Extensions == nil && result.PHP.DisabledExtensions == nil {
		result.PHP = nil
	}
	if options.ShopwareTargetVersion != "" &&
		(result.Shopware == nil || result.Shopware.TargetVersion == nil) {
		if result.Shopware == nil {
			result.Shopware = &projectconfig.ShopwareConfig{}
		}
		value := options.ShopwareTargetVersion
		result.Shopware.TargetVersion = &value
	}
	return result
}

func clonePartial(value projectconfig.Partial) projectconfig.Partial {
	// Partial is small and JSON-compatible. Resolve notifications replace the
	// whole editor overlay, so a serialization clone avoids retaining maps owned
	// by a JSON-RPC decoder.
	encoded, _ := jsonMarshal(value)
	var result projectconfig.Partial
	_ = jsonUnmarshal(encoded, &result)
	return result
}

// These variables make clonePartial independently testable without exposing a
// serialization helper from projectconfig.
var jsonMarshal = json.Marshal
var jsonUnmarshal = json.Unmarshal

func (s *Server) EffectiveConfiguration() projectconfig.Effective {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return cloneEffective(s.effectiveConfiguration)
}

func cloneEffective(value projectconfig.Effective) projectconfig.Effective {
	encoded, _ := json.Marshal(value)
	var result projectconfig.Effective
	_ = json.Unmarshal(encoded, &result)
	return result
}

func (s *Server) featureEnabled(id string) bool {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.effectiveConfiguration.FeatureEnabled(id)
}

func (s *Server) domainEnabled(id string) bool {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.effectiveConfiguration.DomainEnabled(id)
}

// DomainEnabled exposes the immutable structural domain decision to workspace
// composition while keeping live configuration ownership in the server.
func (s *Server) DomainEnabled(id string) bool {
	return s.domainEnabled(id)
}

func (s *Server) diagnosticsEnabled() bool {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.effectiveConfiguration.Diagnostics.Enabled
}

func (s *Server) configurationError() error {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.configurationErr
}

func (s *Server) inspectionEnabled(id string) bool {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.effectiveConfiguration.InspectionEnabled(id)
}

func (s *Server) configuredRuleSeverity(
	id DiagnosticID,
) (projectconfig.Severity, bool) {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.effectiveConfiguration.RuleSeverity(string(id))
}

func (s *Server) configurationCatalog() ConfigurationCatalog {
	s.configurationMu.RLock()
	effective := cloneEffective(s.effectiveConfiguration)
	configurationErr := s.configurationErr
	s.configurationMu.RUnlock()
	result := ConfigurationCatalog{
		Path:      projectconfig.Path(s.rootPath),
		Effective: effective,
		Features:  slices.Clone(projectconfig.FeatureCatalog),
		Domains:   slices.Clone(projectconfig.DomainCatalog),
	}
	if configurationErr != nil {
		result.Error = configurationErr.Error()
	}
	for _, registered := range s.inspections.all() {
		inspection := ConfigurationInspection{ID: registered.definition.ID}
		for _, languageID := range registered.definition.Languages {
			inspection.Languages = append(inspection.Languages, string(languageID))
		}
		for _, problem := range registered.definition.Problems {
			inspection.Rules = append(inspection.Rules, ConfigurationDiagnostic{
				ID:              string(problem.ID),
				Source:          problem.Source,
				DefaultSeverity: diagnosticSeverityName(problem.DefaultSeverity),
			})
		}
		sort.Slice(inspection.Rules, func(i, j int) bool {
			return inspection.Rules[i].ID < inspection.Rules[j].ID
		})
		result.Inspections = append(result.Inspections, inspection)
	}
	sort.Slice(result.Inspections, func(i, j int) bool {
		return result.Inspections[i].ID < result.Inspections[j].ID
	})
	return result
}

func diagnosticSeverityName(value protocol.DiagnosticSeverity) string {
	switch value {
	case protocol.DiagnosticSeverityError:
		return string(projectconfig.SeverityError)
	case protocol.DiagnosticSeverityInformation:
		return string(projectconfig.SeverityInformation)
	case protocol.DiagnosticSeverityHint:
		return string(projectconfig.SeverityHint)
	default:
		return string(projectconfig.SeverityWarning)
	}
}

func protocolDiagnosticSeverity(value projectconfig.Severity) protocol.DiagnosticSeverity {
	switch value {
	case projectconfig.SeverityError:
		return protocol.DiagnosticSeverityError
	case projectconfig.SeverityInformation:
		return protocol.DiagnosticSeverityInformation
	case projectconfig.SeverityHint:
		return protocol.DiagnosticSeverityHint
	default:
		return protocol.DiagnosticSeverityWarning
	}
}

func (s *Server) validateConfiguredDiagnosticIDs() error {
	s.configurationMu.RLock()
	project := s.projectConfiguration
	editor := s.editorConfiguration
	s.configurationMu.RUnlock()
	inspectionIDs := make(map[string]bool, len(s.inspections.byID))
	ruleIDs := make(map[string]bool, len(s.inspections.byCode))
	for id := range s.inspections.byID {
		inspectionIDs[id] = true
	}
	for id := range s.inspections.byCode {
		ruleIDs[string(id)] = true
	}
	var validationErrors []error
	for _, layer := range []struct {
		name  string
		value projectconfig.Partial
	}{{"project", project}, {"editor", editor}} {
		validationErrors = append(validationErrors,
			validateDiagnosticLayer(layer.value, layer.name, inspectionIDs, ruleIDs))
	}
	return errors.Join(validationErrors...)
}

func validateDiagnosticLayer(
	value projectconfig.Partial,
	name string,
	inspectionIDs,
	ruleIDs map[string]bool,
) error {
	if value.Diagnostics == nil {
		return nil
	}
	var validationErrors []error
	for id := range value.Diagnostics.Inspections {
		if !inspectionIDs[id] {
			validationErrors = append(validationErrors,
				fmt.Errorf("%s configuration has unknown inspection %q", name, id))
		}
	}
	for id := range value.Diagnostics.Rules {
		if !ruleIDs[id] {
			validationErrors = append(validationErrors,
				fmt.Errorf("%s configuration has unknown diagnostic rule %q", name, id))
		}
	}
	return errors.Join(validationErrors...)
}

func (s *Server) validateDiagnosticLayer(
	value projectconfig.Partial,
	name string,
) error {
	inspectionIDs := make(map[string]bool, len(s.inspections.byID))
	ruleIDs := make(map[string]bool, len(s.inspections.byCode))
	for id := range s.inspections.byID {
		inspectionIDs[id] = true
	}
	for id := range s.inspections.byCode {
		ruleIDs[string(id)] = true
	}
	return validateDiagnosticLayer(value, name, inspectionIDs, ruleIDs)
}

func (s *Server) reloadProjectConfiguration(ctx context.Context) ConfigurationReloadResult {
	project, _, err := projectconfig.Load(s.rootPath)
	if err != nil {
		s.configurationMu.Lock()
		s.configurationErr = err
		s.configurationMu.Unlock()
		return ConfigurationReloadResult{Error: err.Error()}
	}
	if err := s.validateDiagnosticLayer(project, "project"); err != nil {
		s.configurationMu.Lock()
		s.configurationErr = err
		s.configurationMu.Unlock()
		return ConfigurationReloadResult{Error: err.Error()}
	}
	s.configurationMu.RLock()
	old := cloneEffective(s.effectiveConfiguration)
	editor := clonePartial(s.editorConfiguration)
	s.configurationMu.RUnlock()
	next := projectconfig.Resolve(project, editor)
	restartRequired := old.StructuralFingerprint() != next.StructuralFingerprint()
	nextFingerprint := next.StructuralFingerprint()
	applied := next
	if restartRequired {
		applied.PHP = old.PHP
		applied.Shopware = old.Shopware
		applied.Indexing = old.Indexing
		applied.Domains = old.Domains
		applied.DisabledReason = old.DisabledReason
	}
	s.configurationMu.Lock()
	s.projectConfiguration = project
	s.effectiveConfiguration = applied
	s.configurationErr = nil
	notifyRestart := restartRequired &&
		s.pendingConfigurationFingerprint != nextFingerprint
	if restartRequired {
		s.pendingConfigurationFingerprint = nextFingerprint
	} else {
		s.pendingConfigurationFingerprint = ""
	}
	s.configurationMu.Unlock()
	s.refreshConfigurationEffects(ctx, old, applied)
	if notifyRestart {
		s.notifyConfigurationRestartRequired(ctx)
	}
	return ConfigurationReloadResult{Applied: true, RestartRequired: restartRequired}
}

func (s *Server) replaceEditorConfiguration(
	ctx context.Context,
	editor projectconfig.Partial,
) ConfigurationReloadResult {
	if err := projectconfig.Validate(editor); err != nil {
		s.configurationMu.Lock()
		s.configurationErr = err
		s.configurationMu.Unlock()
		return ConfigurationReloadResult{Error: err.Error()}
	}
	if err := s.validateDiagnosticLayer(editor, "editor"); err != nil {
		s.configurationMu.Lock()
		s.configurationErr = err
		s.configurationMu.Unlock()
		return ConfigurationReloadResult{Error: err.Error()}
	}
	s.configurationMu.RLock()
	old := cloneEffective(s.effectiveConfiguration)
	project := clonePartial(s.projectConfiguration)
	s.configurationMu.RUnlock()
	next := projectconfig.Resolve(project, editor)
	restartRequired := old.StructuralFingerprint() != next.StructuralFingerprint()
	nextFingerprint := next.StructuralFingerprint()
	applied := next
	if restartRequired {
		applied.PHP = old.PHP
		applied.Shopware = old.Shopware
		applied.Indexing = old.Indexing
		applied.Domains = old.Domains
		applied.DisabledReason = old.DisabledReason
	}
	s.configurationMu.Lock()
	s.editorConfiguration = clonePartial(editor)
	s.effectiveConfiguration = applied
	s.configurationErr = nil
	notifyRestart := restartRequired &&
		s.pendingConfigurationFingerprint != nextFingerprint
	if restartRequired {
		s.pendingConfigurationFingerprint = nextFingerprint
	} else {
		s.pendingConfigurationFingerprint = ""
	}
	s.configurationMu.Unlock()
	s.refreshConfigurationEffects(ctx, old, applied)
	if notifyRestart {
		s.notifyConfigurationRestartRequired(ctx)
	}
	return ConfigurationReloadResult{Applied: true, RestartRequired: restartRequired}
}

func (s *Server) refreshConfigurationEffects(
	ctx context.Context,
	old,
	next projectconfig.Effective,
) {
	if reflect.DeepEqual(old.Features, next.Features) &&
		reflect.DeepEqual(old.Diagnostics, next.Diagnostics) {
		return
	}
	s.cancelAllDiagnostics()
	for _, document := range s.documentManager.Documents() {
		if next.Diagnostics.Enabled {
			s.scheduleDiagnostics(document.URI, document.Version, 0)
		} else {
			s.clearPublishedDiagnostics(ctx, document.URI)
		}
	}
}

func (s *Server) notifyConfigurationRestartRequired(ctx context.Context) {
	if conn := s.connection(); conn != nil {
		if err := conn.Notify(ctx, "shopware/configurationRestartRequired", map[string]string{
			"message": "Shopware LSP configuration changes require a restart",
		}); err != nil && ctx.Err() == nil {
			log.Printf("Error publishing configuration restart request: %v", err)
		}
	}
}

func featureForMethod(method string) string {
	switch method {
	case "textDocument/completion":
		return "completion"
	case "textDocument/definition":
		return "definition"
	case "textDocument/implementation":
		return "implementation"
	case "textDocument/prepareTypeHierarchy", "typeHierarchy/supertypes", "typeHierarchy/subtypes":
		return "typeHierarchy"
	case "textDocument/prepareCallHierarchy", "callHierarchy/incomingCalls", "callHierarchy/outgoingCalls":
		return "callHierarchy"
	case "textDocument/references":
		return "references"
	case "textDocument/codeLens", "codeLens/resolve":
		return "codeLens"
	case "textDocument/hover":
		return "hover"
	case "textDocument/signatureHelp":
		return "signatureHelp"
	case "textDocument/rename":
		return "rename"
	case "textDocument/inlayHint":
		return "inlayHints"
	case "textDocument/documentLink":
		return "documentLinks"
	case "textDocument/documentSymbol":
		return "documentSymbols"
	case "textDocument/documentHighlight":
		return "documentHighlights"
	case "textDocument/linkedEditingRange":
		return "linkedEditing"
	case "textDocument/foldingRange":
		return "foldingRanges"
	case "textDocument/selectionRange":
		return "selectionRanges"
	case "textDocument/documentColor", "textDocument/colorPresentation":
		return "documentColors"
	case "textDocument/semanticTokens/full":
		return "semanticTokens"
	case "textDocument/codeAction", "codeAction/resolve":
		return "codeActions"
	case "workspace/symbol":
		return "workspaceSymbols"
	case "workspace/willRenameFiles":
		return "fileRename"
	default:
		return ""
	}
}

func disabledFeatureResult(method string) interface{} {
	switch method {
	case "textDocument/completion":
		return &protocol.CompletionList{Items: []protocol.CompletionItem{}}
	case "textDocument/hover", "textDocument/rename",
		"textDocument/linkedEditingRange", "codeLens/resolve", "codeAction/resolve":
		return nil
	case "textDocument/semanticTokens/full":
		return &protocol.SemanticTokens{Data: []uint32{}}
	default:
		return []interface{}{}
	}
}

func isConfigurationMethod(method string) bool {
	return strings.HasPrefix(method, "shopware/configuration")
}

func inspectionDomain(id string) string {
	switch {
	case id == "php.semantic":
		return "php"
	case id == "shopware.admin":
		return "administration"
	case id == "shopware.migration":
		return "shopware.migrations"
	case id == "shopware.snippet":
		return "shopware.snippets"
	case id == "shopware.app_script":
		return "shopware.appScripts"
	case id == "shopware.entity_snapshot":
		return "shopware.entitySchema"
	case id == "shopware.store_composer":
		return "shopware.store"
	case id == "shopware.theme", id == "shopware.twig_versioning":
		return "shopware.theme"
	case id == "shopware.dal", id == "shopware.criteria":
		return "shopware.dal"
	case strings.HasPrefix(id, "symfony.doctrine"):
		return "symfony.doctrine"
	case strings.HasPrefix(id, "symfony.console"):
		return "symfony.console"
	case strings.HasPrefix(id, "symfony.event"):
		return "symfony.events"
	case strings.HasPrefix(id, "symfony.messenger"):
		return "symfony.messenger"
	case strings.HasPrefix(id, "symfony.form"):
		return "symfony.forms"
	case strings.HasPrefix(id, "symfony.security"):
		return "symfony.security"
	case strings.HasPrefix(id, "symfony.serializer"):
		return "symfony.serializer"
	case strings.HasPrefix(id, "symfony.validation"):
		return "symfony.validation"
	case strings.HasPrefix(id, "symfony.asset"):
		return "symfony.assets"
	case strings.HasPrefix(id, "symfony.stimulus"):
		return "symfony.stimulus"
	case strings.HasPrefix(id, "symfony.service"),
		strings.HasPrefix(id, "symfony.container"),
		strings.HasPrefix(id, "symfony.duplicate"),
		strings.HasPrefix(id, "symfony.legacy"):
		return "symfony.services"
	case strings.HasPrefix(id, "twig.component"):
		return "symfony.twigComponents"
	case strings.HasPrefix(id, "twig."):
		return "twig"
	case strings.HasPrefix(id, "symfony."):
		return "symfony"
	case strings.HasPrefix(id, "shopware."):
		return "shopware"
	default:
		return ""
	}
}
