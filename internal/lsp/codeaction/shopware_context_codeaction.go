package codeaction

import (
	"context"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	extendAdminComponentAction = "shopware.admin.extendComponent"
	overrideAdminMethodAction  = "shopware.admin.overrideMethod"
	createEventListenerAction  = "shopware.createEventListener"
)

var jsParameterNamePattern = regexp.MustCompile(
	`^\s*(?:\.\.\.)?([A-Za-z_$][A-Za-z0-9_$]*)(?:\s*=.*)?$`,
)

// AdminContextProvider exposes the reference plugin's component extension
// intentions through portable command-backed code actions.
type AdminContextProvider struct{}

func NewAdminContextProvider() *AdminContextProvider { return &AdminContextProvider{} }

func (*AdminContextProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (*AdminContextProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || request == nil || request.Document == nil ||
		request.Node == nil || request.Document.SyntaxLanguage != language.JavaScript {
		return nil
	}
	if component := adminComponentReference(request.Node); component != "" {
		return []protocol.CodeAction{{
			Title: "Shopware: Extend or override component '" + component + "'",
			Kind:  protocol.CodeActionRefactorRewrite,
			Command: &protocol.CommandAction{
				Title:     "Extend or override Administration component",
				Command:   extendAdminComponentAction,
				Arguments: []any{component, request.Document.URI},
			},
		}}
	}
	method := adminMethodAt(request.Node)
	if method == nil {
		return nil
	}
	call := jsquery.CallAt(method)
	switch jsquery.CallName(call) {
	case "Component.register", "Shopware.Component.register",
		"Component.extend", "Shopware.Component.extend":
	default:
		return nil
	}
	componentNode := jsquery.StringArgument(call, 0)
	component := jsquery.StringValue(componentNode)
	methodName := jsquery.MethodName(method)
	if component == "" || methodName == "" {
		return nil
	}
	parameters, ok := adminMethodParameters(method)
	if !ok {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Shopware: Override method '" + methodName + "'",
		Kind:  protocol.CodeActionRefactorRewrite,
		Command: &protocol.CommandAction{
			Title:   "Override Administration component method",
			Command: overrideAdminMethodAction,
			Arguments: []any{
				component,
				methodName,
				adminMethodGroup(method, call),
				strings.Join(parameters, ","),
				request.Document.URI,
			},
		},
	}}
}

func adminComponentReference(node *jssyntax.Node) string {
	stringNode := jsquery.StringAt(node)
	if stringNode == nil {
		return ""
	}
	call := jsquery.CallAt(stringNode)
	index := jsquery.StringArgumentIndex(stringNode)
	switch jsquery.CallName(call) {
	case "Component.register", "Shopware.Component.register":
		if index == 0 {
			return jsquery.StringValue(stringNode)
		}
	case "Component.extend", "Shopware.Component.extend":
		if index == 1 {
			return jsquery.StringValue(stringNode)
		}
	case "Component.override", "Shopware.Component.override":
		if index == 0 {
			return jsquery.StringValue(stringNode)
		}
	}
	return ""
}

func adminMethodAt(node *jssyntax.Node) *jssyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsMethod {
			return current
		}
	}
	return nil
}

func adminMethodParameters(method *jssyntax.Node) ([]string, bool) {
	for child := range method.ChildNodes() {
		if child.Kind() != jssyntax.JsArgumentList {
			continue
		}
		var result []string
		for argument := range child.ChildNodes() {
			if argument.Kind() != jssyntax.JsArgument {
				continue
			}
			match := jsParameterNamePattern.FindStringSubmatch(argument.Text())
			if len(match) != 2 {
				return nil, false
			}
			result = append(result, match[1])
		}
		return result, true
	}
	return nil, true
}

func adminMethodGroup(method, call *jssyntax.Node) string {
	object := method.Parent()
	if object == nil || object.Kind() != jssyntax.JsObject {
		return ""
	}
	argumentCount := jsquery.IterateArguments(call).Len()
	registration := jsquery.ObjectArgument(call, argumentCount-1)
	if registration == object {
		return ""
	}
	if property := object.Parent(); property != nil &&
		property.Kind() == jssyntax.JsProperty &&
		jsquery.PropertyValue(property) == object {
		return jsquery.PropertyName(property)
	}
	return ""
}

// EventListenerContextProvider creates an AsEventListener scaffold from an
// event class declaration, retaining the reference plugin's contextual intent.
type EventListenerContextProvider struct {
	phpIndex *php.PHPIndex
}

func NewEventListenerContextProvider(phpIndex *php.PHPIndex) *EventListenerContextProvider {
	return &EventListenerContextProvider{phpIndex: phpIndex}
}

func (*EventListenerContextProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *EventListenerContextProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil || request == nil ||
		request.Document == nil || request.Root == nil || request.Node == nil ||
		request.Document.SyntaxLanguage != language.PHP {
		return nil
	}
	class := phpquery.ClassAt(request.Node)
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return nil
	}
	className := phpClassFullyQualifiedName(request.Root, class)
	if className == "" {
		return nil
	}
	path := request.Document.URI
	if resolved, err := uriutil.Path(path); err == nil {
		path = resolved
	}
	document := p.phpIndex.AnalyzeDocument(
		path,
		request.Document.Version,
		request.Root,
	)
	snapshot := p.phpIndex.SemanticSnapshot().WithDocument(document)
	isEvent := false
	for _, base := range []string{
		`Symfony\Contracts\EventDispatcher\Event`,
		`Symfony\Component\EventDispatcher\Event`,
	} {
		if className != base && snapshot.IsSubtypeOf(className, base) {
			isEvent = true
			break
		}
	}
	if !isEvent {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Shopware: Create listener for " + phpquery.ClassName(class),
		Kind:  protocol.CodeActionRefactorRewrite,
		Command: &protocol.CommandAction{
			Title:   "Create Shopware event listener",
			Command: createEventListenerAction,
			Arguments: []any{
				className,
				phpquery.ClassName(class) + "Listener",
				request.Document.URI,
			},
		},
	}}
}

var _ lsp.ActionProvider = (*AdminContextProvider)(nil)
var _ lsp.ActionProvider = (*EventListenerContextProvider)(nil)
