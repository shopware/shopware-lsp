// Package project loads the Composer model that defines a PHP workspace.
package project

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
)

type Version struct {
	Major int
	Minor int
	Patch int
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) AtLeast(major, minor int) bool {
	return v.Major > major || v.Major == major && v.Minor >= minor
}

type Model struct {
	Root               string
	PHPVersion         Version
	PSR4               map[string][]string
	Classmap           []string
	Files              []string
	Exclude            []string
	Dependencies       []Package
	RequiredExtensions []string
	EnabledExtensions  []string
	DisabledExtensions []string
	LoadedExtensions   []string
}

type Package struct {
	Name        string
	Version     string
	PSR4        map[string][]string
	Classmap    []string
	Files       []string
	InstallPath string
}

// DependencyVersion returns the installed Composer version for the first
// matching package name.
func (m *Model) DependencyVersion(names ...string) (Version, bool) {
	if m == nil {
		return Version{}, false
	}
	for _, name := range names {
		for _, dependency := range m.Dependencies {
			if !strings.EqualFold(dependency.Name, name) {
				continue
			}
			return ParseVersionConstraint(dependency.Version)
		}
	}
	return Version{}, false
}

type composerFile struct {
	Require    map[string]string `json:"require"`
	RequireDev map[string]string `json:"require-dev"`
	Config     struct {
		Platform map[string]json.RawMessage `json:"platform"`
	} `json:"config"`
	Autoload    composerAutoload `json:"autoload"`
	AutoloadDev composerAutoload `json:"autoload-dev"`
}

type composerAutoload struct {
	PSR4     map[string]stringOrStrings `json:"psr-4"`
	Classmap []string                   `json:"classmap"`
	Files    []string                   `json:"files"`
	Exclude  []string                   `json:"exclude-from-classmap"`
}

type stringOrStrings []string

func (s *stringOrStrings) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	var multiple []string
	if err := json.Unmarshal(data, &multiple); err != nil {
		return err
	}
	*s = multiple
	return nil
}

func Load(root string) (*Model, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Composer root: %w", err)
	}
	model := &Model{
		Root:       root,
		PHPVersion: Version{Major: 8, Minor: 2},
		PSR4:       make(map[string][]string),
	}
	content, err := os.ReadFile(filepath.Join(root, "composer.json"))
	if errors.Is(err, os.ErrNotExist) {
		return model, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read composer.json: %w", err)
	}
	var composer composerFile
	if err := json.Unmarshal(content, &composer); err != nil {
		return nil, fmt.Errorf("parse composer.json: %w", err)
	}
	versionConstraint := composer.Require["php"]
	if platform, ok := composerPlatformString(composer.Config.Platform["php"]); ok {
		versionConstraint = platform
	}
	if version, ok := ParseVersionConstraint(versionConstraint); ok {
		model.PHPVersion = version
	}
	model.addAutoload(root, composer.Autoload)
	model.addAutoload(root, composer.AutoloadDev)
	model.RequiredExtensions = composerExtensions(
		composer.Require,
		composer.RequireDev,
	)
	for name, raw := range composer.Config.Platform {
		extension, ok := composerExtensionName(name)
		if !ok {
			continue
		}
		var disabled bool
		if json.Unmarshal(raw, &disabled) == nil && !disabled {
			model.DisabledExtensions = append(model.DisabledExtensions, extension)
			continue
		}
		if _, enabled := composerPlatformString(raw); enabled {
			model.RequiredExtensions = append(model.RequiredExtensions, extension)
		}
	}
	model.RequiredExtensions = normalizedExtensions(model.RequiredExtensions)
	model.DisabledExtensions = normalizedExtensions(model.DisabledExtensions)
	model.addPHPUnitBootstrapAutoloads(root)
	model.Dependencies = loadPackages(root)
	return model, nil
}

func composerPlatformString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", false
	}
	return value, true
}

func composerExtensions(requirements ...map[string]string) []string {
	var result []string
	for _, requirement := range requirements {
		for name := range requirement {
			if extension, ok := composerExtensionName(name); ok {
				result = append(result, extension)
			}
		}
	}
	return normalizedExtensions(result)
}

func composerExtensionName(name string) (string, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.HasPrefix(name, "ext-") || len(name) == len("ext-") {
		return "", false
	}
	return NormalizeExtension(strings.TrimPrefix(name, "ext-")), true
}

// ConfigureExtensions merges explicit editor/runtime overrides with Composer
// requirements. Disabled entries win and provide enough evidence for a
// targeted unavailable-extension diagnostic.
func (m *Model) ConfigureExtensions(enabled, disabled []string) {
	if m == nil {
		return
	}
	m.EnabledExtensions = normalizedExtensions(enabled)
	m.DisabledExtensions = normalizedExtensions(append(
		append([]string(nil), m.DisabledExtensions...),
		disabled...,
	))
}

func (m *Model) StubExtensions() []string {
	if m == nil {
		return nil
	}
	disabled := make(map[string]struct{}, len(m.DisabledExtensions))
	for _, extension := range m.DisabledExtensions {
		disabled[NormalizeExtension(extension)] = struct{}{}
	}
	result := normalizedExtensions(append(
		append([]string(nil), m.RequiredExtensions...),
		m.EnabledExtensions...,
	))
	filtered := result[:0]
	for _, extension := range result {
		if _, excluded := disabled[extension]; !excluded {
			filtered = append(filtered, extension)
		}
	}
	return filtered
}

func (m *Model) ExtensionAvailability(extension string) (enabled, known bool) {
	if m == nil {
		return false, false
	}
	extension = NormalizeExtension(extension)
	for _, disabled := range m.DisabledExtensions {
		if NormalizeExtension(disabled) == extension {
			return false, true
		}
	}
	if m.LoadedExtensions != nil {
		for _, current := range m.LoadedExtensions {
			if NormalizeExtension(current) == extension {
				return true, true
			}
		}
		for _, requested := range m.StubExtensions() {
			if requested == extension {
				return false, true
			}
		}
		return false, false
	}
	for _, current := range m.StubExtensions() {
		if current == extension {
			return true, true
		}
	}
	return false, false
}

func NormalizeExtension(extension string) string {
	extension = strings.ToLower(strings.TrimSpace(extension))
	extension = strings.TrimPrefix(extension, "ext-")
	return strings.ReplaceAll(extension, "-", "_")
}

func normalizedExtensions(extensions []string) []string {
	seen := make(map[string]struct{}, len(extensions))
	result := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		extension = NormalizeExtension(extension)
		if extension == "" {
			continue
		}
		if _, exists := seen[extension]; exists {
			continue
		}
		seen[extension] = struct{}{}
		result = append(result, extension)
	}
	sort.Strings(result)
	return result
}

// addPHPUnitBootstrapAutoloads discovers explicit Composer ClassLoader
// mappings installed by a PHPUnit bootstrap. Projects commonly use these for
// isolated tool dependencies (for example a package under vendor-bin/) that
// intentionally do not appear in the root Composer lock file.
func (m *Model) addPHPUnitBootstrapAutoloads(root string) {
	for _, configName := range []string{"phpunit.xml", "phpunit.xml.dist"} {
		configPath := filepath.Join(root, configName)
		content, err := os.ReadFile(configPath)
		if err != nil {
			continue
		}
		var config struct {
			Bootstrap string `xml:"bootstrap,attr"`
		}
		if xml.Unmarshal(content, &config) != nil ||
			strings.TrimSpace(config.Bootstrap) == "" {
			continue
		}
		bootstrap := config.Bootstrap
		if !filepath.IsAbs(bootstrap) {
			bootstrap = filepath.Join(filepath.Dir(configPath), bootstrap)
		}
		m.addBootstrapAutoloads(filepath.Clean(bootstrap))
	}
}

func (m *Model) addBootstrapAutoloads(path string) {
	content, err := os.ReadFile(path)
	if err != nil {
		return
	}
	root := phpparser.ParseBytes(content).Tree.Root
	values := make(map[string]string)
	for _, assignment := range phpquery.Nodes(root, phpsyntax.PhpAssignmentExpression) {
		nodes := projectDirectNodes(assignment)
		if len(nodes) < 2 || nodes[0].Kind() != phpsyntax.PhpVariable {
			continue
		}
		name := phpquery.VariableKey(nodes[0])
		value, ok := bootstrapStringValue(nodes[len(nodes)-1], path, values)
		if name != "" && ok {
			values[name] = value
		}
	}
	for _, call := range phpquery.Calls(root) {
		if !strings.EqualFold(phpquery.CallMethodName(call), "addPsr4") {
			continue
		}
		namespaceExpression := phpquery.ArgumentExpression(call, 0)
		pathExpression := phpquery.ArgumentExpression(call, 1)
		namespace, namespaceOK := bootstrapStringValue(
			namespaceExpression,
			path,
			values,
		)
		sourceRoot, pathOK := bootstrapStringValue(
			pathExpression,
			path,
			values,
		)
		if !namespaceOK || !pathOK || namespace == "" || sourceRoot == "" {
			continue
		}
		if !filepath.IsAbs(sourceRoot) {
			sourceRoot = filepath.Join(filepath.Dir(path), sourceRoot)
		}
		sourceRoot = filepath.Clean(sourceRoot)
		info, err := os.Stat(sourceRoot)
		if err != nil || !info.IsDir() {
			continue
		}
		namespace = NormalizeNamespace(namespace)
		if !containsString(m.PSR4[namespace], sourceRoot) {
			m.PSR4[namespace] = append(m.PSR4[namespace], sourceRoot)
		}
	}
}

func bootstrapStringValue(
	node *phpsyntax.Node,
	bootstrapPath string,
	variables map[string]string,
) (string, bool) {
	if node == nil {
		return "", false
	}
	switch node.Kind() {
	case phpsyntax.PhpString:
		return bootstrapPHPStringValue(node), true
	case phpsyntax.PhpVariable:
		value, ok := variables[phpquery.VariableKey(node)]
		return value, ok
	case phpsyntax.PhpName:
		if strings.EqualFold(strings.TrimSpace(node.Text()), "__DIR__") {
			return filepath.Dir(bootstrapPath), true
		}
	case phpsyntax.PhpParenthesized:
		nodes := projectDirectNodes(node)
		if len(nodes) > 0 {
			return bootstrapStringValue(nodes[0], bootstrapPath, variables)
		}
	case phpsyntax.PhpBinaryExpression:
		if projectDirectOperator(node) != "." {
			return "", false
		}
		nodes := projectDirectNodes(node)
		if len(nodes) < 2 {
			return "", false
		}
		left, leftOK := bootstrapStringValue(nodes[0], bootstrapPath, variables)
		right, rightOK := bootstrapStringValue(
			nodes[len(nodes)-1], bootstrapPath, variables,
		)
		return left + right, leftOK && rightOK
	case phpsyntax.PhpFunctionCall:
		if !strings.EqualFold(
			strings.TrimPrefix(phpquery.CallMethodName(node), "\\"),
			"dirname",
		) {
			return "", false
		}
		value, ok := bootstrapStringValue(
			phpquery.ArgumentExpression(node, 0), bootstrapPath, variables,
		)
		if !ok {
			return "", false
		}
		levels := 1
		if argument := phpquery.ArgumentExpression(node, 1); argument != nil {
			parsed, err := strconv.Atoi(strings.TrimSpace(argument.Text()))
			if err != nil || parsed < 1 {
				return "", false
			}
			levels = parsed
		}
		for range levels {
			value = filepath.Dir(value)
		}
		return value, true
	}
	return "", false
}

func bootstrapPHPStringValue(node *phpsyntax.Node) string {
	value := phpquery.StringValue(node)
	text := strings.TrimSpace(node.Text())
	if len(text) < 2 {
		return value
	}
	switch text[0] {
	case '\'':
		return strings.NewReplacer(`\\`, `\`, `\'`, `'`).Replace(value)
	case '"':
		return strings.NewReplacer(`\\`, `\`, `\"`, `"`).Replace(value)
	default:
		return value
	}
}

func projectDirectNodes(node *phpsyntax.Node) []*phpsyntax.Node {
	if node == nil {
		return nil
	}
	result := make([]*phpsyntax.Node, 0, node.ChildCount())
	for index := 0; index < node.ChildCount(); index++ {
		if child, ok := node.Child(index).(*phpsyntax.Node); ok {
			result = append(result, child)
		}
	}
	return result
}

func projectDirectOperator(node *phpsyntax.Node) string {
	if node == nil {
		return ""
	}
	for index := 0; index < node.ChildCount(); index++ {
		token, ok := node.Child(index).(*phpsyntax.Token)
		if ok && token.Kind() == phpsyntax.TkOperator {
			return strings.TrimSpace(token.Text())
		}
	}
	return ""
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

func (m *Model) addAutoload(base string, autoload composerAutoload) {
	for namespace, paths := range autoload.PSR4 {
		for _, path := range paths {
			m.PSR4[namespace] = append(m.PSR4[namespace], filepath.Clean(filepath.Join(base, path)))
		}
	}
	for _, path := range autoload.Classmap {
		m.Classmap = append(m.Classmap, filepath.Clean(filepath.Join(base, path)))
	}
	for _, path := range autoload.Files {
		m.Files = append(m.Files, filepath.Clean(filepath.Join(base, path)))
	}
	for _, path := range autoload.Exclude {
		m.Exclude = append(m.Exclude, filepath.Clean(filepath.Join(base, path)))
	}
}

func (m *Model) SourceRoots() []string {
	if m == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var result []string
	for _, paths := range m.PSR4 {
		for _, path := range paths {
			if _, exists := seen[path]; !exists {
				seen[path] = struct{}{}
				result = append(result, path)
			}
		}
	}
	result = append(result, m.Classmap...)
	for _, dependency := range m.Dependencies {
		for _, paths := range dependency.PSR4 {
			for _, path := range paths {
				if _, exists := seen[path]; exists {
					continue
				}
				seen[path] = struct{}{}
				result = append(result, path)
			}
		}
		for _, path := range dependency.Classmap {
			if _, exists := seen[path]; exists {
				continue
			}
			seen[path] = struct{}{}
			result = append(result, path)
		}
	}
	sort.Strings(result)
	return result
}

func ParseVersionConstraint(source string) (Version, bool) {
	match := regexp.MustCompile(`(\d+)(?:\.(\d+))?(?:\.(\d+))?`).FindStringSubmatch(source)
	if len(match) == 0 {
		return Version{}, false
	}
	version := Version{}
	_, _ = fmt.Sscanf(match[1], "%d", &version.Major)
	if len(match) > 2 && match[2] != "" {
		_, _ = fmt.Sscanf(match[2], "%d", &version.Minor)
	}
	if len(match) > 3 && match[3] != "" {
		_, _ = fmt.Sscanf(match[3], "%d", &version.Patch)
	}
	return version, true
}

func loadPackages(root string) []Package {
	content, err := os.ReadFile(filepath.Join(root, "composer.lock"))
	if err != nil {
		return nil
	}
	var lock struct {
		Packages []struct {
			Name     string           `json:"name"`
			Version  string           `json:"version"`
			Autoload composerAutoload `json:"autoload"`
		} `json:"packages"`
		PackagesDev []struct {
			Name     string           `json:"name"`
			Version  string           `json:"version"`
			Autoload composerAutoload `json:"autoload"`
		} `json:"packages-dev"`
	}
	if json.Unmarshal(content, &lock) != nil {
		return nil
	}
	var result []Package
	add := func(name, version string, autoload composerAutoload) {
		packageRoot := filepath.Join(root, "vendor", filepath.FromSlash(name))
		pkg := Package{
			Name:        name,
			Version:     version,
			PSR4:        make(map[string][]string),
			InstallPath: packageRoot,
		}
		for namespace, paths := range autoload.PSR4 {
			for _, path := range paths {
				pkg.PSR4[namespace] = append(pkg.PSR4[namespace], filepath.Join(packageRoot, path))
			}
		}
		for _, path := range autoload.Classmap {
			pkg.Classmap = append(pkg.Classmap, filepath.Join(packageRoot, path))
		}
		for _, path := range autoload.Files {
			pkg.Files = append(pkg.Files, filepath.Join(packageRoot, path))
		}
		result = append(result, pkg)
	}
	for _, pkg := range lock.Packages {
		add(pkg.Name, pkg.Version, pkg.Autoload)
	}
	for _, pkg := range lock.PackagesDev {
		add(pkg.Name, pkg.Version, pkg.Autoload)
	}
	return result
}

func NormalizeNamespace(namespace string) string {
	return strings.Trim(namespace, "\\") + "\\"
}
