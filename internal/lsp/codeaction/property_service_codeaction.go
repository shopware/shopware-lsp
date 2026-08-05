package codeaction

import (
	"context"
	"sort"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/languagelevel"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// PropertyServiceCodeActionProvider ports the Symfony plugin's "Add Property
// Service" intention. It infers an injection type from an undefined
// $this->property reference and updates the constructor using the class's
// existing promoted or traditional injection style.
type PropertyServiceCodeActionProvider struct {
	phpIndex     *php.PHPIndex
	serviceIndex *symfony.ServiceIndex
}

func NewPropertyServiceCodeActionProvider(
	phpIndex *php.PHPIndex,
	serviceIndex *symfony.ServiceIndex,
) *PropertyServiceCodeActionProvider {
	return &PropertyServiceCodeActionProvider{
		phpIndex:     phpIndex,
		serviceIndex: serviceIndex,
	}
}

func (p *PropertyServiceCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *PropertyServiceCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		p.serviceIndex == nil || request == nil ||
		request.CodeActionParams == nil || request.Document == nil ||
		request.Root == nil || request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	access, propertyName, calledMethod := phpThisPropertyAccess(
		request.Node,
	)
	class := phpquery.ClassAt(access)
	if access == nil || len(propertyName) <= 2 || class == nil ||
		class.Kind() != phpsyntax.PhpClassDeclaration {
		return nil
	}
	className := phpClassFullyQualifiedName(request.Root, class)
	if className == "" || !p.isServiceClass(className) {
		return nil
	}
	document := p.phpIndex.AnalyzeDocument(
		propertyServiceDocumentPath(request),
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	if len((phpresolver.MemberResolver{Snapshot: snapshot}).Properties(
		types.Named(className),
		propertyName,
	)) != 0 {
		return nil
	}

	candidates := propertyServiceCandidates(
		p.phpIndex,
		propertyName,
		calledMethod,
	)
	result := make([]protocol.CodeAction, 0, len(candidates))
	for _, candidate := range candidates {
		if ctx.Err() != nil {
			return nil
		}
		qualifier, importEdit := phpClassQualifier(
			request,
			candidate.className,
		)
		if qualifier == "" {
			continue
		}
		edits, ok := p.propertyInjectionEdits(
			request,
			class,
			propertyName,
			qualifier,
		)
		if !ok {
			continue
		}
		if importEdit != nil {
			edits = append(edits, *importEdit)
		}
		result = append(result, protocol.CodeAction{
			Title: "Symfony: Inject " +
				phpClassShortName(candidate.className) +
				" as $" + propertyName,
			Kind: protocol.CodeActionRefactorRewrite,
			Edit: &protocol.WorkspaceEdit{
				Changes: map[string][]protocol.TextEdit{
					request.TextDocument.URI: edits,
				},
			},
		})
	}
	return result
}

func (p *PropertyServiceCodeActionProvider) isServiceClass(
	className string,
) bool {
	locations, err := p.serviceIndex.GetServicesUsageByClassName(
		strings.TrimPrefix(className, "\\"),
	)
	return err == nil && len(locations) != 0
}

func propertyServiceDocumentPath(request *lsp.CodeActionRequest) string {
	if request == nil || request.Document == nil {
		return ""
	}
	path, err := uriutil.Path(request.Document.URI)
	if err == nil {
		return path
	}
	return request.Document.URI
}

func phpThisPropertyAccess(
	node *phpsyntax.Node,
) (*phpsyntax.Node, string, string) {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != phpsyntax.PhpMemberAccess {
			continue
		}
		receiver := phpquery.DirectChild(
			current,
			phpsyntax.PhpVariable,
		)
		if !strings.EqualFold(
			phpquery.VariableName(receiver),
			"this",
		) {
			continue
		}
		propertyName := phpquery.NameValue(
			phpquery.DirectChild(current, phpsyntax.PhpName),
		)
		if propertyName == "" {
			return nil, "", ""
		}
		calledMethod := ""
		if parent := current.Parent(); parent != nil &&
			parent.Kind() == phpsyntax.PhpMemberCall {
			calledMethod = phpquery.NameValue(
				phpquery.DirectChild(parent, phpsyntax.PhpName),
			)
		}
		return current, propertyName, calledMethod
	}
	return nil, "", ""
}

func phpClassFullyQualifiedName(
	root,
	class *phpsyntax.Node,
) string {
	className := phpquery.ClassName(class)
	if className == "" {
		return ""
	}
	if namespace := strings.Trim(
		phpquery.Namespace(root),
		"\\",
	); namespace != "" {
		return namespace + "\\" + className
	}
	return className
}

type propertyServiceCandidate struct {
	className string
	score     int
}

func propertyServiceCandidates(
	phpIndex *php.PHPIndex,
	propertyName,
	calledMethod string,
) []propertyServiceCandidate {
	if phpIndex == nil {
		return nil
	}
	normalizedProperty := normalizePropertyServiceName(propertyName)
	if normalizedProperty == "" {
		return nil
	}
	searchNames := map[string]struct{}{
		normalizedProperty: {},
	}
	if !strings.EqualFold(normalizedProperty, "logger") &&
		strings.HasSuffix(
			strings.ToLower(strings.Trim(propertyName, "_")),
			"logger",
		) {
		searchNames["logger"] = struct{}{}
	}
	scores := make(map[string]int)
	aliases := map[string]string{
		"twig":     "Twig\\Environment",
		"template": "Twig\\Environment",
		"router": "Symfony\\Component\\Routing\\Generator\\" +
			"UrlGeneratorInterface",
		"em": "Doctrine\\ORM\\EntityManagerInterface",
		"om": "Doctrine\\Persistence\\ObjectManager",
	}
	for searchName := range searchNames {
		target := aliases[searchName]
		if target == "" {
			continue
		}
		if _, found := phpIndex.FindClass(target); found {
			scores[target] += 4
		}
	}

	allowSuffix := propertyServiceWordCount(propertyName) >= 3
	symbols := phpIndex.ClassSymbols()
	symbolByName := make(map[string]semantic.Symbol, len(symbols))
	for _, symbol := range symbols {
		className := strings.TrimPrefix(symbol.FullyQualified, "\\")
		if propertyServiceGarbageClass(className, symbol.Path) {
			continue
		}
		symbolByName[className] = symbol
		shortName := phpClassShortName(className)
		normalizedClass := normalizePropertyServiceName(shortName)
		if normalizedClass == "" {
			continue
		}
		score := 0
		for searchName := range searchNames {
			switch {
			case strings.EqualFold(normalizedClass, searchName):
				if score < 3 {
					score = 3
				}
			case allowSuffix &&
				strings.HasSuffix(
					strings.ToLower(normalizedClass),
					strings.ToLower(searchName),
				):
				if score < 1 {
					score = 1
				}
			}
		}
		if score == 0 {
			continue
		}
		scores[className] += score
	}

	result := make([]propertyServiceCandidate, 0, len(scores))
	for className, score := range scores {
		symbol, found := symbolByName[className]
		if !found {
			symbol, found = phpIndex.FindClass(className)
		}
		if !found {
			continue
		}
		if calledMethod != "" &&
			len(phpIndex.FindMethods(className, calledMethod)) == 0 {
			score -= 4
		}
		switch symbol.Kind {
		case semantic.InterfaceSymbol:
			score += 2
			lower := strings.ToLower(className)
			switch {
			case strings.Contains(lower, "\\symfony\\") &&
				strings.Contains(lower, "\\contracts\\"):
				score += 2
			case strings.HasPrefix(lower, "psr\\"):
				score += 3
			}
		case semantic.ClassSymbol:
			if symbol.Flags.Has(semantic.AbstractFlag) {
				score++
			}
		}
		if strings.Contains(
			strings.ToLower(phpClassShortName(className)),
			"decorator",
		) {
			score -= 3
		}
		if score < 0 {
			continue
		}
		result = append(result, propertyServiceCandidate{
			className: className,
			score:     score,
		})
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].score != result[right].score {
			return result[left].score > result[right].score
		}
		return strings.ToLower(result[left].className) <
			strings.ToLower(result[right].className)
	})
	const maximumPropertyServiceCandidates = 8
	if len(result) > maximumPropertyServiceCandidates {
		result = result[:maximumPropertyServiceCandidates]
	}
	return result
}

func normalizePropertyServiceName(value string) string {
	value = strings.ToLower(strings.ReplaceAll(
		strings.Trim(value, "_"),
		"_",
		"",
	))
	for {
		original := value
		for _, keyword := range []string{
			"interface",
			"abstract",
			"decorator",
		} {
			value = strings.TrimPrefix(value, keyword)
			value = strings.TrimSuffix(value, keyword)
		}
		if value == original {
			return value
		}
	}
}

func propertyServiceWordCount(value string) int {
	value = strings.Trim(value, "_")
	if value == "" {
		return 0
	}
	runes := []rune(value)
	count := 1
	for index, current := range runes {
		if current == '_' {
			if index+1 < len(runes) && runes[index+1] != '_' {
				count++
			}
			continue
		}
		if index == 0 || !unicode.IsUpper(current) {
			continue
		}
		previous := runes[index-1]
		nextLower := index+1 < len(runes) &&
			unicode.IsLower(runes[index+1])
		if unicode.IsLower(previous) || unicode.IsDigit(previous) ||
			unicode.IsUpper(previous) && nextLower {
			count++
		}
	}
	return count
}

func propertyServiceGarbageClass(className, path string) bool {
	lowerName := strings.ToLower(className)
	lowerPath := strings.ToLower(strings.ReplaceAll(path, "\\", "/"))
	return strings.HasSuffix(lowerName, "test") ||
		strings.Contains(lowerName, "\\test\\") ||
		strings.Contains(lowerName, "_phpstan_") ||
		strings.Contains(lowerName, "ecsprefix") ||
		strings.Contains(lowerName, "_humbugbox") ||
		strings.Contains(lowerName, "rectorprefix") ||
		strings.Contains(lowerPath, "/tests/")
}

func (p *PropertyServiceCodeActionProvider) propertyInjectionEdits(
	request *lsp.CodeActionRequest,
	class *phpsyntax.Node,
	propertyName,
	qualifier string,
) ([]protocol.TextEdit, bool) {
	constructor := phpClassConstructor(class)
	if constructor == nil {
		edit, ok := p.newPropertyConstructorEdit(
			request,
			class,
			propertyName,
			qualifier,
		)
		if !ok {
			return nil, false
		}
		return []protocol.TextEdit{edit}, true
	}

	promotion := p.supportsPropertyPromotion() &&
		(len(phpquery.Parameters(constructor)) == 0 ||
			phpConstructorUsesPromotion(constructor))
	if promotion {
		modifiers := "private "
		if p.supportsReadonlyProperties() &&
			!phpClassIsReadonly(class) {
			modifiers += "readonly "
		}
		parameterEdit, ok := phpRequiredParameterEdit(
			request,
			class,
			constructor,
			modifiers+qualifier,
			propertyName,
		)
		if !ok {
			return nil, false
		}
		return []protocol.TextEdit{parameterEdit}, true
	}

	parameterEdit, ok := phpRequiredParameterEdit(
		request,
		class,
		constructor,
		qualifier,
		propertyName,
	)
	if !ok {
		return nil, false
	}
	propertyEdit, ok := phpTraditionalPropertyEdit(
		request,
		class,
		constructor,
		qualifier,
		propertyName,
	)
	if !ok {
		return nil, false
	}
	assignmentEdit, ok := phpConstructorAssignmentEdit(
		request,
		class,
		constructor,
		propertyName,
	)
	if !ok {
		return nil, false
	}
	return []protocol.TextEdit{
		parameterEdit,
		propertyEdit,
		assignmentEdit,
	}, true
}

func (p *PropertyServiceCodeActionProvider) newPropertyConstructorEdit(
	request *lsp.CodeActionRequest,
	class *phpsyntax.Node,
	propertyName,
	qualifier string,
) (protocol.TextEdit, bool) {
	methods := phpquery.Methods(class)
	if len(methods) == 0 {
		return protocol.TextEdit{}, false
	}
	firstMethod := methods[0]
	offset := firstMethod.RangeTrimmedTrivia().Start
	methodIndent := phpLineIndentation(request.Document.Source, offset)
	indentUnit := phpMemberIndentUnit(
		request.Document.Source,
		class,
		methodIndent,
	)
	parameterIndent := methodIndent + indentUnit

	if p.supportsPropertyPromotion() {
		modifiers := "private "
		if p.supportsReadonlyProperties() &&
			!phpClassIsReadonly(class) {
			modifiers += "readonly "
		}
		newText := "public function __construct(\n" +
			parameterIndent + modifiers + qualifier + " $" +
			propertyName + ",\n" +
			methodIndent + ") {\n" +
			methodIndent + "}\n\n" +
			methodIndent
		return protocol.TextEdit{
			Range:   offsetRange(request, offset, offset),
			NewText: newText,
		}, true
	}

	newText := "private " + qualifier + " $" + propertyName + ";\n\n" +
		methodIndent + "public function __construct(" +
		qualifier + " $" + propertyName + ")\n" +
		methodIndent + "{\n" +
		parameterIndent + "$this->" + propertyName + " = $" +
		propertyName + ";\n" +
		methodIndent + "}\n\n" +
		methodIndent
	return protocol.TextEdit{
		Range:   offsetRange(request, offset, offset),
		NewText: newText,
	}, true
}

func phpClassConstructor(class *phpsyntax.Node) *phpsyntax.Node {
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(
			phpquery.MethodName(method),
			"__construct",
		) {
			return method
		}
	}
	return nil
}

func phpConstructorUsesPromotion(constructor *phpsyntax.Node) bool {
	for _, parameter := range phpquery.Parameters(constructor) {
		for token := range parameter.ChildTokens() {
			switch strings.ToLower(token.Text()) {
			case "public", "protected", "private":
				return true
			}
		}
	}
	return false
}

func phpClassIsReadonly(class *phpsyntax.Node) bool {
	if class == nil {
		return false
	}
	for token := range class.ChildTokens() {
		if strings.EqualFold(token.Text(), "readonly") {
			return true
		}
	}
	return false
}

func phpTraditionalPropertyEdit(
	request *lsp.CodeActionRequest,
	class,
	constructor *phpsyntax.Node,
	qualifier,
	propertyName string,
) (protocol.TextEdit, bool) {
	if request == nil || request.Document == nil ||
		class == nil || constructor == nil {
		return protocol.TextEdit{}, false
	}
	offset := constructor.RangeTrimmedTrivia().Start
	indent := phpLineIndentation(request.Document.Source, offset)
	return protocol.TextEdit{
		Range: offsetRange(request, offset, offset),
		NewText: "private " + qualifier + " $" + propertyName +
			";\n\n" + indent,
	}, true
}

func phpConstructorAssignmentEdit(
	request *lsp.CodeActionRequest,
	class,
	constructor *phpsyntax.Node,
	propertyName string,
) (protocol.TextEdit, bool) {
	block := phpquery.DirectChild(constructor, phpsyntax.PhpBlock)
	if request == nil || request.Document == nil || class == nil ||
		block == nil {
		return protocol.TextEdit{}, false
	}
	open, close := phpBlockDelimiters(block)
	if open == nil || close == nil ||
		open.Range().End > close.Range().Start ||
		int(close.Range().Start) > len(request.Document.Source) {
		return protocol.TextEdit{}, false
	}
	start := open.Range().End
	end := start
	for end < close.Range().Start {
		switch request.Document.Source[end] {
		case ' ', '\t', '\r', '\n':
			end++
		default:
			goto whitespaceDone
		}
	}
whitespaceDone:
	methodIndent := phpLineIndentation(
		request.Document.Source,
		constructor.RangeTrimmedTrivia().Start,
	)
	statementIndent := methodIndent + phpMemberIndentUnit(
		request.Document.Source,
		class,
		methodIndent,
	)
	newText := "\n" + statementIndent + "$this->" +
		propertyName + " = $" + propertyName + ";"
	if end < close.Range().Start {
		newText += "\n" + statementIndent
	} else {
		newText += "\n" + methodIndent
	}
	return protocol.TextEdit{
		Range:   offsetRange(request, start, end),
		NewText: newText,
	}, true
}

func phpBlockDelimiters(
	block *phpsyntax.Node,
) (*phpsyntax.Token, *phpsyntax.Token) {
	var open, close *phpsyntax.Token
	for token := range block.ChildTokens() {
		switch token.Kind() {
		case phpsyntax.TkOpenBrace:
			if open == nil {
				open = token
			}
		case phpsyntax.TkCloseBrace:
			close = token
		}
	}
	return open, close
}

func phpMemberIndentUnit(
	source string,
	class *phpsyntax.Node,
	memberIndent string,
) string {
	classIndent := ""
	if class != nil {
		classIndent = phpLineIndentation(
			source,
			class.RangeTrimmedTrivia().Start,
		)
	}
	unit := strings.TrimPrefix(memberIndent, classIndent)
	if unit != "" {
		return unit
	}
	if strings.Contains(memberIndent, "\t") {
		return "\t"
	}
	return "    "
}

func (p *PropertyServiceCodeActionProvider) supportsPropertyPromotion() bool {
	model := p.phpIndex.Project()
	return model == nil || languagelevel.Supports(
		model.PHPVersion,
		languagelevel.PropertyPromotion,
	)
}

func (p *PropertyServiceCodeActionProvider) supportsReadonlyProperties() bool {
	model := p.phpIndex.Project()
	return model == nil || languagelevel.Supports(
		model.PHPVersion,
		languagelevel.ReadonlyProperties,
	)
}
