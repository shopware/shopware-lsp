// Package projectconfig owns the committed and editor-supplied configuration
// shared by the language server and CLI.
package projectconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/shopware/shopware-lsp/internal/pathmatch"
)

const (
	CurrentVersion      = 1
	ProjectRelativePath = ".config/shopware-lsp/config.json"
)

type Severity string

const (
	SeverityOff         Severity = "off"
	SeverityHint        Severity = "hint"
	SeverityInformation Severity = "information"
	SeverityWarning     Severity = "warning"
	SeverityError       Severity = "error"
)

var severities = []Severity{
	SeverityOff,
	SeverityHint,
	SeverityInformation,
	SeverityWarning,
	SeverityError,
}

var targetVersionPattern = regexp.MustCompile(
	`^$|^v?[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:\.[0-9]+)?(?:[-+][0-9A-Za-z.-]+)?$`,
)

type PHPConfig struct {
	Extensions         *[]string `json:"extensions,omitempty"`
	DisabledExtensions *[]string `json:"disabledExtensions,omitempty"`
}

type ShopwareConfig struct {
	TargetVersion *string `json:"targetVersion,omitempty"`
}

type IndexingConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type DiagnosticsConfig struct {
	Enabled     *bool                `json:"enabled,omitempty"`
	Inspections map[string]bool      `json:"inspections,omitempty"`
	Rules       map[string]Severity  `json:"rules,omitempty"`
	Overrides   []DiagnosticOverride `json:"overrides,omitempty"`
}

// DiagnosticOverride applies diagnostic settings to workspace-scope-relative
// files. Overrides are ordered; later matching entries win.
type DiagnosticOverride struct {
	Files       []string            `json:"files"`
	Enabled     *bool               `json:"enabled,omitempty"`
	Inspections map[string]bool     `json:"inspections,omitempty"`
	Rules       map[string]Severity `json:"rules,omitempty"`
}

type CheckConfig struct {
	Severity *Severity `json:"severity,omitempty"`
	FailOn   *Severity `json:"failOn,omitempty"`
}

// Partial is an overlay. Pointer fields distinguish an omitted value from an
// explicitly configured zero value.
type Partial struct {
	Schema      string             `json:"$schema,omitempty"`
	Version     *int               `json:"version,omitempty"`
	PHP         *PHPConfig         `json:"php,omitempty"`
	Shopware    *ShopwareConfig    `json:"shopware,omitempty"`
	Features    map[string]bool    `json:"features,omitempty"`
	Indexing    *IndexingConfig    `json:"indexing,omitempty"`
	Domains     map[string]bool    `json:"domains,omitempty"`
	Diagnostics *DiagnosticsConfig `json:"diagnostics,omitempty"`
	Check       *CheckConfig       `json:"check,omitempty"`
}

type Effective struct {
	PHP            EffectivePHP         `json:"php"`
	Shopware       EffectiveShopware    `json:"shopware"`
	Features       map[string]bool      `json:"features"`
	Indexing       EffectiveIndexing    `json:"indexing"`
	Domains        map[string]bool      `json:"domains"`
	Diagnostics    EffectiveDiagnostics `json:"diagnostics"`
	Check          EffectiveCheck       `json:"check"`
	DisabledReason map[string]string    `json:"disabledDomainReasons,omitempty"`
	Origins        map[string]string    `json:"origins,omitempty"`
}

type EffectivePHP struct {
	Extensions         []string `json:"extensions"`
	DisabledExtensions []string `json:"disabledExtensions"`
}

type EffectiveShopware struct {
	TargetVersion string `json:"targetVersion"`
}

type EffectiveIndexing struct {
	Enabled bool `json:"enabled"`
}

type EffectiveDiagnostics struct {
	Enabled     bool                 `json:"enabled"`
	Inspections map[string]bool      `json:"inspections"`
	Rules       map[string]Severity  `json:"rules"`
	Overrides   []DiagnosticOverride `json:"overrides,omitempty"`
}

// DiagnosticPolicy is the resolved policy for one document.
type DiagnosticPolicy struct {
	Enabled     bool
	Inspections map[string]bool
	Rules       map[string]Severity
}

type EffectiveCheck struct {
	Severity Severity `json:"severity"`
	FailOn   Severity `json:"failOn"`
}

type CatalogEntry struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description,omitempty"`
	Parent      string   `json:"parent,omitempty"`
	DependsOn   []string `json:"dependsOn,omitempty"`
}

var FeatureCatalog = []CatalogEntry{
	{ID: "completion", Label: "Completion"},
	{ID: "definition", Label: "Go to Definition"},
	{ID: "implementation", Label: "Go to Implementation"},
	{ID: "typeHierarchy", Label: "Type Hierarchy"},
	{ID: "callHierarchy", Label: "Call Hierarchy"},
	{ID: "references", Label: "References"},
	{ID: "codeLens", Label: "Code Lenses"},
	{ID: "hover", Label: "Hover"},
	{ID: "signatureHelp", Label: "Signature Help"},
	{ID: "rename", Label: "Rename"},
	{ID: "inlayHints", Label: "Inlay Hints"},
	{ID: "documentLinks", Label: "Document Links"},
	{ID: "documentSymbols", Label: "Document Symbols"},
	{ID: "documentHighlights", Label: "Document Highlights"},
	{ID: "linkedEditing", Label: "Linked Editing"},
	{ID: "foldingRanges", Label: "Folding Ranges"},
	{ID: "selectionRanges", Label: "Selection Ranges"},
	{ID: "documentColors", Label: "Document Colors"},
	{ID: "semanticTokens", Label: "Semantic Tokens"},
	{ID: "codeActions", Label: "Code Actions"},
	{ID: "workspaceSymbols", Label: "Workspace Symbols"},
	{ID: "fileRename", Label: "File Rename"},
	{ID: "commands", Label: "Shopware Commands"},
}

// DomainCatalog is deliberately public and stable: these IDs are persisted in
// project configuration and returned by the configuration catalog request.
var DomainCatalog = []CatalogEntry{
	{ID: "php", Label: "PHP"},
	{ID: "twig", Label: "Twig", DependsOn: []string{"php", "symfony.services"}},
	{ID: "scss", Label: "SCSS"},
	{ID: "administration", Label: "Shopware Administration"},
	{ID: "symfony", Label: "Symfony"},
	{ID: "symfony.services", Label: "Symfony Services", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.routes", Label: "Symfony Routes", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.console", Label: "Symfony Console", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.doctrine", Label: "Doctrine", Parent: "symfony", DependsOn: []string{"php", "symfony.services"}},
	{ID: "symfony.assets", Label: "Symfony Assets", Parent: "symfony"},
	{ID: "symfony.events", Label: "Symfony Events", Parent: "symfony", DependsOn: []string{"php", "symfony.services"}},
	{ID: "symfony.messenger", Label: "Symfony Messenger", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.environment", Label: "Environment Variables", Parent: "symfony"},
	{ID: "symfony.forms", Label: "Symfony Forms", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.security", Label: "Symfony Security", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.configuration", Label: "Symfony Configuration", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.serializer", Label: "Symfony Serializer", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.stimulus", Label: "Symfony Stimulus", Parent: "symfony"},
	{ID: "symfony.validation", Label: "Symfony Validation", Parent: "symfony", DependsOn: []string{"php"}},
	{ID: "symfony.twigComponents", Label: "Symfony Twig Components", Parent: "symfony", DependsOn: []string{"twig"}},
	{ID: "shopware", Label: "Shopware"},
	{ID: "shopware.extensions", Label: "Shopware Extensions", Parent: "shopware", DependsOn: []string{"php"}},
	{ID: "shopware.snippets", Label: "Shopware Snippets", Parent: "shopware"},
	{ID: "shopware.translations", Label: "Translations", Parent: "shopware"},
	{ID: "shopware.featureFlags", Label: "Feature Flags", Parent: "shopware"},
	{ID: "shopware.systemConfig", Label: "System Config", Parent: "shopware", DependsOn: []string{"php"}},
	{ID: "shopware.theme", Label: "Theme", Parent: "shopware"},
	{ID: "shopware.twigVersioning", Label: "Twig Block Versioning", Parent: "shopware", DependsOn: []string{"twig"}},
	{ID: "shopware.dal", Label: "Shopware DAL", Parent: "shopware", DependsOn: []string{"php"}},
	{ID: "shopware.appScripts", Label: "App Scripts", Parent: "shopware"},
	{ID: "shopware.migrations", Label: "Shopware Migrations", Parent: "shopware", DependsOn: []string{"php"}},
	{ID: "shopware.entitySchema", Label: "Entity Schema", Parent: "shopware", DependsOn: []string{"shopware.dal"}},
	{ID: "shopware.store", Label: "Shopware Store Metadata", Parent: "shopware"},
}

func Default() Effective {
	features := make(map[string]bool, len(FeatureCatalog))
	for _, entry := range FeatureCatalog {
		features[entry.ID] = true
	}
	domains := make(map[string]bool, len(DomainCatalog))
	for _, entry := range DomainCatalog {
		domains[entry.ID] = true
	}
	return Effective{
		Features: features,
		Indexing: EffectiveIndexing{Enabled: true},
		Domains:  domains,
		Diagnostics: EffectiveDiagnostics{
			Enabled: true, Inspections: map[string]bool{}, Rules: map[string]Severity{},
		},
		Check:          EffectiveCheck{Severity: SeverityWarning, FailOn: SeverityOff},
		DisabledReason: map[string]string{},
		Origins:        map[string]string{},
	}
}

func Path(root string) string {
	return filepath.Join(root, filepath.FromSlash(ProjectRelativePath))
}

func Load(root string) (Partial, bool, error) {
	path := Path(root)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Partial{}, false, nil
		}
		return Partial{}, false, fmt.Errorf("read %s: %w", path, err)
	}
	value, err := Decode(data)
	if err != nil {
		return Partial{}, true, fmt.Errorf("%s: %w", path, err)
	}
	return value, true, nil
}

func Decode(data []byte) (Partial, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value Partial
	if err := decoder.Decode(&value); err != nil {
		return Partial{}, fmt.Errorf("decode configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Partial{}, errors.New("decode configuration: multiple JSON values")
		}
		return Partial{}, fmt.Errorf("decode configuration: %w", err)
	}
	if value.Version == nil {
		return Partial{}, errors.New("configuration version is required")
	}
	if *value.Version != CurrentVersion {
		return Partial{}, fmt.Errorf("unsupported configuration version %d", *value.Version)
	}
	if err := Validate(value); err != nil {
		return Partial{}, err
	}
	return value, nil
}

func Validate(value Partial) error {
	if value.Shopware != nil && value.Shopware.TargetVersion != nil &&
		!targetVersionPattern.MatchString(strings.TrimSpace(*value.Shopware.TargetVersion)) {
		return fmt.Errorf("invalid Shopware target version %q", *value.Shopware.TargetVersion)
	}
	if value.PHP != nil {
		for label, values := range map[string]*[]string{
			"php.extensions":         value.PHP.Extensions,
			"php.disabledExtensions": value.PHP.DisabledExtensions,
		} {
			if values == nil {
				continue
			}
			for _, current := range *values {
				if strings.TrimSpace(current) == "" {
					return fmt.Errorf("%s must not contain an empty extension", label)
				}
			}
		}
	}
	featureIDs := catalogIDs(FeatureCatalog)
	for id := range value.Features {
		if !featureIDs[id] {
			return fmt.Errorf("unknown feature %q", id)
		}
	}
	domainIDs := catalogIDs(DomainCatalog)
	for id := range value.Domains {
		if !domainIDs[id] {
			return fmt.Errorf("unknown domain %q", id)
		}
	}
	if value.Diagnostics != nil {
		if err := validateDiagnostics(*value.Diagnostics); err != nil {
			return err
		}
	}
	if value.Check != nil {
		if value.Check.Severity != nil && !ValidSeverity(*value.Check.Severity, false) {
			return fmt.Errorf("invalid check severity %q", *value.Check.Severity)
		}
		if value.Check.FailOn != nil && !ValidSeverity(*value.Check.FailOn, true) {
			return fmt.Errorf("invalid check failOn severity %q", *value.Check.FailOn)
		}
	}
	return nil
}

func validateDiagnostics(value DiagnosticsConfig) error {
	if err := validateDiagnosticMaps(value.Inspections, value.Rules); err != nil {
		return err
	}
	for index, override := range value.Overrides {
		if len(override.Files) == 0 {
			return fmt.Errorf("diagnostics.overrides[%d].files must not be empty", index)
		}
		if override.Enabled == nil && len(override.Inspections) == 0 && len(override.Rules) == 0 {
			return fmt.Errorf("diagnostics.overrides[%d] must configure enabled, inspections, or rules", index)
		}
		for _, pattern := range override.Files {
			if err := validateDiagnosticPattern(pattern); err != nil {
				return fmt.Errorf("diagnostics.overrides[%d].files: %w", index, err)
			}
		}
		if err := validateDiagnosticMaps(override.Inspections, override.Rules); err != nil {
			return fmt.Errorf("diagnostics.overrides[%d]: %w", index, err)
		}
	}
	return nil
}

func validateDiagnosticMaps(inspections map[string]bool, rules map[string]Severity) error {
	for id, severity := range rules {
		if strings.TrimSpace(id) == "" {
			return errors.New("diagnostic rule ID must not be empty")
		}
		if !ValidSeverity(severity, true) {
			return fmt.Errorf("invalid severity %q for diagnostic %q", severity, id)
		}
	}
	for id := range inspections {
		if strings.TrimSpace(id) == "" {
			return errors.New("inspection ID must not be empty")
		}
	}
	return nil
}

func validateDiagnosticPattern(pattern string) error {
	pattern = filepath.ToSlash(strings.ReplaceAll(strings.TrimSpace(pattern), `\`, "/"))
	if pattern == "" {
		return errors.New("pattern must not be empty")
	}
	windowsAbsolute := len(pattern) >= 3 && pattern[1] == ':' && pattern[2] == '/' &&
		((pattern[0] >= 'a' && pattern[0] <= 'z') || (pattern[0] >= 'A' && pattern[0] <= 'Z'))
	if filepath.IsAbs(filepath.FromSlash(pattern)) || strings.HasPrefix(pattern, "/") || windowsAbsolute {
		return fmt.Errorf("pattern %q must be relative", pattern)
	}
	for _, component := range strings.Split(pattern, "/") {
		if component == ".." {
			return fmt.Errorf("pattern %q must not escape its configuration scope", pattern)
		}
	}
	return nil
}

func ValidSeverity(value Severity, allowOff bool) bool {
	return slices.Contains(severities, value) && (allowOff || value != SeverityOff)
}

func Resolve(project Partial, editor Partial) Effective {
	result := Default()
	apply(&result, project, "project")
	apply(&result, editor, "editor")
	resolveDomainDependencies(&result)
	return result
}

func apply(target *Effective, value Partial, source string) {
	if value.PHP != nil {
		if value.PHP.Extensions != nil {
			target.PHP.Extensions = normalizeStrings(*value.PHP.Extensions)
			target.Origins["php.extensions"] = source
		}
		if value.PHP.DisabledExtensions != nil {
			target.PHP.DisabledExtensions = normalizeStrings(*value.PHP.DisabledExtensions)
			target.Origins["php.disabledExtensions"] = source
		}
	}
	if value.Shopware != nil && value.Shopware.TargetVersion != nil {
		target.Shopware.TargetVersion = strings.TrimSpace(*value.Shopware.TargetVersion)
		target.Origins["shopware.targetVersion"] = source
	}
	for id, enabled := range value.Features {
		target.Features[id] = enabled
		target.Origins["features."+id] = source
	}
	if value.Indexing != nil && value.Indexing.Enabled != nil {
		target.Indexing.Enabled = *value.Indexing.Enabled
		target.Origins["indexing.enabled"] = source
	}
	for id, enabled := range value.Domains {
		target.Domains[id] = enabled
		target.Origins["domains."+id] = source
	}
	if value.Diagnostics != nil {
		if value.Diagnostics.Enabled != nil {
			target.Diagnostics.Enabled = *value.Diagnostics.Enabled
			target.Origins["diagnostics.enabled"] = source
		}
		for id, enabled := range value.Diagnostics.Inspections {
			target.Diagnostics.Inspections[id] = enabled
			target.Origins["diagnostics.inspections."+id] = source
		}
		for id, severity := range value.Diagnostics.Rules {
			target.Diagnostics.Rules[id] = severity
			target.Origins["diagnostics.rules."+id] = source
		}
		target.Diagnostics.Overrides = append(
			target.Diagnostics.Overrides,
			cloneDiagnosticOverrides(value.Diagnostics.Overrides)...,
		)
	}
	if value.Check != nil {
		if value.Check.Severity != nil {
			target.Check.Severity = *value.Check.Severity
			target.Origins["check.severity"] = source
		}
		if value.Check.FailOn != nil {
			target.Check.FailOn = *value.Check.FailOn
			target.Origins["check.failOn"] = source
		}
	}
}

func resolveDomainDependencies(target *Effective) {
	changed := true
	for changed {
		changed = false
		for _, entry := range DomainCatalog {
			if !target.Domains[entry.ID] {
				continue
			}
			requirements := append([]string{}, entry.DependsOn...)
			if entry.Parent != "" {
				requirements = append(requirements, entry.Parent)
			}
			for _, dependency := range requirements {
				if target.Domains[dependency] {
					continue
				}
				target.Domains[entry.ID] = false
				target.DisabledReason[entry.ID] = "requires " + dependency
				changed = true
				break
			}
		}
	}
}

func (c Effective) FeatureEnabled(id string) bool {
	return c.Features[id]
}

func (c Effective) DomainEnabled(id string) bool {
	return c.Domains[id]
}

func (c Effective) InspectionEnabled(id string) bool {
	if !c.Diagnostics.Enabled {
		return false
	}
	enabled, configured := c.Diagnostics.Inspections[id]
	return !configured || enabled
}

func (c Effective) RuleSeverity(id string) (Severity, bool) {
	severity, configured := c.Diagnostics.Rules[id]
	return severity, configured
}

// DefaultDiagnosticPolicy returns an independent mutable policy initialized
// with the built-in diagnostic defaults.
func DefaultDiagnosticPolicy() DiagnosticPolicy {
	return DiagnosticPolicy{
		Enabled: true, Inspections: map[string]bool{}, Rules: map[string]Severity{},
	}
}

// ApplyDiagnostics applies one configuration layer and its matching ordered
// overrides. relativePath must be slash-normalized and relative to the layer's
// owning scope.
func ApplyDiagnostics(policy *DiagnosticPolicy, value *DiagnosticsConfig, relativePath string) {
	if policy == nil || value == nil {
		return
	}
	applyDiagnosticValues(policy, value.Enabled, value.Inspections, value.Rules)
	relativePath = filepath.ToSlash(strings.TrimPrefix(relativePath, "./"))
	for _, override := range value.Overrides {
		matched := false
		for _, pattern := range override.Files {
			if pathmatch.Ant(pattern, relativePath) {
				matched = true
				break
			}
		}
		if matched {
			applyDiagnosticValues(policy, override.Enabled, override.Inspections, override.Rules)
		}
	}
}

func applyDiagnosticValues(
	policy *DiagnosticPolicy,
	enabled *bool,
	inspections map[string]bool,
	rules map[string]Severity,
) {
	if enabled != nil {
		policy.Enabled = *enabled
	}
	for id, value := range inspections {
		policy.Inspections[id] = value
	}
	for id, value := range rules {
		policy.Rules[id] = value
	}
}

func cloneDiagnosticOverrides(values []DiagnosticOverride) []DiagnosticOverride {
	if len(values) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(values)
	var result []DiagnosticOverride
	_ = json.Unmarshal(encoded, &result)
	return result
}

func (c Effective) StructuralFingerprint() string {
	payload := struct {
		PHP      EffectivePHP      `json:"php"`
		Shopware EffectiveShopware `json:"shopware"`
		Indexing EffectiveIndexing `json:"indexing"`
		Domains  map[string]bool   `json:"domains"`
	}{c.PHP, c.Shopware, c.Indexing, c.Domains}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func normalizeStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, found := seen[key]; found {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result
}

func catalogIDs(values []CatalogEntry) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value.ID] = true
	}
	return result
}
