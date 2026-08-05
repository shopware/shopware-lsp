package admin

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	vueparser "github.com/shopware/shopware-lsp/internal/parser/vue"
)

type liveLegacyDocument struct {
	Definition    ComponentDefinition
	HasDefinition bool
	Registrations []VueComponent
	Mixins        []AdminMixin
	Modules       []AdminModule
	Services      []AdminService
	Stores        []AdminStore
	StoreFactory  *AdminStoreFactory
	Directives    []AdminDirective
	Filters       []AdminFilter
	CMS           []AdminCMSRegistration
	Privileges    []AdminPrivilege
}

type liveLegacyDocumentSnapshot struct {
	Path     string
	Document liveLegacyDocument
}

// GetComponentForDocument overlays an open Vue SFC on the persisted effective
// component. It never mutates the persistent workspace generation: all editor
// features can therefore observe unsaved edits while SQLite remains stable.
func (idx *AdminComponentIndexer) GetComponentForDocument(
	filePath string,
	root *cst.Node,
	source string,
	lineIndex *cst.LineIndex,
) (*VueComponent, error) {
	if root == nil {
		return idx.GetComponentByTemplatePath(filePath)
	}
	if strings.EqualFold(filepath.Ext(filePath), ".twig") {
		return idx.componentForLiveTwigDocument(
			filePath, root, source, lineIndex,
		)
	}
	if !isVueComponentPath(filePath) {
		return idx.GetComponentByTemplatePath(filePath)
	}
	definition, liveTypes, found := parseLiveVueDefinition(
		filePath, root, source, lineIndex,
	)
	if !found {
		return idx.GetComponentByTemplatePath(filePath)
	}
	return idx.componentForLiveVueDefinition(filePath, *definition, liveTypes)
}

func (idx *AdminComponentIndexer) componentForLiveVueDefinition(
	filePath string,
	rawDefinition ComponentDefinition,
	liveTypes []AdminTypeFile,
) (*VueComponent, error) {
	component, err := idx.GetComponentByTemplatePath(filePath)
	if err != nil {
		return nil, err
	}
	definition := cloneComponentDefinition(rawDefinition)
	if err := idx.enrichScriptSetupTypeContracts(
		&definition, liveTypes...,
	); err != nil {
		return nil, err
	}
	result := VueComponent{
		Name:           deriveComponentNameFromPath(filePath),
		DefinitionPath: filePath, TemplatePath: filePath,
	}
	if component != nil {
		result = *component
		if persisted, definitionErr := idx.GetComponentDefinition(filePath); definitionErr != nil {
			return nil, definitionErr
		} else if persisted != nil {
			stripComponentDefinition(&result, *persisted, filePath)
		}
	}
	live := VueComponent{
		Name: result.Name, DefinitionPath: filePath, TemplatePath: filePath,
	}
	applyDefinition(&live, definition)
	result = overlayComponents(result, live)
	result.liveTypeFiles = append([]AdminTypeFile(nil), liveTypes...)
	return &result, nil
}

func parseLiveVueDefinition(
	filePath string,
	root *cst.Node,
	source string,
	lineIndex *cst.LineIndex,
) (*ComponentDefinition, []AdminTypeFile, bool) {
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(source)
	}
	sections := vueparser.Sections(source)
	definition := ParseComponentDefinitionWithLineIndex(root, lineIndex)
	hasDefinition := len(jsquery.ExportDefaults(root)) > 0
	hasTemplate := false
	for _, section := range sections {
		switch section.Kind {
		case vueparser.SectionTemplate:
			hasTemplate = true
		case vueparser.SectionScript:
			if !section.Setup {
				continue
			}
			setup := parseScriptSetupDefinition(
				root, source, filePath, lineIndex, section.BodyRange,
			)
			if setup != nil {
				definition = mergeComponentDefinitions(definition, setup)
				hasDefinition = true
			}
		}
	}
	if !hasDefinition && !hasTemplate {
		return nil, nil, false
	}
	if definition == nil {
		definition = &ComponentDefinition{}
	}
	if hasTemplate {
		definition.HasTemplate = true
		definition.TemplatePath = filePath
		template := parseTemplateTree(root, source, lineIndex)
		setTemplateSourcePaths(&template, filePath)
		definition.Slots = template.Slots
		definition.Blocks = template.Blocks
	}
	setDefinitionFilePath(definition, filePath)
	liveTypes := []AdminTypeFile{parseAdminTypeFile(
		filePath, vueScriptTypeSource(source, sections), lineIndex,
	)}
	return definition, liveTypes, true
}

// UpdateLiveDocument publishes one immutable editor snapshot into the
// Administration overlay. Only compact parsed contracts are retained; source
// text and syntax trees remain owned by the LSP document manager.
func (idx *AdminComponentIndexer) UpdateLiveDocument(
	filePath string,
	root *cst.Node,
	source string,
	lineIndex *cst.LineIndex,
) {
	if idx == nil || filePath == "" || !isAdministrationSourcePath(filePath) {
		return
	}
	filePath = filepath.Clean(filePath)
	extension := strings.ToLower(filepath.Ext(filePath))
	switch extension {
	case ".ts":
		typeFile := parseAdminTypeFile(filePath, source, lineIndex)
		var legacy *liveLegacyDocument
		if root != nil {
			parsed := parseLiveLegacyDocument(filePath, root, lineIndex)
			legacy = &parsed
		}
		idx.liveDocumentMu.Lock()
		idx.liveTypeFiles[filePath] = typeFile
		if legacy != nil {
			idx.liveLegacyDocuments[filePath] = cloneLiveLegacyDocument(*legacy)
		}
		idx.liveDocumentMu.Unlock()
		idx.invalidateTemplateComponentCache()
	case ".js":
		if root == nil {
			return
		}
		legacy := parseLiveLegacyDocument(filePath, root, lineIndex)
		idx.liveDocumentMu.Lock()
		idx.liveLegacyDocuments[filePath] = cloneLiveLegacyDocument(legacy)
		idx.liveDocumentMu.Unlock()
		idx.invalidateTemplateComponentCache()
	case ".twig":
		if root == nil {
			return
		}
		template := parseTemplateTree(root, source, lineIndex)
		setTemplateSourcePaths(&template, filePath)
		idx.liveDocumentMu.Lock()
		idx.liveTwigTemplates[filePath] = cloneTemplateParseResult(template)
		idx.liveDocumentMu.Unlock()
		idx.invalidateTemplateComponentCache()
	case ".vue":
		if root == nil {
			return
		}
		runtime := parseLiveRuntimeDocument(filePath, root, lineIndex)
		definition, liveTypes, found := parseLiveVueDefinition(
			filePath, root, source, lineIndex,
		)
		idx.liveDocumentMu.Lock()
		idx.liveRuntimeDocuments[filePath] = cloneLiveLegacyDocument(runtime)
		if len(liveTypes) > 0 {
			idx.liveTypeFiles[filePath] = liveTypes[0]
		}
		if found && definition != nil {
			idx.liveVueDocuments[filePath] = cloneComponentDefinition(*definition)
		} else {
			delete(idx.liveVueDocuments, filePath)
		}
		idx.liveDocumentMu.Unlock()
		idx.invalidateTemplateComponentCache()
	}
}

// RemoveLiveDocument discards an editor overlay on didClose. Subsequent
// lookups immediately fall back to the current persisted index generation.
func (idx *AdminComponentIndexer) RemoveLiveDocument(filePath string) {
	if idx == nil || filePath == "" {
		return
	}
	filePath = filepath.Clean(filePath)
	idx.liveDocumentMu.Lock()
	delete(idx.liveVueDocuments, filePath)
	delete(idx.liveLegacyDocuments, filePath)
	delete(idx.liveRuntimeDocuments, filePath)
	delete(idx.liveTwigTemplates, filePath)
	delete(idx.liveTypeFiles, filePath)
	idx.liveDocumentMu.Unlock()
	idx.invalidateTemplateComponentCache()
}

func parseLiveLegacyDocument(
	filePath string,
	root *cst.Node,
	lineIndex *cst.LineIndex,
) liveLegacyDocument {
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(root.Text())
	}
	result := parseLiveRuntimeDocument(filePath, root, lineIndex)
	result.Registrations = parseComponentRegistrationsWithLineIndex(
		root, filePath, lineIndex,
	)
	for index := range result.Registrations {
		if result.Registrations[index].InlineDefinition != nil {
			definition := cloneComponentDefinition(
				*result.Registrations[index].InlineDefinition,
			)
			result.Registrations[index].InlineDefinition = &definition
		}
	}
	exports := jsquery.ExportDefaults(root)
	if len(exports) == 0 || componentDefinitionObject(
		jsquery.ExportDefaultExpression(exports[0]),
	) == nil {
		return result
	}
	definition := ParseComponentDefinitionWithLineIndex(root, lineIndex)
	if definition == nil {
		return result
	}
	setDefinitionFilePath(definition, filePath)
	if definition.TemplatePath != "" && !filepath.IsAbs(definition.TemplatePath) {
		definition.TemplatePath = ResolveTemplatePath(
			filePath, definition.TemplatePath,
		)
	}
	if definition.TemplatePath != "" {
		if template, err := ParseTemplateFromFile(
			definition.TemplatePath,
		); err == nil {
			definition.Slots = template.Slots
			definition.Blocks = template.Blocks
		}
	}
	result.Definition = cloneComponentDefinition(*definition)
	result.HasDefinition = true

	expression := jsquery.ExportDefaultExpression(exports[0])
	callName := jsquery.CallName(expression)
	if callName == "Shopware.Component.wrapComponentConfig" ||
		callName == "Component.wrapComponentConfig" {
		name := deriveComponentNameFromPath(filePath)
		line, _ := lineIndex.Position(exports[0].RangeTrimmedTrivia().Start)
		inline := cloneComponentDefinition(*definition)
		result.Registrations = append(result.Registrations, VueComponent{
			Name: name, FilePath: filePath, DefinitionPath: filePath,
			Line: int(line) + 1, InlineDefinition: &inline,
		})
	}
	return result
}

func parseLiveRuntimeDocument(
	filePath string,
	root *cst.Node,
	lineIndex *cst.LineIndex,
) liveLegacyDocument {
	if root == nil {
		return liveLegacyDocument{}
	}
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(root.Text())
	}
	var result liveLegacyDocument
	result.Mixins, result.Modules = parseMixinsAndModules(
		root, filePath, lineIndex,
	)
	result.Services, result.Stores = parseAdminRuntimeRegistries(
		root, filePath, lineIndex,
	)
	result.StoreFactory = parseAdminStoreFactory(root, filePath, lineIndex)
	result.Directives = parseAdminDirectives(root, filePath, lineIndex)
	result.Filters = parseAdminFilters(root, filePath, lineIndex)
	result.CMS = parseAdminCMSRegistrations(root, filePath, lineIndex)
	result.Privileges = parseAdminPrivileges(root, filePath, lineIndex)
	return result
}

func cloneLiveLegacyDocument(value liveLegacyDocument) liveLegacyDocument {
	result := value
	result.Definition = cloneComponentDefinition(value.Definition)
	result.Registrations = append([]VueComponent(nil), value.Registrations...)
	for index := range result.Registrations {
		result.Registrations[index] = cloneVueComponent(
			result.Registrations[index],
		)
	}
	result.Mixins = append([]AdminMixin(nil), value.Mixins...)
	for index := range result.Mixins {
		result.Mixins[index].Definition = cloneComponentDefinition(
			value.Mixins[index].Definition,
		)
	}
	result.Modules = append([]AdminModule(nil), value.Modules...)
	for index := range result.Modules {
		result.Modules[index].Routes = append(
			[]AdminModuleRoute(nil), value.Modules[index].Routes...,
		)
	}
	result.Services = append([]AdminService(nil), value.Services...)
	result.Stores = append([]AdminStore(nil), value.Stores...)
	for index := range result.Stores {
		result.Stores[index].Members = append(
			[]AdminStoreMember(nil), value.Stores[index].Members...,
		)
	}
	if value.StoreFactory != nil {
		factory := *value.StoreFactory
		factory.Members = append(
			[]AdminStoreMember(nil), value.StoreFactory.Members...,
		)
		result.StoreFactory = &factory
	}
	result.Directives = append([]AdminDirective(nil), value.Directives...)
	result.Filters = append([]AdminFilter(nil), value.Filters...)
	result.CMS = append([]AdminCMSRegistration(nil), value.CMS...)
	for index := range result.CMS {
		result.CMS[index].Slots = append(
			[]AdminCMSReference(nil), value.CMS[index].Slots...,
		)
	}
	result.Privileges = append([]AdminPrivilege(nil), value.Privileges...)
	return result
}

func cloneVueComponent(value VueComponent) VueComponent {
	result := value
	result.Props = append([]VueComponentProp(nil), value.Props...)
	for index := range result.Props {
		result.Props[index].AllowedValues = append(
			[]string(nil), value.Props[index].AllowedValues...,
		)
	}
	result.Emits = append([]string(nil), value.Emits...)
	result.Events = append([]VueComponentEvent(nil), value.Events...)
	result.Methods = append([]string(nil), value.Methods...)
	result.Computed = append([]string(nil), value.Computed...)
	result.Data = append([]string(nil), value.Data...)
	result.Injected = append([]string(nil), value.Injected...)
	result.Mixins = append([]string(nil), value.Mixins...)
	result.LocalComponents = append(
		[]VueLocalComponent(nil), value.LocalComponents...,
	)
	result.LocalDirectives = append(
		[]VueLocalDirective(nil), value.LocalDirectives...,
	)
	result.Members = append([]VueComponentMember(nil), value.Members...)
	for index := range result.Members {
		result.Members[index].ReturnExpressions = append(
			[]string(nil), value.Members[index].ReturnExpressions...,
		)
		result.Members[index].ElementMembers = append(
			[]VueComponentElementMember(nil), value.Members[index].ElementMembers...,
		)
	}
	result.Assignments = append(
		[]VueComponentAssignment(nil), value.Assignments...,
	)
	result.Slots = cloneComponentSlots(value.Slots)
	result.Blocks = cloneTwigBlocks(value.Blocks)
	if value.InlineDefinition != nil {
		definition := cloneComponentDefinition(*value.InlineDefinition)
		result.InlineDefinition = &definition
	}
	result.liveTypeFiles = append(
		[]AdminTypeFile(nil), value.liveTypeFiles...,
	)
	return result
}

func cloneTemplateParseResult(value TemplateParseResult) TemplateParseResult {
	return TemplateParseResult{
		Slots:  cloneComponentSlots(value.Slots),
		Blocks: cloneTwigBlocks(value.Blocks),
	}
}

func cloneComponentSlots(values []VueComponentSlot) []VueComponentSlot {
	result := append([]VueComponentSlot(nil), values...)
	for index := range result {
		result[index].Members = append(
			[]VueComponentSlotMember(nil), values[index].Members...,
		)
	}
	return result
}

func cloneTwigBlocks(values []TwigBlock) []TwigBlock {
	result := append([]TwigBlock(nil), values...)
	for index := range result {
		result[index].ScopeMembers = append(
			[]TwigBlockScopeMember(nil), values[index].ScopeMembers...,
		)
	}
	return result
}

func (idx *AdminComponentIndexer) componentForLiveTwigDocument(
	filePath string,
	root *cst.Node,
	source string,
	lineIndex *cst.LineIndex,
) (*VueComponent, error) {
	component, err := idx.GetComponentByTemplatePath(filePath)
	if err != nil || component == nil {
		return component, err
	}
	if lineIndex == nil {
		lineIndex = cst.NewLineIndex(source)
	}
	template := parseTemplateTree(root, source, lineIndex)
	setTemplateSourcePaths(&template, filePath)
	inheritLiveTemplateBlockContracts(&template, *component, filePath)
	result := cloneVueComponent(*component)
	stripComponentTemplateSource(&result, filePath)
	live := VueComponent{
		Name: result.Name, TemplatePath: filePath,
		Slots: template.Slots, Blocks: template.Blocks,
	}
	result = overlayComponents(result, live)
	return &result, nil
}

func inheritLiveTemplateBlockContracts(
	template *TemplateParseResult,
	effective VueComponent,
	filePath string,
) {
	if template == nil {
		return
	}
	normalized := normalizeDefinitionPath(filePath)
	for index := range template.Blocks {
		block := &template.Blocks[index]
		current, found := effective.ComponentBlock(block.Name)
		if !found {
			continue
		}
		var inherited []TwigBlockScopeMember
		for _, member := range current.ScopeMembers {
			if member.FilePath != "" &&
				normalizeDefinitionPath(member.FilePath) == normalized {
				continue
			}
			inherited = append(inherited, member)
		}
		block.ScopeMembers = overlayBlockScopeMembers(
			inherited, block.ScopeMembers,
		)
		if block.Deprecated == "" {
			block.Deprecated = current.Deprecated
		}
	}
}

func stripComponentTemplateSource(component *VueComponent, filePath string) {
	if component == nil || filePath == "" {
		return
	}
	normalized := normalizeDefinitionPath(filePath)
	slots := make([]VueComponentSlot, 0, len(component.Slots))
	for _, slot := range component.Slots {
		if normalizeDefinitionPath(slot.FilePath) != normalized {
			slots = append(slots, slot)
		}
	}
	component.Slots = slots
	blocks := make([]TwigBlock, 0, len(component.Blocks))
	for _, block := range component.Blocks {
		if normalizeDefinitionPath(block.FilePath) != normalized {
			blocks = append(blocks, block)
		}
	}
	component.Blocks = blocks
}

func (idx *AdminComponentIndexer) applyLiveTwigTemplate(
	definition *ComponentDefinition,
) {
	if idx == nil || definition == nil || definition.TemplatePath == "" {
		return
	}
	path := filepath.Clean(definition.TemplatePath)
	idx.liveDocumentMu.RLock()
	template, found := idx.liveTwigTemplates[path]
	idx.liveDocumentMu.RUnlock()
	if !found {
		return
	}
	template = cloneTemplateParseResult(template)
	definition.Slots = template.Slots
	definition.Blocks = template.Blocks
}

func (idx *AdminComponentIndexer) liveLegacyDefinition(
	filePath string,
) (ComponentDefinition, bool, bool, error) {
	if idx == nil || filePath == "" {
		return ComponentDefinition{}, false, false, nil
	}
	path := filepath.Clean(filePath)
	idx.liveDocumentMu.RLock()
	document, found := idx.liveLegacyDocuments[path]
	if !found {
		normalized := normalizeDefinitionPath(path)
		for candidatePath, candidate := range idx.liveLegacyDocuments {
			if normalizeDefinitionPath(candidatePath) == normalized {
				document, found = candidate, true
				break
			}
		}
	}
	idx.liveDocumentMu.RUnlock()
	if !found {
		return ComponentDefinition{}, false, false, nil
	}
	if !document.HasDefinition {
		return ComponentDefinition{}, false, true, nil
	}
	definition := cloneComponentDefinition(document.Definition)
	idx.applyLiveTwigTemplate(&definition)
	if err := idx.enrichScriptSetupTypeContracts(&definition); err != nil {
		return ComponentDefinition{}, false, true, err
	}
	return definition, true, true, nil
}

func (idx *AdminComponentIndexer) isLiveLegacyDocumentPath(filePath string) bool {
	if idx == nil || filePath == "" {
		return false
	}
	path := filepath.Clean(filePath)
	normalized := normalizeDefinitionPath(path)
	idx.liveDocumentMu.RLock()
	defer idx.liveDocumentMu.RUnlock()
	if _, found := idx.liveLegacyDocuments[path]; found {
		return true
	}
	for candidate := range idx.liveLegacyDocuments {
		if normalizeDefinitionPath(candidate) == normalized {
			return true
		}
	}
	return false
}

func (idx *AdminComponentIndexer) definitionWithLiveTemplate(
	definition ComponentDefinition,
) (ComponentDefinition, error) {
	result := cloneComponentDefinition(definition)
	idx.applyLiveTwigTemplate(&result)
	if err := idx.enrichScriptSetupTypeContracts(&result); err != nil {
		return ComponentDefinition{}, err
	}
	return result, nil
}

func (idx *AdminComponentIndexer) registrationsWithLiveDocuments(
	persisted []VueComponent,
	name string,
) []VueComponent {
	if idx == nil {
		return persisted
	}
	idx.liveDocumentMu.RLock()
	documents := make(map[string]liveLegacyDocument, len(idx.liveLegacyDocuments))
	for path, document := range idx.liveLegacyDocuments {
		documents[path] = cloneLiveLegacyDocument(document)
	}
	idx.liveDocumentMu.RUnlock()
	if len(documents) == 0 {
		return persisted
	}
	result := make([]VueComponent, 0, len(persisted)+len(documents))
	for _, component := range persisted {
		if _, replaced := documents[filepath.Clean(component.FilePath)]; replaced {
			continue
		}
		if name == "" || component.Name == name {
			result = append(result, component)
		}
	}
	paths := make([]string, 0, len(documents))
	for path := range documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		for _, component := range documents[path].Registrations {
			if name == "" || component.Name == name {
				result = append(result, cloneVueComponent(component))
			}
		}
	}
	return result
}

func (idx *AdminComponentIndexer) liveLegacyDocumentSnapshots() []liveLegacyDocumentSnapshot {
	if idx == nil {
		return nil
	}
	idx.liveDocumentMu.RLock()
	documents := make(
		map[string]liveLegacyDocument,
		len(idx.liveLegacyDocuments)+len(idx.liveRuntimeDocuments),
	)
	for path, document := range idx.liveLegacyDocuments {
		documents[path] = document
	}
	for path, document := range idx.liveRuntimeDocuments {
		documents[path] = document
	}
	paths := make([]string, 0, len(documents))
	for path := range documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	result := make([]liveLegacyDocumentSnapshot, 0, len(paths))
	for _, path := range paths {
		result = append(result, liveLegacyDocumentSnapshot{
			Path:     path,
			Document: cloneLiveLegacyDocument(documents[path]),
		})
	}
	idx.liveDocumentMu.RUnlock()
	return result
}

// overlayLiveLegacyValues applies editor snapshots as file replacements. An
// open source file therefore shadows every persisted symbol it previously
// owned, including symbols deleted or temporarily incomplete in the buffer.
func overlayLiveLegacyValues[T any](
	persisted []T,
	documents []liveLegacyDocumentSnapshot,
	filePath func(T) string,
	values func(liveLegacyDocument) []T,
	match func(T) bool,
) []T {
	if len(documents) == 0 {
		if match == nil {
			return persisted
		}
		result := make([]T, 0, len(persisted))
		for _, value := range persisted {
			if match(value) {
				result = append(result, value)
			}
		}
		return result
	}
	replaced := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		replaced[filepath.Clean(document.Path)] = struct{}{}
	}
	result := make([]T, 0, len(persisted)+len(documents))
	for _, value := range persisted {
		if _, found := replaced[filepath.Clean(filePath(value))]; found {
			continue
		}
		if match == nil || match(value) {
			result = append(result, value)
		}
	}
	for _, document := range documents {
		for _, value := range values(document.Document) {
			if match == nil || match(value) {
				result = append(result, value)
			}
		}
	}
	return result
}

func (idx *AdminComponentIndexer) liveVueComponent(
	filePath string,
) (VueComponent, bool, error) {
	if idx == nil || filePath == "" {
		return VueComponent{}, false, nil
	}
	filePath = filepath.Clean(filePath)
	idx.liveDocumentMu.RLock()
	definition, found := idx.liveVueDocuments[filePath]
	typeFile, typeFound := idx.liveTypeFiles[filePath]
	idx.liveDocumentMu.RUnlock()
	if !found {
		return VueComponent{}, false, nil
	}
	liveTypes := []AdminTypeFile(nil)
	if typeFound {
		liveTypes = append(liveTypes, typeFile)
	}
	component, err := idx.componentForLiveVueDefinition(
		filePath, definition, liveTypes,
	)
	if err != nil || component == nil {
		return VueComponent{}, false, err
	}
	return *component, true, nil
}

func (idx *AdminComponentIndexer) componentWithLiveVueDocument(
	component VueComponent,
) (VueComponent, error) {
	for _, candidate := range []string{
		component.DefinitionPath, component.TemplatePath, component.FilePath,
	} {
		live, found, err := idx.liveVueComponent(candidate)
		if err != nil {
			return VueComponent{}, err
		}
		if !found {
			continue
		}
		if component.Name != "" {
			live.Name = component.Name
		}
		live.Kind = component.Kind
		live.TargetComponent = component.TargetComponent
		live.ExtendsComponent = component.ExtendsComponent
		live.ImportPath = component.ImportPath
		if component.FilePath != "" {
			live.FilePath = component.FilePath
		}
		if component.DefinitionPath != "" {
			live.DefinitionPath = component.DefinitionPath
		}
		if component.Line != 0 {
			live.Line = component.Line
		}
		return live, nil
	}
	return component, nil
}

func (idx *AdminComponentIndexer) liveTypeFileOverlays(
	requestFiles []AdminTypeFile,
) map[string]AdminTypeFile {
	if idx == nil {
		return adminTypeFileOverlays(requestFiles)
	}
	idx.liveDocumentMu.RLock()
	result := make(map[string]AdminTypeFile, len(idx.liveTypeFiles)+len(requestFiles))
	for path, file := range idx.liveTypeFiles {
		result[path] = file
	}
	idx.liveDocumentMu.RUnlock()
	for _, file := range requestFiles {
		if file.FilePath != "" {
			result[filepath.Clean(file.FilePath)] = file
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func cloneComponentDefinition(value ComponentDefinition) ComponentDefinition {
	result := value
	result.Props = append([]VueComponentProp(nil), value.Props...)
	for index := range result.Props {
		result.Props[index].AllowedValues = append(
			[]string(nil), value.Props[index].AllowedValues...,
		)
	}
	result.Emits = append([]string(nil), value.Emits...)
	result.Events = append([]VueComponentEvent(nil), value.Events...)
	result.Methods = append([]string(nil), value.Methods...)
	result.Computed = append([]string(nil), value.Computed...)
	result.Data = append([]string(nil), value.Data...)
	result.Injected = append([]string(nil), value.Injected...)
	result.Mixins = append([]string(nil), value.Mixins...)
	result.LocalComponents = append(
		[]VueLocalComponent(nil), value.LocalComponents...,
	)
	result.LocalDirectives = append(
		[]VueLocalDirective(nil), value.LocalDirectives...,
	)
	result.Members = append([]VueComponentMember(nil), value.Members...)
	for index := range result.Members {
		result.Members[index].ReturnExpressions = append(
			[]string(nil), value.Members[index].ReturnExpressions...,
		)
		result.Members[index].ElementMembers = append(
			[]VueComponentElementMember(nil), value.Members[index].ElementMembers...,
		)
	}
	result.Assignments = append(
		[]VueComponentAssignment(nil), value.Assignments...,
	)
	result.Slots = append([]VueComponentSlot(nil), value.Slots...)
	for index := range result.Slots {
		result.Slots[index].Members = append(
			[]VueComponentSlotMember(nil), value.Slots[index].Members...,
		)
	}
	result.Blocks = append([]TwigBlock(nil), value.Blocks...)
	for index := range result.Blocks {
		result.Blocks[index].ScopeMembers = append(
			[]TwigBlockScopeMember(nil), value.Blocks[index].ScopeMembers...,
		)
	}
	result.ScriptSetupPropTypes = append(
		[]string(nil), value.ScriptSetupPropTypes...,
	)
	result.ScriptSetupEventTypes = append(
		[]string(nil), value.ScriptSetupEventTypes...,
	)
	result.ScriptSetupSlotTypes = append(
		[]string(nil), value.ScriptSetupSlotTypes...,
	)
	result.ScriptSetupPropDefaults = append(
		[]ScriptSetupPropDefault(nil), value.ScriptSetupPropDefaults...,
	)
	result.ScriptSetupPropBindings = append(
		[]ScriptSetupPropBinding(nil), value.ScriptSetupPropBindings...,
	)
	return result
}

func isVueComponentPath(filePath string) bool {
	return filepath.Ext(filePath) == ".vue"
}

func stripComponentDefinition(
	component *VueComponent,
	definition ComponentDefinition,
	filePath string,
) {
	if component == nil {
		return
	}
	propNames := make(map[string]bool, len(definition.Props))
	for _, prop := range definition.Props {
		propNames[prop.Name] = true
	}
	component.Props = filterComponentProps(component.Props, propNames)
	eventNames := make(map[string]bool, len(definition.Events))
	for _, event := range definition.Events {
		eventNames[CanonicalEventName(event.Name)] = true
	}
	component.Events = filterComponentEvents(component.Events, eventNames)
	component.Emits = filterComponentNames(component.Emits, eventNames)
	component.Methods = filterComponentNames(
		component.Methods, stringSet(definition.Methods),
	)
	component.Computed = filterComponentNames(
		component.Computed, stringSet(definition.Computed),
	)
	component.Data = filterComponentNames(
		component.Data, stringSet(definition.Data),
	)
	component.Injected = filterComponentNames(
		component.Injected, stringSet(definition.Injected),
	)
	memberNames := make(map[string]bool, len(definition.Members))
	for _, member := range definition.Members {
		memberNames[member.Name] = true
	}
	component.Members = filterComponentMembers(
		component.Members, filePath, memberNames,
	)
	component.Assignments = filterComponentAssignments(
		component.Assignments, filePath,
	)
	component.LocalComponents = filterLocalComponents(
		component.LocalComponents, filePath,
	)
	component.LocalDirectives = filterLocalDirectives(
		component.LocalDirectives, filePath,
	)
	component.Slots = filterComponentSlots(
		component.Slots, slotIdentitySet(definition.Slots),
	)
	component.Blocks = filterComponentBlocks(
		component.Blocks, stringSetBlocks(definition.Blocks),
	)
	if definition.ModelProp != "" || definition.ModelEvent != "" {
		component.ModelProp = ""
		component.ModelEvent = ""
	}
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func stringSetBlocks(values []TwigBlock) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value.Name] = true
	}
	return result
}

func slotIdentitySet(values []VueComponentSlot) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value.identityKey()] = true
	}
	return result
}

func filterComponentProps(
	values []VueComponentProp,
	remove map[string]bool,
) []VueComponentProp {
	result := make([]VueComponentProp, 0, len(values))
	for _, value := range values {
		if !remove[value.Name] {
			result = append(result, value)
		}
	}
	return result
}

func filterComponentEvents(
	values []VueComponentEvent,
	remove map[string]bool,
) []VueComponentEvent {
	result := make([]VueComponentEvent, 0, len(values))
	for _, value := range values {
		if !remove[CanonicalEventName(value.Name)] {
			result = append(result, value)
		}
	}
	return result
}

func filterComponentNames(values []string, remove map[string]bool) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !remove[value] && !remove[CanonicalEventName(value)] {
			result = append(result, value)
		}
	}
	return result
}

func filterComponentMembers(
	values []VueComponentMember,
	filePath string,
	remove map[string]bool,
) []VueComponentMember {
	result := make([]VueComponentMember, 0, len(values))
	for _, value := range values {
		if !remove[value.Name] &&
			normalizeDefinitionPath(value.FilePath) != normalizeDefinitionPath(filePath) {
			result = append(result, value)
		}
	}
	return result
}

func filterComponentAssignments(
	values []VueComponentAssignment,
	filePath string,
) []VueComponentAssignment {
	result := make([]VueComponentAssignment, 0, len(values))
	for _, value := range values {
		if normalizeDefinitionPath(value.FilePath) != normalizeDefinitionPath(filePath) {
			result = append(result, value)
		}
	}
	return result
}

func filterLocalComponents(
	values []VueLocalComponent,
	filePath string,
) []VueLocalComponent {
	result := make([]VueLocalComponent, 0, len(values))
	for _, value := range values {
		if normalizeDefinitionPath(value.FilePath) != normalizeDefinitionPath(filePath) {
			result = append(result, value)
		}
	}
	return result
}

func filterLocalDirectives(
	values []VueLocalDirective,
	filePath string,
) []VueLocalDirective {
	result := make([]VueLocalDirective, 0, len(values))
	for _, value := range values {
		if normalizeDefinitionPath(value.FilePath) != normalizeDefinitionPath(filePath) {
			result = append(result, value)
		}
	}
	return result
}

func filterComponentSlots(
	values []VueComponentSlot,
	remove map[string]bool,
) []VueComponentSlot {
	result := make([]VueComponentSlot, 0, len(values))
	for _, value := range values {
		if !remove[value.identityKey()] {
			result = append(result, value)
		}
	}
	return result
}

func filterComponentBlocks(
	values []TwigBlock,
	remove map[string]bool,
) []TwigBlock {
	result := make([]TwigBlock, 0, len(values))
	for _, value := range values {
		if !remove[value.Name] {
			result = append(result, value)
		}
	}
	return result
}
