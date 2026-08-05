package twig

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

// PHPUsageReference identifies a typed Twig usage of a PHP declaration.
// Member is empty for direct class-name usages such as enum() and @var.
type PHPUsageReference struct {
	Class    string
	Member   string
	Kind     semantic.SymbolKind
	Access   string
	FilePath string
	Range    cst.TextRange
}

// PHPUsageCatalog groups references under a directly queryable semantic key.
type PHPUsageCatalog struct {
	Key        string
	References []PHPUsageReference
}

var (
	twigVarAnnotationPattern = regexp.MustCompile(
		`(?m)@var[ \t]+([^ \t\r\n#]+)[ \t]+([^ \t\r\n#]+)`,
	)
	twigTypeClassPattern = regexp.MustCompile(
		`\\?[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*(?:\\[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*)*`,
	)
)

var nonClassTwigTypes = map[string]struct{}{
	"array": {}, "array-key": {}, "bool": {}, "boolean": {},
	"callable": {}, "false": {}, "float": {}, "int": {},
	"integer": {}, "iterable": {}, "list": {}, "mixed": {},
	"never": {}, "null": {}, "object": {}, "parent": {},
	"resource": {}, "scalar": {}, "self": {}, "static": {},
	"string": {}, "true": {}, "void": {},
}

// PHPUsageReferenceKey returns the persistent semantic key for a class or
// member target.
func PHPUsageReferenceKey(reference PHPUsageReference) string {
	className := strings.ToLower(
		strings.TrimPrefix(strings.TrimSpace(reference.Class), "\\"),
	)
	if className == "" {
		return ""
	}
	member := strings.ToLower(
		strings.TrimPrefix(strings.TrimSpace(reference.Member), "$"),
	)
	if member == "" {
		return "class\x00" + className
	}
	return fmt.Sprintf(
		"member\x00%s\x00%d\x00%s",
		className,
		reference.Kind,
		member,
	)
}

// PHPUsageTargetForSymbol maps a PHP declaration onto the same key used by
// persisted Twig usages.
func PHPUsageTargetForSymbol(
	snapshot *semantic.Snapshot,
	symbol semantic.Symbol,
) (PHPUsageReference, bool) {
	if symbol.IsClassLike() {
		if symbol.FullyQualified == "" {
			return PHPUsageReference{}, false
		}
		return PHPUsageReference{Class: symbol.FullyQualified}, true
	}
	switch symbol.Kind {
	case semantic.MethodSymbol, semantic.PropertySymbol,
		semantic.ClassConstantSymbol, semantic.EnumCaseSymbol:
	default:
		return PHPUsageReference{}, false
	}
	if snapshot == nil {
		return PHPUsageReference{}, false
	}
	container, found := snapshot.Symbol(symbol.Container)
	if !found || !container.IsClassLike() ||
		container.FullyQualified == "" {
		return PHPUsageReference{}, false
	}
	return PHPUsageReference{
		Class:  container.FullyQualified,
		Member: strings.TrimPrefix(symbol.Name, "$"),
		Kind:   symbol.Kind,
	}, true
}

// PHPUsageReferencesInDocument resolves typed Twig member accesses and direct
// class-name references with the same PHP access semantics used by completion,
// definition, hover, and diagnostics.
func PHPUsageReferencesInDocument(
	templatePath string,
	root *twigsyntax.Node,
	resolver PHPAccessResolver,
) []PHPUsageReference {
	if root == nil {
		return nil
	}
	resolver = resolver.forDocument(root)
	var result []PHPUsageReference
	snapshot := resolver.snapshot()
	for _, accessor := range twigquery.Nodes(
		root,
		twigsyntax.TwigAccessor,
	) {
		resolution, found := resolver.ResolveAccessor(
			templatePath,
			root,
			accessor,
		)
		if !found || resolution.NameNode == nil {
			continue
		}
		for _, member := range resolution.Members {
			target, supported := PHPUsageTargetForSymbol(
				snapshot,
				member.Symbol,
			)
			if !supported {
				continue
			}
			target.Access = strings.TrimSpace(
				resolution.NameNode.Text(),
			)
			target.FilePath = templatePath
			target.Range = resolution.NameNode.RangeTrimmedTrivia()
			result = append(result, target)
		}
	}

	for _, reference := range EnumReferences(root) {
		result = append(result, PHPUsageReference{
			Class:    reference.Name,
			Access:   twigquery.StringValue(reference.Node),
			FilePath: templatePath,
			Range:    reference.Range,
		})
	}
	result = append(
		result,
		twigVarClassReferences(templatePath, root)...,
	)
	return uniquePHPUsageReferences(result)
}

func (r PHPAccessResolver) callbackMethodSymbols(
	path,
	callback string,
) []semantic.Symbol {
	if r.PHP == nil {
		return nil
	}
	methodName := callbackMethodName(callback)
	if methodName == "" {
		return nil
	}
	if receiver := callbackReceiverClass(callback); receiver != "" {
		return r.PHP.FindMethods(receiver, methodName)
	}
	seen := make(map[semantic.SymbolID]struct{})
	var result []semantic.Symbol
	for _, class := range r.snapshot().SymbolsIn(path) {
		if !class.IsClassLike() {
			continue
		}
		for _, method := range r.PHP.FindMethods(
			class.FullyQualified,
			methodName,
		) {
			if _, duplicate := seen[method.ID]; duplicate {
				continue
			}
			seen[method.ID] = struct{}{}
			result = append(result, method)
		}
	}
	return result
}

func twigVarClassReferences(
	templatePath string,
	root *twigsyntax.Node,
) []PHPUsageReference {
	var result []PHPUsageReference
	for _, comment := range twigquery.Nodes(
		root,
		twigsyntax.TwigComment,
	) {
		source := comment.Text()
		for _, match := range twigVarAnnotationPattern.FindAllStringSubmatchIndex(
			source,
			-1,
		) {
			typeStart, typeEnd := twigVarAnnotationTypeRange(
				source,
				match,
			)
			if typeStart < 0 || typeEnd <= typeStart {
				continue
			}
			typeText := source[typeStart:typeEnd]
			for _, classMatch := range twigTypeClassPattern.FindAllStringIndex(
				typeText,
				-1,
			) {
				raw := typeText[classMatch[0]:classMatch[1]]
				className := strings.TrimPrefix(raw, "\\")
				if !twigAnnotationClassName(className) {
					continue
				}
				start := comment.Range().Start +
					uint32(typeStart+classMatch[0])
				result = append(result, PHPUsageReference{
					Class:    className,
					Access:   raw,
					FilePath: templatePath,
					Range: cst.TextRange{
						Start: start,
						End:   start + uint32(len(raw)),
					},
				})
			}
		}
	}
	for _, reference := range TypesTagClassReferences(
		[]byte(root.Text()),
	) {
		result = append(result, PHPUsageReference{
			Class:    reference.Name,
			Access:   reference.Raw,
			FilePath: templatePath,
			Range:    reference.Range,
		})
	}
	return result
}

func twigVarAnnotationTypeRange(
	source string,
	match []int,
) (int, int) {
	if len(match) < 6 {
		return -1, -1
	}
	first := source[match[2]:match[3]]
	second := source[match[4]:match[5]]
	if strings.HasPrefix(second, "$") ||
		twigVarAnnotationTypeFirst(first, second) {
		return match[2], match[3]
	}
	return match[4], match[5]
}

func twigVarAnnotationTypeFirst(first, second string) bool {
	if strings.HasPrefix(first, "\\") ||
		strings.Contains(first, "\\") ||
		strings.ContainsAny(first, "|&?[]<>") {
		return true
	}
	_, firstBuiltin := nonClassTwigTypes[strings.ToLower(first)]
	_, secondBuiltin := nonClassTwigTypes[strings.ToLower(second)]
	return firstBuiltin && !secondBuiltin
}

func twigAnnotationClassName(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	_, excluded := nonClassTwigTypes[strings.ToLower(value)]
	return !excluded
}

func uniquePHPUsageReferences(
	references []PHPUsageReference,
) []PHPUsageReference {
	seen := make(map[string]struct{}, len(references))
	result := make([]PHPUsageReference, 0, len(references))
	for _, reference := range references {
		semanticKey := PHPUsageReferenceKey(reference)
		if semanticKey == "" {
			continue
		}
		key := semanticKey + "\x00" +
			reference.FilePath + "\x00" + reference.Range.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, reference)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		if result[left].Range.Start != result[right].Range.Start {
			return result[left].Range.Start < result[right].Range.Start
		}
		return PHPUsageReferenceKey(result[left]) <
			PHPUsageReferenceKey(result[right])
	})
	return result
}
