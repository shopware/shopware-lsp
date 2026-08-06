package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/shopware"
)

const entitySearchResultGetEntitiesFixID lsp.FixID = "shopware-rector-entity-search-result-get-entities"

const (
	entityExtensionEntityNameFixID lsp.FixID = "shopware-rector-entity-extension-entity-name"
	reverseProxyBanAllFixID        lsp.FixID = "shopware-rector-reverse-proxy-ban-all"
	scheduledTaskLoggerFixID       lsp.FixID = "shopware-rector-scheduled-task-exception-logger"
)

func NewShopwareMigration(
	phpIndex *php.PHPIndex,
	version shopware.ResolvedVersion,
) lsp.Inspection {
	return &boundInspection{
		definition: lsp.InspectionDefinition{
			ID:        "shopware.migration",
			Languages: []language.ID{language.PHP},
			Problems: []lsp.ProblemDefinition{
				{
					ID: "shopware.migration.entity_search_result.get_entities", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.entity_extension.entity_name", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.reverse_proxy.ban_all", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.scheduled_task.exception_logger", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.context_metadata.state", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.faker.property_call", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.product_stream.enrich_criteria", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.api.class.rename", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.api.method.rename", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.api.static_method.rename", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.api.constant.rename", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.api.property", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.api.exception_factory", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.arguments.remove", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.arguments.add_default", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.thumbnail.generate", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.declaration.interface_to_abstract", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.declaration.parameter.add", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.declaration.type", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.annotation.route_defaults", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
				{
					ID: "shopware.migration.message_handler.subscriber", Source: "shopware-rector",
					DefaultSeverity: protocol.DiagnosticSeverityWarning,
				},
			},
		},
		analyzer: diagnostics.NewShopwareMigrationAnalyzer(phpIndex, version),
		fixes: []lsp.QuickFix{
			entitySearchResultGetEntitiesFix{},
			entityExtensionEntityNameFix{},
			reverseProxyBanAllFix{},
			scheduledTaskLoggerFix{},
			contextMetadataStateFix{},
			fakerPropertyCallFix{},
			productStreamEnrichCriteriaFix{},
			apiRenameFix{},
			removeArgumentMigrationFix{},
			addDefaultArgumentFix{},
			thumbnailGenerateFix{},
			declarationMigrationFix{},
			routeAnnotationMigrationFix{},
			messageHandlerSubscriberFix{},
		},
		bind: func(code lsp.DiagnosticID, payload map[string]any) []lsp.BoundFix {
			safe, _ := payload["safe"].(bool)
			if !safe {
				return nil
			}
			var fixID lsp.FixID
			switch string(code) {
			case "shopware.migration.entity_search_result.get_entities":
				fixID = entitySearchResultGetEntitiesFixID
			case "shopware.migration.entity_extension.entity_name":
				fixID = entityExtensionEntityNameFixID
			case "shopware.migration.reverse_proxy.ban_all":
				fixID = reverseProxyBanAllFixID
			case "shopware.migration.scheduled_task.exception_logger":
				fixID = scheduledTaskLoggerFixID
			case "shopware.migration.context_metadata.state":
				fixID = contextMetadataStateFixID
			case "shopware.migration.faker.property_call":
				fixID = fakerPropertyCallFixID
			case "shopware.migration.product_stream.enrich_criteria":
				fixID = productStreamEnrichCriteriaFixID
			case "shopware.migration.api.class.rename",
				"shopware.migration.api.method.rename",
				"shopware.migration.api.static_method.rename",
				"shopware.migration.api.constant.rename",
				"shopware.migration.api.property",
				"shopware.migration.api.exception_factory":
				fixID = apiRenameFixID
			case "shopware.migration.arguments.remove":
				fixID = removeArgumentMigrationFixID
			case "shopware.migration.arguments.add_default":
				fixID = addDefaultArgumentFixID
			case "shopware.migration.thumbnail.generate":
				fixID = thumbnailGenerateFixID
			case "shopware.migration.declaration.interface_to_abstract",
				"shopware.migration.declaration.parameter.add",
				"shopware.migration.declaration.type":
				fixID = declarationMigrationFixID
			case "shopware.migration.annotation.route_defaults":
				fixID = routeAnnotationMigrationFixID
			case "shopware.migration.message_handler.subscriber":
				fixID = messageHandlerSubscriberFixID
			default:
				return nil
			}
			return []lsp.BoundFix{lsp.BindFix(fixID, payload)}
		},
	}
}

type entitySearchResultGetEntitiesFix struct{}

func (entitySearchResultGetEntitiesFix) ID() lsp.FixID {
	return entitySearchResultGetEntitiesFixID
}

func (entitySearchResultGetEntitiesFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.8: Delegate through getEntities()",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "entity-search-result-get-entities", err
}

func (entitySearchResultGetEntitiesFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "entity-search-result-get-entities" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult rewrite is no longer safe")
	}
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	node, ok := element.(*phpsyntax.Node)
	if !ok {
		return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult migration target is unavailable")
	}
	builder := rewrite.NewBuilder(fixContext.Document.Source)
	switch payload.Kind {
	case "method":
		if err := rewriteEntitySearchResultMethod(builder, node); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "function":
		if node.Kind() != phpsyntax.PhpFunctionCall {
			return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult function call changed")
		}
		expression := phpArgumentExpressionForFix(phpquery.Argument(node, 0))
		if !canAppendPHPMemberCallForFix(expression) {
			return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult function argument changed")
		}
		if err := builder.Insert(expression.RangeTrimmedTrivia().End, "->getEntities()"); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "foreach":
		if node.Kind() != phpsyntax.PhpForeachStatement {
			return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult foreach changed")
		}
		expression := phpForeachExpressionForFix(node)
		if !canAppendPHPMemberCallForFix(expression) {
			return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult foreach expression changed")
		}
		if err := builder.Insert(expression.RangeTrimmedTrivia().End, "->getEntities()"); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	default:
		return rewrite.WorkspacePlan{}, fmt.Errorf("unknown EntitySearchResult migration target %q", payload.Kind)
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	updated, err := rewrite.Apply(fixContext.Document.Source, edits)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if len(lsp.NewTextDocument(
		fixContext.Document.URI,
		updated,
		fixContext.Document.Version+1,
	).ParseErrors) != 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("EntitySearchResult rewrite produced invalid PHP")
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}

func rewriteEntitySearchResultMethod(
	builder *rewrite.Builder,
	call *phpsyntax.Node,
) error {
	if call == nil || call.Kind() != phpsyntax.PhpMemberCall {
		return fmt.Errorf("EntitySearchResult method call changed")
	}
	method := strings.ToLower(phpquery.CallMethodName(call))
	if !entitySearchResultDelegatedMethodForFix(method) {
		return fmt.Errorf("EntitySearchResult method %q is not delegated", method)
	}
	receiver := phpquery.CallReceiver(call)
	if receiver == nil {
		return fmt.Errorf("EntitySearchResult receiver is unavailable")
	}
	if operator := call.ChildTokenOfKind(phpsyntax.TkNullsafeObjectOperator); operator != nil {
		if err := builder.Insert(receiver.RangeTrimmedTrivia().End, "?->getEntities()"); err != nil {
			return err
		}
		return builder.ReplaceRange(operator.Range(), "->")
	}
	return builder.Insert(receiver.RangeTrimmedTrivia().End, "->getEntities()")
}

func entitySearchResultDelegatedMethodForFix(method string) bool {
	switch method {
	case "count", "fill", "filter", "filterandreducebyproperty",
		"filterbyproperty", "filterinstance", "first", "firstwhere",
		"flatmap", "fmap", "get", "getat", "getcustomfieldsvalue",
		"getcustomfieldsvalues", "getelements", "getids", "getkeys",
		"getlist", "has", "insert", "isempty", "last", "map",
		"merge", "reduce", "remove", "set", "setcustomfields",
		"slice", "sort", "sortbyidarray":
		return true
	default:
		return false
	}
}

func phpArgumentExpressionForFix(argument *phpsyntax.Node) *phpsyntax.Node {
	if argument == nil {
		return nil
	}
	var expression *phpsyntax.Node
	for child := range argument.ChildNodes() {
		expression = child
	}
	return expression
}

func phpForeachExpressionForFix(foreach *phpsyntax.Node) *phpsyntax.Node {
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

func canAppendPHPMemberCallForFix(expression *phpsyntax.Node) bool {
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

type entityExtensionEntityNameFix struct{}

func (entityExtensionEntityNameFix) ID() lsp.FixID {
	return entityExtensionEntityNameFixID
}

func (entityExtensionEntityNameFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware: Migrate EntityExtension entity name",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "entity-extension-entity-name", err
}

func (entityExtensionEntityNameFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "entity-extension-entity-name" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("EntityExtension rewrite is no longer safe")
	}
	class, root, err := resolveMigrationClass(fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	editor := phprewrite.NewEditor(fixContext.Document.Source, root)
	entityName := phpOwnMethodForMigrationFix(class, "getEntityName")
	definition := phpOwnMethodForMigrationFix(class, "getDefinitionClass")
	if entityName == nil {
		if payload.Replacement == "" {
			return rewrite.WorkspacePlan{}, fmt.Errorf("EntityExtension entity name is unavailable")
		}
		if err := editor.InsertClassMember(class, `
public function getEntityName(): string
{
    return `+payload.Replacement+`;
}`); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	if payload.RemoveLegacy && definition != nil {
		if err := editor.RemoveClassMember(definition); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	return finishPHPRewrite(fixContext, editor)
}

type reverseProxyBanAllFix struct{}

func (reverseProxyBanAllFix) ID() lsp.FixID { return reverseProxyBanAllFixID }

func (reverseProxyBanAllFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware: Add reverse proxy banAll()",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "reverse-proxy-ban-all", err
}

type scheduledTaskLoggerFix struct{}

func (scheduledTaskLoggerFix) ID() lsp.FixID { return scheduledTaskLoggerFixID }

func (scheduledTaskLoggerFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	return lsp.FixPresentation{
		Title:      "Shopware 6.7: Add ScheduledTaskHandler exception logger",
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "scheduled-task-exception-logger", err
}

func (scheduledTaskLoggerFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "scheduled-task-exception-logger" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("scheduled task logger rewrite is no longer safe")
	}
	class, root, err := resolveMigrationClass(fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	constructor := phpOwnMethodForMigrationFix(class, "__construct")
	if constructor == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("scheduled task constructor is unavailable")
	}
	calls := scheduledTaskParentConstructorCallsForFix(constructor)
	if len(calls) == 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("scheduled task parent constructor call changed")
	}
	editor := phprewrite.NewEditor(fixContext.Document.Source, root)
	if payload.AddParameter {
		qualifier, qualifierErr := editor.ClassReference("Psr\\Log\\LoggerInterface")
		if qualifierErr != nil {
			return rewrite.WorkspacePlan{}, qualifierErr
		}
		index := requiredParameterInsertionIndex(constructor)
		if err := editor.InsertParameter(
			constructor,
			index,
			qualifier+" $exceptionLogger",
		); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	for _, call := range calls {
		if err := editor.AppendArgument(call, "$exceptionLogger"); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	}
	return finishPHPRewrite(fixContext, editor)
}

func scheduledTaskParentConstructorCallsForFix(
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

func requiredParameterInsertionIndex(constructor *phpsyntax.Node) int {
	parameters := phpquery.IterateParameters(constructor)
	index := 0
	for parameters.Next() {
		parameter := parameters.Node()
		if phpquery.ParameterOptional(parameter) || phpquery.ParameterVariadic(parameter) {
			return index
		}
		index++
	}
	return index
}

func (reverseProxyBanAllFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "reverse-proxy-ban-all" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("reverse proxy rewrite is no longer safe")
	}
	class, root, err := resolveMigrationClass(fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if phpOwnMethodForMigrationFix(class, "banAll") != nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("reverse proxy banAll() already exists")
	}
	editor := phprewrite.NewEditor(fixContext.Document.Source, root)
	if err := editor.InsertClassMember(class, `
public function banAll(): void
{
    $this->ban([]);
}`); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return finishPHPRewrite(fixContext, editor)
}

func resolveMigrationClass(
	fixContext lsp.FixContext,
) (*phpsyntax.Node, *phpsyntax.Node, error) {
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return nil, nil, err
	}
	class := ancestorNode(element, phpsyntax.PhpClassDeclaration)
	if class == nil {
		return nil, nil, fmt.Errorf("shopware migration class is unavailable")
	}
	return class, fixContext.Document.SyntaxTree.Root, nil
}

func phpOwnMethodForMigrationFix(
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

func finishPHPRewrite(
	fixContext lsp.FixContext,
	editor *phprewrite.Editor,
) (rewrite.WorkspacePlan, error) {
	edits, err := editor.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	updated, err := rewrite.Apply(fixContext.Document.Source, edits)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if len(lsp.NewTextDocument(
		fixContext.Document.URI,
		updated,
		fixContext.Document.Version+1,
	).ParseErrors) != 0 {
		return rewrite.WorkspacePlan{}, fmt.Errorf("shopware migration produced invalid PHP")
	}
	version := fixContext.Document.Version
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(
			fixContext.Document.URI,
			&version,
			fixContext.Document.Source,
			edits,
		),
	}}, nil
}
