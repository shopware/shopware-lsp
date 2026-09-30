package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AppDatasetAnalyzer validates Meteor Admin SDK dataset selectors against the
// owning app's manifest.xml read permissions.
//
// It finds literal `data.subscribe(...)` and `data.get(...)` calls in app
// administration sources, resolves the dataset id suffix after `__` to a DAL
// entity, walks each selector path through DAL associations, and reports
// selected entities without a `read` permission. Calls with dynamic ids or
// selectors are skipped without an error. Subscriptions without selectors
// receive a warning because the whole dataset is sent.
type AppDatasetAnalyzer struct {
	extensions *extension.ExtensionIndexer
	dal        *shopwaredal.Index
}

// NewAppDatasetAnalyzer creates the dataset permission analyzer. Nil indexes
// keep the analyzer fail-open for workspaces without those domains.
func NewAppDatasetAnalyzer(
	extensions *extension.ExtensionIndexer,
	dal *shopwaredal.Index,
) *AppDatasetAnalyzer {
	return &AppDatasetAnalyzer{extensions: extensions, dal: dal}
}

// Analyze implements the inspection analyzer contract.
func (a *AppDatasetAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if a == nil || a.extensions == nil || a.dal == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	if document.SyntaxLanguage != language.JavaScript &&
		document.SyntaxLanguage != language.Vue {
		return nil, nil
	}
	if !strings.Contains(
		filepath.ToSlash(document.URI),
		"/Resources/app/administration",
	) {
		return nil, nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, nil
	}
	app, err := a.extensions.FindAppForFile(path)
	if err != nil || app == nil {
		return nil, err
	}
	readPermissions := make(map[string]struct{})
	for _, permission := range app.Permissions {
		if permission.Operation == "read" && permission.Entity != "" {
			readPermissions[permission.Entity] = struct{}{}
		}
	}
	definitions, err := a.dal.Definitions()
	if err != nil || len(definitions) == 0 {
		return nil, err
	}
	entityFields := make(map[string]map[string]shopwaredal.Field, len(definitions))
	classToEntity := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "" {
			continue
		}
		if _, exists := entityFields[definition.Name]; !exists {
			entityFields[definition.Name] = make(map[string]shopwaredal.Field)
		}
		for _, field := range definition.Fields {
			if field.Name == "" {
				continue
			}
			if _, exists := entityFields[definition.Name][field.Name]; !exists {
				entityFields[definition.Name][field.Name] = field
			}
		}
		if definition.FullyQualifiedClass != "" {
			key := strings.Trim(definition.FullyQualifiedClass, `\`)
			if _, exists := classToEntity[key]; !exists {
				classToEntity[key] = definition.Name
			}
		}
	}
	manifestPath := filepath.Join(app.Path, "manifest.xml")
	root := document.SyntaxTree.Root
	var problems []lsp.Problem
	for call := range jsquery.IterateCalls(root) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		callName := jsquery.CallName(call)
		method := jsquery.CallMethodName(call)
		isSubscribe := method == "subscribe" &&
			(callName == "data.subscribe" || strings.HasSuffix(callName, ".data.subscribe"))
		isGet := method == "get" &&
			(callName == "data.get" || strings.HasSuffix(callName, ".data.get"))
		if !isSubscribe && !isGet {
			continue
		}
		if isSubscribe {
			callProblems, handled := a.subscribeCallProblems(
				call, document, entityFields, classToEntity, readPermissions, manifestPath,
			)
			if !handled {
				continue
			}
			problems = append(problems, callProblems...)
			continue
		}
		callProblems, handled := a.getCallProblems(
			call, document, entityFields, classToEntity, readPermissions, manifestPath,
		)
		if !handled {
			continue
		}
		problems = append(problems, callProblems...)
	}
	return problems, nil
}

func (a *AppDatasetAnalyzer) subscribeCallProblems(
	call *jssyntax.Node,
	document *lsp.TextDocument,
	entityFields map[string]map[string]shopwaredal.Field,
	classToEntity map[string]string,
	readPermissions map[string]struct{},
	manifestPath string,
) ([]lsp.Problem, bool) {
	idExpression := jsquery.ArgumentExpression(call, 0)
	if idExpression == nil || idExpression.Kind() != jssyntax.JsString {
		return nil, false
	}
	datasetID := jsquery.StringValue(idExpression)
	if datasetID == "" || strings.Contains(datasetID, "${") {
		return nil, false
	}
	idRange := javaScriptStringContentRange(idExpression, document.Text)
	optionsArgument := jsquery.Argument(call, 2)
	if optionsArgument == nil {
		return []lsp.Problem{subscribeWithoutSelectorsProblem(idRange, datasetID)}, true
	}
	optionsObject := jsquery.ObjectArgument(call, 2)
	if optionsObject == nil {
		return nil, false
	}
	selectorsProperty := jsquery.Property(optionsObject, "selectors")
	if selectorsProperty == nil {
		return []lsp.Problem{subscribeWithoutSelectorsProblem(idRange, datasetID)}, true
	}
	selectorsValue := jsquery.PropertyValue(selectorsProperty)
	if selectorsValue == nil || selectorsValue.Kind() != jssyntax.JsArray {
		return nil, false
	}
	selectors, ok := literalStringArrayItems(selectorsValue)
	if !ok {
		return nil, false
	}
	if len(selectors) == 0 {
		return []lsp.Problem{subscribeWithoutSelectorsProblem(idRange, datasetID)}, true
	}
	return a.selectorPermissionProblems(
		idExpression, idRange, datasetID, selectors,
		entityFields, classToEntity, readPermissions, manifestPath,
	), true
}

func (a *AppDatasetAnalyzer) getCallProblems(
	call *jssyntax.Node,
	document *lsp.TextDocument,
	entityFields map[string]map[string]shopwaredal.Field,
	classToEntity map[string]string,
	readPermissions map[string]struct{},
	manifestPath string,
) ([]lsp.Problem, bool) {
	firstExpression := jsquery.ArgumentExpression(call, 0)
	if firstExpression == nil {
		return nil, false
	}
	var idNode *jssyntax.Node
	var datasetID string
	var selectorsObject *jssyntax.Node
	if firstExpression.Kind() == jssyntax.JsObject {
		optionsObject := firstExpression
		idProperty := jsquery.Property(optionsObject, "id")
		idValue := jsquery.PropertyValue(idProperty)
		if idValue == nil || idValue.Kind() != jssyntax.JsString {
			return nil, false
		}
		idNode = idValue
		datasetID = jsquery.StringValue(idValue)
		selectorsObject = optionsObject
	} else if firstExpression.Kind() == jssyntax.JsString {
		idNode = firstExpression
		datasetID = jsquery.StringValue(firstExpression)
		secondArgument := jsquery.Argument(call, 1)
		if secondArgument == nil {
			return nil, true
		}
		secondObject := jsquery.ObjectArgument(call, 1)
		if secondObject == nil {
			return nil, false
		}
		selectorsObject = secondObject
	} else {
		return nil, false
	}
	if datasetID == "" || strings.Contains(datasetID, "${") || idNode == nil {
		return nil, false
	}
	if selectorsObject == nil {
		return nil, true
	}
	selectorsProperty := jsquery.Property(selectorsObject, "selectors")
	if selectorsProperty == nil {
		return nil, true
	}
	selectorsValue := jsquery.PropertyValue(selectorsProperty)
	if selectorsValue == nil || selectorsValue.Kind() != jssyntax.JsArray {
		return nil, false
	}
	selectors, ok := literalStringArrayItems(selectorsValue)
	if !ok {
		return nil, false
	}
	if len(selectors) == 0 {
		return nil, true
	}
	idRange := javaScriptStringContentRange(idNode, document.Text)
	return a.selectorPermissionProblems(
		idNode, idRange, datasetID, selectors,
		entityFields, classToEntity, readPermissions, manifestPath,
	), true
}

func (a *AppDatasetAnalyzer) selectorPermissionProblems(
	idNode *jssyntax.Node,
	idRange jssyntax.TextRange,
	datasetID string,
	selectors []string,
	entityFields map[string]map[string]shopwaredal.Field,
	classToEntity map[string]string,
	readPermissions map[string]struct{},
	manifestPath string,
) []lsp.Problem {
	rootEntity, ok := datasetRootEntity(datasetID)
	if !ok {
		return nil
	}
	if _, exists := entityFields[rootEntity]; !exists {
		return nil
	}
	required, examples := requiredDatasetEntities(
		rootEntity, selectors, entityFields, classToEntity,
	)
	var problems []lsp.Problem
	missing := make([]string, 0, len(required))
	for _, entity := range required {
		if _, exists := readPermissions[entity]; !exists {
			missing = append(missing, entity)
		}
	}
	sort.Strings(missing)
	for _, entity := range missing {
		example := examples[entity]
		message := fmt.Sprintf(
			"App manifest needs read permission for entity '%s' (dataset '%s' selects '%s')",
			entity, datasetID, example,
		)
		if example == "" {
			message = fmt.Sprintf(
				"App manifest needs read permission for entity '%s' (dataset '%s')",
				entity, datasetID,
			)
		}
		problems = append(problems, lsp.Problem{
			ID:       "app_dataset.permission-missing",
			Range:    idRange,
			Element:  idNode,
			Message:  message,
			Severity: protocol.DiagnosticSeverityError,
			Source:   "shopware-lsp",
			Payload: map[string]any{
				"entity":   entity,
				"dataset":  datasetID,
				"manifest": manifestPath,
			},
		})
	}
	return problems
}

func subscribeWithoutSelectorsProblem(
	idRange jssyntax.TextRange,
	datasetID string,
) lsp.Problem {
	return lsp.Problem{
		ID:    "app_dataset.subscribe-without-selectors",
		Range: idRange,
		Message: fmt.Sprintf(
			"data.subscribe('%s') without selectors receives the whole dataset; needed privileges depend on core and plugins over time",
			datasetID,
		),
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-lsp",
		Payload:  map[string]any{"dataset": datasetID},
	}
}

func literalStringArrayItems(array *jssyntax.Node) ([]string, bool) {
	if array == nil || array.Kind() != jssyntax.JsArray {
		return nil, false
	}
	items := jsquery.ArrayItems(array)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item == nil || item.Kind() != jssyntax.JsString {
			return nil, false
		}
		value := jsquery.StringValue(item)
		if strings.Contains(value, "${") {
			return nil, false
		}
		result = append(result, value)
	}
	return result, true
}

// datasetRootEntity resolves a dataset id to its root DAL entity. Dataset ids
// have the form `<component>__<root>` where the suffix is the camelCase or
// kebab-case entity hint (for example `sw-product-detail__product` resolves
// to `product` and `sw-sales-channel-detail__salesChannel` to
// `sales_channel`). Ids without a `__` separator cannot be resolved
// statically and are skipped.
func datasetRootEntity(datasetID string) (string, bool) {
	separator := strings.LastIndex(datasetID, "__")
	if separator < 0 {
		return "", false
	}
	suffix := strings.TrimSpace(datasetID[separator+2:])
	if suffix == "" {
		return "", false
	}
	normalized := normalizeDatasetEntityName(suffix)
	if normalized == "" {
		return "", false
	}
	return normalized, true
}

func normalizeDatasetEntityName(value string) string {
	value = strings.ReplaceAll(value, "-", "_")
	var builder strings.Builder
	builder.Grow(len(value) + 4)
	for index, r := range value {
		if r >= 'A' && r <= 'Z' {
			if index > 0 {
				previous := value[index-1]
				if (previous >= 'a' && previous <= 'z') || (previous >= '0' && previous <= '9') {
					builder.WriteByte('_')
				}
			}
			builder.WriteByte(byte(r - 'A' + 'a'))
			continue
		}
		builder.WriteRune(r)
	}
	normalized := strings.ToLower(builder.String())
	normalized = strings.Trim(normalized, "_")
	for strings.Contains(normalized, "__") {
		normalized = strings.ReplaceAll(normalized, "__", "_")
	}
	return normalized
}

// requiredDatasetEntities walks every selector through DAL associations and
// collects the root entity plus each association target. Unknown fields stop
// only their own selector so one renamed field cannot hide the remaining
// privileges.
func requiredDatasetEntities(
	rootEntity string,
	selectors []string,
	entityFields map[string]map[string]shopwaredal.Field,
	classToEntity map[string]string,
) ([]string, map[string]string) {
	seen := map[string]struct{}{rootEntity: {}}
	examples := map[string]string{}
	ordered := []string{rootEntity}
	for _, selector := range selectors {
		segments := selectorPathSegments(selector)
		if len(segments) == 0 {
			continue
		}
		current := rootEntity
		for index, segment := range segments {
			fields, exists := entityFields[current]
			if !exists {
				break
			}
			field, found := fields[segment]
			if !found {
				break
			}
			if !field.Association {
				if index < len(segments)-1 {
					break
				}
				continue
			}
			target := strings.Trim(field.TargetClass, `\`)
			targetEntity, resolved := classToEntity[target]
			if !resolved || targetEntity == "" {
				break
			}
			if _, duplicate := seen[targetEntity]; !duplicate {
				seen[targetEntity] = struct{}{}
				ordered = append(ordered, targetEntity)
			}
			if _, hasExample := examples[targetEntity]; !hasExample {
				examples[targetEntity] = selector
			}
			current = targetEntity
		}
	}
	sort.Strings(ordered)
	return ordered, examples
}

func selectorPathSegments(selector string) []string {
	parts := strings.Split(selector, ".")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "*" {
			continue
		}
		if strings.HasPrefix(part, "[") && strings.HasSuffix(part, "]") {
			continue
		}
		part = strings.Trim(part, "[]")
		if part == "" || part == "*" {
			continue
		}
		segments = append(segments, part)
	}
	return segments
}
