package completion

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/languagelevel"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	phpRouteAttribute     = "Symfony\\Component\\Routing\\Attribute\\Route"
	phpIsGrantedAttribute = "Symfony\\Component\\Security\\Http\\Attribute\\" +
		"IsGranted"
	phpCacheAttribute        = "Symfony\\Component\\HttpKernel\\Attribute\\Cache"
	phpAsControllerAttribute = "Symfony\\Component\\HttpKernel\\Attribute\\" +
		"AsController"
	phpAsTwigFilterAttribute    = "Twig\\Attribute\\AsTwigFilter"
	phpAsTwigFunctionAttribute  = "Twig\\Attribute\\AsTwigFunction"
	phpAsTwigTestAttribute      = "Twig\\Attribute\\AsTwigTest"
	phpAsTwigComponentAttribute = "Symfony\\UX\\TwigComponent\\Attribute\\" +
		"AsTwigComponent"
	phpAsCommandAttribute = "Symfony\\Component\\Console\\Attribute\\AsCommand"

	phpAbstractControllerClass = "Symfony\\Bundle\\FrameworkBundle\\" +
		"Controller\\AbstractController"
	phpTwigAbstractExtensionClass = "Twig\\Extension\\AbstractExtension"
	phpTwigExtensionInterface     = "Twig\\Extension\\ExtensionInterface"
	phpConsoleCommandClass        = "Symfony\\Component\\Console\\Command\\Command"
	phpInputInterface             = "Symfony\\Component\\Console\\Input\\InputInterface"
	phpOutputInterface            = "Symfony\\Component\\Console\\Output\\OutputInterface"

	doctrineMappingNamespace = "Doctrine\\ORM\\Mapping"
	doctrineEntityAttribute  = doctrineMappingNamespace + "\\Entity"
)

type phpAttributeArgumentStyle uint8

const (
	phpAttributeNoArguments phpAttributeArgumentStyle = iota
	phpAttributeQuotedArgument
	phpAttributeArguments
)

type phpAttributeSpec struct {
	name          string
	fqn           string
	detail        string
	documentation string
	arguments     phpAttributeArgumentStyle
	doctrine      bool
}

type phpAttributeEditContext struct {
	replace      cst.TextRange
	wrapGroup    bool
	closeGroup   bool
	existingArgs bool
}

type PHPAttributeCompletionProvider struct {
	phpIndex *php.PHPIndex
}

func NewPHPAttributeCompletionProvider(
	phpIndex *php.PHPIndex,
) *PHPAttributeCompletionProvider {
	return &PHPAttributeCompletionProvider{phpIndex: phpIndex}
}

func (p *PHPAttributeCompletionProvider) GetCompletions(
	ctx context.Context,
	request *lsp.CompletionRequest,
) []protocol.CompletionItem {
	if ctx.Err() != nil || p == nil || p.phpIndex == nil ||
		request == nil || request.Root == nil ||
		request.LineIndex == nil || request.Document == nil ||
		!strings.EqualFold(
			filepath.Ext(request.TextDocument.URI),
			".php",
		) {
		return nil
	}
	if model := p.phpIndex.Project(); model != nil &&
		!languagelevel.Supports(model.PHPVersion, languagelevel.Attributes) {
		return nil
	}
	offset := request.LineIndex.OffsetUTF16(
		uint32(request.Position.Line),
		uint32(request.Position.Character),
	)
	editContext, found := phpAttributeEditAt(request, offset)
	if !found {
		return nil
	}
	class, target := componentAttributeTarget(
		request.Root,
		request.Node,
		offset,
	)
	if class == nil || class.Kind() != phpsyntax.PhpClassDeclaration {
		return nil
	}
	resolver := php.NewNameResolver(request.Root)
	classFQN := phpAttributeClassFQN(request.Root, class)
	snapshot := p.phpIndex.SemanticSnapshot()
	if path, err := uriutil.Path(request.Document.URI); err == nil {
		document := p.phpIndex.AnalyzeDocument(
			path,
			request.Document.Version,
			request.Root,
		)
		snapshot = snapshot.WithDocument(document)
	}
	specs := p.attributeSpecs(
		ctx,
		request,
		class,
		classFQN,
		target,
		resolver,
		snapshot,
	)
	if len(specs) == 0 {
		return nil
	}

	items := make([]protocol.CompletionItem, 0, len(specs))
	seen := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		if ctx.Err() != nil {
			return nil
		}
		if _, available := p.phpIndex.FindClass(spec.fqn); !available {
			continue
		}
		key := strings.ToLower(spec.fqn)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		qualifier, importEdit := phpAttributeQualifier(
			request,
			spec.fqn,
			spec.doctrine,
		)
		if qualifier == "" {
			continue
		}
		newText, snippet := phpAttributeInsertText(
			qualifier,
			spec.arguments,
			editContext,
		)
		item := protocol.CompletionItem{
			Label:      spec.name,
			FilterText: spec.name,
			Kind:       int(protocol.ClassCompletion),
			Detail:     spec.detail + " • " + spec.fqn,
			TextEdit: protocol.TextEdit{
				Range: phpCompletionRange(
					editContext.replace,
					request.LineIndex,
				),
				NewText: newText,
			},
		}
		if snippet {
			item.InsertTextFormat = int(protocol.SnippetTextFormat)
		}
		item.Documentation.Kind = string(protocol.Markdown)
		item.Documentation.Value = spec.documentation
		var additionalEdits []interface{}
		if importEdit != nil {
			additionalEdits = append(additionalEdits, *importEdit)
		}
		if phpDoctrineLifecycleAttribute(spec.fqn) &&
			!phpHasAttribute(
				class,
				resolver,
				doctrineMappingNamespace+"\\HasLifecycleCallbacks",
			) {
			companion := "\\Doctrine\\ORM\\Mapping\\HasLifecycleCallbacks"
			if alias := phpDoctrineMappingAlias(request.Root); alias != "" {
				companion = alias + "\\HasLifecycleCallbacks"
			}
			additionalEdits = append(additionalEdits, protocol.TextEdit{
				Range: phpCompletionRange(
					cst.TextRange{
						Start: class.RangeTrimmedTrivia().Start,
						End:   class.RangeTrimmedTrivia().Start,
					},
					request.LineIndex,
				),
				NewText: "#[" + companion + "]\n",
			})
		}
		item.AdditionalTextEdits = additionalEdits
		items = append(items, item)
	}
	sort.Slice(items, func(left, right int) bool {
		return items[left].Label < items[right].Label
	})
	return items
}

func (p *PHPAttributeCompletionProvider) GetTriggerCharacters() []string {
	return []string{"#"}
}

func (p *PHPAttributeCompletionProvider) attributeSpecs(
	ctx context.Context,
	request *lsp.CompletionRequest,
	class *phpsyntax.Node,
	classFQN string,
	target componentAttributeTargetKind,
	resolver *php.NameResolver,
	snapshot *semantic.Snapshot,
) []phpAttributeSpec {
	switch target {
	case componentAttributeClassTarget:
		var result []phpAttributeSpec
		if phpAttributeControllerClass(class, classFQN, resolver, snapshot) {
			result = append(result, controllerClassAttributeSpecs()...)
		}
		if p.twigComponentCandidate(ctx, classFQN) {
			result = append(result, phpAttributeSpec{
				name:          "AsTwigComponent",
				fqn:           phpAsTwigComponentAttribute,
				detail:        "Declare a Symfony UX Twig component",
				documentation: "Registers this class as a Twig component.",
			})
		}
		if phpAttributeCommandClass(class, classFQN, resolver, snapshot) {
			result = append(result, commandAttributeSpec())
		}
		if phpAttributeDoctrineEntity(class, classFQN, resolver) {
			result = append(result, doctrineClassAttributeSpecs()...)
		}
		return result
	case componentAttributeMethodTarget:
		method := phpAttributeMethodAtOrAfter(class, request.Node,
			request.LineIndex.OffsetUTF16(
				uint32(request.Position.Line),
				uint32(request.Position.Character),
			))
		if method == nil || !phpPublicInstanceMethod(method) {
			return nil
		}
		var result []phpAttributeSpec
		if phpAttributeControllerClass(class, classFQN, resolver, snapshot) {
			result = append(result, controllerMethodAttributeSpecs()...)
		}
		if phpAttributeTwigExtension(class, classFQN, resolver, snapshot) {
			result = append(result, twigExtensionAttributeSpecs()...)
		}
		if phpAttributeDoctrineEntity(class, classFQN, resolver) {
			result = append(result, doctrineMethodAttributeSpecs()...)
		}
		if phpAttributeCommandClass(class, classFQN, resolver, snapshot) ||
			phpAttributeConsoleParameters(method, resolver) {
			result = append(result, commandAttributeSpec())
		}
		return result
	case componentAttributePropertyTarget:
		if phpAttributeDoctrineEntity(class, classFQN, resolver) {
			return doctrinePropertyAttributeSpecs()
		}
	}
	return nil
}

func controllerMethodAttributeSpecs() []phpAttributeSpec {
	return []phpAttributeSpec{
		{
			name:          "Route",
			fqn:           phpRouteAttribute,
			detail:        "Declare a Symfony route",
			documentation: "Maps this public controller method to a route.",
			arguments:     phpAttributeQuotedArgument,
		},
		{
			name:   "IsGranted",
			fqn:    phpIsGrantedAttribute,
			detail: "Restrict controller access",
			documentation: "Requires the configured security attribute " +
				"before invoking this controller.",
			arguments: phpAttributeQuotedArgument,
		},
		{
			name:          "Cache",
			fqn:           phpCacheAttribute,
			detail:        "Configure HTTP response caching",
			documentation: "Configures cache headers for this controller response.",
			arguments:     phpAttributeArguments,
		},
	}
}

func controllerClassAttributeSpecs() []phpAttributeSpec {
	return []phpAttributeSpec{
		controllerMethodAttributeSpecs()[0],
		{
			name:          "AsController",
			fqn:           phpAsControllerAttribute,
			detail:        "Declare a service as a controller",
			documentation: "Marks this class as a Symfony controller service.",
		},
		controllerMethodAttributeSpecs()[1],
	}
}

func twigExtensionAttributeSpecs() []phpAttributeSpec {
	return []phpAttributeSpec{
		{
			name:          "AsTwigFilter",
			fqn:           phpAsTwigFilterAttribute,
			detail:        "Expose this method as a Twig filter",
			documentation: "Registers this public method as a Twig filter.",
			arguments:     phpAttributeQuotedArgument,
		},
		{
			name:          "AsTwigFunction",
			fqn:           phpAsTwigFunctionAttribute,
			detail:        "Expose this method as a Twig function",
			documentation: "Registers this public method as a Twig function.",
			arguments:     phpAttributeQuotedArgument,
		},
		{
			name:          "AsTwigTest",
			fqn:           phpAsTwigTestAttribute,
			detail:        "Expose this method as a Twig test",
			documentation: "Registers this public method as a Twig test.",
			arguments:     phpAttributeQuotedArgument,
		},
	}
}

func commandAttributeSpec() phpAttributeSpec {
	return phpAttributeSpec{
		name:          "AsCommand",
		fqn:           phpAsCommandAttribute,
		detail:        "Declare a Symfony console command",
		documentation: "Registers this class or invokable action as a console command.",
		arguments:     phpAttributeQuotedArgument,
	}
}

func doctrinePropertyAttributeSpecs() []phpAttributeSpec {
	return doctrineAttributeSpecs(
		"Column",
		"Id",
		"GeneratedValue",
		"OneToMany",
		"OneToOne",
		"ManyToOne",
		"ManyToMany",
		"JoinColumn",
	)
}

func doctrineClassAttributeSpecs() []phpAttributeSpec {
	return doctrineAttributeSpecs(
		"Entity",
		"Table",
		"UniqueConstraint",
		"Index",
		"Embeddable",
		"HasLifecycleCallbacks",
	)
}

func doctrineMethodAttributeSpecs() []phpAttributeSpec {
	return doctrineAttributeSpecs(
		"PostLoad",
		"PostPersist",
		"PostRemove",
		"PostUpdate",
		"PrePersist",
		"PreRemove",
		"PreUpdate",
	)
}

func doctrineAttributeSpecs(names ...string) []phpAttributeSpec {
	result := make([]phpAttributeSpec, 0, len(names))
	for _, name := range names {
		result = append(result, phpAttributeSpec{
			name:          name,
			fqn:           doctrineMappingNamespace + "\\" + name,
			detail:        "Doctrine ORM mapping attribute",
			documentation: "Adds Doctrine ORM `" + name + "` mapping metadata.",
			doctrine:      true,
		})
	}
	return result
}

func phpAttributeEditAt(
	request *lsp.CompletionRequest,
	offset uint32,
) (phpAttributeEditContext, bool) {
	replace, found := phpAttributeReplacementRange(request, offset)
	if found {
		source := request.Document.Source
		end := int(replace.End)
		if end > len(source) {
			end = len(source)
		}
		rest := source[end:]
		trimmed := strings.TrimLeft(rest, " \t")
		existingArgs := strings.HasPrefix(trimmed, "(")
		closeGroup := false
		if !existingArgs {
			lineEnd := strings.IndexAny(trimmed, "\r\n")
			line := trimmed
			if lineEnd >= 0 {
				line = trimmed[:lineEnd]
			}
			closeGroup = !strings.Contains(line, "]")
		}
		return phpAttributeEditContext{
			replace:      replace,
			closeGroup:   closeGroup,
			existingArgs: existingArgs,
		}, true
	}

	source := request.Document.Source
	if uint64(offset) > uint64(len(source)) {
		return phpAttributeEditContext{}, false
	}
	lineStart := strings.LastIndex(source[:offset], "\n") + 1
	line := source[lineStart:offset]
	if strings.TrimSpace(line) != "#" {
		return phpAttributeEditContext{}, false
	}
	hash := strings.LastIndex(line, "#")
	return phpAttributeEditContext{
		replace: cst.TextRange{
			Start: uint32(lineStart + hash),
			End:   offset,
		},
		wrapGroup: true,
	}, true
}

func phpAttributeInsertText(
	qualifier string,
	arguments phpAttributeArgumentStyle,
	editContext phpAttributeEditContext,
) (string, bool) {
	text := qualifier
	snippet := false
	if !editContext.existingArgs {
		switch arguments {
		case phpAttributeQuotedArgument:
			text += "('${1}')"
			snippet = true
		case phpAttributeArguments:
			text += "(${1})"
			snippet = true
		}
	}
	if editContext.wrapGroup {
		text = "#[" + text + "]"
	} else if editContext.closeGroup {
		text += "]"
	}
	if snippet {
		text += "$0"
	}
	return text, snippet
}

func phpAttributeQualifier(
	request *lsp.CompletionRequest,
	fqn string,
	doctrine bool,
) (string, *protocol.TextEdit) {
	if doctrine {
		short := fqn[strings.LastIndex(fqn, "\\")+1:]
		if alias := phpDoctrineMappingAlias(request.Root); alias != "" {
			return alias + "\\" + short, nil
		}
	}
	return componentAttributeImport(request, fqn)
}

func phpDoctrineMappingAlias(root *phpsyntax.Node) string {
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(
			declaration.Text(),
		) {
			if imported.Kind == phpresolver.ClassImport &&
				strings.EqualFold(
					strings.Trim(imported.Target, "\\"),
					doctrineMappingNamespace,
				) {
				return imported.Alias
			}
		}
	}
	return ""
}

func phpDoctrineLifecycleAttribute(fqn string) bool {
	name := fqn[strings.LastIndex(fqn, "\\")+1:]
	switch name {
	case "PostLoad", "PostPersist", "PostRemove", "PostUpdate",
		"PrePersist", "PreRemove", "PreUpdate":
		return strings.HasPrefix(
			strings.Trim(fqn, "\\"),
			doctrineMappingNamespace+"\\",
		)
	default:
		return false
	}
}

func phpAttributeClassFQN(
	root,
	class *phpsyntax.Node,
) string {
	name := phpquery.ClassName(class)
	if namespace := phpquery.Namespace(root); namespace != "" {
		return namespace + "\\" + name
	}
	return name
}

func phpAttributeControllerClass(
	class *phpsyntax.Node,
	classFQN string,
	resolver *php.NameResolver,
	snapshot *semantic.Snapshot,
) bool {
	if strings.HasSuffix(phpquery.ClassName(class), "Controller") ||
		strings.Contains(classFQN, "\\Controller\\") ||
		phpHasAttribute(class, resolver,
			phpRouteAttribute,
			phpAsControllerAttribute,
		) ||
		snapshot.IsSubtypeOf(classFQN, phpAbstractControllerClass) {
		return true
	}
	for _, method := range phpquery.Methods(class) {
		if phpPublicInstanceMethod(method) &&
			phpHasAttribute(method, resolver, phpRouteAttribute) {
			return true
		}
	}
	return false
}

func phpAttributeTwigExtension(
	class *phpsyntax.Node,
	classFQN string,
	resolver *php.NameResolver,
	snapshot *semantic.Snapshot,
) bool {
	if strings.HasSuffix(phpquery.ClassName(class), "TwigExtension") ||
		snapshot.IsSubtypeOf(classFQN, phpTwigAbstractExtensionClass) ||
		snapshot.IsSubtypeOf(classFQN, phpTwigExtensionInterface) {
		return true
	}
	for _, method := range phpquery.Methods(class) {
		if !phpPublicInstanceMethod(method) {
			continue
		}
		if phpHasAttribute(
			method,
			resolver,
			phpAsTwigFilterAttribute,
			phpAsTwigFunctionAttribute,
			phpAsTwigTestAttribute,
		) {
			return true
		}
	}
	return false
}

func phpAttributeDoctrineEntity(
	class *phpsyntax.Node,
	classFQN string,
	resolver *php.NameResolver,
) bool {
	return strings.Contains(classFQN, "\\Entity\\") ||
		phpHasAttribute(class, resolver, doctrineEntityAttribute)
}

func phpAttributeCommandClass(
	class *phpsyntax.Node,
	classFQN string,
	resolver *php.NameResolver,
	snapshot *semantic.Snapshot,
) bool {
	if strings.HasSuffix(phpquery.ClassName(class), "Command") ||
		strings.Contains(classFQN, "\\Command\\") ||
		snapshot.IsSubtypeOf(classFQN, phpConsoleCommandClass) {
		return true
	}
	for _, method := range phpquery.Methods(class) {
		if phpquery.MethodName(method) == "__invoke" &&
			phpAttributeConsoleParameters(method, resolver) {
			return true
		}
	}
	return false
}

func phpAttributeConsoleParameters(
	method *phpsyntax.Node,
	resolver *php.NameResolver,
) bool {
	for _, parameter := range phpquery.Parameters(method) {
		for _, candidate := range strings.FieldsFunc(
			phpquery.ParameterType(parameter),
			func(value rune) bool {
				return value == '|' || value == '&' ||
					value == '?' || value == '(' || value == ')'
			},
		) {
			resolved := strings.Trim(resolver.Resolve(candidate), "\\")
			if strings.EqualFold(resolved, phpInputInterface) ||
				strings.EqualFold(resolved, phpOutputInterface) {
				return true
			}
		}
	}
	return false
}

func phpHasAttribute(
	node *phpsyntax.Node,
	resolver *php.NameResolver,
	targets ...string,
) bool {
	for _, attribute := range phpquery.Attributes(node) {
		name := strings.Trim(
			resolver.Resolve(phpquery.AttributeName(attribute)),
			"\\",
		)
		for _, target := range targets {
			if strings.EqualFold(name, strings.Trim(target, "\\")) {
				return true
			}
		}
	}
	return false
}

func phpPublicInstanceMethod(method *phpsyntax.Node) bool {
	if method == nil {
		return false
	}
	visibility := phpquery.DeclarationVisibility(method)
	if visibility != "" && visibility != "public" {
		return false
	}
	for token := range method.ChildTokens() {
		if strings.EqualFold(token.Text(), "static") {
			return false
		}
	}
	return true
}

func phpAttributeMethodAtOrAfter(
	class,
	node *phpsyntax.Node,
	offset uint32,
) *phpsyntax.Node {
	if method := phpquery.MethodAt(node); method != nil {
		return method
	}
	var nearest *phpsyntax.Node
	for _, method := range phpquery.Methods(class) {
		if method.Range().Start < offset {
			continue
		}
		if nearest == nil || method.Range().Start < nearest.Range().Start {
			nearest = method
		}
	}
	return nearest
}

func (p *PHPAttributeCompletionProvider) twigComponentCandidate(
	ctx context.Context,
	classFQN string,
) bool {
	namespace := classFQN
	if separator := strings.LastIndex(namespace, "\\"); separator >= 0 {
		namespace = namespace[:separator]
	} else {
		return false
	}
	if namespace == "Components" ||
		strings.Contains(namespace, "\\Components\\") ||
		strings.HasSuffix(namespace, "\\Components") {
		return true
	}
	for _, symbol := range p.phpIndex.ClassSymbols() {
		if ctx.Err() != nil {
			return false
		}
		otherNamespace := symbol.FullyQualified
		if separator := strings.LastIndex(otherNamespace, "\\"); separator >= 0 {
			otherNamespace = otherNamespace[:separator]
		} else {
			continue
		}
		if !strings.EqualFold(namespace, otherNamespace) {
			continue
		}
		for _, attribute := range symbol.Attributes() {
			if strings.EqualFold(
				strings.Trim(attribute.Name, "\\"),
				phpAsTwigComponentAttribute,
			) {
				return true
			}
		}
	}
	return false
}
