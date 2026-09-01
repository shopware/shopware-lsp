package callhierarchy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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

const (
	adminCallComponentMember = "component-member"
	adminCallStoreAction     = "store-action"
	adminCallTemplate        = "template"
	adminCallFile            = "file"
)

type adminCallHierarchyData struct {
	Domain string                `json:"domain"`
	Kind   admin.AdminSymbolKind `json:"kind,omitempty"`
	Owner  string                `json:"owner,omitempty"`
	Name   string                `json:"name,omitempty"`
	Path   string                `json:"path,omitempty"`
}

func (data adminCallHierarchyData) target() admin.AdminSymbolTarget {
	return admin.AdminSymbolTarget{
		Kind: data.Kind, Owner: data.Owner, Name: data.Name,
	}
}

type resolvedAdminCall struct {
	target     adminCallHierarchyData
	targetItem protocol.CallHierarchyItem
	caller     adminCallHierarchyData
	callerItem protocol.CallHierarchyItem
	rangeValue protocol.Range
}

type resolvedAdminCallable struct {
	data  adminCallHierarchyData
	item  protocol.CallHierarchyItem
	found bool
}

// AdminCallHierarchyProvider exposes the native Administration call graph for
// source-owned component methods and Pinia actions. The persisted usage graph
// narrows incoming-call candidate files; their live/disk CSTs remain the
// authority for whether an occurrence is actually invoked and who contains it.
type AdminCallHierarchyProvider struct {
	index *admin.AdminComponentIndexer
}

func NewAdminCallHierarchyProvider(
	index *admin.AdminComponentIndexer,
) *AdminCallHierarchyProvider {
	return &AdminCallHierarchyProvider{index: index}
}

func (p *AdminCallHierarchyProvider) PrepareCallHierarchy(
	ctx context.Context,
	request *lsp.CallHierarchyPrepareRequest,
) ([]protocol.CallHierarchyItem, error) {
	if ctx.Err() != nil || p == nil || p.index == nil || request == nil ||
		request.CallHierarchyPrepareParams == nil || request.Document == nil ||
		request.Root == nil || request.Node == nil || request.LineIndex == nil {
		return nil, nil
	}
	path, err := uriutil.Path(request.TextDocument.URI)
	if err != nil {
		return nil, err
	}
	var targets []admin.AdminSymbolTarget
	switch lsp.EffectiveSyntaxLanguage(
		request.Document.SyntaxLanguage, request.Node,
	) {
	case language.JavaScript:
		target, found, resolveErr := p.index.JavaScriptSymbolAt(
			path, request.Node,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if found {
			targets = append(targets, target)
		}
		if len(targets) == 0 {
			actions, actionErr := p.storeActionsAtJavaScriptNode(
				path, request.Node, request.LineIndex,
			)
			if actionErr != nil {
				return nil, actionErr
			}
			targets = append(targets, actions...)
		}
	case language.Twig:
		offset := request.LineIndex.OffsetUTF16(
			uint32(request.Position.Line),
			uint32(request.Position.Character),
		)
		target, _, found, resolveErr := p.index.TwigComponentMemberAt(
			path, request.Root, request.DocumentContent, offset,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if found {
			targets = append(targets, target)
		}
	default:
		return nil, nil
	}
	var result []protocol.CallHierarchyItem
	seen := make(map[admin.AdminSymbolTarget]bool)
	for _, target := range targets {
		if seen[target] {
			continue
		}
		seen[target] = true
		data, item, found, itemErr := p.callableItem(target)
		if itemErr != nil {
			return nil, itemErr
		}
		if !found {
			continue
		}
		item.Data = data
		result = append(result, item)
	}
	return result, nil
}

func (p *AdminCallHierarchyProvider) IncomingCalls(
	ctx context.Context,
	request *lsp.CallHierarchyCallsRequest,
) ([]protocol.CallHierarchyIncomingCall, error) {
	if ctx.Err() != nil || p == nil || p.index == nil || request == nil {
		return nil, nil
	}
	data, found := decodeAdminCallHierarchyData(request.Item.Data)
	if !found || data.Domain != adminCallComponentMember &&
		data.Domain != adminCallStoreAction {
		return nil, nil
	}
	_, _, callable, err := p.callableItem(data.target())
	if err != nil || !callable {
		return nil, err
	}
	documents, err := p.incomingCandidateDocuments(
		ctx, data.target(), request.Documents,
	)
	if err != nil {
		return nil, err
	}
	type group struct {
		item   protocol.CallHierarchyItem
		ranges []protocol.Range
	}
	groups := make(map[string]*group)
	callables := make(map[admin.AdminSymbolTarget]resolvedAdminCallable)
	for _, document := range documents {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		calls, callErr := p.resolvedCalls(document, callables)
		if callErr != nil {
			return nil, callErr
		}
		for _, call := range calls {
			if !sameAdminCallTarget(call.target, data) {
				continue
			}
			key := adminCallHierarchyItemKey(call.callerItem)
			current := groups[key]
			if current == nil {
				current = &group{item: call.callerItem}
				groups[key] = current
			}
			current.ranges = append(current.ranges, call.rangeValue)
		}
	}
	result := make([]protocol.CallHierarchyIncomingCall, 0, len(groups))
	for _, current := range groups {
		result = append(result, protocol.CallHierarchyIncomingCall{
			From:       current.item,
			FromRanges: uniqueAdminCallRanges(current.ranges),
		})
	}
	sort.SliceStable(result, func(left, right int) bool {
		return adminCallHierarchyItemKey(result[left].From) <
			adminCallHierarchyItemKey(result[right].From)
	})
	return result, nil
}

func (p *AdminCallHierarchyProvider) OutgoingCalls(
	ctx context.Context,
	request *lsp.CallHierarchyCallsRequest,
) ([]protocol.CallHierarchyOutgoingCall, error) {
	if ctx.Err() != nil || p == nil || p.index == nil || request == nil {
		return nil, nil
	}
	data, found := decodeAdminCallHierarchyData(request.Item.Data)
	if !found || data.Path == "" {
		return nil, nil
	}
	document, found, err := adminCallHierarchyDocument(
		data.Path, request.Documents,
	)
	if err != nil || !found {
		return nil, err
	}
	calls, err := p.resolvedCalls(
		document, make(map[admin.AdminSymbolTarget]resolvedAdminCallable),
	)
	if err != nil {
		return nil, err
	}
	type group struct {
		item   protocol.CallHierarchyItem
		ranges []protocol.Range
	}
	groups := make(map[string]*group)
	for _, call := range calls {
		if !sameAdminCallCaller(call.caller, data) {
			continue
		}
		key := adminCallHierarchyItemKey(call.targetItem)
		current := groups[key]
		if current == nil {
			current = &group{item: call.targetItem}
			groups[key] = current
		}
		current.ranges = append(current.ranges, call.rangeValue)
	}
	result := make([]protocol.CallHierarchyOutgoingCall, 0, len(groups))
	for _, current := range groups {
		result = append(result, protocol.CallHierarchyOutgoingCall{
			To:         current.item,
			FromRanges: uniqueAdminCallRanges(current.ranges),
		})
	}
	sort.SliceStable(result, func(left, right int) bool {
		return adminCallHierarchyItemKey(result[left].To) <
			adminCallHierarchyItemKey(result[right].To)
	})
	return result, nil
}

func (p *AdminCallHierarchyProvider) resolvedCalls(
	document *lsp.TextDocument,
	callables map[admin.AdminSymbolTarget]resolvedAdminCallable,
) ([]resolvedAdminCall, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil || document.LineIndex == nil {
		return nil, nil
	}
	if document.SyntaxLanguage == language.Vue {
		javascriptDocument := *document
		javascriptDocument.SyntaxLanguage = language.JavaScript
		javascriptCalls, err := p.resolvedCalls(
			&javascriptDocument, callables,
		)
		if err != nil {
			return nil, err
		}
		twigDocument := *document
		twigDocument.SyntaxLanguage = language.Twig
		twigCalls, err := p.resolvedCalls(&twigDocument, callables)
		if err != nil {
			return nil, err
		}
		return append(javascriptCalls, twigCalls...), nil
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return nil, err
	}
	var result []resolvedAdminCall
	switch document.SyntaxLanguage {
	case language.JavaScript:
		for call := range jsquery.IterateCalls(document.SyntaxTree.Root) {
			callee := jsquery.CallCallee(call)
			if callee == nil {
				continue
			}
			target, found, targetErr := p.index.JavaScriptSymbolAt(path, callee)
			if targetErr != nil {
				return nil, targetErr
			}
			if !found {
				continue
			}
			targetData, targetItem, callable, itemErr := p.cachedCallableItem(
				target, callables,
			)
			if itemErr != nil {
				return nil, itemErr
			}
			if !callable {
				continue
			}
			nameNode := jsquery.MemberNameNode(callee)
			if nameNode == nil {
				continue
			}
			callerData, callerItem, callerErr := p.javaScriptCaller(
				document, path, call,
			)
			if callerErr != nil {
				return nil, callerErr
			}
			result = append(result, resolvedAdminCall{
				target: targetData, targetItem: targetItem,
				caller: callerData, callerItem: callerItem,
				rangeValue: adminCallProtocolRange(
					document.LineIndex, nameNode.RangeTrimmedTrivia(),
				),
			})
		}
	case language.Twig:
		callerData, callerItem, callerErr := p.templateCaller(document, path)
		if callerErr != nil {
			return nil, callerErr
		}
		for _, call := range admin.TwigVueCalls(
			document.SyntaxTree.Root, document.Text,
		) {
			if call.Filter {
				continue
			}
			target, _, found, targetErr := p.index.TwigComponentMemberAt(
				path, document.SyntaxTree.Root, document.Text,
				call.NameRange.Start,
			)
			if targetErr != nil {
				return nil, targetErr
			}
			if !found {
				continue
			}
			targetData, targetItem, callable, itemErr := p.cachedCallableItem(
				target, callables,
			)
			if itemErr != nil {
				return nil, itemErr
			}
			if !callable {
				continue
			}
			result = append(result, resolvedAdminCall{
				target: targetData, targetItem: targetItem,
				caller: callerData, callerItem: callerItem,
				rangeValue: adminCallProtocolRange(
					document.LineIndex, call.NameRange,
				),
			})
		}
	}
	return result, nil
}

func (p *AdminCallHierarchyProvider) cachedCallableItem(
	target admin.AdminSymbolTarget,
	cache map[admin.AdminSymbolTarget]resolvedAdminCallable,
) (adminCallHierarchyData, protocol.CallHierarchyItem, bool, error) {
	if cached, found := cache[target]; found {
		return cached.data, cached.item, cached.found, nil
	}
	data, item, found, err := p.callableItem(target)
	if err == nil && cache != nil {
		cache[target] = resolvedAdminCallable{
			data: data, item: item, found: found,
		}
	}
	return data, item, found, err
}

func (p *AdminCallHierarchyProvider) javaScriptCaller(
	document *lsp.TextDocument,
	path string,
	call *jssyntax.Node,
) (adminCallHierarchyData, protocol.CallHierarchyItem, error) {
	for current := call.Parent(); current != nil; current = current.Parent() {
		if current.Kind() != jssyntax.JsMethod &&
			current.Kind() != jssyntax.JsProperty {
			continue
		}
		nameNode := jsquery.PropertyNameNode(current)
		name := jsquery.PropertyName(current)
		if nameNode == nil || name == "" {
			continue
		}
		line, character := document.LineIndex.PositionUTF16(
			nameNode.RangeTrimmedTrivia().Start,
		)
		member, found, err := p.index.GetComponentMemberAtDefinitionPosition(
			path, int(line), int(character),
		)
		if err != nil {
			return adminCallHierarchyData{}, protocol.CallHierarchyItem{}, err
		}
		if found && (member.Kind == admin.ComponentMemberMethod ||
			member.Kind == admin.ComponentMemberComputed) {
			data := componentMemberCallData(member)
			item, itemFound, itemErr := p.itemForData(data, document)
			if itemErr != nil {
				return adminCallHierarchyData{}, protocol.CallHierarchyItem{}, itemErr
			}
			if itemFound {
				return data, item, nil
			}
		}
		actions, err := p.index.StoreActionTargetsAtDefinitionPosition(
			path, name, int(line),
		)
		if err != nil {
			return adminCallHierarchyData{}, protocol.CallHierarchyItem{}, err
		}
		if len(actions) == 1 {
			data, item, callable, itemErr := p.callableItem(actions[0])
			if itemErr != nil {
				return adminCallHierarchyData{}, protocol.CallHierarchyItem{}, itemErr
			}
			if callable {
				liveItem, liveFound, liveErr := p.itemForData(data, document)
				if liveErr != nil {
					return adminCallHierarchyData{}, protocol.CallHierarchyItem{}, liveErr
				}
				if liveFound {
					return data, liveItem, nil
				}
				return data, item, nil
			}
		}
	}
	data := adminCallHierarchyData{
		Domain: adminCallFile, Path: path,
	}
	item, _, err := p.itemForData(data, document)
	return data, item, err
}

func (p *AdminCallHierarchyProvider) templateCaller(
	document *lsp.TextDocument,
	path string,
) (adminCallHierarchyData, protocol.CallHierarchyItem, error) {
	data := adminCallHierarchyData{
		Domain: adminCallTemplate, Path: path,
	}
	item, _, err := p.itemForData(data, document)
	return data, item, err
}

func (p *AdminCallHierarchyProvider) callableItem(
	target admin.AdminSymbolTarget,
) (adminCallHierarchyData, protocol.CallHierarchyItem, bool, error) {
	var data adminCallHierarchyData
	switch target.Kind {
	case admin.AdminSymbolComponentMember:
		member, found, err := p.index.ResolveComponentMemberTarget(target)
		if err != nil || !found || member.Kind != admin.ComponentMemberMethod {
			return data, protocol.CallHierarchyItem{}, false, err
		}
		data = componentMemberCallData(member)
	case admin.AdminSymbolStoreMember:
		stores, err := p.index.GetStore(target.Owner)
		if err != nil {
			return data, protocol.CallHierarchyItem{}, false, err
		}
		var member admin.AdminStoreMember
		found := false
		for _, store := range stores {
			candidate, exists := store.Member(target.Name)
			if exists && candidate.Kind == admin.AdminStoreAction {
				member, found = candidate, true
				break
			}
		}
		if !found {
			return data, protocol.CallHierarchyItem{}, false, nil
		}
		data = adminCallHierarchyData{
			Domain: adminCallStoreAction,
			Kind:   target.Kind,
			Owner:  target.Owner,
			Name:   target.Name,
			Path:   member.FilePath,
		}
	default:
		return data, protocol.CallHierarchyItem{}, false, nil
	}
	item, found, err := p.itemForData(data, nil)
	if found {
		item.Data = data
	}
	return data, item, found, err
}

func componentMemberCallData(
	member admin.VueComponentMember,
) adminCallHierarchyData {
	return adminCallHierarchyData{
		Domain: adminCallComponentMember,
		Kind:   admin.AdminSymbolComponentMember,
		Owner:  member.SourceIdentity(),
		Name:   member.Name,
		Path:   member.FilePath,
	}
}

func (p *AdminCallHierarchyProvider) itemForData(
	data adminCallHierarchyData,
	document *lsp.TextDocument,
) (protocol.CallHierarchyItem, bool, error) {
	switch data.Domain {
	case adminCallComponentMember:
		member, found, err := p.index.ResolveComponentMemberTarget(data.target())
		if err != nil || !found {
			return protocol.CallHierarchyItem{}, false, err
		}
		kind := protocol.SymbolMethod
		if member.Kind == admin.ComponentMemberComputed {
			kind = protocol.SymbolProperty
		}
		selection := adminSourceCallRange(member.NameRange)
		components, err := p.index.GetComponentsExposingMember(data.target())
		if err != nil {
			return protocol.CallHierarchyItem{}, false, err
		}
		var names []string
		for _, component := range components {
			names = append(names, component.Name)
		}
		sort.Strings(names)
		detail := "Administration component " + string(member.Kind)
		if len(names) > 0 {
			detail += " · " + strings.Join(names, ", ")
		}
		return protocol.CallHierarchyItem{
			Name: member.Name, Kind: kind, Detail: detail,
			URI:   uriutil.FileURI(member.FilePath),
			Range: selection, SelectionRange: selection, Data: data,
		}, true, nil
	case adminCallStoreAction:
		stores, err := p.index.GetStore(data.Owner)
		if err != nil {
			return protocol.CallHierarchyItem{}, false, err
		}
		for _, store := range stores {
			member, found := store.Member(data.Name)
			if !found || member.Kind != admin.AdminStoreAction {
				continue
			}
			selection := protocol.Range{
				Start: protocol.Position{Line: max(member.Line-1, 0)},
				End:   protocol.Position{Line: max(member.Line-1, 0)},
			}
			if document == nil || normalizeAdminCallPath(member.FilePath) !=
				normalizeAdminCallDocumentPath(document) {
				var documentFound bool
				document, documentFound, err = adminCallHierarchyDocument(
					member.FilePath, nil,
				)
				if err != nil {
					return protocol.CallHierarchyItem{}, false, err
				}
				if !documentFound {
					document = nil
				}
			}
			if exact, exactFound := adminCallNameRange(
				document, member.Name, max(member.Line-1, 0),
			); exactFound {
				selection = exact
			}
			return protocol.CallHierarchyItem{
				Name: member.Name, Kind: protocol.SymbolMethod,
				Detail: "Administration store action · " + store.Name,
				URI:    uriutil.FileURI(member.FilePath),
				Range:  selection, SelectionRange: selection, Data: data,
			}, true, nil
		}
	case adminCallTemplate, adminCallFile:
		if document == nil {
			var found bool
			var err error
			document, found, err = adminCallHierarchyDocument(data.Path, nil)
			if err != nil || !found {
				return protocol.CallHierarchyItem{}, false, err
			}
		}
		endLine, endCharacter := document.LineIndex.PositionUTF16(
			uint32(len(document.Text)),
		)
		name := filepath.Base(data.Path)
		detail := "Administration JavaScript file"
		if data.Domain == adminCallTemplate {
			detail = "Administration component template"
			component, err := p.index.GetComponentByTemplatePath(data.Path)
			if err != nil {
				return protocol.CallHierarchyItem{}, false, err
			}
			if component != nil {
				name = component.Name + " template"
			}
		}
		fullRange := protocol.Range{
			End: protocol.Position{
				Line: int(endLine), Character: int(endCharacter),
			},
		}
		return protocol.CallHierarchyItem{
			Name: name, Kind: protocol.SymbolFile, Detail: detail,
			URI: document.URI, Range: fullRange,
			SelectionRange: protocol.Range{}, Data: data,
		}, true, nil
	}
	return protocol.CallHierarchyItem{}, false, nil
}

func (p *AdminCallHierarchyProvider) storeActionsAtJavaScriptNode(
	path string,
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) ([]admin.AdminSymbolTarget, error) {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() != jssyntax.JsMethod &&
			current.Kind() != jssyntax.JsProperty {
			continue
		}
		nameNode := jsquery.PropertyNameNode(current)
		name := jsquery.PropertyName(current)
		if nameNode == nil || name == "" {
			continue
		}
		line, _ := lineIndex.PositionUTF16(
			nameNode.RangeTrimmedTrivia().Start,
		)
		return p.index.StoreActionTargetsAtDefinitionPosition(
			path, name, int(line),
		)
	}
	return nil, nil
}

func (p *AdminCallHierarchyProvider) incomingCandidateDocuments(
	ctx context.Context,
	target admin.AdminSymbolTarget,
	open []*lsp.TextDocument,
) ([]*lsp.TextDocument, error) {
	sets, err := p.index.GetSymbolUsages(target)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]string)
	for _, set := range sets {
		paths[normalizeAdminCallPath(set.FilePath)] = set.FilePath
	}
	openByPath := make(map[string]*lsp.TextDocument)
	for _, document := range open {
		if document == nil {
			continue
		}
		path, pathErr := uriutil.Path(document.URI)
		if pathErr != nil {
			continue
		}
		normalized := normalizeAdminCallPath(path)
		openByPath[normalized] = document
		if document.SyntaxLanguage == language.JavaScript ||
			document.SyntaxLanguage == language.Twig ||
			document.SyntaxLanguage == language.Vue {
			paths[normalized] = path
		}
	}
	ordered := make([]string, 0, len(paths))
	for normalized := range paths {
		ordered = append(ordered, normalized)
	}
	sort.Strings(ordered)
	result := make([]*lsp.TextDocument, 0, len(ordered))
	for _, normalized := range ordered {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if document := openByPath[normalized]; document != nil {
			result = append(result, document)
			continue
		}
		document, found, loadErr := adminCallHierarchyDocument(
			paths[normalized], nil,
		)
		if loadErr != nil {
			return nil, loadErr
		}
		if found {
			result = append(result, document)
		}
	}
	return result, nil
}

func adminCallHierarchyDocument(
	path string,
	open []*lsp.TextDocument,
) (*lsp.TextDocument, bool, error) {
	normalized := normalizeAdminCallPath(path)
	for _, document := range open {
		if document == nil {
			continue
		}
		candidate, err := uriutil.Path(document.URI)
		if err == nil && normalizeAdminCallPath(candidate) == normalized {
			return document, true, nil
		}
	}
	source, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return lsp.NewTextDocument(
		uriutil.FileURI(path), string(source), 0,
	), true, nil
}

func adminCallProtocolRange(
	lineIndex *cst.LineIndex,
	rangeValue cst.TextRange,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rangeValue.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rangeValue.End)
	return protocol.Range{
		Start: protocol.Position{
			Line: int(startLine), Character: int(startCharacter),
		},
		End: protocol.Position{
			Line: int(endLine), Character: int(endCharacter),
		},
	}
}

func adminSourceCallRange(value admin.AdminSourceRange) protocol.Range {
	return protocol.Range{
		Start: protocol.Position{
			Line: value.StartLine, Character: value.StartCharacter,
		},
		End: protocol.Position{
			Line: value.EndLine, Character: value.EndCharacter,
		},
	}
}

func decodeAdminCallHierarchyData(value any) (adminCallHierarchyData, bool) {
	if data, ok := value.(adminCallHierarchyData); ok {
		return data, data.Domain != ""
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return adminCallHierarchyData{}, false
	}
	var data adminCallHierarchyData
	if json.Unmarshal(payload, &data) != nil || data.Domain == "" {
		return adminCallHierarchyData{}, false
	}
	return data, true
}

func sameAdminCallTarget(
	left,
	right adminCallHierarchyData,
) bool {
	return left.Kind == right.Kind && left.Owner == right.Owner &&
		left.Name == right.Name
}

func sameAdminCallCaller(
	left,
	right adminCallHierarchyData,
) bool {
	if left.Domain != right.Domain ||
		normalizeAdminCallPath(left.Path) != normalizeAdminCallPath(right.Path) {
		return false
	}
	if left.Domain == adminCallComponentMember ||
		left.Domain == adminCallStoreAction {
		return sameAdminCallTarget(left, right)
	}
	return true
}

func normalizeAdminCallPath(value string) string {
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func normalizeAdminCallDocumentPath(document *lsp.TextDocument) string {
	if document == nil {
		return ""
	}
	path, err := uriutil.Path(document.URI)
	if err != nil {
		return ""
	}
	return normalizeAdminCallPath(path)
}

func adminCallNameRange(
	document *lsp.TextDocument,
	name string,
	line int,
) (protocol.Range, bool) {
	if document == nil || document.LineIndex == nil || name == "" || line < 0 {
		return protocol.Range{}, false
	}
	start := document.LineIndex.OffsetUTF16(uint32(line), 0)
	end := document.LineIndex.LineEnd(uint32(line))
	if start > end || int(end) > len(document.Text) {
		return protocol.Range{}, false
	}
	lineSource := document.Text[start:end]
	searchFrom := 0
	for {
		index := bytes.Index(lineSource[searchFrom:], []byte(name))
		if index < 0 {
			return protocol.Range{}, false
		}
		index += searchFrom
		beforeOK := index == 0 || !adminCallIdentifierByte(lineSource[index-1])
		after := index + len(name)
		afterOK := after == len(lineSource) ||
			!adminCallIdentifierByte(lineSource[after])
		if beforeOK && afterOK {
			return adminCallProtocolRange(document.LineIndex, cst.TextRange{
				Start: start + uint32(index), End: start + uint32(after),
			}), true
		}
		searchFrom = after
		if searchFrom >= len(lineSource) {
			return protocol.Range{}, false
		}
	}
}

func adminCallIdentifierByte(value byte) bool {
	return value == '_' || value == '$' || value >= 'a' && value <= 'z' ||
		value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func adminCallHierarchyItemKey(item protocol.CallHierarchyItem) string {
	return item.URI + "\x00" + item.Name + "\x00" +
		strconv.Itoa(item.SelectionRange.Start.Line) + ":" +
		strconv.Itoa(item.SelectionRange.Start.Character)
}

func uniqueAdminCallRanges(values []protocol.Range) []protocol.Range {
	result := make([]protocol.Range, 0, len(values))
	seen := make(map[protocol.Range]bool)
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Start.Line != result[right].Start.Line {
			return result[left].Start.Line < result[right].Start.Line
		}
		return result[left].Start.Character < result[right].Start.Character
	})
	return result
}

var _ lsp.CallHierarchyProvider = (*AdminCallHierarchyProvider)(nil)
