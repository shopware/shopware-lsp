package definition

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigquery "github.com/shopware/shopware-lsp/internal/parser/twig/query"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

// AdminDefinitionProvider provides go-to-definition for Shopware Admin Vue components
type AdminDefinitionProvider struct {
	adminIndexer *admin.AdminComponentIndexer
}

// NewAdminDefinitionProvider creates a new admin definition provider
func NewAdminDefinitionProvider(adminIndexer *admin.AdminComponentIndexer) *AdminDefinitionProvider {
	return &AdminDefinitionProvider{adminIndexer: adminIndexer}
}

// GetDefinition returns the definition location for Vue components
func (p *AdminDefinitionProvider) GetDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	ext := strings.ToLower(filepath.Ext(params.TextDocument.URI))
	languageAtCursor := lsp.EffectiveSyntaxLanguage(params.Language, params.Node)

	// Handle JS/TS files
	if ext == ".js" || ext == ".ts" ||
		ext == ".vue" && languageAtCursor == language.JavaScript {
		if params.Node == nil {
			return []protocol.Location{}
		}
		return p.jsDefinition(ctx, params)
	}

	// Handle Twig files (admin templates)
	if ext == ".twig" || ext == ".vue" && languageAtCursor == language.Twig {
		if params.Node == nil {
			return []protocol.Location{}
		}
		// Only process Twig files in administration directory
		if strings.Contains(params.TextDocument.URI, "Resources/app/administration") {
			return p.twigDefinition(ctx, params)
		}
	}

	return []protocol.Location{}
}

// twigDefinition handles go-to-definition for Vue components in Twig templates
func (p *AdminDefinitionProvider) twigDefinition(_ context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	node := params.Node
	templatePath := adminDefinitionTemplatePath(params.TextDocument.URI)
	liveOwner, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	if blockName, found := admin.TwigBlockNameAt(node, params.Token); found {
		return p.parentBlockDefinition(templatePath, blockName)
	}
	vueExpression := false
	if params.Root != nil && params.LineIndex != nil &&
		params.DefinitionParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line),
			uint32(params.Position.Character),
		)
		if directive, found := admin.TwigDirectiveAtOffset(
			params.Root, offset,
		); found {
			return p.directiveDefinitionForTemplate(
				directive.Name, templatePath,
			)
		}
		if reference, found := admin.TwigRegistryReferenceAtOffset(
			params.Root,
			offset,
		); found && reference.Name != "" {
			switch reference.Kind {
			case admin.AdminSymbolPrivilege:
				return p.privilegeDefinition(reference.Name)
			case admin.AdminSymbolModuleRoute:
				return p.moduleRouteDefinition(reference.Name)
			}
		}
		if candidate, found := adminDynamicComponentCandidateAt(
			params.Root, params.Node, offset,
		); found {
			return p.componentDefinitionByName(
				candidate.Name, templatePath, liveOwner,
			)
		}
		if startTag, field, found :=
			admin.TwigComponentObjectBindingFieldAtOffset(
				params.Root, offset,
			); found {
			return p.propDefinitionByName(
				startTag, admin.NormalizePropName(field.Name), templatePath,
				liveOwner,
			)
		}
		if locations, handled := p.twigVueMemberDefinition(params, offset); handled {
			return locations
		}
		resolvedSlot, slotErr :=
			p.adminIndexer.ResolveTwigScopedSlotBindingForOwner(
				params.Root, params.Node, params.DocumentContent, offset,
				templatePath, liveOwner,
			)
		if slotErr != nil {
			return nil
		}
		resolvedVue, vueErr := p.adminIndexer.ResolveTwigVueBindingForComponent(
			params.Root, params.DocumentContent, offset,
			templatePath, liveOwner,
		)
		if vueErr == nil && resolvedVue != nil && (resolvedSlot == nil ||
			resolvedVue.ScopeRange.Len() <=
				resolvedSlot.Scope.TemplateRange.Len()) {
			return twigVueBindingDefinition(params, *resolvedVue)
		}
		if resolvedSlot != nil {
			return scopedSlotBindingDefinition(*resolvedSlot)
		}
		vueExpression = admin.IsTwigVueExpressionAt(params.Node, offset)
	}
	if twigquery.ClosestNodeOfKind(node, twigsyntax.TwigVar) != nil ||
		vueExpression {
		return p.templateMemberDefinition(params)
	}

	// <sw-button<caret>> - cursor on component tag name
	if params.Token != nil {
		startTag := twigquery.StartingHTMLTagAt(node)
		if startTag != nil && twigquery.HTMLTagName(startTag) == params.Token.Text() {
			return p.componentDefinition(startTag, templatePath, liveOwner)
		}
	}

	attribute := twigquery.HTMLAttributeAt(node)
	if attribute != nil {
		name := twigquery.HTMLAttributeName(attribute)
		if _, model := admin.NormalizeModelArgument(name); model {
			return p.modelDefinition(attribute, templatePath, liveOwner)
		}
		if strings.HasPrefix(name, "#") || strings.HasPrefix(name, "v-slot:") ||
			name == "v-slot" {
			return p.slotDefinition(attribute, templatePath, liveOwner)
		}
		if admin.NormalizeEventName(name) != "" {
			return p.eventDefinition(attribute, templatePath, liveOwner)
		}
		return p.propDefinition(attribute, templatePath, liveOwner)
	}

	return []protocol.Location{}
}

func (p *AdminDefinitionProvider) parentBlockDefinition(
	templatePath,
	blockName string,
) []protocol.Location {
	if p == nil || p.adminIndexer == nil || templatePath == "" || blockName == "" {
		return nil
	}
	parent, err := p.adminIndexer.GetParentComponentForTemplate(templatePath)
	if err != nil || parent == nil {
		return nil
	}
	block, found := parent.ComponentBlock(blockName)
	if !found || block.FilePath == "" {
		return nil
	}
	return []protocol.Location{adminBlockLocation(block)}
}

func adminBlockLocation(block admin.TwigBlock) protocol.Location {
	if block.NameRange.Identifier || block.NameRange.Declaration {
		return protocol.Location{
			URI: uriutil.FileURI(block.FilePath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      block.NameRange.StartLine,
					Character: block.NameRange.StartCharacter,
				},
				End: protocol.Position{
					Line:      block.NameRange.EndLine,
					Character: block.NameRange.EndCharacter,
				},
			},
		}
	}
	return lineLocation(block.FilePath, block.Line)
}

func (p *AdminDefinitionProvider) modelDefinition(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	startTag := twigquery.StartingHTMLTagAt(attribute)
	components := p.componentsForMarkupTag(startTag, templatePath, owners...)
	if len(components) == 0 {
		return nil
	}
	var result []protocol.Location
	seen := make(map[string]bool)
	add := func(
		component admin.VueComponent,
		path string,
		line int,
		nameRange admin.AdminSourceRange,
	) {
		if path == "" {
			path = component.DefinitionPath
		}
		if path == "" {
			path = component.FilePath
		}
		if path == "" {
			return
		}
		key := path + ":" + strconv.Itoa(line) + ":" +
			strconv.Itoa(nameRange.StartLine) + ":" +
			strconv.Itoa(nameRange.StartCharacter)
		if seen[key] {
			return
		}
		seen[key] = true
		result = append(result, adminDeclarationLocation(path, line, nameRange))
	}
	for _, component := range components {
		model, found := component.ComponentModel(
			twigquery.HTMLAttributeName(attribute),
		)
		if !found {
			continue
		}
		add(
			component, model.Prop.FilePath, model.Prop.Line,
			model.Prop.NameRange,
		)
		add(
			component, model.Event.FilePath, model.Event.Line,
			model.Event.NameRange,
		)
	}
	return result
}

func (p *AdminDefinitionProvider) twigVueMemberDefinition(
	params *lsp.DefinitionRequest,
	offset uint32,
) ([]protocol.Location, bool) {
	if params == nil || params.Root == nil || params.Node == nil {
		return nil, false
	}
	access, found := admin.TwigVueExpressionMemberAtOffset(
		params.Root, params.DocumentContent, offset,
	)
	if !found || access.Member == "" {
		return nil, false
	}
	templatePath := adminDefinitionTemplatePath(params.TextDocument.URI)
	liveComponent, _ := p.adminIndexer.GetComponentForDocument(
		templatePath, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	resolvedSlot, slotErr :=
		p.adminIndexer.ResolveTwigScopedSlotMemberForOwner(
			params.Root, params.Node, params.DocumentContent, offset,
			templatePath, liveComponent,
		)
	if slotErr != nil {
		return nil, true
	}
	resolvedVue, vueErr := p.adminIndexer.ResolveTwigVueMemberForComponent(
		params.Root, params.DocumentContent, offset, templatePath, liveComponent,
	)
	if vueErr != nil {
		return nil, true
	}
	if resolvedVue != nil && (resolvedSlot == nil ||
		resolvedVue.Binding.ScopeRange.Len() <=
			resolvedSlot.Scope.TemplateRange.Len()) {
		if resolvedVue.MemberFound && resolvedVue.Member.DefinitionPath != "" {
			return []protocol.Location{lineLocation(
				resolvedVue.Member.DefinitionPath,
				resolvedVue.Member.DefinitionLine,
			)}, true
		}
		// Observed JavaScript object properties have no authoritative declaration.
		return nil, true
	}
	if resolvedSlot != nil {
		if !resolvedSlot.MemberFound {
			return nil, true
		}
		locations := slotMemberDefinitionLocations(
			resolvedSlot.Members, resolvedSlot.Member,
		)
		return locations, true
	}
	resolvedInstance, instanceErr :=
		p.adminIndexer.ResolveTwigVueInstanceMemberForComponent(
			params.Root, params.DocumentContent, offset,
			templatePath, liveComponent,
		)
	if instanceErr != nil {
		return nil, true
	}
	if resolvedInstance != nil {
		if !resolvedInstance.MemberFound ||
			resolvedInstance.Member.DefinitionPath == "" {
			return nil, true
		}
		return []protocol.Location{lineLocation(
			resolvedInstance.Member.DefinitionPath,
			resolvedInstance.Member.DefinitionLine,
		)}, true
	}
	return nil, true
}

func adminDefinitionTemplatePath(uri string) string {
	path, err := uriutil.Path(uri)
	if err != nil {
		return ""
	}
	return path
}

func twigVueBindingDefinition(
	params *lsp.DefinitionRequest,
	binding admin.TwigVueBinding,
) []protocol.Location {
	if binding.DefinitionPath != "" {
		return []protocol.Location{lineLocation(
			binding.DefinitionPath, binding.DefinitionLine,
		)}
	}
	if binding.DeclarationRange.Len() == 0 || params == nil ||
		params.LineIndex == nil {
		return nil
	}
	startLine, startCharacter := params.LineIndex.PositionUTF16(
		binding.DeclarationRange.Start,
	)
	endLine, endCharacter := params.LineIndex.PositionUTF16(
		binding.DeclarationRange.End,
	)
	return []protocol.Location{{
		URI: params.TextDocument.URI,
		Range: protocol.Range{
			Start: protocol.Position{
				Line: int(startLine), Character: int(startCharacter),
			},
			End: protocol.Position{
				Line: int(endLine), Character: int(endCharacter),
			},
		},
	}}
}

func scopedSlotBindingDefinition(
	resolved admin.ResolvedTwigSlotBinding,
) []protocol.Location {
	if resolved.MemberFound && !resolved.Binding.WholeObject {
		if locations := slotMemberDefinitionLocations(
			resolved.Members, resolved.Member,
		); len(locations) > 0 {
			return locations
		}
	}
	var locations []protocol.Location
	seen := make(map[string]bool)
	for _, contract := range resolved.Contracts {
		filePath := contract.Slot.FilePath
		if filePath == "" {
			filePath = contract.Component.TemplatePath
		}
		if filePath == "" {
			filePath = contract.Component.DefinitionPath
		}
		if filePath == "" {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d:%d", filePath, contract.Slot.Line,
			contract.Slot.NameRange.StartLine,
			contract.Slot.NameRange.StartCharacter,
			contract.Slot.NameRange.EndLine,
			contract.Slot.NameRange.EndCharacter,
		)
		if seen[key] {
			continue
		}
		seen[key] = true
		locations = append(
			locations, componentSlotLocation(filePath, contract.Slot),
		)
	}
	if len(locations) > 0 {
		return locations
	}
	filePath := resolved.Slot.FilePath
	if resolved.MemberFound && resolved.Member.FilePath != "" {
		filePath = resolved.Member.FilePath
	}
	if filePath == "" {
		filePath = resolved.Component.TemplatePath
	}
	if filePath == "" {
		filePath = resolved.Component.DefinitionPath
	}
	if filePath == "" {
		return nil
	}
	if resolved.MemberFound && resolved.Member.FilePath != "" {
		return []protocol.Location{
			componentSlotMemberLocation(filePath, resolved.Member),
		}
	}
	return []protocol.Location{
		componentSlotLocation(filePath, resolved.Slot),
	}
}

func slotMemberDefinitionLocations(
	members []admin.VueComponentSlotMember,
	fallback admin.VueComponentSlotMember,
) []protocol.Location {
	if len(members) == 0 && fallback.Name != "" {
		members = []admin.VueComponentSlotMember{fallback}
	}
	locations := make([]protocol.Location, 0, len(members))
	seen := make(map[string]bool)
	for _, member := range members {
		if member.FilePath == "" {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d:%d", member.FilePath, member.Line,
			member.NameRange.StartLine, member.NameRange.StartCharacter,
			member.NameRange.EndLine, member.NameRange.EndCharacter,
		)
		if seen[key] {
			continue
		}
		seen[key] = true
		locations = append(locations, componentSlotMemberLocation(
			member.FilePath, member,
		))
	}
	return locations
}

func (p *AdminDefinitionProvider) eventDefinition(
	attribute *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	eventName := admin.NormalizeEventName(
		twigquery.HTMLAttributeName(attribute),
	)
	if eventName == "" {
		return nil
	}
	startTag := twigquery.StartingHTMLTagAt(attribute)
	var result []protocol.Location
	seen := make(map[string]bool)
	for _, component := range p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	) {
		event, found := component.ComponentEvent(eventName)
		if !found {
			continue
		}
		targetPath := event.FilePath
		if targetPath == "" {
			targetPath = component.DefinitionPath
		}
		if targetPath == "" {
			targetPath = component.FilePath
		}
		key := targetPath + ":" + strconv.Itoa(event.Line) + ":" +
			strconv.Itoa(event.NameRange.StartLine) + ":" +
			strconv.Itoa(event.NameRange.StartCharacter)
		if targetPath == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(
			result,
			componentEventLocation(targetPath, event),
		)
	}
	return result
}

func (p *AdminDefinitionProvider) templateMemberDefinition(
	params *lsp.DefinitionRequest,
) []protocol.Location {
	name := ""
	if twigquery.ClosestNodeOfKind(params.Node, twigsyntax.TwigVar) != nil {
		name = adminTemplateRootName(
			params.Node,
			params.Token,
			params.DocumentContent,
		)
	} else if params.LineIndex != nil && params.DefinitionParams != nil {
		offset := params.LineIndex.OffsetUTF16(
			uint32(params.Position.Line), uint32(params.Position.Character),
		)
		name, _, _ = admin.ExpressionRootIdentifierAtOffset(
			params.DocumentContent, offset,
		)
	}
	if name == "" {
		return nil
	}
	path, err := uriutil.Path(params.TextDocument.URI)
	if err != nil {
		return nil
	}
	component, err := p.adminIndexer.GetComponentForDocument(
		path, params.Root, string(params.DocumentContent), params.LineIndex,
	)
	if err != nil || component == nil {
		return nil
	}
	if scopeMember, _, scoped := admin.TwigBlockScopeMemberAt(
		*component, params.Node, name,
	); scoped && scopeMember.FilePath != "" {
		if scopeMember.NameRange.Identifier ||
			scopeMember.NameRange.Declaration {
			return []protocol.Location{{
				URI: uriutil.FileURI(scopeMember.FilePath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      scopeMember.NameRange.StartLine,
						Character: scopeMember.NameRange.StartCharacter,
					},
					End: protocol.Position{
						Line:      scopeMember.NameRange.EndLine,
						Character: scopeMember.NameRange.EndCharacter,
					},
				},
			}}
		}
		return []protocol.Location{lineLocation(
			scopeMember.FilePath, scopeMember.Line,
		)}
	}
	member, found := component.TemplateMember(name)
	if !found || member.FilePath == "" {
		return nil
	}
	return []protocol.Location{componentMemberLocation(member)}
}

func adminTemplateRootName(
	node *twigsyntax.Node,
	token *twigsyntax.Token,
	content []byte,
) string {
	if node == nil || token == nil {
		return ""
	}
	accessor := twigquery.ClosestNodeOfKind(node, twigsyntax.TwigAccessor)
	if accessor != nil {
		start := accessor.RangeTrimmedTrivia().Start
		end := token.Range().Start
		if start < end && int(end) <= len(content) &&
			strings.Contains(string(content[start:end]), ".") {
			return ""
		}
	}
	return strings.TrimSpace(token.Text())
}

// componentDefinition returns the definition location for a component tag name
func (p *AdminDefinitionProvider) componentDefinition(
	node *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	componentName := twigquery.HTMLTagName(node)
	return p.componentDefinitionByName(
		componentName, templatePath, owners...,
	)
}

func (p *AdminDefinitionProvider) componentDefinitionByName(
	componentName string,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	if componentName == "" {
		return []protocol.Location{}
	}

	// Look up the component in the index
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, componentName, owners...,
	)
	if err != nil || !found || component == nil {
		return []protocol.Location{}
	}

	// Build location results
	var locations []protocol.Location
	for _, comp := range []admin.VueComponent{*component} {
		// Prefer definition path if available, otherwise use registration file
		targetPath := comp.DefinitionPath
		targetLine := 1 // Default to start of file for definition files
		if targetPath != "" && filepath.Clean(targetPath) == filepath.Clean(comp.FilePath) {
			// Inline component definitions live at the registration call, not at
			// the beginning of the containing source file.
			targetLine = comp.Line
			if targetLine < 1 {
				targetLine = 1
			}
		}

		if targetPath == "" || !fileExists(targetPath) {
			// Fallback to registration file
			targetPath = comp.FilePath
			targetLine = comp.Line
		}

		if targetPath == "" {
			continue
		}

		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(targetPath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      targetLine - 1, // Convert to 0-based
					Character: 0,
				},
				End: protocol.Position{
					Line:      targetLine - 1,
					Character: 0,
				},
			},
		})
	}

	return locations
}

func adminDynamicComponentCandidateAt(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	offset uint32,
) (admin.VueDynamicComponentCandidate, bool) {
	if root == nil || node == nil {
		return admin.VueDynamicComponentCandidate{}, false
	}
	startTag := twigquery.StartingHTMLTagAt(node)
	selector, found := admin.TwigDynamicComponentSelector(startTag)
	if !found {
		return admin.VueDynamicComponentCandidate{}, false
	}
	return selector.CandidateAt(offset)
}

// propDefinition returns the definition location for a prop attribute
// <sw-button label<caret>="x"> - jump to prop definition in component's JS file
func (p *AdminDefinitionProvider) propDefinition(
	node *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	// Get the attribute name
	attrName := twigquery.HTMLAttributeName(node)
	if attrName == "" {
		return []protocol.Location{}
	}

	// Normalize attribute name: remove Vue binding prefixes and convert to camelCase
	// e.g., ":position-identifier" -> "positionIdentifier"
	propName := admin.NormalizePropName(attrName)
	if propName == "" {
		return []protocol.Location{}
	}

	// Find the parent component tag name
	// e.g., for <sw-button label="x">, returns "sw-button"
	startTag := twigquery.StartingHTMLTagAt(node)
	return p.propDefinitionByName(
		startTag, propName, templatePath, owners...,
	)
}

func (p *AdminDefinitionProvider) propDefinitionByName(
	startTag *twigsyntax.Node,
	propName,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	if propName == "" || startTag == nil {
		return nil
	}
	var result []protocol.Location
	seen := make(map[string]bool)
	for _, comp := range p.componentsForMarkupTag(
		startTag, templatePath, owners...,
	) {
		for _, prop := range comp.Props {
			if prop.Name != propName {
				continue
			}
			// Inherited props retain their owning definition path.
			targetPath := prop.FilePath
			if targetPath == "" {
				targetPath = comp.DefinitionPath
			}
			if targetPath == "" {
				targetPath = comp.FilePath
			}

			key := targetPath + ":" + strconv.Itoa(prop.Line) + ":" +
				strconv.Itoa(prop.NameRange.StartLine) + ":" +
				strconv.Itoa(prop.NameRange.StartCharacter)
			if targetPath == "" ||
				prop.Line == 0 && !prop.NameRange.Declaration || seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, componentPropLocation(targetPath, prop))
		}
	}
	return result
}

func (p *AdminDefinitionProvider) componentsForMarkupTag(
	startTag *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) []admin.VueComponent {
	if p == nil || p.adminIndexer == nil || startTag == nil {
		return nil
	}
	if selector, dynamic := admin.TwigDynamicComponentSelector(startTag); dynamic {
		_, components, complete, err :=
			p.adminIndexer.ResolveDynamicComponentContractsForOwner(
				templatePath, selector, firstAdminOwner(owners), startTag,
			)
		if err != nil || !complete {
			return nil
		}
		return components
	}
	componentName, found := admin.StaticComponentNameForTag(startTag)
	if !found {
		return nil
	}
	component, found, err := p.adminIndexer.GetComponentForTemplateTag(
		templatePath, componentName, owners...,
	)
	if err != nil || !found || component == nil {
		return nil
	}
	return []admin.VueComponent{*component}
}

// slotDefinition returns the definition location for a slot reference
// <template #default<caret>> - jump to <slot name="default"> in component's template
func (p *AdminDefinitionProvider) slotDefinition(
	node *twigsyntax.Node,
	templatePath string,
	owners ...*admin.VueComponent,
) []protocol.Location {
	// Extract slot name from the node text
	slotName := p.extractSlotName(node)
	if slotName == "" {
		return []protocol.Location{}
	}

	startTag := twigquery.StartingHTMLTagAt(node)
	if startTag == nil {
		return []protocol.Location{}
	}
	components, complete, err :=
		p.adminIndexer.ResolveTwigSlotConsumerComponents(
			templatePath, startTag, owners...,
		)
	if err != nil || !complete {
		return []protocol.Location{}
	}

	locations := make([]protocol.Location, 0, len(components))
	seen := make(map[string]bool)
	for _, component := range components {
		slot, found := component.ComponentSlot(slotName)
		if !found {
			continue
		}
		sourcePath := slot.FilePath
		if sourcePath == "" {
			sourcePath = component.TemplatePath
		}
		if sourcePath == "" {
			continue
		}
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d:%d", sourcePath, slot.Line,
			slot.NameRange.StartLine, slot.NameRange.StartCharacter,
			slot.NameRange.EndLine, slot.NameRange.EndCharacter,
		)
		if seen[key] {
			continue
		}
		seen[key] = true
		locations = append(
			locations, componentSlotLocation(sourcePath, slot),
		)
	}
	return locations
}

// extractSlotName extracts the slot name from a slot reference node
// "#default" -> "default", "v-slot:actions" -> "actions"
func (p *AdminDefinitionProvider) extractSlotName(node *twigsyntax.Node) string {
	return admin.NormalizeSlotName(twigquery.HTMLAttributeName(node))
}

// kebabToCamel converts kebab-case to camelCase (used by tests)
func kebabToCamel(s string) string {
	return admin.KebabToCamel(s)
}

func firstAdminOwner(values []*admin.VueComponent) *admin.VueComponent {
	if len(values) == 0 {
		return nil
	}
	return values[0]
}

func (p *AdminDefinitionProvider) jsDefinition(ctx context.Context, params *lsp.DefinitionRequest) []protocol.Location {
	node := params.Node
	if _, eventName, matched := admin.JavaScriptShopwareEventBusEventAt(
		node,
	); matched && eventName != "" {
		return p.shopwareEventBusEventDefinition(
			params.TextDocument.URI, eventName,
		)
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareUtilsMember(node); matched && memberName != "" {
		return p.shopwareUtilsMemberDefinition(
			params.TextDocument.URI, strings.Join(receiver, "."), memberName,
		)
	}
	if receiver, memberName, matched :=
		admin.JavaScriptShopwareContextMember(node); matched && memberName != "" {
		return p.shopwareContextMemberDefinition(
			params.TextDocument.URI, strings.Join(receiver, "."), memberName,
		)
	}
	if containerName, memberName, matched :=
		admin.JavaScriptApplicationContainerMember(node); matched && memberName != "" {
		return p.applicationContainerMemberDefinition(
			params.TextDocument.URI, containerName, memberName,
		)
	}
	if storeName, memberName, matched := jsquery.StoreMember(node); matched && memberName != "" {
		return p.storeMemberDefinition(storeName, memberName)
	}
	if member, matched := jsquery.ThisMember(node); matched && member != "" {
		return p.thisMemberDefinition(params.TextDocument.URI, member)
	}
	if admin.IsServiceReference(node) {
		return p.serviceDefinition(jsquery.StringValue(node))
	}
	if admin.IsStoreReference(node) {
		return p.storeDefinition(jsquery.StringValue(node))
	}
	if admin.IsPrivilegeReference(node) {
		return p.privilegeDefinition(jsquery.StringValue(node))
	}
	if jsquery.StringInCall(
		node,
		0,
		"Mixin.getByName",
		"Shopware.Mixin.getByName",
	) {
		return p.mixinDefinition(jsquery.StringValue(node))
	}
	if p.isModuleRouteReference(node) {
		return p.moduleRouteDefinition(jsquery.StringValue(node))
	}
	if path, err := uriutil.Path(params.TextDocument.URI); err == nil {
		if indexedTarget, indexedFound, _ := p.adminIndexer.JavaScriptSymbolAt(
			path, node,
		); indexedFound && indexedTarget.Kind == admin.AdminSymbolDirective &&
			indexedTarget.Owner != "" {
			return p.directiveDefinitionTarget(indexedTarget)
		}
	}

	target, found := admin.JavaScriptSymbolAt(node)
	if !found {
		return []protocol.Location{}
	}
	switch target.Kind {
	case admin.AdminSymbolModule:
		return p.moduleDefinition(target.Name)
	case admin.AdminSymbolMixin:
		return p.mixinDefinition(target.Name)
	case admin.AdminSymbolDirective:
		return p.directiveDefinition(target.Name)
	case admin.AdminSymbolFilter:
		return p.filterDefinition(target.Name)
	case admin.AdminSymbolCMSElement:
		return p.cmsDefinition(admin.AdminCMSElement, target.Name)
	case admin.AdminSymbolCMSBlock:
		return p.cmsDefinition(admin.AdminCMSBlock, target.Name)
	case admin.AdminSymbolComponent:
		// Continue with the component lookup below.
	default:
		return []protocol.Location{}
	}

	componentName := target.Name
	if componentName == "" {
		return []protocol.Location{}
	}

	// Look up the component in the index
	components, err := p.adminIndexer.GetComponent(componentName)
	if err != nil || len(components) == 0 {
		return []protocol.Location{}
	}

	// Build location results
	var locations []protocol.Location
	for _, comp := range components {
		// Prefer definition path if available, otherwise use registration file
		targetPath := comp.DefinitionPath
		targetLine := 1 // Default to start of file for definition files

		if targetPath == "" || !fileExists(targetPath) {
			// Fallback to registration file
			targetPath = comp.FilePath
			targetLine = comp.Line
		}

		if targetPath == "" {
			continue
		}

		locations = append(locations, protocol.Location{
			URI: uriutil.FileURI(targetPath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      targetLine - 1, // Convert to 0-based
					Character: 0,
				},
				End: protocol.Position{
					Line:      targetLine - 1,
					Character: 0,
				},
			},
		})
	}

	return locations
}

func (p *AdminDefinitionProvider) shopwareEventBusEventDefinition(
	uri,
	eventName string,
) []protocol.Location {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil
	}
	event, found, err := p.adminIndexer.ResolveShopwareEventBusEvent(
		eventName, path,
	)
	if err != nil || !found || event.DefinitionPath == "" {
		return nil
	}
	if event.DefinitionRange.Declaration || event.DefinitionRange.Identifier {
		return []protocol.Location{{
			URI: uriutil.FileURI(event.DefinitionPath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      event.DefinitionRange.StartLine,
					Character: event.DefinitionRange.StartCharacter,
				},
				End: protocol.Position{
					Line:      event.DefinitionRange.EndLine,
					Character: event.DefinitionRange.EndCharacter,
				},
			},
		}}
	}
	return []protocol.Location{lineLocation(
		event.DefinitionPath, event.DefinitionLine,
	)}
}

func (p *AdminDefinitionProvider) shopwareUtilsMemberDefinition(
	uri,
	receiver,
	memberName string,
) []protocol.Location {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveShopwareUtils(receiver, path)
	if err != nil {
		return nil
	}
	for _, member := range shape.Members {
		if member.Name != memberName || member.DefinitionPath == "" {
			continue
		}
		if member.DefinitionRange.Declaration ||
			member.DefinitionRange.Identifier {
			return []protocol.Location{{
				URI: uriutil.FileURI(member.DefinitionPath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      member.DefinitionRange.StartLine,
						Character: member.DefinitionRange.StartCharacter,
					},
					End: protocol.Position{
						Line:      member.DefinitionRange.EndLine,
						Character: member.DefinitionRange.EndCharacter,
					},
				},
			}}
		}
		return []protocol.Location{lineLocation(
			member.DefinitionPath, member.DefinitionLine,
		)}
	}
	return nil
}

func (p *AdminDefinitionProvider) applicationContainerMemberDefinition(
	uri,
	containerName,
	memberName string,
) []protocol.Location {
	if containerName == "service" {
		if locations := p.serviceDefinition(memberName); len(locations) > 0 {
			return locations
		}
	}
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveApplicationContainer(
		containerName, path,
	)
	if err != nil {
		return nil
	}
	for _, member := range shape.Members {
		if member.Name != memberName || member.DefinitionPath == "" {
			continue
		}
		if member.DefinitionRange.Declaration ||
			member.DefinitionRange.Identifier {
			return []protocol.Location{{
				URI: uriutil.FileURI(member.DefinitionPath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      member.DefinitionRange.StartLine,
						Character: member.DefinitionRange.StartCharacter,
					},
					End: protocol.Position{
						Line:      member.DefinitionRange.EndLine,
						Character: member.DefinitionRange.EndCharacter,
					},
				},
			}}
		}
		return []protocol.Location{lineLocation(
			member.DefinitionPath, member.DefinitionLine,
		)}
	}
	return nil
}

func (p *AdminDefinitionProvider) shopwareContextMemberDefinition(
	uri,
	receiver,
	memberName string,
) []protocol.Location {
	path, err := uriutil.Path(uri)
	if err != nil || p == nil || p.adminIndexer == nil {
		return nil
	}
	shape, err := p.adminIndexer.ResolveShopwareContext(receiver, path)
	if err != nil {
		return nil
	}
	for _, member := range shape.Members {
		if member.Name != memberName || member.DefinitionPath == "" {
			continue
		}
		if member.DefinitionRange.Declaration ||
			member.DefinitionRange.Identifier {
			return []protocol.Location{{
				URI: uriutil.FileURI(member.DefinitionPath),
				Range: protocol.Range{
					Start: protocol.Position{
						Line:      member.DefinitionRange.StartLine,
						Character: member.DefinitionRange.StartCharacter,
					},
					End: protocol.Position{
						Line:      member.DefinitionRange.EndLine,
						Character: member.DefinitionRange.EndCharacter,
					},
				},
			}}
		}
		return []protocol.Location{lineLocation(
			member.DefinitionPath, member.DefinitionLine,
		)}
	}
	return nil
}

func (p *AdminDefinitionProvider) serviceDefinition(name string) []protocol.Location {
	services, err := p.adminIndexer.GetService(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(services))
	for _, service := range services {
		if service.ImplementationPath != "" {
			locations = append(
				locations,
				lineLocation(service.ImplementationPath, 1),
			)
			continue
		}
		locations = append(locations, lineLocation(service.FilePath, service.Line))
	}
	return locations
}

func (p *AdminDefinitionProvider) storeDefinition(name string) []protocol.Location {
	stores, err := p.adminIndexer.GetStore(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(stores))
	for _, store := range stores {
		locations = append(locations, lineLocation(store.FilePath, store.Line))
	}
	return locations
}

func (p *AdminDefinitionProvider) privilegeDefinition(name string) []protocol.Location {
	privileges, err := p.adminIndexer.GetPrivilege(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(privileges))
	for _, privilege := range privileges {
		if privilege.IsBuiltin() {
			continue
		}
		locations = append(locations, lineLocation(privilege.FilePath, privilege.Line))
	}
	return locations
}

func (p *AdminDefinitionProvider) storeMemberDefinition(
	storeName,
	memberName string,
) []protocol.Location {
	stores, err := p.adminIndexer.GetStore(storeName)
	if err != nil {
		return nil
	}
	var locations []protocol.Location
	for _, store := range stores {
		for _, member := range store.Members {
			if member.Name == memberName {
				locations = append(
					locations,
					lineLocation(member.FilePath, member.Line),
				)
			}
		}
	}
	return locations
}

func (p *AdminDefinitionProvider) thisMemberDefinition(
	uri,
	name string,
) []protocol.Location {
	path, err := uriutil.Path(uri)
	if err != nil {
		return nil
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(path)
	if err != nil {
		return nil
	}
	var locations []protocol.Location
	seen := make(map[string]bool)
	for _, component := range components {
		member, found := component.TemplateMember(name)
		if !found || member.FilePath == "" {
			continue
		}
		location := componentMemberLocation(member)
		key := fmt.Sprintf(
			"%s:%d:%d:%d:%d",
			location.URI,
			location.Range.Start.Line,
			location.Range.Start.Character,
			location.Range.End.Line,
			location.Range.End.Character,
		)
		if seen[key] {
			continue
		}
		seen[key] = true
		locations = append(locations, location)
		if member.Kind == admin.ComponentMemberInject {
			locations = append(
				locations,
				p.serviceDefinition(member.Name)...,
			)
		}
	}
	return locations
}

func componentMemberLocation(member admin.VueComponentMember) protocol.Location {
	if member.Renameable() {
		return protocol.Location{
			URI: uriutil.FileURI(member.FilePath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      member.NameRange.StartLine,
					Character: member.NameRange.StartCharacter,
				},
				End: protocol.Position{
					Line:      member.NameRange.EndLine,
					Character: member.NameRange.EndCharacter,
				},
			},
		}
	}
	return lineLocation(member.FilePath, member.Line)
}

func componentPropLocation(
	filePath string,
	prop admin.VueComponentProp,
) protocol.Location {
	if prop.NameRange.Declaration || prop.NameRange.Identifier {
		return protocol.Location{
			URI: uriutil.FileURI(filePath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line:      prop.NameRange.StartLine,
					Character: prop.NameRange.StartCharacter,
				},
				End: protocol.Position{
					Line:      prop.NameRange.EndLine,
					Character: prop.NameRange.EndCharacter,
				},
			},
		}
	}
	return lineLocation(filePath, prop.Line)
}

func componentEventLocation(
	filePath string,
	event admin.VueComponentEvent,
) protocol.Location {
	return adminDeclarationLocation(filePath, event.Line, event.NameRange)
}

func componentSlotLocation(
	filePath string,
	slot admin.VueComponentSlot,
) protocol.Location {
	return adminDeclarationLocation(filePath, slot.Line, slot.NameRange)
}

func componentSlotMemberLocation(
	filePath string,
	member admin.VueComponentSlotMember,
) protocol.Location {
	return adminDeclarationLocation(filePath, member.Line, member.NameRange)
}

func adminDeclarationLocation(
	filePath string,
	line int,
	nameRange admin.AdminSourceRange,
) protocol.Location {
	if nameRange.Declaration || nameRange.Identifier {
		return protocol.Location{
			URI: uriutil.FileURI(filePath),
			Range: protocol.Range{
				Start: protocol.Position{
					Line: nameRange.StartLine, Character: nameRange.StartCharacter,
				},
				End: protocol.Position{
					Line: nameRange.EndLine, Character: nameRange.EndCharacter,
				},
			},
		}
	}
	return lineLocation(filePath, line)
}

func (p *AdminDefinitionProvider) mixinDefinition(name string) []protocol.Location {
	mixins, err := p.adminIndexer.GetMixin(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(mixins))
	for _, mixin := range mixins {
		locations = append(locations, lineLocation(mixin.FilePath, mixin.Line))
	}
	return locations
}

func (p *AdminDefinitionProvider) directiveDefinition(
	name string,
) []protocol.Location {
	directives, err := p.adminIndexer.GetDirective(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(directives))
	for _, directive := range directives {
		locations = append(
			locations,
			lineLocation(directive.FilePath, directive.Line),
		)
	}
	return locations
}

func (p *AdminDefinitionProvider) filterDefinition(
	name string,
) []protocol.Location {
	filters, err := p.adminIndexer.GetFilter(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(filters))
	for _, filter := range filters {
		locations = append(
			locations,
			lineLocation(filter.FilePath, filter.Line),
		)
	}
	return locations
}

func (p *AdminDefinitionProvider) cmsDefinition(
	kind admin.AdminCMSRegistrationKind,
	name string,
) []protocol.Location {
	registrations, err := p.adminIndexer.GetCMSRegistration(kind, name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(registrations))
	for _, registration := range registrations {
		locations = append(
			locations,
			lineLocation(registration.FilePath, registration.Line),
		)
	}
	return locations
}

func (p *AdminDefinitionProvider) directiveDefinitionForTemplate(
	name,
	templatePath string,
) []protocol.Location {
	directives, err := p.adminIndexer.GetDirectiveForTemplate(templatePath, name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(directives))
	for _, directive := range directives {
		locations = append(
			locations,
			lineLocation(directive.FilePath, directive.Line),
		)
	}
	return locations
}

func (p *AdminDefinitionProvider) directiveDefinitionTarget(
	target admin.AdminSymbolTarget,
) []protocol.Location {
	if target.Owner == "" {
		return p.directiveDefinition(target.Name)
	}
	components, err := p.adminIndexer.GetComponentsByDefinitionPath(target.Owner)
	if err != nil {
		return nil
	}
	var locations []protocol.Location
	for _, component := range components {
		if local, found := component.LocalDirective(target.Name); found {
			locations = append(
				locations, lineLocation(local.FilePath, local.Line),
			)
		}
	}
	return locations
}

func (p *AdminDefinitionProvider) moduleRouteDefinition(name string) []protocol.Location {
	module, route, err := p.adminIndexer.GetModuleRoute(name)
	if err != nil || module == nil || route == nil {
		return nil
	}
	return []protocol.Location{lineLocation(module.FilePath, route.Line)}
}

func (p *AdminDefinitionProvider) moduleDefinition(name string) []protocol.Location {
	modules, err := p.adminIndexer.GetModule(name)
	if err != nil {
		return nil
	}
	locations := make([]protocol.Location, 0, len(modules))
	for _, module := range modules {
		locations = append(locations, lineLocation(module.FilePath, module.Line))
	}
	return locations
}

func lineLocation(filePath string, line int) protocol.Location {
	if line < 1 {
		line = 1
	}
	return protocol.Location{
		URI: uriutil.FileURI(filePath),
		Range: protocol.Range{
			Start: protocol.Position{Line: line - 1},
			End:   protocol.Position{Line: line - 1},
		},
	}
}

func (p *AdminDefinitionProvider) isModuleRouteReference(node *jssyntax.Node) bool {
	return admin.IsJavaScriptModuleRouteReference(node)
}

func (p *AdminDefinitionProvider) isInComponentCall(node *jssyntax.Node) bool {
	target, found := admin.JavaScriptSymbolAt(node)
	return found && target.Kind == admin.AdminSymbolComponent
}

func (p *AdminDefinitionProvider) extractComponentName(node *jssyntax.Node) string {
	target, found := admin.JavaScriptSymbolAt(node)
	if !found || target.Kind != admin.AdminSymbolComponent {
		return ""
	}
	return target.Name
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	// Simple check - we could use os.Stat but for LSP purposes
	// we'll just return true and let the editor handle missing files
	return path != ""
}
