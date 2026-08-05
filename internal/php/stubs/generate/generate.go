// Package generate builds the embedded runtime catalog from JetBrains'
// phpstorm-stubs repository.
package generate

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	"github.com/shopware/shopware-lsp/internal/php/binder"
	"github.com/shopware/shopware-lsp/internal/php/phpstormmeta"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/stubs/catalog"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

var (
	versionPairPattern = regexp.MustCompile(
		`["']([0-9]+\.[0-9]+)["']\s*=>\s*["']([^"']*)["']`,
	)
	defaultTypePattern = regexp.MustCompile(
		`\bdefault\s*:\s*["']([^"']*)["']`,
	)
	fromVersionPattern = regexp.MustCompile(
		`\bfrom\s*:\s*["']([0-9]+(?:\.[0-9]+){1,2})["']`,
	)
	toVersionPattern = regexp.MustCompile(
		`\bto\s*:\s*["']([0-9]+(?:\.[0-9]+){1,2})["']`,
	)
	sincePattern = regexp.MustCompile(
		`(?m)@since\s+([0-9]+(?:\.[0-9]+){1,2})\b`,
	)
	removedPattern = regexp.MustCompile(
		`(?m)@removed\s+([0-9]+(?:\.[0-9]+){1,2})\b`,
	)
	deprecatedDocPattern = regexp.MustCompile(
		`(?mi)@deprecated\b`,
	)
	deprecatedSincePattern = regexp.MustCompile(
		`(?i)\bsince\s*:\s*["']?([0-9]+(?:\.[0-9]+){1,2})["']?`,
	)
)

type Lock struct {
	Repository  string   `json:"repository"`
	Commit      string   `json:"commit"`
	Versions    []string `json:"versions"`
	Directories []string `json:"directories"`
}

type Stats struct {
	Files         int
	SourceBytes   int64
	ParsedSymbols int
	Records       int
	ParserErrors  int
}

type sourceDocument struct {
	path      string
	extension string
	source    string
	document  *semantic.Document
	container map[semantic.SymbolID]string
}

func LoadLock(path string) (Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Lock{}, fmt.Errorf("read PhpStorm stubs lock: %w", err)
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, fmt.Errorf("decode PhpStorm stubs lock: %w", err)
	}
	if lock.Repository == "" || len(lock.Commit) != 40 ||
		len(lock.Versions) == 0 || len(lock.Directories) == 0 {
		return Lock{}, fmt.Errorf("decode PhpStorm stubs lock: incomplete lock file")
	}
	if len(lock.Versions) > 16 {
		return Lock{}, fmt.Errorf("decode PhpStorm stubs lock: at most 16 versions are supported")
	}
	return lock, nil
}

func VerifySource(root string, lock Lock) error {
	command := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	output, err := command.Output()
	if err != nil {
		return fmt.Errorf("read PhpStorm stubs source revision: %w", err)
	}
	revision := strings.TrimSpace(string(output))
	if revision != lock.Commit {
		return fmt.Errorf(
			"PhpStorm stubs source is at %s; expected locked revision %s",
			revision,
			lock.Commit,
		)
	}
	return nil
}

func Build(root string, lock Lock) (catalog.Catalog, Stats, error) {
	versions, catalogVersions, err := parseVersions(lock.Versions)
	if err != nil {
		return catalog.Catalog{}, Stats{}, err
	}
	files, err := sourceFiles(root, lock.Directories)
	if err != nil {
		return catalog.Catalog{}, Stats{}, err
	}
	stats := Stats{Files: len(files)}
	documents := make([]sourceDocument, 0, len(files))
	semanticBinder := binder.New()
	for _, path := range files {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return catalog.Catalog{}, stats, fmt.Errorf("read stub %s: %w", path, readErr)
		}
		stats.SourceBytes += int64(len(content))
		parsed := phpparser.ParseBytes(content)
		if len(parsed.Errors) != 0 {
			stats.ParserErrors += len(parsed.Errors)
			first := parsed.Errors[0]
			return catalog.Catalog{}, stats, fmt.Errorf(
				"parse stub %s: %s (%d total errors)",
				path,
				first.String(),
				len(parsed.Errors),
			)
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return catalog.Catalog{}, stats, fmt.Errorf("resolve stub path %s: %w", path, relativeErr)
		}
		document := semanticBinder.Bind(
			"phpstorm-stubs://"+filepath.ToSlash(relative),
			1,
			parsed.Tree.Root,
		)
		stats.ParsedSymbols += len(document.Symbols)
		containers := make(map[semantic.SymbolID]string)
		for _, symbol := range document.Symbols {
			if symbol.IsClassLike() {
				containers[symbol.ID] = symbol.FullyQualified
			}
		}
		documents = append(documents, sourceDocument{
			path:      filepath.ToSlash(relative),
			extension: stubExtension(relative),
			source:    string(content),
			document:  document,
			container: containers,
		})
	}

	result := catalog.Catalog{
		Format:     catalog.FormatVersion,
		Repository: lock.Repository,
		Commit:     lock.Commit,
		Versions:   catalogVersions,
	}
	metadataFiles, err := metadataSourceFiles(root, lock.Directories)
	if err != nil {
		return catalog.Catalog{}, stats, err
	}
	contractKeys := make(map[string]struct{})
	for _, path := range metadataFiles {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return catalog.Catalog{}, stats, fmt.Errorf(
				"read PhpStorm metadata %s: %w",
				path,
				readErr,
			)
		}
		parsed := phpparser.ParseBytes(content)
		if len(parsed.Errors) != 0 {
			first := parsed.Errors[0]
			return catalog.Catalog{}, stats, fmt.Errorf(
				"parse PhpStorm metadata %s: %s (%d total errors)",
				path,
				first.String(),
				len(parsed.Errors),
			)
		}
		for _, contract := range phpstormmeta.Parse(parsed.Tree.Root) {
			key := callContractKey(contract)
			if _, duplicate := contractKeys[key]; duplicate {
				continue
			}
			contractKeys[key] = struct{}{}
			result.Contracts = append(result.Contracts, contract)
			relative, relativeErr := filepath.Rel(root, path)
			if relativeErr != nil {
				return catalog.Catalog{}, stats, fmt.Errorf(
					"resolve metadata path %s: %w",
					path,
					relativeErr,
				)
			}
			result.ContractExtensions = append(
				result.ContractExtensions,
				stubExtension(relative),
			)
		}
	}
	recordIndex := make(map[string]int)
	for versionIndex, version := range versions {
		versionSymbols := make(map[string]catalog.Symbol)
		for _, document := range documents {
			availableClasses := availableClassNames(document, version)
			for _, symbol := range document.document.Symbols {
				if !supportedKind(symbol.Kind) {
					continue
				}
				owner := document.container[symbol.Container]
				if owner != "" {
					if _, ok := availableClasses[strings.ToLower(owner)]; !ok {
						continue
					}
				}
				converted, ok, convertErr := convertSymbol(
					document,
					symbol,
					owner,
					version,
				)
				if convertErr != nil {
					return catalog.Catalog{}, stats, fmt.Errorf(
						"convert %s in %s: %w",
						symbol.FullyQualified,
						document.path,
						convertErr,
					)
				}
				if !ok {
					continue
				}
				converted.Extension = document.extension
				key := symbolKey(converted)
				if existing, duplicate := versionSymbols[key]; duplicate {
					versionSymbols[key] = preferSymbol(existing, converted)
				} else {
					versionSymbols[key] = converted
				}
			}
		}
		keys := make([]string, 0, len(versionSymbols))
		for key := range versionSymbols {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			symbol := versionSymbols[key]
			symbol.VersionMask = 0
			encoded, encodeErr := catalog.Encode(catalog.Catalog{
				Format:   catalog.FormatVersion,
				Versions: []catalog.Version{{Major: 1}},
				Symbols:  []catalog.Symbol{symbol},
			})
			if encodeErr != nil {
				return catalog.Catalog{}, stats, fmt.Errorf("encode catalog symbol %s: %w", key, encodeErr)
			}
			recordKey := string(encoded)
			if index, exists := recordIndex[recordKey]; exists {
				result.Symbols[index].VersionMask |= uint16(1) << versionIndex
				continue
			}
			symbol.VersionMask = uint16(1) << versionIndex
			recordIndex[recordKey] = len(result.Symbols)
			result.Symbols = append(result.Symbols, symbol)
		}
	}
	stats.Records = len(result.Symbols)
	if err := result.PackBundles(); err != nil {
		return catalog.Catalog{}, stats, err
	}
	return result, stats, nil
}

func stubExtension(relative string) string {
	relative = filepath.ToSlash(relative)
	directory, _, _ := strings.Cut(relative, "/")
	switch strings.ToLower(directory) {
	case "core", "standard", "superglobals":
		return "core"
	case "sqlite":
		return "pdo_sqlite"
	default:
		return project.NormalizeExtension(directory)
	}
}

func callContractKey(contract semantic.CallContract) string {
	contract.Target.Name = strings.ToLower(contract.Target.Name)
	contract.Target.Class = strings.ToLower(contract.Target.Class)
	return fmt.Sprintf("%#v", contract)
}

func parseVersions(values []string) ([]project.Version, []catalog.Version, error) {
	versions := make([]project.Version, len(values))
	wire := make([]catalog.Version, len(values))
	for index, value := range values {
		parsed, err := parseVersion(value)
		if err != nil {
			return nil, nil, fmt.Errorf("parse catalog PHP version %q: %w", value, err)
		}
		if index > 0 && compareVersion(parsed, versions[index-1]) <= 0 {
			return nil, nil, fmt.Errorf("catalog PHP versions must be strictly increasing")
		}
		if parsed.Major > 255 || parsed.Minor > 255 {
			return nil, nil, fmt.Errorf("catalog PHP version %q exceeds format limits", value)
		}
		versions[index] = parsed
		wire[index] = catalog.Version{Major: uint8(parsed.Major), Minor: uint8(parsed.Minor)}
	}
	return versions, wire, nil
}

func sourceFiles(root string, directories []string) ([]string, error) {
	var files []string
	for _, directory := range directories {
		base := filepath.Join(root, filepath.FromSlash(directory))
		info, err := os.Stat(base)
		if err != nil {
			return nil, fmt.Errorf("inspect locked stub directory %s: %w", directory, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("locked stub path %s is not a directory", directory)
		}
		err = filepath.WalkDir(base, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "tests" || entry.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if filepath.Ext(entry.Name()) != ".php" || entry.Name() == ".phpstorm.meta.php" {
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("scan locked stub directory %s: %w", directory, err)
		}
	}
	sort.Strings(files)
	return files, nil
}

func metadataSourceFiles(root string, directories []string) ([]string, error) {
	searchRoots := make([]string, 0, len(directories)+1)
	metaRoot := filepath.Join(root, "meta")
	if info, err := os.Stat(metaRoot); err == nil && info.IsDir() {
		searchRoots = append(searchRoots, metaRoot)
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("inspect PhpStorm metadata directory: %w", err)
	}
	for _, directory := range directories {
		searchRoots = append(
			searchRoots,
			filepath.Join(root, filepath.FromSlash(directory)),
		)
	}
	seen := make(map[string]struct{})
	var files []string
	for _, searchRoot := range searchRoots {
		err := filepath.WalkDir(
			searchRoot,
			func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					if entry.Name() == "tests" || entry.Name() == "vendor" {
						return filepath.SkipDir
					}
					return nil
				}
				if entry.Name() != ".phpstorm.meta.php" {
					return nil
				}
				if _, duplicate := seen[path]; !duplicate {
					seen[path] = struct{}{}
					files = append(files, path)
				}
				return nil
			},
		)
		if err != nil {
			return nil, fmt.Errorf(
				"scan PhpStorm metadata in %s: %w",
				searchRoot,
				err,
			)
		}
	}
	sort.Strings(files)
	return files, nil
}

func availableClassNames(document sourceDocument, version project.Version) map[string]struct{} {
	result := make(map[string]struct{})
	for _, symbol := range document.document.Symbols {
		if !symbol.IsClassLike() || !available(document.source, symbol.Range.Start, declarationPrefix(document.source, symbol), version) {
			continue
		}
		result[strings.ToLower(symbol.FullyQualified)] = struct{}{}
	}
	return result
}

func supportedKind(kind semantic.SymbolKind) bool {
	switch kind {
	case semantic.ClassSymbol,
		semantic.InterfaceSymbol,
		semantic.TraitSymbol,
		semantic.EnumSymbol,
		semantic.MethodSymbol,
		semantic.FunctionSymbol,
		semantic.PropertySymbol,
		semantic.ClassConstantSymbol,
		semantic.GlobalConstantSymbol,
		semantic.EnumCaseSymbol:
		return true
	default:
		return false
	}
}

func convertSymbol(
	document sourceDocument,
	source semantic.Symbol,
	owner string,
	version project.Version,
) (catalog.Symbol, bool, error) {
	prefix := declarationPrefix(document.source, source)
	if !available(document.source, source.Range.Start, prefix, version) {
		return catalog.Symbol{}, false, nil
	}
	fullyQualified := canonicalName(source, owner)
	parameters := make([]catalog.Parameter, 0, len(source.Parameters))
	for _, parameter := range source.Parameters {
		fragment := rangeText(document.source, parameter.Range.Start, parameter.Range.End)
		if !available("", 0, fragment, version) {
			continue
		}
		nativeType := parameter.NativeType
		effectiveType := parameter.Type
		if nativeType.IsUnknown() || nativeType.Kind() == types.ErrorKind {
			aware, parseErr := languageLevelType(fragment, version, namespaceOf(fullyQualified))
			if parseErr != nil {
				return catalog.Symbol{}, false, fmt.Errorf("parameter %s: %w", parameter.Name, parseErr)
			}
			if !aware.IsUnknown() {
				nativeType = aware
				// Upstream defines LanguageLevelTypeAware as the effective
				// signature for this PHP version. PHPDoc often describes the
				// pre-change default and must not reintroduce removed union arms.
				effectiveType = aware
			}
		}
		convertedParameter := catalog.Parameter{
			Name:          parameter.Name,
			Type:          effectiveType,
			NativeType:    nativeType,
			DocType:       parameter.DocType,
			AssistantTags: slices.Clone(parameter.AssistantTags),
			Attributes:    catalogAttributes(parameter.Attributes),
			Flags:         parameter.Flags,
			Optional:      parameter.Optional,
		}
		if parameter.DefaultValue != nil {
			value := cloneAttributeValue(*parameter.DefaultValue)
			convertedParameter.DefaultValue = &value
		}
		parameters = append(parameters, convertedParameter)
	}
	nativeType := source.NativeType
	effectiveType := source.Type
	returnType := source.ReturnType
	if nativeType.IsUnknown() || nativeType.Kind() == types.ErrorKind {
		aware, parseErr := languageLevelType(prefix, version, namespaceOf(fullyQualified))
		if parseErr != nil {
			return catalog.Symbol{}, false, parseErr
		}
		if !aware.IsUnknown() {
			nativeType = aware
			switch source.Kind {
			case semantic.MethodSymbol, semantic.FunctionSymbol:
				returnType = aware
			default:
				effectiveType = aware
			}
		}
	}
	templates := make([]catalog.TemplateParameter, len(source.Templates))
	for index, template := range source.Templates {
		templates[index] = catalog.TemplateParameter{
			Name:          template.Name,
			Bound:         template.Bound,
			Default:       template.Default,
			Covariant:     template.Covariant,
			Contravariant: template.Contravariant,
		}
	}
	attributes := catalogAttributes(source.Attributes)
	return catalog.Symbol{
		Kind:               source.Kind,
		Name:               source.Name,
		FullyQualified:     fullyQualified,
		Container:          owner,
		Visibility:         source.Visibility,
		WriteVisibility:    source.WriteVisibility,
		HasWriteVisibility: source.HasWriteVisibility,
		Flags: versionedFlags(
			source.Flags,
			document.source,
			source.Range.Start,
			prefix,
			version,
		) |
			semantic.InternalFlag |
			semantic.GeneratedStubFlag,
		Type:            effectiveType,
		NativeType:      nativeType,
		DocType:         source.DocType,
		ReturnType:      returnType,
		Parameters:      parameters,
		Templates:       templates,
		Extends:         slices.Clone(source.Extends),
		Implements:      slices.Clone(source.Implements),
		Traits:          slices.Clone(source.Traits),
		ExtendsTypes:    slices.Clone(source.ExtendsTypes),
		ImplementsTypes: slices.Clone(source.ImplementsTypes),
		TraitTypes:      slices.Clone(source.TraitTypes),
		Throws:          slices.Clone(source.Throws),
		Attributes:      attributes,
		DocSummary:      source.DocSummary,
	}, true, nil
}

func cloneAttributes(source []semantic.Attribute) []semantic.Attribute {
	if source == nil {
		return nil
	}
	result := slices.Clone(source)
	for attributeIndex := range result {
		arguments := slices.Clone(source[attributeIndex].Arguments)
		result[attributeIndex].Arguments = arguments
		for argumentIndex := range arguments {
			arguments[argumentIndex].Value = cloneAttributeValue(
				source[attributeIndex].Arguments[argumentIndex].Value,
			)
		}
	}
	return result
}

func catalogAttributes(
	source []semantic.Attribute,
) []semantic.Attribute {
	result := cloneAttributes(source)
	for index := range result {
		attribute := &result[index]
		attribute.Range = cst.TextRange{}
		if !retainCatalogAttributeArguments(attribute.Name) {
			attribute.Arguments = nil
			continue
		}
		for argumentIndex := range attribute.Arguments {
			attribute.Arguments[argumentIndex].Range = cst.TextRange{}
		}
	}
	return result
}

func retainCatalogAttributeArguments(name string) bool {
	name = strings.ToLower(strings.Trim(name, "\\"))
	for _, suffix := range [...]string{
		"\\arrayshape",
		"\\deprecated",
		"\\expectedvalues",
		"\\noreturn",
		"\\objectshape",
		"\\returntypecontract",
	} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func cloneAttributeValue(
	source semantic.AttributeValue,
) semantic.AttributeValue {
	result := source
	result.Items = slices.Clone(source.Items)
	for index := range result.Items {
		result.Items[index].Key = cloneAttributeValue(source.Items[index].Key)
		result.Items[index].Value = cloneAttributeValue(
			source.Items[index].Value,
		)
	}
	return result
}

func versionedFlags(
	flags semantic.Flags,
	source string,
	start uint32,
	fragment string,
	version project.Version,
) semantic.Flags {
	if !flags.Has(semantic.DeprecatedFlag) {
		return flags
	}
	arguments, ok := attributeArguments(fragment, "Deprecated")
	if !ok {
		return flags
	}
	since := versionFromMatch(deprecatedSincePattern.FindStringSubmatch(arguments))
	if since == (project.Version{}) || compareVersion(version, since) >= 0 {
		return flags
	}
	// A PHPDoc deprecation is an independent declaration. Do not erase it
	// merely because a second, versioned attribute starts in a future release.
	if deprecatedDocPattern.MatchString(leadingDocComment(source, start)) {
		return flags
	}
	return flags &^ semantic.DeprecatedFlag
}

func canonicalName(source semantic.Symbol, owner string) string {
	if owner == "" {
		return strings.TrimPrefix(source.FullyQualified, "\\")
	}
	switch source.Kind {
	case semantic.PropertySymbol:
		return owner + "::$" + strings.TrimPrefix(source.Name, "$")
	default:
		return owner + "::" + source.Name
	}
}

func symbolKey(symbol catalog.Symbol) string {
	return strconv.Itoa(int(symbol.Kind)) + ":" + strings.ToLower(symbol.FullyQualified)
}

func preferSymbol(left, right catalog.Symbol) catalog.Symbol {
	if symbolScore(right) > symbolScore(left) {
		return right
	}
	return left
}

func symbolScore(symbol catalog.Symbol) int {
	score := len(symbol.Parameters)*4 + len(symbol.DocSummary)/80
	for _, value := range []types.Type{symbol.Type, symbol.NativeType, symbol.DocType, symbol.ReturnType} {
		if !value.IsUnknown() && value.Kind() != types.ErrorKind {
			score += 3
		}
	}
	return score
}

func declarationPrefix(source string, symbol semantic.Symbol) string {
	text := rangeText(source, symbol.Range.Start, symbol.Range.End)
	lower := strings.ToLower(text)
	keyword := ""
	switch symbol.Kind {
	case semantic.ClassSymbol:
		keyword = "class"
	case semantic.InterfaceSymbol:
		keyword = "interface"
	case semantic.TraitSymbol:
		keyword = "trait"
	case semantic.EnumSymbol:
		keyword = "enum"
	case semantic.MethodSymbol, semantic.FunctionSymbol:
		keyword = "function"
	case semantic.ClassConstantSymbol, semantic.GlobalConstantSymbol:
		keyword = "const"
	case semantic.EnumCaseSymbol:
		keyword = "case"
	case semantic.PropertySymbol:
		keyword = "$" + strings.ToLower(strings.TrimPrefix(symbol.Name, "$"))
	}
	if keyword != "" {
		if index := strings.Index(lower, keyword); index >= 0 {
			return text[:index]
		}
	}
	return text
}

func available(source string, start uint32, fragment string, version project.Version) bool {
	from, to, hasAttribute := availabilityAttribute(fragment)
	if !hasAttribute {
		doc := leadingDocComment(source, start)
		from = versionFromMatch(sincePattern.FindStringSubmatch(doc))
		to = versionFromMatch(removedPattern.FindStringSubmatch(doc))
		if to != (project.Version{}) {
			// @removed marks the first unavailable version, unlike the
			// inclusive `to` argument on PhpStormStubsElementAvailable.
			if compareVersion(version, to) >= 0 {
				return false
			}
			to = project.Version{}
		}
	}
	if from != (project.Version{}) && compareVersion(version, from) < 0 {
		return false
	}
	if to != (project.Version{}) && compareVersion(version, to) > 0 {
		return false
	}
	return true
}

func availabilityAttribute(fragment string) (project.Version, project.Version, bool) {
	arguments, ok := attributeArguments(fragment, "PhpStormStubsElementAvailable")
	if !ok {
		return project.Version{}, project.Version{}, false
	}
	return versionFromMatch(fromVersionPattern.FindStringSubmatch(arguments)),
		versionFromMatch(toVersionPattern.FindStringSubmatch(arguments)),
		true
}

func languageLevelType(fragment string, version project.Version, namespace string) (types.Type, error) {
	arguments, ok := attributeArguments(fragment, "LanguageLevelTypeAware")
	if !ok {
		return types.Unknown(), nil
	}
	selected := ""
	selectedVersion := project.Version{}
	if match := defaultTypePattern.FindStringSubmatch(arguments); len(match) == 2 {
		selected = match[1]
	}
	for _, match := range versionPairPattern.FindAllStringSubmatch(arguments, -1) {
		candidate, err := parseVersion(match[1])
		if err != nil {
			return types.Unknown(), err
		}
		if compareVersion(candidate, version) <= 0 &&
			(selectedVersion == (project.Version{}) || compareVersion(candidate, selectedVersion) > 0) {
			selected = match[2]
			selectedVersion = candidate
		}
	}
	selected = strings.TrimSpace(strings.ReplaceAll(selected, `\\`, `\`))
	if selected == "" {
		return types.Unknown(), nil
	}
	value, err := types.Parse(selected)
	if err != nil {
		return types.Unknown(), fmt.Errorf("parse LanguageLevelTypeAware type %q: %w", selected, err)
	}
	return resolver.NewNameContext(namespace).ResolvePHPDocType(value, nil), nil
}

func attributeArguments(fragment, name string) (string, bool) {
	searchFrom := 0
	for searchFrom < len(fragment) {
		relative := strings.Index(fragment[searchFrom:], name)
		if relative < 0 {
			return "", false
		}
		index := searchFrom + relative + len(name)
		for index < len(fragment) && (fragment[index] == ' ' || fragment[index] == '\t' || fragment[index] == '\r' || fragment[index] == '\n') {
			index++
		}
		if index >= len(fragment) || fragment[index] != '(' {
			searchFrom = index
			continue
		}
		start := index + 1
		depth := 1
		quote := byte(0)
		escaped := false
		for index = start; index < len(fragment); index++ {
			current := fragment[index]
			if quote != 0 {
				if escaped {
					escaped = false
					continue
				}
				if current == '\\' {
					escaped = true
					continue
				}
				if current == quote {
					quote = 0
				}
				continue
			}
			switch current {
			case '\'', '"':
				quote = current
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					return fragment[start:index], true
				}
			}
		}
		return "", false
	}
	return "", false
}

func leadingDocComment(source string, start uint32) string {
	if source == "" || int(start) > len(source) {
		return ""
	}
	prefix := strings.TrimSpace(source[:start])
	if !strings.HasSuffix(prefix, "*/") {
		return ""
	}
	begin := strings.LastIndex(prefix, "/**")
	if begin < 0 {
		return ""
	}
	return prefix[begin:]
}

func rangeText(source string, start, end uint32) string {
	if start > end || int(start) > len(source) {
		return ""
	}
	if int(end) > len(source) {
		end = uint32(len(source))
	}
	return source[start:end]
}

func namespaceOf(fullyQualified string) string {
	owner := fullyQualified
	if separator := strings.Index(owner, "::"); separator >= 0 {
		owner = owner[:separator]
	}
	if separator := strings.LastIndex(owner, "\\"); separator >= 0 {
		return owner[:separator]
	}
	return ""
}

func versionFromMatch(match []string) project.Version {
	if len(match) != 2 {
		return project.Version{}
	}
	value, err := parseVersion(match[1])
	if err != nil {
		return project.Version{}
	}
	return value
}

func parseVersion(value string) (project.Version, error) {
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return project.Version{}, fmt.Errorf("expected major.minor[.patch]")
	}
	numbers := [3]int{}
	for index, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 {
			return project.Version{}, fmt.Errorf("invalid numeric component %q", part)
		}
		numbers[index] = parsed
	}
	return project.Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2]}, nil
}

func compareVersion(left, right project.Version) int {
	return slices.Compare(
		[]int{left.Major, left.Minor, left.Patch},
		[]int{right.Major, right.Minor, right.Patch},
	)
}
