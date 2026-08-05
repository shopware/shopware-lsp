package phpsemantic

import (
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/project"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

type phpClassCompletionPlan struct {
	qualifier string
	imported  bool
	edit      *protocol.TextEdit
}

type phpGlobalCompletionContext struct {
	request          *lsp.CompletionRequest
	document         *semantic.Document
	project          *project.Model
	namespace        string
	prefix           string
	replacement      cst.TextRange
	explicitlyScoped bool
	importsByTarget  map[string]string
	importsByAlias   map[string]string
	localClasses     map[string]string
	importOffset     uint32
	hasImports       bool
}

func newPHPGlobalCompletionContext(
	request *lsp.CompletionRequest,
	document *semantic.Document,
	projectModel *project.Model,
	offset uint32,
) phpGlobalCompletionContext {
	context := phpGlobalCompletionContext{
		request:         request,
		document:        document,
		project:         projectModel,
		importsByTarget: make(map[string]string),
		importsByAlias:  make(map[string]string),
		localClasses:    make(map[string]string),
	}
	if document != nil {
		context.namespace = document.Namespace
		if scope, found := document.ScopeAt(offset); found {
			context.namespace = scope.Namespace
		}
		for _, symbol := range document.Symbols {
			if !symbol.IsClassLike() || symbol.Container != "" {
				continue
			}
			context.localClasses[strings.ToLower(symbol.Name)] =
				strings.TrimPrefix(symbol.FullyQualified, "\\")
		}
	}
	if request == nil {
		return context
	}
	context.prefix, context.replacement, context.explicitlyScoped =
		phpCompletionPrefix(request.DocumentContent, offset)
	if request.Root == nil {
		return context
	}
	declarations := phpquery.UseDeclarations(request.Root)
	context.hasImports = len(declarations) != 0
	for _, declaration := range declarations {
		if declaration.Range().Start <= offset && declaration.Range().End > context.importOffset {
			context.importOffset = declaration.Range().End
		}
		for _, imported := range resolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind != resolver.ClassImport {
				continue
			}
			target := strings.Trim(imported.Target, "\\")
			context.importsByTarget[strings.ToLower(target)] = imported.Alias
			context.importsByAlias[strings.ToLower(imported.Alias)] = target
		}
	}
	if context.importOffset == 0 {
		context.importOffset = phpGlobalImportInsertionOffset(request.Root, offset)
	}
	return context
}

func (context phpGlobalCompletionContext) classPlan(
	symbol semantic.Symbol,
) phpClassCompletionPlan {
	fqn := strings.Trim(symbol.FullyQualified, "\\")
	short := symbol.Name
	if short == "" {
		short = phpShortName(fqn)
	}
	if context.explicitlyScoped {
		return phpClassCompletionPlan{qualifier: "\\" + fqn}
	}
	if alias := context.importsByTarget[strings.ToLower(fqn)]; alias != "" {
		return phpClassCompletionPlan{qualifier: alias, imported: true}
	}
	if namespace := phpNamespaceName(fqn); strings.EqualFold(namespace, context.namespace) ||
		namespace == "" {
		return phpClassCompletionPlan{qualifier: short}
	}
	if target, conflict := context.importsByAlias[strings.ToLower(short)]; conflict &&
		!strings.EqualFold(target, fqn) {
		return phpClassCompletionPlan{qualifier: "\\" + fqn}
	}
	if target, conflict := context.localClasses[strings.ToLower(short)]; conflict &&
		!strings.EqualFold(target, fqn) {
		return phpClassCompletionPlan{qualifier: "\\" + fqn}
	}
	if context.request == nil || context.request.LineIndex == nil {
		return phpClassCompletionPlan{qualifier: "\\" + fqn}
	}
	textRange := rangeFromText(
		context.request.LineIndex,
		cst.TextRange{Start: context.importOffset, End: context.importOffset},
	)
	if textRange == nil {
		return phpClassCompletionPlan{qualifier: "\\" + fqn}
	}
	newText := "\nuse " + fqn + ";"
	if !context.hasImports {
		newText = "\n\nuse " + fqn + ";"
	}
	return phpClassCompletionPlan{
		qualifier: short,
		edit: &protocol.TextEdit{
			Range:   *textRange,
			NewText: newText,
		},
	}
}

func (context phpGlobalCompletionContext) sortText(
	symbol semantic.Symbol,
	imported bool,
) string {
	name := symbol.Name
	if name == "" {
		name = phpShortName(symbol.FullyQualified)
	}
	match := phpCompletionMatch(context.prefix, name, symbol.FullyQualified)
	relation := 3
	symbolNamespace := phpNamespaceName(symbol.FullyQualified)
	switch {
	case strings.EqualFold(symbolNamespace, context.namespace):
		relation = 0
	case imported:
		relation = 1
	case samePHPCompletionPackage(
		context.project,
		symbol.FullyQualified,
		context.namespace+"\\_CurrentDocument",
	):
		relation = 2
	}
	deprecated := 0
	if symbol.Flags.Has(semantic.DeprecatedFlag) {
		deprecated = 1
	}
	return fmt.Sprintf(
		"10-%d-%d-%d-%s",
		match,
		relation,
		deprecated,
		strings.ToLower(symbol.FullyQualified),
	)
}

func phpCompletionPrefix(
	source []byte,
	offset uint32,
) (string, cst.TextRange, bool) {
	position := min(int(offset), len(source))
	start := position
	for start > 0 && phpCompletionNameByte(source[start-1]) {
		start--
	}
	raw := string(source[start:position])
	explicit := strings.HasPrefix(raw, "\\") || strings.Contains(raw, "\\")
	prefix := strings.TrimPrefix(raw, "\\")
	if separator := strings.LastIndexByte(prefix, '\\'); separator >= 0 {
		prefix = prefix[separator+1:]
	}
	return prefix, cst.TextRange{Start: uint32(start), End: offset}, explicit
}

func phpCompletionNameByte(value byte) bool {
	return value == '\\' || value == '_' || value >= 0x80 ||
		value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func phpGlobalImportInsertionOffset(root *phpsyntax.Node, offset uint32) uint32 {
	var namespaceEnd uint32
	for _, namespace := range phpquery.Nodes(root, phpsyntax.PhpNamespace) {
		if namespace.Range().Start > offset {
			continue
		}
		text := namespace.Text()
		if end := strings.IndexAny(text, ";{"); end >= 0 {
			candidate := namespace.Range().Start + uint32(end+1)
			if candidate > namespaceEnd {
				namespaceEnd = candidate
			}
		}
	}
	if namespaceEnd != 0 {
		return namespaceEnd
	}
	if openTag := root.FirstToken(); openTag != nil && openTag.Kind() == phpsyntax.TkOpenTag {
		return openTag.Range().End
	}
	return 0
}

func phpCompletionMatch(prefix, name, fullyQualified string) int {
	if prefix == "" {
		return 3
	}
	prefix = strings.ToLower(prefix)
	name = strings.ToLower(name)
	fullyQualified = strings.ToLower(strings.TrimPrefix(fullyQualified, "\\"))
	switch {
	case prefix == name || prefix == fullyQualified:
		return 0
	case strings.HasPrefix(name, prefix):
		return 1
	case strings.Contains(name, prefix) || strings.Contains(fullyQualified, prefix):
		return 2
	default:
		return 3
	}
}

func phpNamespaceName(name string) string {
	name = strings.Trim(name, "\\")
	if separator := strings.LastIndexByte(name, '\\'); separator >= 0 {
		return name[:separator]
	}
	return ""
}

func phpShortName(name string) string {
	name = strings.Trim(name, "\\")
	if separator := strings.LastIndexByte(name, '\\'); separator >= 0 {
		return name[separator+1:]
	}
	return name
}

func samePHPNamespacePackage(left, right string) bool {
	leftRoot := left
	if separator := strings.IndexByte(leftRoot, '\\'); separator >= 0 {
		leftRoot = leftRoot[:separator]
	}
	rightRoot := right
	if separator := strings.IndexByte(rightRoot, '\\'); separator >= 0 {
		rightRoot = rightRoot[:separator]
	}
	return leftRoot != "" && rightRoot != "" && strings.EqualFold(leftRoot, rightRoot)
}

func samePHPCompletionPackage(
	model *project.Model,
	left,
	right string,
) bool {
	leftPackage := phpCompletionPackage(model, left)
	rightPackage := phpCompletionPackage(model, right)
	if leftPackage != "" && rightPackage != "" {
		return strings.EqualFold(leftPackage, rightPackage)
	}
	return samePHPNamespacePackage(
		phpNamespaceName(left),
		phpNamespaceName(right),
	)
}

func phpCompletionPackage(model *project.Model, fullyQualified string) string {
	if model == nil {
		return ""
	}
	name := strings.Trim(fullyQualified, "\\")
	bestLength := 0
	bestPackage := ""
	for prefix := range model.PSR4 {
		prefix = strings.Trim(prefix, "\\")
		if len(prefix) > bestLength && phpNamespaceHasPrefix(name, prefix) {
			bestLength = len(prefix)
			bestPackage = "@root"
		}
	}
	for _, dependency := range model.Dependencies {
		for prefix := range dependency.PSR4 {
			prefix = strings.Trim(prefix, "\\")
			if len(prefix) > bestLength && phpNamespaceHasPrefix(name, prefix) {
				bestLength = len(prefix)
				bestPackage = dependency.Name
			}
		}
	}
	return bestPackage
}

func phpNamespaceHasPrefix(name, prefix string) bool {
	if prefix == "" || len(name) < len(prefix) ||
		!strings.EqualFold(name[:len(prefix)], prefix) {
		return false
	}
	return len(name) == len(prefix) || name[len(prefix)] == '\\'
}
