package admin

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
)

// ApplicationContainer describes one of Shopware's Bottle.js sub-containers.
// InterfaceName is the ambient TypeScript contract declared by the
// Administration. Keeping this map small and stable lets projects extend the
// contracts through ordinary TypeScript declaration merging.
type ApplicationContainer struct {
	Name          string
	InterfaceName string
	Description   string
}

var applicationContainers = []ApplicationContainer{
	{Name: "factory", InterfaceName: "FactoryContainer", Description: "Administration factories"},
	{Name: "service", InterfaceName: "ServiceContainer", Description: "Administration services"},
	{Name: "init", InterfaceName: "InitContainer", Description: "Administration initializers"},
	{Name: "init-pre", InterfaceName: "InitPreContainer", Description: "pre-initialization services"},
	{Name: "init-post", InterfaceName: "InitPostContainer", Description: "post-initialization services"},
}

// ApplicationContainers returns the statically supported getContainer names.
func ApplicationContainers() []ApplicationContainer {
	return append([]ApplicationContainer(nil), applicationContainers...)
}

func ApplicationContainerNamed(name string) (ApplicationContainer, bool) {
	for _, container := range applicationContainers {
		if container.Name == name {
			return container, true
		}
	}
	return ApplicationContainer{}, false
}

// IsApplicationContainerNameReference reports whether the cursor is in the
// static name argument of Application.getContainer(...).
func IsApplicationContainerNameReference(node *jssyntax.Node) bool {
	if jsquery.StringAt(node) == nil || jsquery.StringArgumentIndex(node) != 0 {
		return false
	}
	return isApplicationContainerCallName(jsquery.CallName(node))
}

// JavaScriptApplicationContainerMember reports a direct member accessed from
// Application.getContainer(...). A lexically visible const alias is also
// accepted, while mutable, reassigned, later, or shadowed bindings remain
// unresolved through visibleJavaScriptConstInitializer.
func JavaScriptApplicationContainerMember(
	node *jssyntax.Node,
) (containerName, memberName string, matched bool) {
	return javaScriptApplicationContainerMember(node, nil)
}

func javaScriptApplicationContainerMember(
	node *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) (containerName, memberName string, matched bool) {
	member := jsquery.MemberExpressionAt(node)
	if member == nil {
		return "", "", false
	}
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return "", "", false
	}
	receiver := cursor.Node()
	if node != member && javaScriptNodeWithin(node, receiver) {
		return "", "", false
	}
	containerName, matched = applicationContainerReceiverName(receiver, analysis)
	if !matched {
		return "", "", false
	}
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == jssyntax.JsIdentifier {
			memberName = strings.TrimSpace(child.Text())
		}
	}
	return containerName, memberName, true
}

// JavaScriptApplicationContainerMemberNameNode returns the identifier owned by
// a recognized direct container-member access. It is used for exact diagnostic
// and refactoring ranges without exposing parser-specific traversal elsewhere.
func JavaScriptApplicationContainerMemberNameNode(
	node *jssyntax.Node,
) *jssyntax.Node {
	return javaScriptApplicationContainerMemberNameNode(node, nil)
}

func javaScriptApplicationContainerMemberNameNode(
	node *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) *jssyntax.Node {
	_, name, matched := javaScriptApplicationContainerMember(node, analysis)
	if !matched || name == "" {
		return nil
	}
	member := jsquery.MemberExpressionAt(node)
	if member == nil {
		return nil
	}
	var result *jssyntax.Node
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return nil
	}
	for cursor.Next() {
		child := cursor.Node()
		if child.Kind() == jssyntax.JsIdentifier &&
			strings.TrimSpace(child.Text()) == name {
			result = child
		}
	}
	return result
}

func javaScriptNodeWithin(node, ancestor *jssyntax.Node) bool {
	for current := node; current != nil; current = current.Parent() {
		if current == ancestor {
			return true
		}
	}
	return false
}

func applicationContainerReceiverName(
	receiver *jssyntax.Node,
	analysis *JavaScriptDocumentAnalysis,
) (string, bool) {
	if receiver == nil {
		return "", false
	}
	if receiver.Kind() == jssyntax.JsCallExpression {
		return applicationContainerCallValue(receiver)
	}
	if receiver.Kind() != jssyntax.JsIdentifier {
		return "", false
	}
	identifier := jsquery.IdentifierText(receiver)
	if identifier == "" {
		return "", false
	}
	root := receiver
	for root.Parent() != nil {
		root = root.Parent()
	}
	initializer, found := visibleJavaScriptConstInitializerIndexed(
		receiver, identifier, root, analysis,
	)
	if !found {
		return "", false
	}
	parsed := javascriptparser.Parse(initializer)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return "", false
	}
	for _, call := range jsquery.Calls(parsed.Tree.Root) {
		if name, matched := applicationContainerCallValue(call); matched {
			return name, true
		}
	}
	return "", false
}

func applicationContainerConstAliasNames(
	root *jssyntax.Node,
) map[string]bool {
	if root == nil {
		return nil
	}
	result := make(map[string]bool)
	for _, declaration := range jsquery.Nodes(
		root, jssyntax.JsVariableDeclaration,
	) {
		name, initializer, found := directComponentConstInitializer(
			declaration.Text(),
		)
		if !found {
			continue
		}
		if !strings.Contains(initializer, "getContainer") {
			continue
		}
		parsed := javascriptparser.Parse(initializer)
		if parsed.Tree == nil || parsed.Tree.Root == nil {
			continue
		}
		for _, call := range jsquery.Calls(parsed.Tree.Root) {
			if _, matched := applicationContainerCallValue(call); matched {
				result[name] = true
				break
			}
		}
	}
	return result
}

func potentialApplicationContainerMember(
	member *jssyntax.Node,
	aliases map[string]bool,
) bool {
	if member == nil || member.Kind() != jssyntax.JsMemberExpression {
		return false
	}
	cursor := member.ChildNodeCursor()
	if !cursor.Next() {
		return false
	}
	receiver := cursor.Node()
	if receiver.Kind() == jssyntax.JsCallExpression {
		return isApplicationContainerCallName(jsquery.CallName(receiver))
	}
	return receiver.Kind() == jssyntax.JsIdentifier &&
		aliases[jsquery.IdentifierText(receiver)]
}

func applicationContainerCallValue(call *jssyntax.Node) (string, bool) {
	if call == nil || !isApplicationContainerCallName(jsquery.CallName(call)) {
		return "", false
	}
	name := jsquery.StringValue(jsquery.StringArgument(call, 0))
	if _, found := ApplicationContainerNamed(name); !found {
		return "", false
	}
	return name, true
}

func isApplicationContainerCallName(name string) bool {
	switch name {
	case "Application.getContainer",
		"Shopware.Application.getContainer",
		"this.Application.getContainer",
		"this.getContainer":
		return true
	default:
		return false
	}
}

// ResolveApplicationContainer composes all ambient declarations for a
// Shopware container. TypeScript interfaces intentionally support declaration
// merging, so ServiceContainer extensions from modules and plugins are merged
// rather than treated as an ambiguous short type name.
func (idx *AdminComponentIndexer) ResolveApplicationContainer(
	containerName,
	contextPath string,
) (VueTypeShape, error) {
	container, found := ApplicationContainerNamed(containerName)
	if !found {
		return VueTypeShape{}, nil
	}
	result := VueTypeShape{Type: container.InterfaceName, Complete: true}
	liveFiles := idx.liveTypeFileOverlays(nil)
	files, err := idx.allAdminTypeFiles(liveFiles)
	if err != nil {
		return result, err
	}
	declarations := 0
	seenDeclarations := make(map[string]bool)
	for _, file := range files {
		path := filepath.Clean(file.FilePath)
		if path == "." {
			continue
		}
		for declarationIndex, declaration := range file.Declarations {
			if declaration.Name != container.InterfaceName ||
				!declaration.Interface {
				continue
			}
			key := path + "\x00" + strconv.Itoa(declarationIndex)
			if seenDeclarations[key] {
				continue
			}
			seenDeclarations[key] = true
			declarations++
			overlayDeclarations := []AdminTypeDeclaration{declaration}
			for otherIndex, other := range file.Declarations {
				if otherIndex == declarationIndex ||
					other.Name == container.InterfaceName {
					continue
				}
				overlayDeclarations = append(overlayDeclarations, other)
			}
			shape, resolveErr := idx.ResolveVueType(
				container.InterfaceName, file.FilePath,
				AdminTypeFile{
					FilePath: file.FilePath, Imports: file.Imports,
					Declarations: overlayDeclarations,
				},
			)
			if resolveErr != nil {
				return result, resolveErr
			}
			result.Members = mergeTwigVueMembers(result.Members, shape.Members)
			result.Complete = result.Complete && shape.Complete
		}
	}
	if declarations == 0 {
		result.Complete = false
		result.Members = applicationContainerBaseMembers(containerName)
	}

	// Runtime registrations supplement older JavaScript projects and plugin
	// services which have not augmented ServiceContainer. The typed declaration
	// remains authoritative when both sources describe the same service.
	if containerName == "service" {
		services, serviceErr := idx.GetAllServices()
		if serviceErr != nil {
			return result, serviceErr
		}
		for _, service := range services {
			result.Members = mergeTwigVueMembers(result.Members, []TwigVueMember{{
				Name: service.Name, Type: service.ImplementationName,
				DefinitionPath: service.FilePath, DefinitionLine: service.Line,
			}})
		}
		// Plugins can register additional services at runtime, so this remains an
		// open contract even when all indexed declarations are structurally closed.
		result.Complete = false
	}
	sort.Slice(result.Members, func(left, right int) bool {
		return result.Members[left].Name < result.Members[right].Name
	})
	return result, nil
}

func applicationContainerBaseMembers(containerName string) []TwigVueMember {
	return []TwigVueMember{
		{Name: "$decorator", Type: "(name: string, decorator: Function) => unknown"},
		{Name: "$list", Type: "() => (keyof " + containerName + ")[]"},
		{Name: "$register", Type: "(object: unknown) => unknown"},
	}
}
