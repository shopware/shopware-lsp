package admin

import (
	"errors"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	vueparser "github.com/shopware/shopware-lsp/internal/parser/vue"
)

type AdminComponentIndexer struct {
	componentIndex       *indexer.DataIndexer[VueComponent]
	definitionIndex      *indexer.DataIndexer[ComponentDefinition]
	mixinIndex           *indexer.DataIndexer[AdminMixin]
	directiveIndex       *indexer.DataIndexer[AdminDirective]
	filterIndex          *indexer.DataIndexer[AdminFilter]
	cmsIndex             *indexer.DataIndexer[AdminCMSRegistration]
	moduleIndex          *indexer.DataIndexer[AdminModule]
	serviceIndex         *indexer.DataIndexer[AdminService]
	storeIndex           *indexer.DataIndexer[AdminStore]
	storeFactoryIndex    *indexer.DataIndexer[AdminStoreFactory]
	privilegeIndex       *indexer.DataIndexer[AdminPrivilege]
	usageIndex           *indexer.DataIndexer[AdminUsageSet]
	typeIndex            *indexer.DataIndexer[AdminTypeFile]
	templateCacheMu      sync.RWMutex
	templateCache        map[string]string
	templateCatalogBuilt bool
	effectiveCacheMu     sync.RWMutex
	effectiveCache       map[string]VueComponent
	effectiveCacheEpoch  uint64
	liveDocumentMu       sync.RWMutex
	liveVueDocuments     map[string]ComponentDefinition
	liveLegacyDocuments  map[string]liveLegacyDocument
	liveRuntimeDocuments map[string]liveLegacyDocument
	liveTwigTemplates    map[string]TemplateParseResult
	liveTypeFiles        map[string]AdminTypeFile
}

func NewAdminComponentIndexer(
	configDir string,
	stores ...*indexer.Store,
) (*AdminComponentIndexer, error) {
	opening := adminRepositoryOpening{configDir: configDir, stores: stores}
	defer opening.closeOnError()

	componentIndex := openAdminRepository[VueComponent](
		&opening, "admin_component.db", "admin.components",
	)
	definitionIndex := openAdminRepository[ComponentDefinition](
		&opening, "admin_component_definition.db", "admin.definitions",
	)
	mixinIndex := openAdminRepository[AdminMixin](
		&opening, "admin_mixin.db", "admin.mixins",
	)
	directiveIndex := openAdminRepository[AdminDirective](
		&opening, "admin_directive.db", "admin.directives",
	)
	filterIndex := openAdminRepository[AdminFilter](
		&opening, "admin_filter.db", "admin.filters",
	)
	cmsIndex := openAdminRepository[AdminCMSRegistration](
		&opening, "admin_cms.db", "admin.cms",
	)
	moduleIndex := openAdminRepository[AdminModule](
		&opening, "admin_module.db", "admin.modules",
	)
	serviceIndex := openAdminRepository[AdminService](
		&opening, "admin_service.db", "admin.services",
	)
	storeIndex := openAdminRepository[AdminStore](
		&opening, "admin_store.db", "admin.stores",
	)
	storeFactoryIndex := openAdminRepository[AdminStoreFactory](
		&opening, "admin_store_factory.db", "admin.store_factories",
	)
	privilegeIndex := openAdminRepository[AdminPrivilege](
		&opening, "admin_privilege.db", "admin.privileges",
	)
	usageIndex := openAdminRepository[AdminUsageSet](
		&opening, "admin_usage.db", "admin.usages",
	)
	typeIndex := openAdminRepository[AdminTypeFile](
		&opening, "admin_type.db", "admin.types",
	)
	if opening.err != nil {
		return nil, opening.err
	}

	result := &AdminComponentIndexer{
		componentIndex:       componentIndex,
		definitionIndex:      definitionIndex,
		mixinIndex:           mixinIndex,
		directiveIndex:       directiveIndex,
		filterIndex:          filterIndex,
		cmsIndex:             cmsIndex,
		moduleIndex:          moduleIndex,
		serviceIndex:         serviceIndex,
		storeIndex:           storeIndex,
		storeFactoryIndex:    storeFactoryIndex,
		privilegeIndex:       privilegeIndex,
		usageIndex:           usageIndex,
		typeIndex:            typeIndex,
		liveVueDocuments:     make(map[string]ComponentDefinition),
		liveLegacyDocuments:  make(map[string]liveLegacyDocument),
		liveRuntimeDocuments: make(map[string]liveLegacyDocument),
		liveTwigTemplates:    make(map[string]TemplateParseResult),
		liveTypeFiles:        make(map[string]AdminTypeFile),
	}
	opening.committed = true
	return result, nil
}

type adminRepositoryOpening struct {
	configDir string
	stores    []*indexer.Store
	opened    []func() error
	err       error
	committed bool
}

func openAdminRepository[T any](
	opening *adminRepositoryOpening,
	fileName,
	namespace string,
) *indexer.DataIndexer[T] {
	if opening.err != nil {
		return nil
	}
	repository, err := indexer.NewRepository[T](
		path.Join(opening.configDir, fileName), namespace, opening.stores...,
	)
	if err != nil {
		opening.err = err
		return nil
	}
	opening.opened = append(opening.opened, repository.Close)
	return repository
}

func (opening *adminRepositoryOpening) closeOnError() {
	if opening.committed {
		return
	}
	for index := len(opening.opened) - 1; index >= 0; index-- {
		_ = opening.opened[index]()
	}
}

func (idx *AdminComponentIndexer) ID() string {
	return "admin.component.indexer"
}

func (idx *AdminComponentIndexer) Index(file *indexer.ParsedFile) error {
	filePath := file.Path
	if isMeteorDeclarationPath(filePath) {
		idx.invalidateTemplateComponentCache()
		if err := idx.saveTypeFile(
			file.Mutation(), filePath, string(file.Source), file.LineIndex(),
		); err != nil {
			return err
		}
		component := parseMeteorDeclaration(filePath, file.Source)
		batch := map[string]map[string]VueComponent{filePath: {}}
		if component != nil {
			batch[filePath][component.Name] = *component
			addAdminComponentWorkspaceSymbols(file, *component)
		}
		if err := idx.componentIndex.BatchSaveItemsIn(file.Mutation(), batch); err != nil {
			return err
		}
		return idx.saveUsages(file.Mutation(), filePath, nil)
	}
	ext := file.Extension()
	if ext == ".twig" && isAdministrationSourcePath(filePath) {
		idx.invalidateTemplateComponentCache()
		tree := file.SyntaxTree()
		if tree == nil {
			return idx.saveUsages(file.Mutation(), filePath, nil)
		}
		return idx.saveUsages(
			file.Mutation(), filePath,
			parseAdminTwigUsages(tree.Root, filePath, file.LineIndex()),
		)
	}
	if ext != ".js" && ext != ".ts" && ext != ".vue" {
		return nil
	}

	// Generated Vite, Storybook and test bundles can be several megabytes and
	// repeat registry-looking strings without declaring project symbols. The
	// Administration extension contract puts source under this directory.
	if !isAdministrationSourcePath(filePath) {
		return nil
	}
	idx.invalidateTemplateComponentCache()
	tree := file.SyntaxTree()
	if tree == nil || tree.Root == nil {
		return nil
	}
	root := tree.Root
	lineIndex := file.LineIndex()
	vueSections := []vueparser.Section(nil)
	vueUsesTypeScript := false
	if ext == ".vue" {
		vueSections = vueparser.Sections(file.Source)
		for _, section := range vueSections {
			if section.Kind == vueparser.SectionScript &&
				strings.EqualFold(section.Language, "ts") {
				vueUsesTypeScript = true
				break
			}
		}
	}
	if ext == ".ts" || vueUsesTypeScript {
		typeSource := string(file.Source)
		if ext == ".vue" {
			typeSource = vueScriptTypeSource(typeSource, vueSections)
		}
		if err := idx.saveTypeFile(
			file.Mutation(), filePath, typeSource, lineIndex,
		); err != nil {
			return err
		}
	}
	usages := parseAdminJavaScriptUsages(root, filePath, lineIndex)
	if ext == ".vue" {
		usages = append(
			usages,
			parseAdminTwigUsages(root, filePath, lineIndex)...,
		)
	}
	if err := idx.saveUsages(file.Mutation(), filePath, usages); err != nil {
		return err
	}
	if err := idx.componentIndex.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]VueComponent{filePath: {}},
	); err != nil {
		return err
	}
	if err := idx.definitionIndex.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]ComponentDefinition{filePath: {}},
	); err != nil {
		return err
	}
	if err := idx.mixinIndex.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]AdminMixin{filePath: {}},
	); err != nil {
		return err
	}
	if err := idx.moduleIndex.BatchSaveItemsIn(
		file.Mutation(),
		map[string]map[string]AdminModule{filePath: {}},
	); err != nil {
		return err
	}
	// Try to parse component registrations (Shopware.Component.register/extend or Component.register/extend)
	if err := idx.indexRegistrations(file, file.Mutation(), filePath, root, lineIndex); err != nil {
		return err
	}
	if err := idx.indexMixinsAndModules(file, file.Mutation(), filePath, root, lineIndex); err != nil {
		return err
	}
	if err := idx.indexRuntimeRegistries(file, file.Mutation(), filePath, root, lineIndex); err != nil {
		return err
	}

	// Try to parse wrapped component configs (export default Shopware.Component.wrapComponentConfig({...}))
	// Returns true if this file was a wrapComponentConfig file
	handledByWrap, err := idx.indexWrappedComponents(file, file.Mutation(), filePath, root, lineIndex)
	if err != nil {
		return err
	}

	// Try to parse component definitions (export default { ... })
	// Skip if already handled by wrapComponentConfig to avoid duplicate indexing
	if !handledByWrap {
		var definitionErr error
		if ext == ".vue" {
			definitionErr = idx.indexVueDefinition(
				file.Mutation(), filePath, root, file.Source, lineIndex, vueSections,
			)
		} else {
			definitionErr = idx.indexDefinition(
				file.Mutation(), filePath, root, lineIndex,
			)
		}
		if definitionErr != nil {
			return definitionErr
		}
	}

	return nil
}

func (idx *AdminComponentIndexer) saveTypeFile(
	mutation *indexer.Mutation,
	filePath,
	source string,
	lineIndex *cst.LineIndex,
) error {
	if idx == nil || idx.typeIndex == nil {
		return nil
	}
	typeFile := parseAdminTypeFile(filePath, source, lineIndex)
	items := map[string]AdminTypeFile{}
	if len(typeFile.Declarations) > 0 || len(typeFile.Imports) > 0 {
		items[filePath] = typeFile
	}
	return idx.typeIndex.BatchSaveItemsIn(
		mutation, map[string]map[string]AdminTypeFile{filePath: items},
	)
}

func vueScriptTypeSource(
	source string,
	sections []vueparser.Section,
) string {
	masked := []byte(source)
	for index := range masked {
		if masked[index] != '\n' && masked[index] != '\r' {
			masked[index] = ' '
		}
	}
	for _, section := range sections {
		if section.Kind != vueparser.SectionScript ||
			section.BodyRange.End > uint32(len(source)) {
			continue
		}
		copy(
			masked[section.BodyRange.Start:section.BodyRange.End],
			source[section.BodyRange.Start:section.BodyRange.End],
		)
	}
	return string(masked)
}

func isAdministrationSourcePath(filePath string) bool {
	normalized := filepath.ToSlash(filepath.Clean(filePath))
	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}
	return strings.Contains(normalized, "/Resources/app/administration/src/")
}

func (idx *AdminComponentIndexer) saveUsages(
	mutation *indexer.Mutation,
	filePath string,
	usages []AdminUsageSet,
) error {
	batch := map[string]map[string]AdminUsageSet{filePath: {}}
	for _, usage := range usages {
		batch[filePath][AdminUsageKey(usage.Kind, usage.Owner, usage.Name)] = usage
	}
	return idx.usageIndex.BatchSaveItemsIn(mutation, batch)
}

func (idx *AdminComponentIndexer) ShouldEnterDirectory(directory string) bool {
	relative, matched := meteorPackageRelativePath(directory)
	if !matched {
		return false
	}
	relative = filepath.ToSlash(relative)
	switch relative {
	case "", "@shopware-ag", meteorPackagePath,
		meteorPackagePath + "/dist", meteorPackagePath + "/dist/esm":
		return true
	default:
		return false
	}
}

func (idx *AdminComponentIndexer) ShouldIndexPath(filePath string) bool {
	return isMeteorDeclarationPath(filePath)
}

func (idx *AdminComponentIndexer) ShouldPreparsePath(string) bool {
	return false
}

func meteorPackageRelativePath(filePath string) (string, bool) {
	normalized := filepath.ToSlash(filepath.Clean(filePath))
	marker := "/Resources/app/administration/node_modules/"
	position := strings.Index(normalized, marker)
	if position < 0 {
		if strings.HasSuffix(normalized, strings.TrimSuffix(marker, "/")) {
			return "", true
		}
		return "", false
	}
	return strings.TrimPrefix(normalized[position+len(marker):], "/"), true
}

func isMeteorDeclarationPath(filePath string) bool {
	relative, matched := meteorPackageRelativePath(filePath)
	if !matched {
		return false
	}
	normalized := filepath.ToSlash(relative)
	if !strings.HasPrefix(normalized, meteorPackagePath+"/dist/esm/") {
		return false
	}
	base := filepath.Base(normalized)
	return strings.HasPrefix(base, "Mt") && strings.HasSuffix(base, ".d.ts")
}

func (idx *AdminComponentIndexer) indexRuntimeRegistries(
	file *indexer.ParsedFile,
	mutation *indexer.Mutation,
	filePath string,
	root *jssyntax.Node,
	lineIndex *cst.LineIndex,
) error {
	services, stores := parseAdminRuntimeRegistries(root, filePath, lineIndex)
	serviceBatch := map[string]map[string]AdminService{filePath: {}}
	for _, service := range services {
		serviceBatch[filePath][service.Name] = service
	}
	if err := idx.serviceIndex.BatchSaveItemsIn(mutation, serviceBatch); err != nil {
		return err
	}
	storeBatch := map[string]map[string]AdminStore{filePath: {}}
	for _, store := range stores {
		storeBatch[filePath][store.Name] = store
	}
	if err := idx.storeIndex.BatchSaveItemsIn(mutation, storeBatch); err != nil {
		return err
	}
	factoryBatch := map[string]map[string]AdminStoreFactory{filePath: {}}
	if factory := parseAdminStoreFactory(root, filePath, lineIndex); factory != nil {
		factoryBatch[filePath][normalizeDefinitionPath(filePath)] = *factory
	}
	if err := idx.storeFactoryIndex.BatchSaveItemsIn(mutation, factoryBatch); err != nil {
		return err
	}
	directives := parseAdminDirectives(root, filePath, lineIndex)
	directiveBatch := map[string]map[string]AdminDirective{filePath: {}}
	for _, directive := range directives {
		directiveBatch[filePath][directive.Name] = directive
	}
	if err := idx.directiveIndex.BatchSaveItemsIn(
		mutation, directiveBatch,
	); err != nil {
		return err
	}
	filters := parseAdminFilters(root, filePath, lineIndex)
	filterBatch := map[string]map[string]AdminFilter{filePath: {}}
	for _, filter := range filters {
		filterBatch[filePath][filter.Name] = filter
	}
	if err := idx.filterIndex.BatchSaveItemsIn(mutation, filterBatch); err != nil {
		return err
	}
	cmsRegistrations := parseAdminCMSRegistrations(
		root, filePath, lineIndex,
	)
	cmsBatch := map[string]map[string]AdminCMSRegistration{filePath: {}}
	for _, registration := range cmsRegistrations {
		cmsBatch[filePath][AdminCMSKey(registration.Kind, registration.Name)] =
			registration
	}
	if err := idx.cmsIndex.BatchSaveItemsIn(mutation, cmsBatch); err != nil {
		return err
	}
	privileges := parseAdminPrivileges(root, filePath, lineIndex)
	privilegeBatch := map[string]map[string]AdminPrivilege{filePath: {}}
	for _, privilege := range privileges {
		privilegeBatch[filePath][privilege.Name] = privilege
	}
	if err := idx.privilegeIndex.BatchSaveItemsIn(mutation, privilegeBatch); err != nil {
		return err
	}
	addAdminRuntimeWorkspaceSymbols(
		file, services, stores, directives, filters, cmsRegistrations, privileges,
	)
	return nil
}

func (idx *AdminComponentIndexer) indexMixinsAndModules(
	file *indexer.ParsedFile,
	mutation *indexer.Mutation,
	filePath string,
	root *jssyntax.Node,
	lineIndex *cst.LineIndex,
) error {
	mixins, modules := parseMixinsAndModules(root, filePath, lineIndex)
	mixinBatch := map[string]map[string]AdminMixin{filePath: {}}
	for _, mixin := range mixins {
		mixinBatch[filePath][mixin.Name] = mixin
	}
	if err := idx.mixinIndex.BatchSaveItemsIn(mutation, mixinBatch); err != nil {
		return err
	}
	moduleBatch := map[string]map[string]AdminModule{filePath: {}}
	for _, module := range modules {
		moduleBatch[filePath][module.Name] = module
	}
	if err := idx.moduleIndex.BatchSaveItemsIn(mutation, moduleBatch); err != nil {
		return err
	}
	addAdminMixinsAndModulesWorkspaceSymbols(file, mixins, modules)
	return nil
}

// indexRegistrations indexes Shopware.Component.register/extend calls
func (idx *AdminComponentIndexer) indexRegistrations(
	file *indexer.ParsedFile,
	mutation *indexer.Mutation,
	filePath string,
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) error {
	components := parseComponentRegistrationsWithLineIndex(node, filePath, lineIndex)
	if len(components) == 0 {
		return nil
	}

	batchSave := make(map[string]map[string]VueComponent)
	batchSaveDefs := make(map[string]map[string]ComponentDefinition)

	for _, comp := range components {
		if _, ok := batchSave[comp.FilePath]; !ok {
			batchSave[comp.FilePath] = make(map[string]VueComponent)
		}
		batchSave[comp.FilePath][comp.Name] = comp

		// If there's an inline definition, also save it
		if comp.InlineDefinition != nil {
			if _, ok := batchSaveDefs[comp.FilePath]; !ok {
				batchSaveDefs[comp.FilePath] = make(map[string]ComponentDefinition)
			}
			// Use component name as key for inline definitions
			batchSaveDefs[comp.FilePath][comp.Name] = *comp.InlineDefinition
		}
	}

	if err := idx.componentIndex.BatchSaveItemsIn(mutation, batchSave); err != nil {
		return err
	}

	if len(batchSaveDefs) > 0 {
		if err := idx.definitionIndex.BatchSaveItemsIn(mutation, batchSaveDefs); err != nil {
			return err
		}
	}
	addAdminComponentWorkspaceSymbols(file, components...)

	return nil
}

// indexWrappedComponents indexes Shopware.Component.wrapComponentConfig() calls
// These are used for wrapping Meteor component library components
// Returns true if the file was handled (contains wrapComponentConfig), false otherwise
func (idx *AdminComponentIndexer) indexWrappedComponents(
	file *indexer.ParsedFile,
	mutation *indexer.Mutation,
	filePath string,
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) (bool, error) {
	var exportNode, callExpr *jssyntax.Node
	for _, candidate := range jsquery.ExportDefaults(node) {
		expression := jsquery.ExportDefaultExpression(candidate)
		if expression != nil &&
			(jsquery.CallName(expression) == "Shopware.Component.wrapComponentConfig" ||
				jsquery.CallName(expression) == "Component.wrapComponentConfig") {
			exportNode = candidate
			callExpr = expression
			break
		}
	}
	if exportNode == nil || callExpr == nil {
		return false, nil
	}

	// Derive component name from directory name
	// e.g., /path/to/mt-card/index.ts -> "mt-card"
	componentName := deriveComponentNameFromPath(filePath)
	if componentName == "" {
		return true, nil // Still handled, just can't derive name
	}

	configObject := componentDefinitionObject(callExpr)
	if configObject == nil {
		return true, nil
	}

	// Parse the component definition from the config object
	def := parseInlineDefinition(node, configObject, filePath, lineIndex)
	if def != nil {
		def.Deprecated = JavaScriptDeprecation(exportNode)
	}

	// Find template import from the root node and parse slots/blocks
	if def != nil {
		setDefinitionFilePath(def, filePath)
		templatePath := jsquery.ImportPath(node, "template")
		if templatePath != "" {
			templateAbsPath := ResolveTemplatePath(filePath, templatePath)
			def.TemplatePath = templateAbsPath // Store absolute path
			if result, err := ParseTemplateFromFile(templateAbsPath); err == nil {
				def.Slots = result.Slots
				def.Blocks = result.Blocks
			}
		}
	}

	// Create the component entry
	line, _ := lineIndex.Position(exportNode.RangeTrimmedTrivia().Start)
	comp := VueComponent{
		Name:             componentName,
		FilePath:         filePath,
		Line:             int(line) + 1,
		DefinitionPath:   filePath,
		InlineDefinition: def,
	}

	// Copy props/emits/methods/computed/slots/blocks from definition to component
	if def != nil {
		comp.Deprecated = def.Deprecated
		comp.Props = def.Props
		comp.ModelProp = def.ModelProp
		comp.ModelEvent = def.ModelEvent
		comp.Emits = def.Emits
		comp.Events = def.Events
		comp.Methods = def.Methods
		comp.Computed = def.Computed
		comp.Data = def.Data
		comp.Injected = def.Injected
		comp.Mixins = def.Mixins
		comp.LocalComponents = def.LocalComponents
		comp.LocalDirectives = def.LocalDirectives
		comp.Members = def.Members
		comp.Slots = def.Slots
		comp.Blocks = def.Blocks
		comp.TemplatePath = def.TemplatePath
	}

	// Save the component
	batchSave := make(map[string]map[string]VueComponent)
	batchSave[filePath] = map[string]VueComponent{
		componentName: comp,
	}

	if err := idx.componentIndex.BatchSaveItemsIn(mutation, batchSave); err != nil {
		return true, err
	}

	// Also save the definition
	if def != nil {
		batchSaveDefs := make(map[string]map[string]ComponentDefinition)
		batchSaveDefs[filePath] = map[string]ComponentDefinition{
			componentName: *def,
		}
		if err := idx.definitionIndex.BatchSaveItemsIn(mutation, batchSaveDefs); err != nil {
			return true, err
		}
	}
	addAdminComponentWorkspaceSymbols(file, comp)

	return true, nil
}

// deriveComponentNameFromPath extracts the component name from the file path
// e.g., /path/to/mt-card/index.ts -> "mt-card"
// e.g., /path/to/sw-button.js -> "sw-button"
func deriveComponentNameFromPath(filePath string) string {
	dir := filepath.Dir(filePath)
	base := filepath.Base(filePath)

	// If file is an index module/SFC, use directory name.
	if base == "index.js" || base == "index.ts" || base == "index.vue" {
		return filepath.Base(dir)
	}

	// Otherwise use file name without extension
	ext := filepath.Ext(base)
	return strings.TrimSuffix(base, ext)
}

func (idx *AdminComponentIndexer) indexVueDefinition(
	mutation *indexer.Mutation,
	filePath string,
	root *jssyntax.Node,
	source string,
	lineIndex *cst.LineIndex,
	sections []vueparser.Section,
) error {
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
		return nil
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
	return idx.definitionIndex.BatchSaveItemsIn(
		mutation,
		map[string]map[string]ComponentDefinition{
			filePath: {normalizeDefinitionPath(filePath): *definition},
		},
	)
}

func mergeComponentDefinitions(
	base,
	overlay *ComponentDefinition,
) *ComponentDefinition {
	if base == nil {
		base = &ComponentDefinition{}
	}
	if overlay == nil {
		return base
	}
	base.Props = overlayScriptSetupProps(base.Props, overlay.Props)
	base.Members = overlayScriptSetupMembers(base.Members, overlay.Members)
	base.Methods = appendUniqueValues(base.Methods, overlay.Methods...)
	base.Computed = appendUniqueValues(base.Computed, overlay.Computed...)
	base.Data = appendUniqueValues(base.Data, overlay.Data...)
	base.Injected = appendUniqueValues(base.Injected, overlay.Injected...)
	base.Mixins = appendUniqueValues(base.Mixins, overlay.Mixins...)
	base.Emits = appendUniqueValues(base.Emits, overlay.Emits...)
	for _, event := range overlay.Events {
		base.Events = appendComponentEvent(base.Events, event)
	}
	base.ScriptSetupPropTypes = appendUniqueValues(
		base.ScriptSetupPropTypes, overlay.ScriptSetupPropTypes...,
	)
	base.ScriptSetupEventTypes = appendUniqueValues(
		base.ScriptSetupEventTypes, overlay.ScriptSetupEventTypes...,
	)
	base.ScriptSetupSlotTypes = appendUniqueValues(
		base.ScriptSetupSlotTypes, overlay.ScriptSetupSlotTypes...,
	)
	base.ScriptSetupPropDefaults = overlayScriptSetupPropDefaults(
		base.ScriptSetupPropDefaults, overlay.ScriptSetupPropDefaults,
	)
	base.ScriptSetupPropBindings = overlayScriptSetupPropBindings(
		base.ScriptSetupPropBindings, overlay.ScriptSetupPropBindings,
	)
	base.LocalComponents = overlayLocalComponents(
		base.LocalComponents, overlay.LocalComponents,
	)
	base.LocalDirectives = overlayLocalDirectives(
		base.LocalDirectives, overlay.LocalDirectives,
	)
	base.Assignments = append(base.Assignments, overlay.Assignments...)
	base.OpenRuntimeMembers = base.OpenRuntimeMembers || overlay.OpenRuntimeMembers
	if overlay.ModelProp != "" || overlay.ModelEvent != "" {
		base.ModelProp, base.ModelEvent = overlay.ModelProp, overlay.ModelEvent
	}
	if overlay.Deprecated != "" {
		base.Deprecated = overlay.Deprecated
	}
	return base
}

func appendUniqueValues(values []string, additions ...string) []string {
	for _, value := range additions {
		values = appendUnique(values, value)
	}
	return values
}

// indexDefinition indexes component definition files (export default { ... })
func (idx *AdminComponentIndexer) indexDefinition(
	mutation *indexer.Mutation,
	filePath string,
	node *jssyntax.Node,
	lineIndex *cst.LineIndex,
) error {
	exports := jsquery.ExportDefaults(node)
	if len(exports) == 0 {
		return nil
	}
	expression := jsquery.ExportDefaultExpression(exports[0])
	if componentDefinitionObject(expression) == nil {
		return nil
	}

	// Parse the component definition
	def := ParseComponentDefinitionWithLineIndex(node, lineIndex)
	if def == nil {
		return nil
	}

	setDefinitionFilePath(def, filePath)

	// Parse slots and blocks from the template if available
	if def.TemplatePath != "" {
		// If TemplatePath is relative, resolve it
		templateAbsPath := def.TemplatePath
		if !filepath.IsAbs(templateAbsPath) {
			templateAbsPath = ResolveTemplatePath(filePath, def.TemplatePath)
			def.TemplatePath = templateAbsPath // Store absolute path
		}
		if result, err := ParseTemplateFromFile(templateAbsPath); err == nil {
			def.Slots = result.Slots
			def.Blocks = result.Blocks
		}
	}

	// Store the definition indexed by the file path (normalized)
	// We'll use the file path as the key so we can look it up later
	normalizedPath := normalizeDefinitionPath(filePath)

	batchSave := make(map[string]map[string]ComponentDefinition)
	batchSave[filePath] = map[string]ComponentDefinition{
		normalizedPath: *def,
	}

	return idx.definitionIndex.BatchSaveItemsIn(mutation, batchSave)
}

// normalizeDefinitionPath creates a normalized key from a definition file path.
// It removes the source extension and handles index modules/SFCs.
func normalizeDefinitionPath(filePath string) string {
	// Remove extension
	ext := filepath.Ext(filePath)
	normalized := strings.TrimSuffix(filePath, ext)

	// If it ends with /index, keep the directory path
	normalized = strings.TrimSuffix(normalized, "/index")

	return normalized
}

func (idx *AdminComponentIndexer) RemovedFiles(paths []string) error {
	idx.invalidateTemplateComponentCache()
	return errors.Join(
		idx.componentIndex.BatchDeleteByFilePaths(paths),
		idx.definitionIndex.BatchDeleteByFilePaths(paths),
		idx.mixinIndex.BatchDeleteByFilePaths(paths),
		idx.directiveIndex.BatchDeleteByFilePaths(paths),
		idx.filterIndex.BatchDeleteByFilePaths(paths),
		idx.cmsIndex.BatchDeleteByFilePaths(paths),
		idx.moduleIndex.BatchDeleteByFilePaths(paths),
		idx.serviceIndex.BatchDeleteByFilePaths(paths),
		idx.storeIndex.BatchDeleteByFilePaths(paths),
		idx.storeFactoryIndex.BatchDeleteByFilePaths(paths),
		idx.privilegeIndex.BatchDeleteByFilePaths(paths),
		idx.usageIndex.BatchDeleteByFilePaths(paths),
		idx.typeIndex.BatchDeleteByFilePaths(paths),
	)
}

func (idx *AdminComponentIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	idx.invalidateTemplateComponentCache()
	return errors.Join(
		idx.componentIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.definitionIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.mixinIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.directiveIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.filterIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.cmsIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.moduleIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.serviceIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.storeIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.storeFactoryIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.privilegeIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.usageIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.typeIndex.BatchDeleteByFilePathsIn(mutation, paths),
	)
}

func (idx *AdminComponentIndexer) Close() error {
	return errors.Join(
		idx.componentIndex.Close(),
		idx.definitionIndex.Close(),
		idx.mixinIndex.Close(),
		idx.directiveIndex.Close(),
		idx.filterIndex.Close(),
		idx.cmsIndex.Close(),
		idx.moduleIndex.Close(),
		idx.serviceIndex.Close(),
		idx.storeIndex.Close(),
		idx.storeFactoryIndex.Close(),
		idx.privilegeIndex.Close(),
		idx.usageIndex.Close(),
		idx.typeIndex.Close(),
	)
}

func (idx *AdminComponentIndexer) Clear() error {
	idx.invalidateTemplateComponentCache()
	return errors.Join(
		idx.componentIndex.Clear(),
		idx.definitionIndex.Clear(),
		idx.mixinIndex.Clear(),
		idx.directiveIndex.Clear(),
		idx.filterIndex.Clear(),
		idx.cmsIndex.Clear(),
		idx.moduleIndex.Clear(),
		idx.serviceIndex.Clear(),
		idx.storeIndex.Clear(),
		idx.storeFactoryIndex.Clear(),
		idx.privilegeIndex.Clear(),
		idx.usageIndex.Clear(),
		idx.typeIndex.Clear(),
	)
}

func (idx *AdminComponentIndexer) ClearIn(mutation *indexer.Mutation) error {
	idx.invalidateTemplateComponentCache()
	return errors.Join(
		idx.componentIndex.ClearIn(mutation),
		idx.definitionIndex.ClearIn(mutation),
		idx.mixinIndex.ClearIn(mutation),
		idx.directiveIndex.ClearIn(mutation),
		idx.filterIndex.ClearIn(mutation),
		idx.cmsIndex.ClearIn(mutation),
		idx.moduleIndex.ClearIn(mutation),
		idx.serviceIndex.ClearIn(mutation),
		idx.storeIndex.ClearIn(mutation),
		idx.storeFactoryIndex.ClearIn(mutation),
		idx.privilegeIndex.ClearIn(mutation),
		idx.usageIndex.ClearIn(mutation),
		idx.typeIndex.ClearIn(mutation),
	)
}
