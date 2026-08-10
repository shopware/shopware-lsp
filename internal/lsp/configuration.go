package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/projectconfig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type ConfigurationCatalog struct {
	Path        string                       `json:"path"`
	Effective   projectconfig.Effective      `json:"effective"`
	Scopes      []projectconfig.Scope        `json:"scopes,omitempty"`
	Features    []projectconfig.CatalogEntry `json:"features"`
	MCPTools    []projectconfig.CatalogEntry `json:"mcpTools"`
	Domains     []projectconfig.CatalogEntry `json:"domains"`
	Inspections []ConfigurationInspection    `json:"inspections"`
	Error       string                       `json:"error,omitempty"`
	Errors      []ConfigurationIssue         `json:"errors,omitempty"`
}

type ConfigurationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type configurationScopeLoad struct {
	scopes []projectconfig.Scope
	err    error
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
	DefaultEnabled  bool   `json:"defaultEnabled"`
}

type ConfigurationReloadResult struct {
	Applied         bool                 `json:"applied"`
	RestartRequired bool                 `json:"restartRequired"`
	Error           string               `json:"error,omitempty"`
	Errors          []ConfigurationIssue `json:"errors,omitempty"`
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
	s.configurationIssues = configurationIssues(root, loadErr, nil)
	s.configurationMu.Unlock()
	return nil
}

func loadConfigurationScopes(root string) configurationScopeLoad {
	scopes, err := projectconfig.LoadScopes(root)
	return configurationScopeLoad{scopes: scopes, err: err}
}

func (s *Server) installInitialConfigurationScopes(loaded configurationScopeLoad) error {
	scopeErr := errors.Join(loaded.err, projectconfig.ScopeErrors(loaded.scopes))
	if scopeErr != nil && s.initializationOptions.CLIMode {
		return scopeErr
	}
	issues := scopeConfigurationIssues(loaded.scopes)
	if loaded.err != nil {
		issues = append(issues, ConfigurationIssue{
			Path: projectconfig.Path(s.rootPath), Message: loaded.err.Error(),
		})
	}
	s.configurationMu.Lock()
	s.scopedConfigurations = loaded.scopes
	s.configurationErr = errors.Join(s.configurationErr, scopeErr)
	s.configurationIssues = append(s.configurationIssues, issues...)
	s.configurationMu.Unlock()
	return nil
}

func configurationIssues(root string, rootErr error, scopes []projectconfig.Scope) []ConfigurationIssue {
	result := make([]ConfigurationIssue, 0, len(scopes)+1)
	if rootErr != nil {
		// Scope errors are added with their precise paths below. Keep the root
		// issue only when the root configuration itself cannot be loaded.
		if _, _, loadErr := projectconfig.Load(root); loadErr != nil {
			result = append(result, ConfigurationIssue{
				Path: projectconfig.Path(root), Message: loadErr.Error(),
			})
		}
	}
	for _, scope := range scopes {
		if scope.Error != "" {
			result = append(result, ConfigurationIssue{Path: scope.Path, Message: scope.Error})
		}
	}
	return result
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

func (s *Server) diagnosticPolicy(uri string) projectconfig.DiagnosticPolicy {
	policy := projectconfig.DefaultDiagnosticPolicy()
	documentPath, pathErr := uriutil.Path(uri)
	if pathErr == nil {
		documentPath, pathErr = filepath.Abs(documentPath)
	}
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	workspaceRelative := "\x00outside-workspace"
	if pathErr == nil && projectconfig.Contains(s.rootPath, documentPath) {
		if relative, err := filepath.Rel(s.rootPath, documentPath); err == nil {
			workspaceRelative = filepath.ToSlash(relative)
		}
	}
	projectconfig.ApplyDiagnostics(
		&policy,
		diagnosticsConfiguration(s.projectConfiguration),
		workspaceRelative,
	)
	if pathErr == nil {
		for _, scope := range s.scopedConfigurations {
			if scope.Configuration.Diagnostics == nil ||
				!projectconfig.Contains(scope.Root, documentPath) {
				continue
			}
			relative, err := filepath.Rel(scope.Root, documentPath)
			if err != nil {
				continue
			}
			projectconfig.ApplyDiagnostics(
				&policy,
				diagnosticsConfiguration(scope.Configuration),
				filepath.ToSlash(relative),
			)
		}
	}
	projectconfig.ApplyDiagnostics(
		&policy,
		diagnosticsConfiguration(s.editorConfiguration),
		workspaceRelative,
	)
	return policy
}

func diagnosticsConfiguration(value projectconfig.Partial) *projectconfig.DiagnosticsConfig {
	return value.Diagnostics
}

func diagnosticInspectionEnabled(policy projectconfig.DiagnosticPolicy, id string) bool {
	if !policy.Enabled {
		return false
	}
	enabled, configured := policy.Inspections[id]
	return !configured || enabled
}

func diagnosticRuleSeverity(
	policy projectconfig.DiagnosticPolicy,
	id DiagnosticID,
) (projectconfig.Severity, bool) {
	severity, configured := policy.Rules[string(id)]
	return severity, configured
}

func (s *Server) configurationError() error {
	s.configurationMu.RLock()
	defer s.configurationMu.RUnlock()
	return s.configurationErr
}

func (s *Server) configurationCatalog() ConfigurationCatalog {
	s.configurationMu.RLock()
	effective := cloneEffective(s.effectiveConfiguration)
	configurationErr := s.configurationErr
	scopes := cloneScopes(s.scopedConfigurations)
	issues := slices.Clone(s.configurationIssues)
	s.configurationMu.RUnlock()
	result := ConfigurationCatalog{
		Path:      projectconfig.Path(s.rootPath),
		Effective: effective,
		Scopes:    scopes,
		Features:  slices.Clone(projectconfig.FeatureCatalog),
		MCPTools:  slices.Clone(projectconfig.MCPToolCatalog),
		Domains:   slices.Clone(projectconfig.DomainCatalog),
		Errors:    issues,
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
				DefaultEnabled:  !problem.DisabledByDefault,
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

func cloneScopes(value []projectconfig.Scope) []projectconfig.Scope {
	encoded, _ := json.Marshal(value)
	var result []projectconfig.Scope
	_ = json.Unmarshal(encoded, &result)
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
	scopes := cloneScopes(s.scopedConfigurations)
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
		layerErr := validateDiagnosticLayer(
			layer.value, layer.name, inspectionIDs, ruleIDs,
		)
		validationErrors = append(validationErrors, layerErr)
		if layerErr != nil && layer.name == "project" && !s.initializationOptions.CLIMode {
			s.configurationMu.Lock()
			s.configurationIssues = append(s.configurationIssues, ConfigurationIssue{
				Path: projectconfig.Path(s.rootPath), Message: layerErr.Error(),
			})
			s.configurationMu.Unlock()
		}
	}
	for index, scope := range scopes {
		if scope.Error != "" {
			continue
		}
		scopeErr := validateDiagnosticLayer(
			scope.Configuration, scope.Path, inspectionIDs, ruleIDs,
		)
		validationErrors = append(validationErrors, scopeErr)
		if scopeErr != nil && !s.initializationOptions.CLIMode {
			s.configurationMu.Lock()
			if index < len(s.scopedConfigurations) &&
				s.scopedConfigurations[index].Root == scope.Root {
				s.scopedConfigurations[index].Error = scopeErr.Error()
				s.scopedConfigurations[index].Configuration = projectconfig.Partial{}
			}
			s.configurationIssues = append(s.configurationIssues, ConfigurationIssue{
				Path: scope.Path, Message: scopeErr.Error(),
			})
			s.configurationMu.Unlock()
		}
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
	validateMaps := func(inspections map[string]bool, rules map[string]projectconfig.Severity) {
		for id := range inspections {
			if !inspectionIDs[id] {
				validationErrors = append(validationErrors,
					fmt.Errorf("%s configuration has unknown inspection %q", name, id))
			}
		}
		for id := range rules {
			if !ruleIDs[id] {
				validationErrors = append(validationErrors,
					fmt.Errorf("%s configuration has unknown diagnostic rule %q", name, id))
			}
		}
	}
	validateMaps(value.Diagnostics.Inspections, value.Diagnostics.Rules)
	for _, override := range value.Diagnostics.Overrides {
		validateMaps(override.Inspections, override.Rules)
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
		issues := []ConfigurationIssue{{
			Path: projectconfig.Path(s.rootPath), Message: err.Error(),
		}}
		s.configurationMu.Lock()
		s.configurationErr = err
		s.configurationIssues = issues
		s.configurationMu.Unlock()
		return ConfigurationReloadResult{
			Error: err.Error(), Errors: slices.Clone(issues),
		}
	}
	if err := s.validateDiagnosticLayer(project, "project"); err != nil {
		issues := []ConfigurationIssue{{
			Path: projectconfig.Path(s.rootPath), Message: err.Error(),
		}}
		s.configurationMu.Lock()
		s.configurationErr = err
		s.configurationIssues = issues
		s.configurationMu.Unlock()
		return ConfigurationReloadResult{
			Error: err.Error(), Errors: slices.Clone(issues),
		}
	}
	scopes, discoveryErr := projectconfig.LoadScopes(s.rootPath)
	s.configurationMu.RLock()
	old := cloneEffective(s.effectiveConfiguration)
	editor := clonePartial(s.editorConfiguration)
	previousScopes := cloneScopes(s.scopedConfigurations)
	s.configurationMu.RUnlock()
	scopes = s.validateAndRetainScopes(scopes, previousScopes)
	issues := scopeConfigurationIssues(scopes)
	configurationErr := errors.Join(discoveryErr, projectconfig.ScopeErrors(scopes))
	if discoveryErr != nil {
		issues = append(issues, ConfigurationIssue{
			Path: projectconfig.Path(s.rootPath), Message: discoveryErr.Error(),
		})
	}
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
	s.scopedConfigurations = scopes
	s.effectiveConfiguration = applied
	s.configurationErr = configurationErr
	s.configurationIssues = issues
	notifyRestart := restartRequired &&
		s.pendingConfigurationFingerprint != nextFingerprint
	if restartRequired {
		s.pendingConfigurationFingerprint = nextFingerprint
	} else {
		s.pendingConfigurationFingerprint = ""
	}
	s.configurationMu.Unlock()
	s.refreshConfigurationEffects(ctx, old, applied, true)
	if notifyRestart {
		s.notifyConfigurationRestartRequired(ctx)
	}
	result := ConfigurationReloadResult{
		Applied: true, RestartRequired: restartRequired, Errors: slices.Clone(issues),
	}
	if configurationErr != nil {
		result.Error = configurationErr.Error()
	}
	return result
}

func (s *Server) validateAndRetainScopes(
	next,
	previous []projectconfig.Scope,
) []projectconfig.Scope {
	previousByRoot := make(map[string]projectconfig.Scope, len(previous))
	for _, scope := range previous {
		if scope.Configuration.Diagnostics != nil {
			previousByRoot[scope.Root] = scope
		}
	}
	for index := range next {
		validationErr := error(nil)
		if next[index].Error == "" {
			validationErr = s.validateDiagnosticLayer(
				next[index].Configuration, next[index].Path,
			)
			if validationErr != nil {
				next[index].Error = validationErr.Error()
			}
		}
		if next[index].Error == "" {
			continue
		}
		if old, found := previousByRoot[next[index].Root]; found {
			next[index].Configuration = old.Configuration
		}
	}
	return next
}

func scopeConfigurationIssues(scopes []projectconfig.Scope) []ConfigurationIssue {
	var result []ConfigurationIssue
	for _, scope := range scopes {
		if scope.Error != "" {
			result = append(result, ConfigurationIssue{
				Path: scope.Path, Message: scope.Error,
			})
		}
	}
	return result
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
	scopes := cloneScopes(s.scopedConfigurations)
	issues := slices.Clone(s.configurationIssues)
	s.configurationMu.RUnlock()
	next := projectconfig.Resolve(project, editor)
	restartRequired := old.StructuralFingerprint() != next.StructuralFingerprint()
	nextFingerprint := next.StructuralFingerprint()
	scopeErr := projectconfig.ScopeErrors(scopes)
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
	s.configurationErr = scopeErr
	s.configurationIssues = issues
	notifyRestart := restartRequired &&
		s.pendingConfigurationFingerprint != nextFingerprint
	if restartRequired {
		s.pendingConfigurationFingerprint = nextFingerprint
	} else {
		s.pendingConfigurationFingerprint = ""
	}
	s.configurationMu.Unlock()
	s.refreshConfigurationEffects(ctx, old, applied, false)
	if notifyRestart {
		s.notifyConfigurationRestartRequired(ctx)
	}
	result := ConfigurationReloadResult{
		Applied: true, RestartRequired: restartRequired, Errors: issues,
	}
	if scopeErr != nil {
		result.Error = scopeErr.Error()
	}
	return result
}

func (s *Server) refreshConfigurationEffects(
	ctx context.Context,
	old,
	next projectconfig.Effective,
	force bool,
) {
	if !force && reflect.DeepEqual(old.Features, next.Features) &&
		reflect.DeepEqual(old.Diagnostics, next.Diagnostics) {
		return
	}
	s.cancelAllDiagnostics()
	for _, document := range s.documentManager.Documents() {
		s.scheduleDiagnostics(document.URI, document.Version, 0)
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
	case id == "shopware.theme":
		return "shopware.theme"
	case id == "shopware.twig_versioning":
		return "shopware.twigVersioning"
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
