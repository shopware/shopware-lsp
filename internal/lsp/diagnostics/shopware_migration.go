package diagnostics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
	"github.com/shopware/shopware-lsp/internal/shopware"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	entitySearchResultGetEntitiesCode lsp.DiagnosticID = "shopware.migration.entity_search_result.get_entities"
	entityExtensionEntityNameCode     lsp.DiagnosticID = "shopware.migration.entity_extension.entity_name"
	reverseProxyBanAllCode            lsp.DiagnosticID = "shopware.migration.reverse_proxy.ban_all"
	scheduledTaskLoggerCode           lsp.DiagnosticID = "shopware.migration.scheduled_task.exception_logger"
	entitySearchResultClass                            = "Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult"
	entityExtensionClass                               = "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityExtension"
	reverseProxyGatewayClass                           = "Shopware\\Storefront\\Framework\\Cache\\ReverseProxy\\AbstractReverseProxyGateway"
	scheduledTaskHandlerClass                          = "Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTaskHandler"
	loggerInterfaceClass                               = "Psr\\Log\\LoggerInterface"
)

var entitySearchResultDelegatedMethods = map[string]struct{}{
	"count": {}, "fill": {}, "filter": {}, "filterandreducebyproperty": {},
	"filterbyproperty": {}, "filterinstance": {}, "first": {},
	"firstwhere": {}, "flatmap": {}, "fmap": {}, "get": {}, "getat": {},
	"getcustomfieldsvalue": {}, "getcustomfieldsvalues": {},
	"getelements": {}, "getids": {}, "getkeys": {}, "getlist": {},
	"has": {}, "insert": {}, "isempty": {}, "last": {}, "map": {},
	"merge": {}, "reduce": {}, "remove": {}, "set": {},
	"setcustomfields": {}, "slice": {}, "sort": {}, "sortbyidarray": {},
}

var entitySearchResultDelegatedFunctions = map[string]struct{}{
	"count": {}, "iterator_count": {}, "iterator_to_array": {}, "sizeof": {},
}

type ShopwareMigrationPayload struct {
	Rule          string                  `json:"rule"`
	Kind          string                  `json:"kind"`
	Safe          bool                    `json:"safe"`
	Replacement   string                  `json:"replacement,omitempty"`
	Original      string                  `json:"original,omitempty"`
	RemoveLegacy  bool                    `json:"removeLegacy,omitempty"`
	AddParameter  bool                    `json:"addParameter,omitempty"`
	Start         uint32                  `json:"start,omitempty"`
	End           uint32                  `json:"end,omitempty"`
	Edits         []ShopwareMigrationEdit `json:"edits,omitempty"`
	ArgumentIndex int                     `json:"argumentIndex,omitempty"`
	Value         string                  `json:"value,omitempty"`
}

type ShopwareMigrationEdit struct {
	Start       uint32 `json:"start"`
	End         uint32 `json:"end"`
	Original    string `json:"original"`
	Replacement string `json:"replacement"`
}

// ShopwareMigrationAnalyzer ports versioned, deterministic shopware-rector
// rules to byte-oriented LSP problems. Unknown target versions deliberately
// disable the rules rather than guessing which migration set applies.
type ShopwareMigrationAnalyzer struct {
	phpIndex *php.PHPIndex
	version  shopware.ResolvedVersion
}

func NewShopwareMigrationAnalyzer(
	phpIndex *php.PHPIndex,
	version shopware.ResolvedVersion,
) *ShopwareMigrationAnalyzer {
	return &ShopwareMigrationAnalyzer{phpIndex: phpIndex, version: version}
}

func (p *ShopwareMigrationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || !p.version.AtLeast(6, 5, 0) ||
		document == nil || document.SyntaxLanguage != language.PHP ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil ||
		document.LineIndex == nil ||
		strings.ToLower(filepath.Ext(document.URI)) != ".php" {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	semanticDocument := p.phpIndex.AnalyzeDocument(
		path,
		document.Version,
		document.SyntaxTree.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(semanticDocument)
	root := document.SyntaxTree.Root
	result := make([]lsp.Problem, 0)
	result = append(result, p.entityExtensionProblems(ctx, root, snapshot)...)
	result = append(result, p.reverseProxyProblems(ctx, root, snapshot)...)
	result = append(result, p.contextMetadataProblems(ctx, root, semanticDocument, snapshot)...)
	result = append(result, p.fakerPropertyProblems(ctx, root, semanticDocument, snapshot)...)
	result = append(result, p.apiMigrationProblems(ctx, root, semanticDocument, snapshot, document.Source)...)
	result = append(result, p.argumentMigrationProblems(ctx, root, semanticDocument, snapshot, document.Source)...)
	result = append(result, p.declarationMigrationProblems(ctx, root, snapshot)...)
	result = append(result, p.routeAnnotationMigrationProblems(ctx, root)...)
	result = append(result, p.messageHandlerMigrationProblems(ctx, root)...)
	if p.version.AtLeast(6, 7, 0) {
		result = append(result, p.scheduledTaskLoggerProblems(ctx, root, snapshot)...)
	}
	if !p.version.AtLeast(6, 8, 0) {
		return result, nil
	}
	result = append(result, p.productStreamBuilderProblems(
		ctx,
		root,
		semanticDocument,
		snapshot,
		document.Source,
	)...)

	for _, call := range phpquery.Nodes(root, phpsyntax.PhpMemberCall) {
		if ctx.Err() != nil {
			return nil, nil
		}
		method := strings.ToLower(phpquery.CallMethodName(call))
		if _, delegated := entitySearchResultDelegatedMethods[method]; !delegated {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver == nil || !isEntitySearchResultType(
			semanticDocument.TypeOf(receiver).Type,
			snapshot,
		) {
			continue
		}
		name := callTargetName(call)
		rng := call.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		result = append(result, lsp.Problem{
			ID:       entitySearchResultGetEntitiesCode,
			Range:    rng,
			Element:  call,
			Message:  "Shopware 6.8: delegate EntitySearchResult::" + method + "() through getEntities()",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "entity-search-result-get-entities",
				Kind: "method",
				Safe: true,
			},
		})
	}

	for _, call := range phpquery.Nodes(root, phpsyntax.PhpFunctionCall) {
		if ctx.Err() != nil {
			return nil, nil
		}
		name := strings.ToLower(strings.TrimPrefix(phpquery.CallName(call), "\\"))
		if _, delegated := entitySearchResultDelegatedFunctions[name]; !delegated {
			continue
		}
		expression := phpArgumentExpression(phpquery.Argument(call, 0))
		if expression == nil || !isEntitySearchResultType(
			semanticDocument.TypeOf(expression).Type,
			snapshot,
		) {
			continue
		}
		result = append(result, lsp.Problem{
			ID:       entitySearchResultGetEntitiesCode,
			Range:    expression.RangeTrimmedTrivia(),
			Element:  call,
			Message:  "Shopware 6.8: pass EntitySearchResult::getEntities() to " + name + "()",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "entity-search-result-get-entities",
				Kind: "function",
				Safe: canAppendPHPMemberCall(expression),
			},
		})
	}

	for _, foreach := range phpquery.Nodes(root, phpsyntax.PhpForeachStatement) {
		if ctx.Err() != nil {
			return nil, nil
		}
		expression := phpForeachExpression(foreach)
		if expression == nil || !isEntitySearchResultType(
			semanticDocument.TypeOf(expression).Type,
			snapshot,
		) {
			continue
		}
		result = append(result, lsp.Problem{
			ID:       entitySearchResultGetEntitiesCode,
			Range:    expression.RangeTrimmedTrivia(),
			Element:  foreach,
			Message:  "Shopware 6.8: iterate EntitySearchResult::getEntities()",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "entity-search-result-get-entities",
				Kind: "foreach",
				Safe: canAppendPHPMemberCall(expression),
			},
		})
	}
	return result, nil
}

func (p *ShopwareMigrationAnalyzer) entityExtensionProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	removeLegacy := p.version.AtLeast(6, 7, 0)
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil || !phpClassIsSubtype(class, root, snapshot, entityExtensionClass) {
			continue
		}
		getEntityName := phpOwnMethodForMigration(class, "getEntityName")
		getDefinitionClass := phpOwnMethodForMigration(class, "getDefinitionClass")
		missingEntityName := getEntityName == nil
		legacyDefinition := removeLegacy && getDefinitionClass != nil
		if !missingEntityName && !legacyDefinition {
			continue
		}
		replacement := ""
		if getDefinitionClass != nil {
			replacement = entityNameConstantReference(getDefinitionClass)
		}
		safe := !missingEntityName || replacement != ""
		message := "Shopware: EntityExtension must implement getEntityName()"
		if legacyDefinition && !missingEntityName {
			message = "Shopware 6.7: remove legacy EntityExtension::getDefinitionClass()"
		} else if legacyDefinition {
			message = "Shopware 6.7: replace EntityExtension::getDefinitionClass() with getEntityName()"
		}
		name := phpquery.DirectChild(class, phpsyntax.PhpName)
		rng := class.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		result = append(result, lsp.Problem{
			ID:       entityExtensionEntityNameCode,
			Range:    rng,
			Element:  class,
			Message:  message,
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule:         "entity-extension-entity-name",
				Kind:         "class",
				Safe:         safe,
				Replacement:  replacement,
				RemoveLegacy: legacyDefinition,
			},
		})
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) reverseProxyProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil ||
			!phpClassIsSubtype(class, root, snapshot, reverseProxyGatewayClass) ||
			phpOwnMethodForMigration(class, "banAll") != nil {
			continue
		}
		name := phpquery.DirectChild(class, phpsyntax.PhpName)
		rng := class.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		result = append(result, lsp.Problem{
			ID:       reverseProxyBanAllCode,
			Range:    rng,
			Element:  class,
			Message:  "Shopware: reverse proxy gateways must implement banAll()",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule: "reverse-proxy-ban-all",
				Kind: "class",
				Safe: true,
			},
		})
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) scheduledTaskLoggerProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
) []lsp.Problem {
	var result []lsp.Problem
	resolver := php.NewNameResolver(root)
	for _, class := range phpquery.Classes(root) {
		if ctx.Err() != nil ||
			!phpClassIsSubtype(class, root, snapshot, scheduledTaskHandlerClass) {
			continue
		}
		constructor := phpOwnMethodForMigration(class, "__construct")
		if constructor == nil {
			continue
		}
		missingCalls := scheduledTaskParentConstructorCalls(constructor)
		if len(missingCalls) == 0 {
			continue
		}
		parameterExists := false
		parameterCompatible := false
		parameters := phpquery.IterateParameters(constructor)
		for parameters.Next() {
			parameter := parameters.Node()
			if !strings.EqualFold(phpquery.ParameterName(parameter), "$exceptionLogger") {
				continue
			}
			parameterExists = true
			parameterType := strings.TrimPrefix(
				resolver.Resolve(phpquery.ParameterType(parameter)),
				"\\",
			)
			parameterCompatible = strings.EqualFold(parameterType, loggerInterfaceClass)
			break
		}
		safe := !parameterExists || parameterCompatible
		name := phpquery.DirectChild(class, phpsyntax.PhpName)
		rng := class.RangeTrimmedTrivia()
		if name != nil {
			rng = name.RangeTrimmedTrivia()
		}
		result = append(result, lsp.Problem{
			ID:       scheduledTaskLoggerCode,
			Range:    rng,
			Element:  class,
			Message:  "Shopware 6.7: pass an exception logger to ScheduledTaskHandler::__construct()",
			Severity: protocol.DiagnosticSeverityWarning,
			Source:   "shopware-rector",
			Payload: ShopwareMigrationPayload{
				Rule:         "scheduled-task-exception-logger",
				Kind:         "class",
				Safe:         safe,
				AddParameter: !parameterExists,
			},
		})
	}
	return result
}

func scheduledTaskParentConstructorCalls(
	constructor *phpsyntax.Node,
) []*phpsyntax.Node {
	var result []*phpsyntax.Node
	for _, call := range phpquery.Nodes(constructor, phpsyntax.PhpScopedCall) {
		if phpquery.FunctionLikeAt(call) != constructor ||
			!strings.EqualFold(phpquery.CallMethodName(call), "__construct") ||
			len(phpquery.Arguments(call)) != 1 {
			continue
		}
		receiver := phpquery.CallReceiver(call)
		if receiver != nil && strings.EqualFold(strings.TrimSpace(receiver.Text()), "parent") {
			result = append(result, call)
		}
	}
	return result
}

func phpClassIsSubtype(
	class *phpsyntax.Node,
	root *phpsyntax.Node,
	snapshot *semantic.Snapshot,
	target string,
) bool {
	if class == nil || root == nil || snapshot == nil {
		return false
	}
	name := phpquery.ClassName(class)
	if name == "" {
		return false
	}
	if namespace := phpquery.Namespace(root); namespace != "" {
		name = namespace + "\\" + name
	}
	return snapshot.IsSubtypeOf(name, target)
}

func phpOwnMethodForMigration(
	class *phpsyntax.Node,
	name string,
) *phpsyntax.Node {
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(method), name) {
			return method
		}
	}
	return nil
}

func entityNameConstantReference(method *phpsyntax.Node) string {
	var result string
	for _, phpReturn := range phpquery.Nodes(method, phpsyntax.PhpReturnStatement) {
		if phpquery.FunctionLikeAt(phpReturn) != method {
			continue
		}
		expression := directReturnExpression(phpReturn)
		if expression == nil ||
			(expression.Kind() != phpsyntax.PhpScopedAccess &&
				expression.Kind() != phpsyntax.PhpMemberAccess) {
			return ""
		}
		value := strings.TrimSpace(expression.Text())
		separator := strings.LastIndex(value, "::")
		if separator <= 0 || !strings.EqualFold(strings.TrimSpace(value[separator+2:]), "class") {
			return ""
		}
		candidate := strings.TrimSpace(value[:separator]) + "::ENTITY_NAME"
		if result != "" && result != candidate {
			return ""
		}
		result = candidate
	}
	return result
}

func isEntitySearchResultType(
	value types.Type,
	snapshot *semantic.Snapshot,
) bool {
	if snapshot == nil {
		return false
	}
	if value.Kind() == types.ObjectKind && value.Name() != "" {
		return snapshot.IsSubtypeOf(value.Name(), entitySearchResultClass)
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		if isEntitySearchResultType(value.Argument(index), snapshot) {
			return true
		}
	}
	return false
}

func callTargetName(call *phpsyntax.Node) *phpsyntax.Node {
	if call == nil {
		return nil
	}
	var target *phpsyntax.Node
	for index := 0; index < call.ChildCount(); index++ {
		child, ok := call.Child(index).(*phpsyntax.Node)
		if !ok {
			continue
		}
		if child.Kind() == phpsyntax.PhpArgumentList {
			return target
		}
		if child.Kind() == phpsyntax.PhpName {
			target = child
		}
	}
	return target
}

func phpArgumentExpression(argument *phpsyntax.Node) *phpsyntax.Node {
	if argument == nil {
		return nil
	}
	var expression *phpsyntax.Node
	for child := range argument.ChildNodes() {
		expression = child
	}
	return expression
}

func phpForeachExpression(foreach *phpsyntax.Node) *phpsyntax.Node {
	if foreach == nil {
		return nil
	}
	for index := 0; index < foreach.ChildCount(); index++ {
		if child, ok := foreach.Child(index).(*phpsyntax.Node); ok {
			return child
		}
	}
	return nil
}

func canAppendPHPMemberCall(expression *phpsyntax.Node) bool {
	if expression == nil {
		return false
	}
	switch expression.Kind() {
	case phpsyntax.PhpVariable, phpsyntax.PhpMemberCall,
		phpsyntax.PhpScopedCall, phpsyntax.PhpFunctionCall,
		phpsyntax.PhpMemberAccess, phpsyntax.PhpScopedAccess,
		phpsyntax.PhpArrayAccess, phpsyntax.PhpObjectCreation,
		phpsyntax.PhpParenthesized:
		return true
	default:
		return false
	}
}
