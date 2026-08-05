package signature

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminSignatureProvider exposes callable types already retained by the
// Administration component, structural-type, Vue builtin, and Pinia indexes.
// It deliberately resolves only statically named calls whose owning scope is
// known; arbitrary JavaScript receivers continue to belong to a JS/TS language
// service rather than being guessed here.
type AdminSignatureProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminSignatureProvider(
	index *admin.AdminComponentIndexer,
) *AdminSignatureProvider {
	return &AdminSignatureProvider{index: index}
}

// GetSignatureHelpAtOffset resolves the same Administration signatures used
// by interactive signature help for another document-oriented LSP feature.
// node may identify a JavaScript call directly; Twig callers can pass nil and
// let the live document select the expression at offset.
func (p *AdminSignatureProvider) GetSignatureHelpAtOffset(
	ctx context.Context,
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) (*protocol.SignatureHelp, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil || document.LineIndex == nil ||
		offset > uint32(len(document.Text)) {
		return nil, nil
	}
	if node == nil {
		node = document.SyntaxTree.Root.NodeAtOffset(offset)
		if node == nil && offset > 0 {
			node = document.SyntaxTree.Root.NodeAtOffset(offset - 1)
		}
	}
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SignatureHelpParams{}
	params.TextDocument.URI = document.URI
	params.Position = protocol.Position{
		Line: int(line), Character: int(character),
	}
	return p.GetSignatureHelp(ctx, &lsp.SignatureHelpRequest{
		SignatureHelpParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, Language: document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	})
}

func (p *AdminSignatureProvider) GetSignatureHelp(
	_ context.Context,
	request *lsp.SignatureHelpRequest,
) (*protocol.SignatureHelp, error) {
	if p == nil || p.index == nil || request == nil ||
		request.SignatureHelpParams == nil || request.Node == nil ||
		request.Root == nil || request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line), uint32(request.Position.Character),
	)
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".ts":
		call := jsquery.CallAt(request.Node)
		if call == nil {
			return nil, nil
		}
		callables, resolveErr := p.javaScriptCallables(path, call)
		if resolveErr != nil {
			return nil, resolveErr
		}
		return adminSignatureHelp(
			callables,
			adminActiveArgument(
				directAdminChild(call, jssyntax.JsArgumentList),
				jssyntax.TkComma,
				offset,
			),
		), nil
	case ".twig":
		if !strings.Contains(filepath.ToSlash(path), "/Resources/app/administration/") {
			return nil, nil
		}
		call, found := admin.TwigVueCallAtOffset(
			request.Root, request.DocumentContent, offset,
		)
		if !found || call.Filter {
			return nil, nil
		}
		callables, resolveErr := p.twigCallables(path, request, call)
		if resolveErr != nil {
			return nil, resolveErr
		}
		return adminSignatureHelp(
			callables,
			call.ActiveArgument,
		), nil
	case ".vue":
		switch lsp.EffectiveSyntaxLanguage(language.Vue, request.Node) {
		case language.JavaScript:
			call := jsquery.CallAt(request.Node)
			if call == nil {
				return nil, nil
			}
			callables, resolveErr := p.javaScriptCallables(path, call)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return adminSignatureHelp(
				callables,
				adminActiveArgument(
					directAdminChild(call, jssyntax.JsArgumentList),
					jssyntax.TkComma,
					offset,
				),
			), nil
		case language.Twig:
			call, found := admin.TwigVueCallAtOffset(
				request.Root, request.DocumentContent, offset,
			)
			if !found || call.Filter {
				return nil, nil
			}
			callables, resolveErr := p.twigCallables(path, request, call)
			if resolveErr != nil {
				return nil, resolveErr
			}
			return adminSignatureHelp(callables, call.ActiveArgument), nil
		default:
			return nil, nil
		}
	default:
		return nil, nil
	}
}

type adminCallable struct {
	Name   string
	Type   string
	Detail string
}

func (p *AdminSignatureProvider) javaScriptCallables(
	path string,
	call *jssyntax.Node,
) ([]adminCallable, error) {
	callee := adminJavaScriptCallCallee(call)
	if callee == nil {
		return nil, nil
	}
	if callable, found, err := p.shopwareEventBusCallable(
		path, call,
	); err != nil {
		return nil, err
	} else if found {
		return []adminCallable{callable}, nil
	}
	if name, matched := jsquery.ThisMember(callee); matched {
		components, err := p.index.GetComponentsByDefinitionPath(path)
		if err != nil {
			return nil, err
		}
		var result []adminCallable
		for _, component := range components {
			member, found := component.TemplateMember(name)
			if !found {
				continue
			}
			result = append(result, adminCallable{
				Name: name, Type: member.Type,
				Detail: "Administration component `" + component.Name + "`",
			})
		}
		return result, nil
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareUtilsMember(callee); matched && memberName != "" {
		shape, err := p.index.ResolveShopwareUtils(
			strings.Join(receiver, "."), path,
		)
		if err != nil {
			return nil, err
		}
		member, found := func() (admin.TwigVueMember, bool) {
			for _, candidate := range shape.Members {
				if candidate.Name == memberName {
					return candidate, true
				}
			}
			return admin.TwigVueMember{}, false
		}()
		if !found {
			return nil, nil
		}
		return []adminCallable{{
			Name: memberName, Type: member.Type,
			Detail: "Shopware utility `Shopware.Utils." +
				strings.Join(append(receiver, memberName), ".") + "`",
		}}, nil
	}
	if filterName, matched := admin.JavaScriptFilterNameForCallee(callee); matched {
		filters, err := p.index.GetFilter(filterName)
		if err != nil {
			return nil, err
		}
		result := make([]adminCallable, 0, len(filters))
		for _, filter := range filters {
			if filter.Signature == "" {
				continue
			}
			result = append(result, adminCallable{
				Name: filter.Name, Type: filter.Signature,
				Detail: "Administration filter `" + filter.Name + "`",
			})
		}
		return result, nil
	}
	storeName, memberName, matched := jsquery.StoreMember(callee)
	if !matched || memberName == "" {
		return nil, nil
	}
	stores, err := p.index.GetStore(storeName)
	if err != nil {
		return nil, err
	}
	var result []adminCallable
	for _, store := range stores {
		member, found := store.Member(memberName)
		if !found || member.Kind != admin.AdminStoreAction {
			continue
		}
		result = append(result, adminCallable{
			Name: memberName, Type: member.Type,
			Detail: "Administration store `" + store.Name + "`",
		})
	}
	return result, nil
}

func (p *AdminSignatureProvider) shopwareEventBusCallable(
	path string,
	call *jssyntax.Node,
) (adminCallable, bool, error) {
	eventNode := jsquery.StringArgument(call, 0)
	operation, eventName, matched :=
		admin.JavaScriptShopwareEventBusEventAt(eventNode)
	if !matched || eventName == "" {
		return adminCallable{}, false, nil
	}
	event, found, err := p.index.ResolveShopwareEventBusEvent(eventName, path)
	if err != nil || !found {
		return adminCallable{}, false, err
	}
	payloadType := strings.TrimSpace(event.Type)
	if payloadType == "" {
		payloadType = "unknown"
	}
	eventType := strconv.Quote(eventName)
	var callableType string
	switch operation {
	case "emit":
		optional := ""
		if admin.VueTypeAllowsUndefined(payloadType) {
			optional = "?"
		}
		callableType = fmt.Sprintf(
			"(event: %s, payload%s: %s) => void",
			eventType, optional, payloadType,
		)
	case "on":
		callableType = fmt.Sprintf(
			"(event: %s, handler: (payload: %s) => void) => void",
			eventType, payloadType,
		)
	case "off":
		callableType = fmt.Sprintf(
			"(event: %s, handler?: (payload: %s) => void) => void",
			eventType, payloadType,
		)
	default:
		return adminCallable{}, false, nil
	}
	return adminCallable{
		Name: operation, Type: callableType,
		Detail: fmt.Sprintf(
			"Shopware EventBus event `%s`\n\nPayload: `%s`",
			eventName, payloadType,
		),
	}, true, nil
}

func (p *AdminSignatureProvider) twigCallables(
	path string,
	request *lsp.SignatureHelpRequest,
	call admin.TwigVueCall,
) ([]adminCallable, error) {
	calleeOffset := call.NameRange.End - 1
	calleeNode := request.Root.NodeAtOffset(calleeOffset)
	liveComponent, err := p.index.GetComponentForDocument(
		path, request.Root, string(request.DocumentContent), request.LineIndex,
	)
	if err != nil {
		return nil, err
	}

	resolvedSlot, err := p.index.ResolveTwigScopedSlotMemberForOwner(
		request.Root, calleeNode, request.DocumentContent, calleeOffset, path,
		liveComponent,
	)
	if err != nil {
		return nil, err
	}
	if resolvedSlot != nil {
		if !resolvedSlot.MemberFound {
			return nil, nil
		}
		if callables := scopedSlotCallables(
			resolvedSlot.ResolvedTwigScopedSlot,
			resolvedSlot.Access.Member,
		); len(callables) > 0 {
			return callables, nil
		}
		return []adminCallable{{
			Name: resolvedSlot.Member.Name, Type: resolvedSlot.Member.Type,
			Detail: "slot `" + resolvedSlot.QualifiedName() + "`",
		}}, nil
	}
	resolvedLexical, err := p.index.ResolveTwigVueMemberForComponent(
		request.Root, request.DocumentContent, calleeOffset, path, liveComponent,
	)
	if err != nil {
		return nil, err
	}
	if resolvedLexical != nil {
		if !resolvedLexical.MemberFound {
			return nil, nil
		}
		return []adminCallable{{
			Name: resolvedLexical.Member.Name, Type: resolvedLexical.Member.Type,
			Detail: "typed Vue binding `" + resolvedLexical.Binding.Name + "`",
		}}, nil
	}
	resolvedInstance, err := p.index.ResolveTwigVueInstanceMemberForComponent(
		request.Root, request.DocumentContent, calleeOffset, path, liveComponent,
	)
	if err != nil {
		return nil, err
	}
	if resolvedInstance != nil {
		if !resolvedInstance.MemberFound {
			return nil, nil
		}
		return []adminCallable{{
			Name: resolvedInstance.Member.Name, Type: resolvedInstance.Member.Type,
			Detail: "Administration component `" +
				resolvedInstance.Component.Name + "`",
		}}, nil
	}

	name, rootRange, found := admin.TwigVueExpressionRootIdentifierAtOffset(
		request.Root, request.DocumentContent, calleeOffset,
	)
	if !found {
		return nil, nil
	}
	if binding, resolveErr := p.index.ResolveTwigVueBindingForComponent(
		request.Root, request.DocumentContent, rootRange.Start,
		path, liveComponent,
	); resolveErr != nil {
		return nil, resolveErr
	} else if binding != nil {
		return []adminCallable{{
			Name: name, Type: binding.Type, Detail: "Vue template binding",
		}}, nil
	}
	if binding, resolveErr := p.index.ResolveTwigScopedSlotBindingForOwner(
		request.Root, calleeNode, request.DocumentContent, rootRange.Start,
		path, liveComponent,
	); resolveErr != nil {
		return nil, resolveErr
	} else if binding != nil {
		if callables := scopedSlotCallables(
			binding.ResolvedTwigScopedSlot,
			binding.Binding.MemberName,
		); len(callables) > 0 {
			return callables, nil
		}
		return []adminCallable{{
			Name: name, Type: binding.Member.Type,
			Detail: "slot `" + binding.QualifiedName() + "`",
		}}, nil
	}
	component, err := p.index.GetComponentByTemplatePath(path)
	if err != nil {
		return nil, err
	}
	if component != nil {
		if member, exists := component.TemplateMember(name); exists {
			return []adminCallable{{
				Name: name, Type: member.Type,
				Detail: "Administration component `" + component.Name + "`",
			}}, nil
		}
	}
	if member, exists := admin.VueBuiltinMember(name); exists {
		return []adminCallable{{
			Name: name, Type: member.Type, Detail: "Vue instance builtin",
		}}, nil
	}
	return nil, nil
}

func scopedSlotCallables(
	resolved admin.ResolvedTwigScopedSlot,
	memberName string,
) []adminCallable {
	if memberName == "" {
		return nil
	}
	var result []adminCallable
	for _, contract := range resolved.Contracts {
		member, found := contract.Slot.Member(memberName)
		if !found || member.Type == "" {
			continue
		}
		result = append(result, adminCallable{
			Name: memberName, Type: member.Type,
			Detail: "slot `" + contract.Component.Name + "." +
				resolved.Scope.SlotName + "`",
		})
	}
	return result
}

func adminSignatureHelp(
	callables []adminCallable,
	active int,
) *protocol.SignatureHelp {
	if len(callables) == 0 {
		return nil
	}
	sort.SliceStable(callables, func(left, right int) bool {
		if callables[left].Name != callables[right].Name {
			return callables[left].Name < callables[right].Name
		}
		return callables[left].Type < callables[right].Type
	})
	result := &protocol.SignatureHelp{}
	seen := make(map[string]bool)
	for _, callable := range callables {
		parameters, returnType, found := admin.VueCallableSignature(callable.Type)
		if !found {
			continue
		}
		label := callable.Name + "(" + strings.Join(parameters, ", ") + ")"
		if returnType != "" {
			label += ": " + returnType
		}
		if seen[label] {
			continue
		}
		seen[label] = true
		activeParameter := active
		if len(parameters) == 0 {
			activeParameter = 0
		} else if activeParameter >= len(parameters) {
			activeParameter = len(parameters) - 1
		}
		information := protocol.SignatureInformation{
			Label: label, ActiveParameter: activeParameter,
		}
		if callable.Detail != "" {
			information.Documentation = &protocol.MarkupContent{
				Kind: protocol.Markdown, Value: callable.Detail,
			}
		}
		for _, parameter := range parameters {
			information.Parameters = append(
				information.Parameters,
				protocol.ParameterInformation{Label: parameter},
			)
		}
		result.Signatures = append(result.Signatures, information)
	}
	if len(result.Signatures) == 0 {
		return nil
	}
	result.ActiveParameter = result.Signatures[0].ActiveParameter
	return result
}

func adminJavaScriptCallCallee(call *jssyntax.Node) *jssyntax.Node {
	if call == nil || call.Kind() != jssyntax.JsCallExpression {
		return nil
	}
	cursor := call.ChildNodeCursor()
	if cursor.Next() {
		return cursor.Node()
	}
	return nil
}

func directAdminChild(node *cst.Node, kind cst.Kind) *cst.Node {
	if node == nil {
		return nil
	}
	for child := range node.ChildNodes() {
		if child.Kind() == kind {
			return child
		}
	}
	return nil
}

func adminActiveArgument(
	arguments *cst.Node,
	commaKind cst.Kind,
	offset uint32,
) int {
	if arguments == nil {
		return 0
	}
	active := 0
	for token := range arguments.ChildTokens() {
		if token.Range().Start >= offset {
			break
		}
		if token.Kind() == commaKind {
			active++
		}
	}
	return active
}

var _ lsp.SignatureHelpProvider = (*AdminSignatureProvider)(nil)
