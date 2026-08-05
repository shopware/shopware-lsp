package admin

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	javascriptparser "github.com/shopware/shopware-lsp/internal/parser/javascript"
	jsquery "github.com/shopware/shopware-lsp/internal/parser/javascript/query"
	jssyntax "github.com/shopware/shopware-lsp/internal/parser/javascript/syntax"
	twigsyntax "github.com/shopware/shopware-lsp/internal/parser/twig/syntax"
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

func NewAdminComponentIndexer(configDir string, stores ...*indexer.Store) (*AdminComponentIndexer, error) {
	componentIndex, err := indexer.NewRepository[VueComponent](path.Join(configDir, "admin_component.db"), "admin.components", stores...)
	if err != nil {
		return nil, err
	}

	definitionIndex, err := indexer.NewRepository[ComponentDefinition](path.Join(configDir, "admin_component_definition.db"), "admin.definitions", stores...)
	if err != nil {
		_ = componentIndex.Close()
		return nil, err
	}
	mixinIndex, err := indexer.NewRepository[AdminMixin](path.Join(configDir, "admin_mixin.db"), "admin.mixins", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		return nil, err
	}
	directiveIndex, err := indexer.NewRepository[AdminDirective](path.Join(configDir, "admin_directive.db"), "admin.directives", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		return nil, err
	}
	filterIndex, err := indexer.NewRepository[AdminFilter](path.Join(configDir, "admin_filter.db"), "admin.filters", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		return nil, err
	}
	cmsIndex, err := indexer.NewRepository[AdminCMSRegistration](path.Join(configDir, "admin_cms.db"), "admin.cms", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		return nil, err
	}
	moduleIndex, err := indexer.NewRepository[AdminModule](path.Join(configDir, "admin_module.db"), "admin.modules", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		return nil, err
	}
	serviceIndex, err := indexer.NewRepository[AdminService](path.Join(configDir, "admin_service.db"), "admin.services", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		_ = moduleIndex.Close()
		return nil, err
	}
	storeIndex, err := indexer.NewRepository[AdminStore](path.Join(configDir, "admin_store.db"), "admin.stores", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		_ = moduleIndex.Close()
		_ = serviceIndex.Close()
		return nil, err
	}
	storeFactoryIndex, err := indexer.NewRepository[AdminStoreFactory](path.Join(configDir, "admin_store_factory.db"), "admin.store_factories", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		_ = moduleIndex.Close()
		_ = serviceIndex.Close()
		_ = storeIndex.Close()
		return nil, err
	}
	privilegeIndex, err := indexer.NewRepository[AdminPrivilege](path.Join(configDir, "admin_privilege.db"), "admin.privileges", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		_ = moduleIndex.Close()
		_ = serviceIndex.Close()
		_ = storeIndex.Close()
		_ = storeFactoryIndex.Close()
		return nil, err
	}
	usageIndex, err := indexer.NewRepository[AdminUsageSet](path.Join(configDir, "admin_usage.db"), "admin.usages", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		_ = moduleIndex.Close()
		_ = serviceIndex.Close()
		_ = storeIndex.Close()
		_ = storeFactoryIndex.Close()
		_ = privilegeIndex.Close()
		return nil, err
	}
	typeIndex, err := indexer.NewRepository[AdminTypeFile](path.Join(configDir, "admin_type.db"), "admin.types", stores...)
	if err != nil {
		_ = componentIndex.Close()
		_ = definitionIndex.Close()
		_ = mixinIndex.Close()
		_ = directiveIndex.Close()
		_ = filterIndex.Close()
		_ = cmsIndex.Close()
		_ = moduleIndex.Close()
		_ = serviceIndex.Close()
		_ = storeIndex.Close()
		_ = storeFactoryIndex.Close()
		_ = privilegeIndex.Close()
		_ = usageIndex.Close()
		return nil, err
	}

	return &AdminComponentIndexer{
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
	}, nil
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

func (idx *AdminComponentIndexer) GetAllServices() ([]AdminService, error) {
	values, err := idx.serviceIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminService) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminService {
			return document.Services
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetService(name string) ([]AdminService, error) {
	values, err := idx.serviceIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminService) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminService {
			return document.Services
		},
		func(value AdminService) bool { return value.Name == name },
	)
	return preferredServices(values), nil
}

func (idx *AdminComponentIndexer) GetAllDirectives() ([]AdminDirective, error) {
	values, err := idx.directiveIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminDirective) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminDirective {
			return document.Directives
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetAllFilters() ([]AdminFilter, error) {
	values, err := idx.filterIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminFilter) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminFilter {
			return document.Filters
		},
		nil,
	)
	return preferRuntimeDefinitions(values, func(value AdminFilter) string {
		return value.FilePath
	}), nil
}

func (idx *AdminComponentIndexer) GetAllCMSRegistrations() ([]AdminCMSRegistration, error) {
	values, err := idx.cmsIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminCMSRegistration) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminCMSRegistration {
			return document.CMS
		},
		nil,
	)
	return preferredCMSRegistrations(values), nil
}

func (idx *AdminComponentIndexer) GetAllCMSRegistrationsByKind(
	kind AdminCMSRegistrationKind,
) ([]AdminCMSRegistration, error) {
	values, err := idx.GetAllCMSRegistrations()
	if err != nil {
		return nil, err
	}
	result := make([]AdminCMSRegistration, 0, len(values))
	for _, value := range values {
		if value.Kind == kind {
			result = append(result, value)
		}
	}
	return preferredCMSRegistrations(result), nil
}

func (idx *AdminComponentIndexer) GetCMSRegistration(
	kind AdminCMSRegistrationKind,
	name string,
) ([]AdminCMSRegistration, error) {
	values, err := idx.cmsIndex.GetValues(AdminCMSKey(kind, name))
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminCMSRegistration) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminCMSRegistration {
			return document.CMS
		},
		func(value AdminCMSRegistration) bool {
			return value.Kind == kind && value.Name == name
		},
	)
	return preferredCMSRegistrations(values), nil
}

func (idx *AdminComponentIndexer) GetDirective(
	name string,
) ([]AdminDirective, error) {
	values, err := idx.directiveIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminDirective) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminDirective {
			return document.Directives
		},
		func(value AdminDirective) bool { return value.Name == name },
	), nil
}

func (idx *AdminComponentIndexer) GetFilter(
	name string,
) ([]AdminFilter, error) {
	values, err := idx.filterIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminFilter) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminFilter {
			return document.Filters
		},
		func(value AdminFilter) bool { return value.Name == name },
	)
	return preferRuntimeDefinitions(values, func(value AdminFilter) string {
		return value.FilePath
	}), nil
}

// GetLocalDirectiveForTemplate resolves an Options API directive in the
// component that owns templatePath. Local declarations shadow the global
// registry only in that component's effective template scope.
func (idx *AdminComponentIndexer) GetLocalDirectiveForTemplate(
	templatePath,
	name string,
) (VueLocalDirective, bool, error) {
	component, err := idx.GetComponentByTemplatePath(templatePath)
	if err != nil || component == nil {
		return VueLocalDirective{}, false, err
	}
	directive, found := component.LocalDirective(name)
	return directive, found, nil
}

func (idx *AdminComponentIndexer) GetDirectiveForTemplate(
	templatePath,
	name string,
) ([]AdminDirective, error) {
	local, found, err := idx.GetLocalDirectiveForTemplate(templatePath, name)
	if err != nil {
		return nil, err
	}
	if found {
		return []AdminDirective{{
			Name: local.Name, FilePath: local.FilePath, Line: local.Line,
			NameRange: local.NameRange, Local: true,
		}}, nil
	}
	return idx.GetDirective(name)
}

func (idx *AdminComponentIndexer) GetAllDirectivesForTemplate(
	templatePath string,
) ([]AdminDirective, error) {
	global, err := idx.GetAllDirectives()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]AdminDirective, len(global))
	order := make([]string, 0, len(global))
	for _, directive := range global {
		key := strings.ToLower(directive.Name)
		if key == "" {
			continue
		}
		if _, exists := byName[key]; !exists {
			order = append(order, key)
		}
		byName[key] = directive
	}
	component, componentErr := idx.GetComponentByTemplatePath(templatePath)
	if componentErr != nil {
		return nil, componentErr
	}
	if component != nil {
		for _, local := range component.LocalDirectives {
			key := strings.ToLower(local.Name)
			if key == "" {
				continue
			}
			if _, exists := byName[key]; !exists {
				order = append(order, key)
			}
			byName[key] = AdminDirective{
				Name: local.Name, FilePath: local.FilePath, Line: local.Line,
				NameRange: local.NameRange, Local: true,
			}
		}
	}
	result := make([]AdminDirective, 0, len(byName))
	for _, key := range order {
		result = append(result, byName[key])
	}
	return result, nil
}

func (idx *AdminComponentIndexer) GetAllStores() ([]AdminStore, error) {
	values, err := idx.storeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminStore) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminStore {
			return document.Stores
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetStore(name string) ([]AdminStore, error) {
	values, err := idx.storeIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	documents := idx.liveLegacyDocumentSnapshots()
	values = overlayLiveLegacyValues(
		values,
		documents,
		func(value AdminStore) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminStore {
			return document.Stores
		},
		func(value AdminStore) bool { return value.Name == name },
	)
	values = preferredStores(values)
	for index := range values {
		if values[index].FactoryPath != "" {
			factories, factoryErr := idx.storeFactoryIndex.GetValues(
				normalizeDefinitionPath(values[index].FactoryPath),
			)
			if factoryErr != nil {
				return nil, factoryErr
			}
			factoryPath := normalizeDefinitionPath(values[index].FactoryPath)
			factories = overlayLiveLegacyValues(
				factories,
				documents,
				func(value AdminStoreFactory) string { return value.FilePath },
				func(document liveLegacyDocument) []AdminStoreFactory {
					if document.StoreFactory == nil {
						return nil
					}
					return []AdminStoreFactory{*document.StoreFactory}
				},
				func(value AdminStoreFactory) bool {
					return normalizeDefinitionPath(value.FilePath) == factoryPath
				},
			)
			for _, factory := range factories {
				values[index].Members = mergeStoreMembers(
					factory.Members,
					values[index].Members,
				)
			}
		}
		if values[index].StateType != "" {
			shape, shapeErr := idx.ResolveVueType(
				values[index].StateType, values[index].FilePath,
			)
			if shapeErr != nil {
				return nil, shapeErr
			}
			for memberIndex := range values[index].Members {
				member := &values[index].Members[memberIndex]
				if member.Kind != AdminStoreState {
					continue
				}
				declared, found := twigVueMemberNamed(shape.Members, member.Name)
				if found && declared.Type != "" {
					member.Type = declared.Type
				}
			}
		}
	}
	return values, nil
}

func (idx *AdminComponentIndexer) GetAllPrivileges() ([]AdminPrivilege, error) {
	values, err := idx.privilegeIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminPrivilege) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminPrivilege {
			return document.Privileges
		},
		nil,
	)
	hasAdministrator := false
	for _, value := range values {
		if value.Name == AdminPrivilegeAdministrator {
			hasAdministrator = true
			break
		}
	}
	if !hasAdministrator {
		administrator, _ := builtinAdminPrivilege(AdminPrivilegeAdministrator)
		values = append(values, administrator)
	}
	return values, nil
}

func (idx *AdminComponentIndexer) GetPrivilege(
	name string,
) ([]AdminPrivilege, error) {
	values, err := idx.privilegeIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	values = overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminPrivilege) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminPrivilege {
			return document.Privileges
		},
		func(value AdminPrivilege) bool { return value.Name == name },
	)
	values = preferredPrivileges(values)
	if len(values) == 0 {
		if builtin, ok := builtinAdminPrivilege(name); ok {
			return []AdminPrivilege{builtin}, nil
		}
	}
	return values, nil
}

func (idx *AdminComponentIndexer) GetUsages(
	kind AdminSymbolKind,
	owner,
	name string,
) ([]AdminUsageSet, error) {
	return idx.usageIndex.GetValues(AdminUsageKey(kind, owner, name))
}

// GetSymbolUsages expands source-owned component events and slots through all
// effective components that expose the same declaration. Other symbol kinds
// retain their direct persisted identity.
func (idx *AdminComponentIndexer) GetSymbolUsages(
	target AdminSymbolTarget,
) ([]AdminUsageSet, error) {
	sets, err := idx.GetUsages(target.Kind, target.Owner, target.Name)
	if err != nil {
		return sets, err
	}
	if target.Kind == AdminSymbolDirective {
		return idx.directiveSymbolUsages(target, sets)
	}
	if target.Kind == AdminSymbolComponentMember {
		return idx.componentMemberSymbolUsages(target, sets)
	}
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot {
		return sets, err
	}
	components, err := idx.GetComponentsExposingSymbol(target)
	if err != nil {
		return nil, err
	}
	for _, component := range components {
		componentSets, usageErr := idx.GetUsages(
			target.Kind,
			component.Name,
			target.Name,
		)
		if usageErr != nil {
			return nil, usageErr
		}
		for _, set := range componentSets {
			matches, matchErr := idx.componentUsageSetMatchesSource(
				set, component.Name, target,
			)
			if matchErr != nil {
				return nil, matchErr
			}
			if matches {
				sets = append(sets, set)
			}
		}
		modelSets, modelErr := idx.componentModelUsageSets(
			component, component.Name, target,
		)
		if modelErr != nil {
			return nil, modelErr
		}
		for _, set := range modelSets {
			matches, matchErr := idx.componentModelUsageSetMatchesSource(
				set, component.Name, target,
			)
			if matchErr != nil {
				return nil, matchErr
			}
			if matches {
				sets = append(sets, set)
			}
		}
	}
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		owner, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if owner == nil || owner.TemplatePath == "" {
			continue
		}
		for _, local := range owner.LocalComponents {
			component, found, localErr := idx.GetComponentForTemplateTag(
				owner.TemplatePath, local.Name,
			)
			if localErr != nil {
				return nil, localErr
			}
			if !found || component == nil {
				continue
			}
			source, exposes := component.SymbolSource(
				target.Kind, target.Name,
			)
			if !exposes || filepath.Clean(source) != filepath.Clean(target.Owner) {
				continue
			}
			localSets, usageErr := idx.GetUsages(
				target.Kind, local.Name, target.Name,
			)
			if usageErr != nil {
				return nil, usageErr
			}
			for _, set := range localSets {
				if normalizeDefinitionPath(set.FilePath) ==
					normalizeDefinitionPath(owner.TemplatePath) {
					sets = append(sets, set)
				}
			}
			modelSets, modelErr := idx.componentModelUsageSets(
				*component, local.Name, target,
			)
			if modelErr != nil {
				return nil, modelErr
			}
			for _, set := range modelSets {
				if normalizeDefinitionPath(set.FilePath) ==
					normalizeDefinitionPath(owner.TemplatePath) {
					sets = append(sets, set)
				}
			}
		}
	}
	dynamicSets, err := idx.dynamicComponentSymbolUsages(target)
	if err != nil {
		return nil, err
	}
	sets = append(sets, dynamicSets...)
	return uniqueAdminUsageSets(sets), nil
}

type dynamicUsageResolutionKey struct {
	filePath   string
	selector   string
	routerView bool
}

type dynamicUsageResolution struct {
	components []VueComponent
	complete   bool
}

// dynamicComponentSymbolUsages resolves symbolically persisted dynamic
// component usages against the latest effective component graph. Keeping this
// join query-time avoids making template indexing depend on whether component
// definitions, CMS registrations, or module routes happened to be indexed
// first.
func (idx *AdminComponentIndexer) dynamicComponentSymbolUsages(
	target AdminSymbolTarget,
) ([]AdminUsageSet, error) {
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot {
		return nil, nil
	}
	raw, err := idx.GetUsages(
		target.Kind, adminDynamicComponentUsageOwner, target.Name,
	)
	if err != nil {
		return nil, err
	}
	if target.Kind == AdminSymbolComponentProp ||
		target.Kind == AdminSymbolComponentEvent {
		keys, keyErr := idx.usageIndex.GetAllKeys()
		if keyErr != nil {
			return nil, keyErr
		}
		prefix := AdminUsageKey(
			AdminSymbolComponentModel,
			adminDynamicComponentUsageOwner,
			"",
		)
		for _, key := range keys {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			modelSets, modelErr := idx.GetUsages(
				AdminSymbolComponentModel,
				adminDynamicComponentUsageOwner,
				strings.TrimPrefix(key, prefix),
			)
			if modelErr != nil {
				return nil, modelErr
			}
			raw = append(raw, modelSets...)
		}
	}
	cache := make(map[dynamicUsageResolutionKey]dynamicUsageResolution)
	var result []AdminUsageSet
	for _, set := range raw {
		filtered := set
		filtered.Occurrences = nil
		for _, occurrence := range set.Occurrences {
			key := dynamicUsageResolutionKey{
				filePath:   normalizeDefinitionPath(set.FilePath),
				selector:   occurrence.DynamicComponentSelector,
				routerView: occurrence.DynamicRouterView,
			}
			resolution, cached := cache[key]
			if !cached {
				components, complete, resolveErr :=
					idx.resolvePersistedDynamicUsageComponents(
						set.FilePath, occurrence,
					)
				if resolveErr != nil {
					return nil, resolveErr
				}
				resolution = dynamicUsageResolution{
					components: components, complete: complete,
				}
				cache[key] = resolution
			}
			if !resolution.complete ||
				!dynamicUsageComponentsMatchTarget(
					resolution.components, set, target,
				) {
				continue
			}
			filtered.Occurrences = append(
				filtered.Occurrences, occurrence,
			)
		}
		if len(filtered.Occurrences) > 0 {
			result = append(result, filtered)
		}
	}
	return result, nil
}

func (idx *AdminComponentIndexer) resolvePersistedDynamicUsageComponents(
	templatePath string,
	occurrence AdminSourceRange,
) ([]VueComponent, bool, error) {
	owner, err := idx.GetComponentByTemplatePath(templatePath)
	if err != nil || owner == nil {
		return nil, false, err
	}
	var resolved dynamicComponentNames
	if occurrence.DynamicRouterView {
		resolved, err = idx.resolveRouterViewRouteComponentNames(*owner)
	} else {
		resolved = idx.resolveDynamicComponentNames(
			*owner,
			occurrence.DynamicComponentSelector,
			make(map[string]bool),
		)
	}
	if err != nil || !resolved.found || !resolved.complete ||
		len(resolved.names) == 0 {
		return nil, false, err
	}
	selector := VueDynamicComponentSelector{Complete: true}
	for _, name := range resolved.names {
		selector.Candidates = append(
			selector.Candidates,
			VueDynamicComponentCandidate{Name: name},
		)
	}
	return idx.ResolveDynamicComponents(templatePath, selector)
}

func dynamicUsageComponentsMatchTarget(
	components []VueComponent,
	set AdminUsageSet,
	target AdminSymbolTarget,
) bool {
	for _, component := range components {
		if set.Kind == AdminSymbolComponentModel {
			for _, binding := range component.ComponentModels() {
				if binding.AttributeName == set.Name &&
					componentModelBindingMatchesTarget(
						component, binding, target,
					) {
					return true
				}
			}
			continue
		}
		source, found := component.SymbolSource(target.Kind, target.Name)
		if found && normalizeDefinitionPath(source) ==
			normalizeDefinitionPath(target.Owner) {
			return true
		}
	}
	return false
}

// DynamicComponentUsageRenameSafe reports whether rewriting a dynamically
// owned attribute keeps the same declaration identity for every possible
// component. References may legitimately belong to several declarations, but
// a rename must not change a shared spelling when another runtime candidate
// owns a distinct contract.
func (idx *AdminComponentIndexer) DynamicComponentUsageRenameSafe(
	set AdminUsageSet,
	occurrence AdminSourceRange,
	target AdminSymbolTarget,
) (bool, error) {
	if occurrence.DynamicComponentSelector == "" &&
		!occurrence.DynamicRouterView {
		return true, nil
	}
	components, complete, err := idx.resolvePersistedDynamicUsageComponents(
		set.FilePath, occurrence,
	)
	if err != nil || !complete || len(components) == 0 {
		return false, err
	}
	if set.Kind != target.Kind || set.Name != target.Name {
		return false, nil
	}
	for _, component := range components {
		source, found := component.SymbolSource(set.Kind, set.Name)
		if !found || normalizeDefinitionPath(source) !=
			normalizeDefinitionPath(target.Owner) {
			return false, nil
		}
	}
	return true, nil
}

func (idx *AdminComponentIndexer) directiveSymbolUsages(
	target AdminSymbolTarget,
	direct []AdminUsageSet,
) ([]AdminUsageSet, error) {
	var result []AdminUsageSet
	if target.Owner != "" {
		// Source-owned sets contain the local declaration. Template references
		// are intentionally persisted without an owner and are scoped below.
		result = append(result, direct...)
	}
	raw, err := idx.GetUsages(AdminSymbolDirective, "", target.Name)
	if err != nil {
		return nil, err
	}
	for _, set := range raw {
		if !strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
			if target.Owner == "" {
				result = append(result, set)
			}
			continue
		}
		local, found, localErr := idx.GetLocalDirectiveForTemplate(
			set.FilePath, target.Name,
		)
		if localErr != nil {
			return nil, localErr
		}
		if target.Owner == "" {
			if !found {
				result = append(result, set)
			}
			continue
		}
		if found && normalizeDefinitionPath(local.FilePath) ==
			normalizeDefinitionPath(target.Owner) {
			result = append(result, set)
		}
	}
	return uniqueAdminUsageSets(result), nil
}

func (idx *AdminComponentIndexer) componentModelUsageSets(
	component VueComponent,
	usageOwner string,
	target AdminSymbolTarget,
) ([]AdminUsageSet, error) {
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent {
		return nil, nil
	}
	var result []AdminUsageSet
	for _, binding := range component.ComponentModels() {
		if !componentModelBindingMatchesTarget(component, binding, target) {
			continue
		}
		sets, err := idx.GetUsages(
			AdminSymbolComponentModel, usageOwner, binding.AttributeName,
		)
		if err != nil {
			return nil, err
		}
		result = append(result, sets...)
	}
	return result, nil
}

func componentModelBindingMatchesTarget(
	component VueComponent,
	binding VueComponentModelBinding,
	target AdminSymbolTarget,
) bool {
	name := binding.PropName
	if target.Kind == AdminSymbolComponentEvent {
		name = binding.EventName
	}
	if name != target.Name {
		return false
	}
	source, found := component.SymbolSource(target.Kind, target.Name)
	return found && filepath.Clean(source) == filepath.Clean(target.Owner)
}

func (idx *AdminComponentIndexer) componentModelUsageSetMatchesSource(
	set AdminUsageSet,
	componentName string,
	target AdminSymbolTarget,
) (bool, error) {
	if !strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
		return true, nil
	}
	owner, err := idx.GetComponentByTemplatePath(set.FilePath)
	if err != nil || owner == nil {
		return owner == nil, err
	}
	if _, local := owner.LocalComponent(componentName); !local {
		return true, nil
	}
	component, found, err := idx.GetComponentForTemplateTag(
		set.FilePath, componentName,
	)
	if err != nil || !found || component == nil {
		return false, err
	}
	for _, binding := range component.ComponentModels() {
		if binding.AttributeName == set.Name &&
			componentModelBindingMatchesTarget(*component, binding, target) {
			return true, nil
		}
	}
	return false, nil
}

func (idx *AdminComponentIndexer) componentMemberSymbolUsages(
	target AdminSymbolTarget,
	sets []AdminUsageSet,
) ([]AdminUsageSet, error) {
	raw, err := idx.GetUsages(
		AdminSymbolComponentMember, "", target.Name,
	)
	if err != nil {
		return nil, err
	}
	for _, set := range raw {
		matches, matchErr := idx.componentMemberUsageSetMatchesSource(
			set, target,
		)
		if matchErr != nil {
			return nil, matchErr
		}
		if matches {
			sets = append(sets, set)
		}
	}
	return uniqueAdminUsageSets(sets), nil
}

func (idx *AdminComponentIndexer) componentMemberUsageSetMatchesSource(
	set AdminUsageSet,
	target AdminSymbolTarget,
) (bool, error) {
	identities := make(map[string]bool)
	add := func(component *VueComponent) {
		if component == nil {
			return
		}
		member, found := component.TemplateMember(target.Name)
		if !found || !member.Renameable() {
			return
		}
		if identity := member.SourceIdentity(); identity != "" {
			identities[identity] = true
		}
	}
	if strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
		component, err := idx.GetComponentByTemplatePath(set.FilePath)
		if err != nil {
			return false, err
		}
		add(component)
	} else {
		components, err := idx.GetComponentsByDefinitionPath(set.FilePath)
		if err != nil {
			return false, err
		}
		for index := range components {
			add(&components[index])
		}
	}
	return len(identities) == 1 && identities[target.Owner], nil
}

func (idx *AdminComponentIndexer) GetComponentsExposingMember(
	target AdminSymbolTarget,
) ([]VueComponent, error) {
	if target.Kind != AdminSymbolComponentMember || target.Name == "" ||
		target.Owner == "" {
		return nil, nil
	}
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	var result []VueComponent
	for _, name := range names {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil {
			continue
		}
		member, found := component.TemplateMember(target.Name)
		if found && member.Renameable() &&
			member.SourceIdentity() == target.Owner {
			result = append(result, *component)
		}
	}
	return result, nil
}

func (idx *AdminComponentIndexer) componentUsageSetMatchesSource(
	set AdminUsageSet,
	componentName string,
	target AdminSymbolTarget,
) (bool, error) {
	if !strings.EqualFold(filepath.Ext(set.FilePath), ".twig") {
		return true, nil
	}
	owner, err := idx.GetComponentByTemplatePath(set.FilePath)
	if err != nil || owner == nil {
		return owner == nil, err
	}
	if _, local := owner.LocalComponent(componentName); !local {
		return true, nil
	}
	component, found, err := idx.GetComponentForTemplateTag(
		set.FilePath, componentName,
	)
	if err != nil || !found || component == nil {
		return false, err
	}
	source, exposes := component.SymbolSource(target.Kind, target.Name)
	return exposes && filepath.Clean(source) == filepath.Clean(target.Owner), nil
}

func uniqueAdminUsageSets(values []AdminUsageSet) []AdminUsageSet {
	result := make([]AdminUsageSet, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		key := AdminUsageKey(value.Kind, value.Owner, value.Name) + "\x00" +
			normalizeDefinitionPath(value.FilePath)
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func (idx *AdminComponentIndexer) GetComponentsExposingSymbol(
	target AdminSymbolTarget,
) ([]VueComponent, error) {
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot {
		return nil, nil
	}
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	result := make([]VueComponent, 0)
	for _, name := range names {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil {
			continue
		}
		owner, found := component.SymbolSource(target.Kind, target.Name)
		if !found || filepath.Clean(owner) != filepath.Clean(target.Owner) {
			continue
		}
		result = append(result, *component)
	}
	return result, nil
}

// IsDynamicComponentSlot reports whether a resolved concrete consumer name is
// owned by a computed slot family. Such consumers support navigation and
// references, but renaming one concrete spelling cannot safely rewrite the
// runtime declaration expression.
func (idx *AdminComponentIndexer) IsDynamicComponentSlot(
	target AdminSymbolTarget,
) (bool, error) {
	if target.Kind != AdminSymbolComponentSlot {
		return false, nil
	}
	components, err := idx.GetComponentsExposingSymbol(target)
	if err != nil {
		return false, err
	}
	for _, component := range components {
		slot, found := component.ComponentSlot(target.Name)
		if found && slot.IsDynamicName() {
			return true, nil
		}
	}
	return false, nil
}

func (idx *AdminComponentIndexer) JavaScriptSymbolAt(
	filePath string,
	node *jssyntax.Node,
) (AdminSymbolTarget, bool, error) {
	if name, found := JavaScriptComponentPropAt(node); found {
		components, err := idx.GetComponentsByDefinitionPath(filePath)
		if err != nil {
			return AdminSymbolTarget{}, false, err
		}
		for _, component := range components {
			prop, exists := component.ComponentProp(name)
			if !exists {
				continue
			}
			owner := prop.FilePath
			if owner == "" {
				owner = component.DefinitionPath
			}
			if owner == "" {
				owner = filePath
			}
			return AdminSymbolTarget{
				Kind: AdminSymbolComponentProp, Owner: owner, Name: prop.Name,
			}, true, nil
		}
	}
	if name, found := JavaScriptComponentEventAt(node); found {
		components, err := idx.GetComponentsByDefinitionPath(filePath)
		if err != nil {
			return AdminSymbolTarget{}, false, err
		}
		for _, component := range components {
			event, exists := component.ComponentEvent(name)
			if !exists {
				continue
			}
			owner := event.FilePath
			if owner == "" {
				owner = component.DefinitionPath
			}
			if owner == "" {
				owner = filePath
			}
			return AdminSymbolTarget{
				Kind:  AdminSymbolComponentEvent,
				Owner: owner,
				Name:  CanonicalEventName(event.Name),
			}, true, nil
		}
	}
	if node != nil {
		root := node
		for root.Parent() != nil {
			root = root.Parent()
		}
		lineIndex := jssyntax.NewLineIndex(root.Text())
		if event, found, err := idx.shopwareEventBusEventAtDefinitionRange(
			filePath, node.RangeTrimmedTrivia(), lineIndex,
		); err != nil {
			return AdminSymbolTarget{}, false, err
		} else if found {
			return AdminSymbolTarget{
				Kind: AdminSymbolEventBusEvent, Name: event.Name,
			}, true, nil
		}
		line, character := lineIndex.PositionUTF16(
			node.RangeTrimmedTrivia().Start,
		)
		_, directive, directiveFound, directiveErr :=
			idx.GetLocalDirectiveAtDefinitionPosition(
				filePath, int(line), int(character),
			)
		if directiveErr != nil {
			return AdminSymbolTarget{}, false, directiveErr
		}
		if directiveFound {
			return AdminSymbolTarget{
				Kind:  AdminSymbolDirective,
				Owner: directive.FilePath,
				Name:  directive.Name,
			}, true, nil
		}
		member, found, err := idx.GetComponentMemberAtDefinitionPosition(
			filePath, int(line), int(character),
		)
		if err != nil {
			return AdminSymbolTarget{}, false, err
		}
		if found {
			return componentMemberTarget(member), true, nil
		}
	}
	if target, found := JavaScriptSymbolAt(node); found {
		return target, true, nil
	}
	name, matched := jsquery.ThisMember(node)
	if !matched || name == "" {
		return AdminSymbolTarget{}, false, nil
	}
	components, err := idx.GetComponentsByDefinitionPath(filePath)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	for _, component := range components {
		if prop, found := component.ComponentProp(name); found {
			owner := prop.FilePath
			if owner == "" {
				owner = component.DefinitionPath
			}
			if owner == "" {
				owner = filePath
			}
			return AdminSymbolTarget{
				Kind: AdminSymbolComponentProp, Owner: owner, Name: prop.Name,
			}, true, nil
		}
		for _, injected := range component.Injected {
			if injected == name {
				return AdminSymbolTarget{
					Kind: AdminSymbolService, Name: name,
				}, true, nil
			}
		}
		if member, found := component.TemplateMember(name); found &&
			member.Renameable() {
			return componentMemberTarget(member), true, nil
		}
	}
	declared, err := idx.componentMembersDeclaredIn(filePath)
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	var target *VueComponentMember
	for index := range declared {
		if declared[index].Name != name || !declared[index].Renameable() {
			continue
		}
		if target != nil && target.SourceIdentity() != declared[index].SourceIdentity() {
			return AdminSymbolTarget{}, false, nil
		}
		candidate := declared[index]
		target = &candidate
	}
	if target != nil {
		return componentMemberTarget(*target), true, nil
	}
	return AdminSymbolTarget{}, false, nil
}

func componentMemberTarget(member VueComponentMember) AdminSymbolTarget {
	return AdminSymbolTarget{
		Kind:  AdminSymbolComponentMember,
		Owner: member.SourceIdentity(),
		Name:  member.Name,
	}
}

// ResolveComponentMemberTarget returns the source declaration encoded by a
// component-member symbol identity. Effective components may expose the same
// declaration through inheritance, but the returned member is the one stable
// source node used by navigation, references, rename, and call hierarchy.
func (idx *AdminComponentIndexer) ResolveComponentMemberTarget(
	target AdminSymbolTarget,
) (VueComponentMember, bool, error) {
	if target.Kind != AdminSymbolComponentMember || target.Owner == "" ||
		target.Name == "" {
		return VueComponentMember{}, false, nil
	}
	separator := strings.IndexByte(target.Owner, 0)
	if separator <= 0 {
		return VueComponentMember{}, false, nil
	}
	members, err := idx.componentMembersDeclaredIn(target.Owner[:separator])
	if err != nil {
		return VueComponentMember{}, false, err
	}
	for _, member := range members {
		if member.SourceIdentity() == target.Owner && member.Name == target.Name {
			return member, true, nil
		}
	}
	return VueComponentMember{}, false, nil
}

// StoreActionTargetsAtDefinitionPosition resolves a Pinia action declaration
// by its indexed source line. A setup-store factory can feed more than one
// public store, so all unambiguous public targets are returned.
func (idx *AdminComponentIndexer) StoreActionTargetsAtDefinitionPosition(
	filePath,
	name string,
	line int,
) ([]AdminSymbolTarget, error) {
	if filePath == "" || name == "" || line < 0 {
		return nil, nil
	}
	stores, err := idx.GetAllStores()
	if err != nil {
		return nil, err
	}
	var result []AdminSymbolTarget
	seen := make(map[AdminSymbolTarget]bool)
	for _, store := range stores {
		member, found := store.Member(name)
		if !found || member.Kind != AdminStoreAction ||
			normalizeDefinitionPath(member.FilePath) !=
				normalizeDefinitionPath(filePath) || member.Line != line+1 {
			continue
		}
		target := AdminSymbolTarget{
			Kind: AdminSymbolStoreMember, Owner: store.Name, Name: member.Name,
		}
		if seen[target] {
			continue
		}
		seen[target] = true
		result = append(result, target)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Owner != result[right].Owner {
			return result[left].Owner < result[right].Owner
		}
		return result[left].Name < result[right].Name
	})
	return result, nil
}

func (idx *AdminComponentIndexer) TwigComponentMemberAt(
	filePath string,
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
) (AdminSymbolTarget, VueComponentMember, bool, error) {
	name, rangeValue, found := TwigVueExpressionRootIdentifierAtOffset(
		root, content, offset,
	)
	if !found || twigVueRootIdentifierIsLocal(
		root, content, TwigVueMember{Name: name, Range: rangeValue},
	) {
		return AdminSymbolTarget{}, VueComponentMember{}, false, nil
	}
	component, err := idx.GetComponentByTemplatePath(filePath)
	if err != nil || component == nil {
		return AdminSymbolTarget{}, VueComponentMember{}, false, err
	}
	member, found := component.TemplateMember(name)
	if !found || !member.Renameable() {
		return AdminSymbolTarget{}, VueComponentMember{}, false, nil
	}
	return componentMemberTarget(member), member, true, nil
}

func (idx *AdminComponentIndexer) GetComponentMemberAtDefinitionPosition(
	filePath string,
	line,
	character int,
) (VueComponentMember, bool, error) {
	members, err := idx.componentMembersDeclaredIn(filePath)
	if err != nil {
		return VueComponentMember{}, false, err
	}
	for _, member := range members {
		if member.Renameable() && adminSourceRangeContainsPosition(
			member.NameRange, line, character,
		) {
			return member, true, nil
		}
	}
	return VueComponentMember{}, false, nil
}

func (idx *AdminComponentIndexer) componentMembersDeclaredIn(
	filePath string,
) ([]VueComponentMember, error) {
	normalized := normalizeDefinitionPath(filePath)
	definitions, err := idx.definitionIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var result []VueComponentMember
	add := func(definition ComponentDefinition) {
		if normalizeDefinitionPath(definition.FilePath) != normalized {
			return
		}
		for _, member := range definition.Members {
			identity := member.SourceIdentity()
			if identity == "" || seen[identity] {
				continue
			}
			seen[identity] = true
			result = append(result, member)
		}
	}
	for _, definition := range definitions {
		add(definition)
	}
	mixins, err := idx.GetAllMixins()
	if err != nil {
		return nil, err
	}
	for _, mixin := range mixins {
		add(mixin.Definition)
	}
	return result, nil
}

func (idx *AdminComponentIndexer) TwigSymbolAt(
	filePath string,
	root *twigsyntax.Node,
	offset uint32,
) (AdminSymbolTarget, bool, error) {
	target, found := TwigSymbolAtOffset(root, offset)
	if !found {
		return AdminSymbolTarget{}, false, nil
	}
	if target.Kind != AdminSymbolComponentProp &&
		target.Kind != AdminSymbolComponentEvent &&
		target.Kind != AdminSymbolComponentSlot &&
		target.Kind != AdminSymbolDirective {
		return target, true, nil
	}
	if target.Kind == AdminSymbolDirective {
		local, localFound, localErr := idx.GetLocalDirectiveForTemplate(
			filePath, target.Name,
		)
		if localErr != nil {
			return AdminSymbolTarget{}, false, localErr
		}
		if localFound {
			return AdminSymbolTarget{
				Kind:  AdminSymbolDirective,
				Owner: local.FilePath,
				Name:  local.Name,
			}, true, nil
		}
		return target, true, nil
	}
	var component *VueComponent
	var err error
	if target.Owner != "" {
		component, _, err = idx.GetComponentForTemplateTag(
			filePath, target.Owner,
		)
	} else if target.Kind == AdminSymbolComponentSlot {
		component, err = idx.GetComponentByTemplatePath(filePath)
	}
	if err != nil {
		return AdminSymbolTarget{}, false, err
	}
	if component == nil {
		return AdminSymbolTarget{}, false, nil
	}
	switch target.Kind {
	case AdminSymbolComponentProp:
		prop, exists := component.ComponentProp(target.Name)
		if !exists {
			return AdminSymbolTarget{}, false, nil
		}
		owner := prop.FilePath
		if owner == "" {
			owner = component.DefinitionPath
		}
		if owner == "" {
			owner = component.FilePath
		}
		if owner == "" {
			return AdminSymbolTarget{}, false, nil
		}
		return AdminSymbolTarget{
			Kind: AdminSymbolComponentProp, Owner: owner, Name: prop.Name,
		}, true, nil
	case AdminSymbolComponentEvent:
		event, exists := component.ComponentEvent(target.Name)
		if !exists {
			return AdminSymbolTarget{}, false, nil
		}
		owner := event.FilePath
		if owner == "" {
			owner = component.DefinitionPath
		}
		if owner == "" {
			owner = component.FilePath
		}
		if owner == "" {
			return AdminSymbolTarget{}, false, nil
		}
		return AdminSymbolTarget{
			Kind:  AdminSymbolComponentEvent,
			Owner: owner,
			Name:  CanonicalEventName(event.Name),
		}, true, nil
	case AdminSymbolComponentSlot:
		slot, exists := component.ComponentSlot(target.Name)
		if !exists {
			return AdminSymbolTarget{}, false, nil
		}
		owner := slot.FilePath
		if owner == "" {
			owner = component.TemplatePath
		}
		if owner == "" {
			return AdminSymbolTarget{}, false, nil
		}
		return AdminSymbolTarget{
			Kind: AdminSymbolComponentSlot, Owner: owner, Name: target.Name,
		}, true, nil
	}
	return AdminSymbolTarget{}, false, nil
}

func (idx *AdminComponentIndexer) GetAllMixins() ([]AdminMixin, error) {
	values, err := idx.mixinIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminMixin) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminMixin {
			return document.Mixins
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetMixin(name string) ([]AdminMixin, error) {
	values, err := idx.mixinIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminMixin) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminMixin {
			return document.Mixins
		},
		func(value AdminMixin) bool { return value.Name == name },
	), nil
}

func (idx *AdminComponentIndexer) GetAllModules() ([]AdminModule, error) {
	values, err := idx.moduleIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminModule) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminModule {
			return document.Modules
		},
		nil,
	), nil
}

func (idx *AdminComponentIndexer) GetModule(name string) ([]AdminModule, error) {
	values, err := idx.moduleIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return overlayLiveLegacyValues(
		values,
		idx.liveLegacyDocumentSnapshots(),
		func(value AdminModule) string { return value.FilePath },
		func(document liveLegacyDocument) []AdminModule {
			return document.Modules
		},
		func(value AdminModule) bool { return value.Name == name },
	), nil
}

func (idx *AdminComponentIndexer) GetAllModuleRoutes() ([]AdminModuleRoute, error) {
	modules, err := idx.GetAllModules()
	if err != nil {
		return nil, err
	}
	var routes []AdminModuleRoute
	for _, module := range modules {
		routes = append(routes, module.Routes...)
	}
	return routes, nil
}

func (idx *AdminComponentIndexer) GetModuleRoute(name string) (*AdminModule, *AdminModuleRoute, error) {
	modules, err := idx.GetAllModules()
	if err != nil {
		return nil, nil, err
	}
	for moduleIndex := range modules {
		for routeIndex := range modules[moduleIndex].Routes {
			if modules[moduleIndex].Routes[routeIndex].Name == name {
				return &modules[moduleIndex], &modules[moduleIndex].Routes[routeIndex], nil
			}
		}
	}
	return nil, nil, nil
}

// GetAllComponents returns all registered Vue components
func (idx *AdminComponentIndexer) GetAllComponents() ([]VueComponent, error) {
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	return append([]VueComponent(nil), components...), nil
}

// GetAllComponentsView returns an immutable component catalog. Consumers that
// only inspect components can avoid copying the complete Administration
// registry for every document.
func (idx *AdminComponentIndexer) GetAllComponentsView() ([]VueComponent, error) {
	components, err := idx.componentIndex.GetAllValuesView()
	if err != nil {
		return nil, err
	}
	return idx.registrationsWithLiveDocuments(components, ""), nil
}

// GetComponentByTemplatePath returns the component that uses the given template path
func (idx *AdminComponentIndexer) GetComponentByTemplatePath(templatePath string) (*VueComponent, error) {
	normalizedPath := normalizeDefinitionPath(templatePath)
	if normalizedPath == "" {
		return nil, nil
	}
	resolveCached := func() (*VueComponent, bool, error) {
		name := idx.cachedTemplateComponent(normalizedPath)
		if name == "" {
			return nil, false, nil
		}
		component, err := idx.GetEffectiveComponent(name)
		if err != nil || component == nil {
			return component, true, err
		}
		if normalizeDefinitionPath(component.TemplatePath) == normalizedPath {
			return component, true, nil
		}
		return nil, false, nil
	}
	if component, found, err := resolveCached(); found || err != nil {
		return component, err
	}
	allComponents, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	if err := idx.ensureTemplateComponentCatalog(allComponents); err != nil {
		return nil, err
	}
	if component, found, err := resolveCached(); found || err != nil {
		return component, err
	}

	for _, comp := range allComponents {
		// Check if the component's template path matches
		if comp.TemplatePath != "" && normalizeDefinitionPath(comp.TemplatePath) == normalizedPath {
			// Get full component with definition
			fullComps, err := idx.GetComponentWithDefinition(comp.Name)
			if err == nil && len(fullComps) > 0 {
				idx.cacheTemplateComponent(normalizedPath, fullComps[0].Name)
				return &fullComps[0], nil
			}
			idx.cacheTemplateComponent(normalizedPath, comp.Name)
			return &comp, nil
		}

	}

	// Wrapped TypeScript definitions can acquire their template only after the
	// effective component is assembled. Resolve that authoritative view once
	// and cache the ownership for subsequent interactive requests.
	names, err := idx.GetAllComponentNames()
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	for _, name := range names {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component == nil ||
			normalizeDefinitionPath(component.TemplatePath) != normalizedPath {
			continue
		}
		idx.cacheTemplateComponent(normalizedPath, component.Name)
		return component, nil
	}

	return nil, nil
}

// GetComponentRegistrationByTemplatePath returns the source registration that
// owns templatePath without folding sibling overrides into it. Source-oriented
// features use this view to distinguish a template's own block declarations
// from the parent contract they override.
func (idx *AdminComponentIndexer) GetComponentRegistrationByTemplatePath(
	templatePath string,
) (*VueComponent, error) {
	normalizedPath := normalizeDefinitionPath(templatePath)
	if idx == nil || normalizedPath == "" {
		return nil, nil
	}
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	var matches []VueComponent
	for _, component := range components {
		resolved, resolveErr := idx.GetComponentRegistrationWithDefinition(component)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if resolved == nil || normalizeDefinitionPath(resolved.TemplatePath) != normalizedPath {
			continue
		}
		matches = append(matches, *resolved)
	}
	if len(matches) == 0 {
		return nil, nil
	}
	sort.SliceStable(matches, func(left, right int) bool {
		if matches[left].FilePath != matches[right].FilePath {
			return matches[left].FilePath < matches[right].FilePath
		}
		if matches[left].Line != matches[right].Line {
			return matches[left].Line < matches[right].Line
		}
		return matches[left].Name < matches[right].Name
	})
	return &matches[0], nil
}

// GetParentComponentForTemplate returns the effective component contract that
// exists immediately before the source registration owning templatePath. For
// Component.extend this is the named parent. For Component.override it is the
// base registration plus preceding overrides in deterministic index order.
func (idx *AdminComponentIndexer) GetParentComponentForTemplate(
	templatePath string,
) (*VueComponent, error) {
	owner, err := idx.GetComponentRegistrationByTemplatePath(templatePath)
	if err != nil || owner == nil {
		return nil, err
	}
	if owner.Kind == ComponentExtend || owner.ExtendsComponent != "" {
		parentName := owner.ExtendsComponent
		if parentName == "" {
			parentName = owner.TargetComponent
		}
		return idx.GetEffectiveComponent(parentName)
	}
	if owner.Kind != ComponentOverride {
		return nil, nil
	}

	components, err := idx.GetComponent(owner.Name)
	if err != nil {
		return nil, err
	}
	for index := range components {
		if err := idx.populateComponentDefinition(&components[index]); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(components, func(left, right int) bool {
		leftOverride := components[left].Kind == ComponentOverride
		rightOverride := components[right].Kind == ComponentOverride
		if leftOverride != rightOverride {
			return !leftOverride
		}
		if components[left].FilePath != components[right].FilePath {
			return components[left].FilePath < components[right].FilePath
		}
		return components[left].Line < components[right].Line
	})

	result := VueComponent{Name: owner.Name}
	foundPredecessor := false
	for _, component := range components {
		if sameComponentRegistration(component, *owner) {
			break
		}
		own, mergeErr := idx.componentWithMixins(component)
		if mergeErr != nil {
			return nil, mergeErr
		}
		if own.ExtendsComponent != "" {
			parent, parentErr := idx.GetEffectiveComponent(own.ExtendsComponent)
			if parentErr != nil {
				return nil, parentErr
			}
			if parent != nil {
				own = overlayComponents(*parent, own)
			}
		}
		result = overlayComponents(result, own)
		foundPredecessor = true
	}
	if !foundPredecessor {
		return nil, nil
	}
	return &result, nil
}

func sameComponentRegistration(left, right VueComponent) bool {
	return left.Name == right.Name && left.Kind == right.Kind &&
		left.Line == right.Line &&
		normalizeDefinitionPath(left.FilePath) ==
			normalizeDefinitionPath(right.FilePath) &&
		normalizeDefinitionPath(left.TemplatePath) ==
			normalizeDefinitionPath(right.TemplatePath)
}

// GetComponentForTemplateTag resolves a global Administration component or an
// Options API component local to the owner of templatePath. Local aliases are
// deliberately resolved only in their declaring template and never leak into
// workspace-wide component completion.
func (idx *AdminComponentIndexer) GetComponentForTemplateTag(
	templatePath,
	name string,
	owners ...*VueComponent,
) (*VueComponent, bool, error) {
	if templatePath != "" {
		var owner *VueComponent
		if len(owners) > 0 {
			owner = owners[0]
		}
		if owner == nil {
			var err error
			owner, err = idx.GetComponentByTemplatePath(templatePath)
			if err != nil {
				return nil, false, err
			}
		}
		if owner != nil {
			local, found := owner.LocalComponent(name)
			if !found {
				local, found = owner.LocalComponent(CamelToKebab(name))
			}
			if found {
				for _, targetName := range localComponentTargetNames(local) {
					target, targetErr := idx.GetEffectiveComponent(targetName)
					if targetErr != nil {
						return nil, false, targetErr
					}
					if target == nil {
						continue
					}
					resolved, liveErr := idx.componentWithLiveVueDocument(*target)
					if liveErr != nil {
						return nil, false, liveErr
					}
					resolved.Name = local.Name
					resolved.FilePath = local.FilePath
					resolved.DefinitionPath = local.FilePath
					resolved.Line = local.Line
					return &resolved, true, nil
				}
				for _, definitionPath := range localComponentDefinitionCandidates(local) {
					if live, liveFound, liveErr := idx.liveVueComponent(
						definitionPath,
					); liveErr != nil {
						return nil, false, liveErr
					} else if liveFound {
						live.Name = local.Name
						live.FilePath = local.FilePath
						live.DefinitionPath = definitionPath
						live.Line = local.Line
						return &live, true, nil
					}
					definition, definitionErr := idx.GetComponentDefinition(
						definitionPath,
					)
					if definitionErr != nil {
						return nil, false, definitionErr
					}
					if definition == nil {
						continue
					}
					resolved := VueComponent{
						Name: local.Name, FilePath: local.FilePath,
						DefinitionPath: definitionPath, Line: local.Line,
					}
					applyDefinition(&resolved, *definition)
					return &resolved, true, nil
				}
				return &VueComponent{
					Name: local.Name, FilePath: local.FilePath,
					DefinitionPath: local.FilePath, Line: local.Line,
				}, true, nil
			}
		}
	}

	components, err := idx.GetComponentWithDefinition(name)
	if err != nil {
		return nil, false, err
	}
	if len(components) == 0 {
		return nil, false, nil
	}
	resolved, err := idx.componentWithLiveVueDocument(components[0])
	if err != nil {
		return nil, false, err
	}
	return &resolved, true, nil
}

// ResolveDynamicComponents returns the complete finite component contract for
// a dynamic selector. It succeeds only when every possible selector name is a
// registered global or template-local component; native/runtime branches keep
// callers conservative.
func (idx *AdminComponentIndexer) ResolveDynamicComponents(
	templatePath string,
	selector VueDynamicComponentSelector,
	owners ...*VueComponent,
) ([]VueComponent, bool, error) {
	if idx == nil || !selector.Complete {
		return nil, false, nil
	}
	names := selector.Names()
	if len(names) == 0 {
		return nil, false, nil
	}
	result := make([]VueComponent, 0, len(names))
	for _, name := range names {
		if !IsComponentTag(name) {
			return nil, false, nil
		}
		component, found, err := idx.GetComponentForTemplateTag(
			templatePath, name, owners...,
		)
		if err != nil {
			return nil, false, err
		}
		if !found || component == nil {
			return nil, false, nil
		}
		result = append(result, *component)
	}
	return result, true, nil
}

// GetLocalComponentAtDefinitionPosition resolves an Options API component
// alias declaration under a JavaScript/TypeScript LSP position. It preserves
// the owning component so callers can keep references and refactors scoped to
// exactly one template.
func (idx *AdminComponentIndexer) GetLocalComponentAtDefinitionPosition(
	definitionPath string,
	line,
	character int,
) (*VueComponent, VueLocalComponent, bool, error) {
	components, err := idx.GetComponentsByDefinitionPath(definitionPath)
	if err != nil {
		return nil, VueLocalComponent{}, false, err
	}
	for componentIndex := range components {
		component := &components[componentIndex]
		for _, local := range component.LocalComponents {
			if !adminSourceRangeContainsPosition(
				local.NameRange, line, character,
			) {
				continue
			}
			return component, local, true, nil
		}
	}
	return nil, VueLocalComponent{}, false, nil
}

func (idx *AdminComponentIndexer) GetLocalDirectiveAtDefinitionPosition(
	definitionPath string,
	line,
	character int,
) (*VueComponent, VueLocalDirective, bool, error) {
	components, err := idx.GetComponentsByDefinitionPath(definitionPath)
	if err != nil {
		return nil, VueLocalDirective{}, false, err
	}
	for componentIndex := range components {
		component := &components[componentIndex]
		for _, local := range component.LocalDirectives {
			if !adminSourceRangeContainsPosition(
				local.NameRange, line, character,
			) {
				continue
			}
			return component, local, true, nil
		}
	}
	return nil, VueLocalDirective{}, false, nil
}

func adminSourceRangeContainsPosition(
	rangeValue AdminSourceRange,
	line,
	character int,
) bool {
	if line < rangeValue.StartLine || line > rangeValue.EndLine {
		return false
	}
	if line == rangeValue.StartLine && character < rangeValue.StartCharacter {
		return false
	}
	if line == rangeValue.EndLine && character > rangeValue.EndCharacter {
		return false
	}
	return true
}

func localComponentTargetNames(local VueLocalComponent) []string {
	var result []string
	appendName := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		for _, existing := range result {
			if existing == name {
				return
			}
		}
		result = append(result, name)
	}
	symbol := strings.TrimSuffix(local.Symbol, "Original")
	appendName(CamelToKebab(symbol))
	if local.ImportPath != "" && local.ImportPath != meteorPackagePath {
		base := filepath.Base(strings.TrimSuffix(local.ImportPath, "/"))
		base = strings.TrimSuffix(base, filepath.Ext(base))
		if base != "index" {
			appendName(CamelToKebab(base))
		}
	}
	return result
}

func localComponentDefinitionCandidates(local VueLocalComponent) []string {
	var result []string
	appendPath := func(path string) {
		path = filepath.Clean(path)
		if path == "." || path == "" {
			return
		}
		for _, current := range result {
			if normalizeDefinitionPath(current) == normalizeDefinitionPath(path) {
				return
			}
		}
		result = append(result, path)
	}
	appendPath(resolveImportPath(local.FilePath, local.ImportPath))
	for _, candidate := range adminTypeImportCandidates(
		local.FilePath, local.ImportPath,
	) {
		appendPath(candidate)
	}
	return result
}

func (idx *AdminComponentIndexer) cachedTemplateComponent(path string) string {
	if idx == nil {
		return ""
	}
	idx.templateCacheMu.RLock()
	defer idx.templateCacheMu.RUnlock()
	return idx.templateCache[path]
}

func (idx *AdminComponentIndexer) cacheTemplateComponent(path, name string) {
	if idx == nil || path == "" || name == "" {
		return
	}
	idx.templateCacheMu.Lock()
	defer idx.templateCacheMu.Unlock()
	if idx.templateCache == nil {
		idx.templateCache = make(map[string]string)
	}
	idx.templateCache[path] = name
}

// ensureTemplateComponentCatalog projects the persisted component and
// definition repositories into a template-path lookup once per index
// generation. Previously every new Twig document scanned every registration
// and resolved each definition independently.
func (idx *AdminComponentIndexer) ensureTemplateComponentCatalog(
	components []VueComponent,
) error {
	if idx == nil {
		return nil
	}
	idx.templateCacheMu.Lock()
	defer idx.templateCacheMu.Unlock()
	if idx.templateCatalogBuilt {
		return nil
	}
	definitions, err := idx.definitionIndex.GetAllValuesView()
	if err != nil {
		return err
	}
	templatesByDefinition := make(map[string]string, len(definitions))
	for _, definition := range definitions {
		definitionPath := normalizeDefinitionPath(definition.FilePath)
		templatePath := normalizeDefinitionPath(definition.TemplatePath)
		if definitionPath == "" || templatePath == "" {
			continue
		}
		if _, exists := templatesByDefinition[definitionPath]; !exists {
			templatesByDefinition[definitionPath] = templatePath
		}
	}
	if idx.templateCache == nil {
		idx.templateCache = make(map[string]string)
	}
	cache := func(templatePath, name string) {
		templatePath = normalizeDefinitionPath(templatePath)
		if templatePath == "" || name == "" {
			return
		}
		if _, exists := idx.templateCache[templatePath]; !exists {
			idx.templateCache[templatePath] = name
		}
	}
	for _, component := range components {
		cache(component.TemplatePath, component.Name)
		definitionPath := normalizeDefinitionPath(component.DefinitionPath)
		cache(templatesByDefinition[definitionPath], component.Name)
		if component.InlineDefinition != nil {
			cache(component.InlineDefinition.TemplatePath, component.Name)
		}
	}
	idx.templateCatalogBuilt = true
	return nil
}

func (idx *AdminComponentIndexer) invalidateTemplateComponentCache() {
	if idx == nil {
		return
	}
	idx.templateCacheMu.Lock()
	idx.templateCache = nil
	idx.templateCatalogBuilt = false
	idx.templateCacheMu.Unlock()
	idx.effectiveCacheMu.Lock()
	idx.effectiveCache = nil
	idx.effectiveCacheEpoch++
	idx.effectiveCacheMu.Unlock()
}

// ResolveTwigScopedSlot resolves the lexical v-slot scope at offset against
// the effective component API. It is shared by completion, hover, and
// definition so inherited slot contracts behave identically in every feature.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlot(
	root *twigsyntax.Node,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigScopedSlot, error) {
	resolved, err := idx.resolveTwigScopedSlots(
		root, offset, firstOptionalString(templatePath), nil,
	)
	if err != nil || len(resolved) == 0 {
		return nil, err
	}
	return &resolved[len(resolved)-1], nil
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotForOwner(
	root *twigsyntax.Node,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigScopedSlot, error) {
	resolved, err := idx.resolveTwigScopedSlots(
		root, offset, templatePath, owner,
	)
	if err != nil || len(resolved) == 0 {
		return nil, err
	}
	return &resolved[len(resolved)-1], nil
}

// ResolveTwigScopedSlots resolves every lexical v-slot scope visible at the
// offset, ordered from the outermost scope to the innermost one. Consumers
// that build a lexical environment need all of them; member access keeps using
// the innermost matching binding through ResolveTwigScopedSlotBinding.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlots(
	root *twigsyntax.Node,
	offset uint32,
	templatePath ...string,
) ([]ResolvedTwigScopedSlot, error) {
	return idx.resolveTwigScopedSlots(
		root, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotsForOwner(
	root *twigsyntax.Node,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) ([]ResolvedTwigScopedSlot, error) {
	return idx.resolveTwigScopedSlots(root, offset, templatePath, owner)
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlots(
	root *twigsyntax.Node,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) ([]ResolvedTwigScopedSlot, error) {
	if idx == nil || root == nil {
		return nil, nil
	}
	scopes := TwigScopedSlotsAtOffset(root, offset)
	resolved := make([]ResolvedTwigScopedSlot, 0, len(scopes))
	for _, scope := range scopes {
		current, err := idx.resolveTwigScopedSlot(
			root, scope, templatePath, owner,
		)
		if err != nil {
			return nil, err
		}
		if current != nil {
			resolved = append(resolved, *current)
		}
	}
	return resolved, nil
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlot(
	root *twigsyntax.Node,
	scope TwigScopedSlot,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigScopedSlot, error) {
	if startTag := TwigScopedSlotStartingTag(root, scope); startTag != nil {
		components, complete, resolveErr :=
			idx.ResolveTwigSlotConsumerComponents(
				templatePath, startTag, owner,
			)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if complete && len(components) > 0 {
			return resolveTwigScopedSlotContracts(scope, components, complete), nil
		}
		if _, dynamic := TwigDynamicComponentSelector(
			TwigSlotOwnerStartingTag(startTag),
		); dynamic {
			// Runtime-open selectors retain lexical locals, but never borrow a
			// payload shape from only the statically visible branches.
			return &ResolvedTwigScopedSlot{
				Component: VueComponent{Name: scope.ComponentName},
				Slot: VueComponentSlot{
					Name: scope.SlotName, MembersComplete: false,
				},
				Scope: scope,
			}, nil
		}
	}
	component, err := idx.GetEffectiveComponent(scope.ComponentName)
	if err != nil {
		return nil, err
	}
	if component == nil {
		return &ResolvedTwigScopedSlot{
			Component: VueComponent{Name: scope.ComponentName},
			Slot: VueComponentSlot{
				Name: scope.SlotName, MembersComplete: false,
			},
			Scope: scope,
		}, nil
	}
	if slot, exists := component.ComponentSlot(scope.SlotName); exists {
		return &ResolvedTwigScopedSlot{
			Component: *component, Slot: slot, Scope: scope,
			Contracts: []ResolvedTwigSlotContract{{
				Component: *component, Slot: slot,
			}},
			ContractsComplete: true,
		}, nil
	}
	return &ResolvedTwigScopedSlot{
		Component: *component,
		Slot:      VueComponentSlot{Name: scope.SlotName}, Scope: scope,
	}, nil
}

func resolveTwigScopedSlotContracts(
	scope TwigScopedSlot,
	components []VueComponent,
	selectorComplete bool,
) *ResolvedTwigScopedSlot {
	contracts := make([]ResolvedTwigSlotContract, 0, len(components))
	allFound := selectorComplete
	for _, component := range components {
		slot, found := component.ComponentSlot(scope.SlotName)
		if !found {
			allFound = false
			continue
		}
		contracts = append(contracts, ResolvedTwigSlotContract{
			Component: component, Slot: slot,
		})
	}
	component := VueComponent{}
	if len(components) == 1 {
		component = components[0]
	} else {
		names := make([]string, 0, len(components))
		for _, candidate := range components {
			names = append(names, candidate.Name)
		}
		component.Name = strings.Join(names, " | ")
	}
	result := &ResolvedTwigScopedSlot{
		Component: component,
		Slot: VueComponentSlot{
			Name: scope.SlotName, MembersComplete: false,
		},
		Scope: scope, Contracts: contracts,
		ContractsComplete: allFound && len(contracts) == len(components),
	}
	if !result.ContractsComplete {
		return result
	}
	if len(contracts) == 1 {
		result.Slot = contracts[0].Slot
		return result
	}
	result.Slot = commonTwigScopedSlotContract(scope.SlotName, contracts)
	return result
}

func commonTwigScopedSlotContract(
	name string,
	contracts []ResolvedTwigSlotContract,
) VueComponentSlot {
	result := VueComponentSlot{Name: name, MembersComplete: len(contracts) > 0}
	if len(contracts) == 0 {
		return result
	}
	result.FilePath = contracts[0].Slot.FilePath
	result.Line = contracts[0].Slot.Line
	payloadTypes := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		result.MembersComplete = result.MembersComplete &&
			contract.Slot.MembersComplete
		if contract.Slot.FilePath != result.FilePath ||
			contract.Slot.Line != result.Line {
			result.FilePath = ""
			result.Line = 0
		}
		if contract.Slot.PayloadType == "" {
			payloadTypes = nil
		} else if payloadTypes != nil {
			payloadTypes = append(payloadTypes, contract.Slot.PayloadType)
		}
	}
	if len(payloadTypes) == len(contracts) {
		result.PayloadType = mergeVueTypes(payloadTypes...)
	}
	for _, candidate := range contracts[0].Slot.Members {
		members := []VueComponentSlotMember{candidate}
		common := true
		for _, contract := range contracts[1:] {
			member, found := contract.Slot.Member(candidate.Name)
			if !found {
				common = false
				break
			}
			members = append(members, member)
		}
		if !common {
			continue
		}
		member := candidate
		types := make([]string, 0, len(members))
		for _, current := range members {
			if current.Type != "" {
				types = append(types, current.Type)
			}
			if current.FilePath != member.FilePath || current.Line != member.Line {
				member.FilePath = ""
				member.Line = 0
			}
		}
		if len(types) == len(members) {
			member.Type = mergeVueTypes(types...)
		} else {
			member.Type = ""
		}
		result.Members = append(result.Members, member)
	}
	return result
}

// ResolveTwigScopedSlotBinding resolves an identifier in either the binding
// declaration or the evaluated body of a scoped slot.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlotBinding(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigSlotBinding, error) {
	return idx.resolveTwigScopedSlotBinding(
		root, node, content, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotBindingForOwner(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotBinding, error) {
	return idx.resolveTwigScopedSlotBinding(
		root, node, content, offset, templatePath, owner,
	)
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlotBinding(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotBinding, error) {
	identifier, rangeValue, found := IdentifierAtOffset(content, offset)
	if !found {
		return nil, nil
	}
	scopes := TwigScopedSlotsAtOffset(root, offset)
	for scopeIndex := len(scopes) - 1; scopeIndex >= 0; scopeIndex-- {
		scope := scopes[scopeIndex]
		inBinding := scope.IsBindingOffset(offset)
		currentIdentifier := identifier
		currentRange := rangeValue
		if !inBinding {
			if !IsTwigVueExpressionAt(node, offset) {
				continue
			}
			currentIdentifier, currentRange, found =
				ExpressionRootIdentifierAtOffset(content, offset)
			if !found {
				continue
			}
		}
		for _, binding := range scope.Bindings {
			matched := currentIdentifier == binding.LocalName
			if inBinding && currentIdentifier == binding.MemberName {
				matched = true
			}
			if !matched {
				continue
			}
			resolved, err := idx.resolveTwigScopedSlot(
				root, scope, templatePath, owner,
			)
			if err != nil || resolved == nil {
				return nil, err
			}
			member, memberFound := resolved.Slot.Member(binding.MemberName)
			members := resolvedTwigSlotContractMembers(
				resolved.Contracts, binding.MemberName,
			)
			return &ResolvedTwigSlotBinding{
				ResolvedTwigScopedSlot: *resolved,
				Binding:                binding, Member: member, Members: members,
				MemberFound: memberFound,
				Identifier:  currentIdentifier, Range: currentRange,
			}, nil
		}
	}
	return nil, nil
}

// ResolveTwigScopedSlotMember resolves a direct property accessed through a
// whole-object scoped-slot local. It returns a result even when MemberFound is
// false so callers can suppress unrelated component-scope fallbacks.
func (idx *AdminComponentIndexer) ResolveTwigScopedSlotMember(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigSlotMember, error) {
	return idx.resolveTwigScopedSlotMember(
		root, node, content, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigScopedSlotMemberForOwner(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotMember, error) {
	return idx.resolveTwigScopedSlotMember(
		root, node, content, offset, templatePath, owner,
	)
}

func (idx *AdminComponentIndexer) resolveTwigScopedSlotMember(
	root *twigsyntax.Node,
	node *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	owner *VueComponent,
) (*ResolvedTwigSlotMember, error) {
	if !IsTwigVueExpressionAt(node, offset) {
		return nil, nil
	}
	access, found := TwigVueExpressionMemberAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	scopes := TwigScopedSlotsAtOffset(root, offset)
	for scopeIndex := len(scopes) - 1; scopeIndex >= 0; scopeIndex-- {
		scope := scopes[scopeIndex]
		for _, binding := range scope.Bindings {
			if !binding.WholeObject || binding.LocalName != access.Root {
				continue
			}
			resolved, err := idx.resolveTwigScopedSlot(
				root, scope, templatePath, owner,
			)
			if err != nil || resolved == nil {
				return nil, err
			}
			result := &ResolvedTwigSlotMember{
				ResolvedTwigScopedSlot: *resolved,
				Binding:                binding,
				Access:                 access,
				ReceiverFound:          true,
				ReceiverMembers:        slotTwigVueMembers(resolved.Slot.Members),
				MembersComplete:        resolved.Slot.MembersComplete,
				ReceiverType:           resolved.Slot.PayloadType,
			}
			contextPath := resolved.Slot.FilePath
			for _, segment := range access.Receiver {
				indexType, indexErr := idx.resolveTwigVueIndexExpressionType(
					root, content, segment, "", nil,
				)
				if indexErr != nil {
					return nil, indexErr
				}
				receiverType, receiverMembers, membersComplete,
					resolvedContext, receiverFound, resolveErr :=
					idx.resolveTwigVueReceiverSegment(
						result.ReceiverType,
						result.ReceiverMembers,
						contextPath,
						segment,
						indexType,
					)
				if resolveErr != nil {
					return nil, resolveErr
				}
				if !receiverFound {
					result.ReceiverFound = false
					result.ReceiverMembers = nil
					result.MembersComplete = false
					return result, nil
				}
				result.ReceiverType = receiverType
				result.ReceiverMembers = receiverMembers
				result.MembersComplete = membersComplete
				contextPath = resolvedContext
			}
			member, memberFound := twigVueMemberNamed(
				result.ReceiverMembers, access.Member,
			)
			result.Member = slotMemberFromTwigVue(member)
			result.MemberFound = memberFound
			if len(access.Receiver) == 0 {
				result.Members = resolvedTwigSlotContractMembers(
					resolved.Contracts, access.Member,
				)
			}
			return result, nil
		}
	}
	return nil, nil
}

func resolvedTwigSlotContractMembers(
	contracts []ResolvedTwigSlotContract,
	name string,
) []VueComponentSlotMember {
	result := make([]VueComponentSlotMember, 0, len(contracts))
	seen := make(map[VueComponentSlotMember]bool)
	for _, contract := range contracts {
		member, found := contract.Slot.Member(name)
		if !found {
			continue
		}
		if seen[member] {
			continue
		}
		seen[member] = true
		result = append(result, member)
	}
	return result
}

func slotTwigVueMembers(members []VueComponentSlotMember) []TwigVueMember {
	result := make([]TwigVueMember, 0, len(members))
	for _, member := range members {
		result = append(result, TwigVueMember{
			Name: member.Name, Type: member.Type,
			DefinitionPath: member.FilePath, DefinitionLine: member.Line,
			DefinitionRange: member.NameRange,
		})
	}
	return result
}

func slotMemberFromTwigVue(member TwigVueMember) VueComponentSlotMember {
	return VueComponentSlotMember{
		Name: member.Name, Type: member.Type,
		FilePath: member.DefinitionPath, Line: member.DefinitionLine,
		NameRange: member.DefinitionRange,
	}
}

// ResolveTwigVueBindings resolves document-local Vue template variables and
// enriches an implicit $event with the indexed component event payload type
// and declaration. v-for bindings never enter the persistent workspace index.
func (idx *AdminComponentIndexer) ResolveTwigVueBindings(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) ([]TwigVueBinding, error) {
	return idx.resolveTwigVueBindings(
		root, content, offset, firstOptionalString(templatePath), nil,
	)
}

// ResolveTwigVueBindingsForComponent resolves lexical bindings against a
// request-local owner component. Open Vue buffers use this path so v-for
// element inference sees unsaved props, computed values, and type imports.
func (idx *AdminComponentIndexer) ResolveTwigVueBindingsForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) ([]TwigVueBinding, error) {
	return idx.resolveTwigVueBindings(
		root, content, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueBindings(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) ([]TwigVueBinding, error) {
	bindings := TwigVueBindingsAtOffset(root, content, offset)
	for index := range bindings {
		bindings[index].Members = TwigVueBindingMembers(
			root, content, bindings[index],
		)
		if err := idx.enrichTwigVueBindingWithVisible(
			&bindings[index], templatePath, bindings[:index], component,
		); err != nil {
			return nil, err
		}
	}
	return bindings, nil
}

// ResolveTwigVueBinding resolves the lexical Vue variable under the cursor.
func (idx *AdminComponentIndexer) ResolveTwigVueBinding(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*TwigVueBinding, error) {
	return idx.resolveTwigVueBinding(
		root, content, offset, firstOptionalString(templatePath), nil,
	)
}

func (idx *AdminComponentIndexer) ResolveTwigVueBindingForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*TwigVueBinding, error) {
	return idx.resolveTwigVueBinding(
		root, content, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueBinding(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*TwigVueBinding, error) {
	target, found := TwigVueBindingAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	bindings, err := idx.resolveTwigVueBindings(
		root, content, offset, templatePath, component,
	)
	if err != nil {
		return nil, err
	}
	for bindingIndex := range bindings {
		if bindings[bindingIndex].sameIdentity(*target) {
			return &bindings[bindingIndex], nil
		}
	}
	return nil, nil
}

// ResolveTwigVueExpressionType resolves one complete Administration template
// expression against its lexical v-for/event/slot scope and effective
// component instance. It is intentionally limited to statically inspectable
// expressions so callers can use the result for correctness diagnostics.
func (idx *AdminComponentIndexer) ResolveTwigVueExpressionType(
	root *twigsyntax.Node,
	content []byte,
	expression string,
	offset uint32,
	templatePath string,
) (string, bool, error) {
	return idx.resolveTwigVueExpressionType(
		root, content, expression, offset, templatePath, nil,
	)
}

// ResolveTwigVueExpressionTypeForComponent is the live-document counterpart
// of ResolveTwigVueExpressionType.
func (idx *AdminComponentIndexer) ResolveTwigVueExpressionTypeForComponent(
	root *twigsyntax.Node,
	content []byte,
	expression string,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (string, bool, error) {
	return idx.resolveTwigVueExpressionType(
		root, content, expression, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueExpressionType(
	root *twigsyntax.Node,
	content []byte,
	expression string,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (string, bool, error) {
	if idx == nil || root == nil || strings.TrimSpace(expression) == "" {
		return "", false, nil
	}
	visible, err := idx.resolveTwigVueBindings(
		root, content, offset, templatePath, component,
	)
	if err != nil {
		return "", false, err
	}
	if component == nil && templatePath != "" {
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil {
			return "", false, err
		}
	}
	if mutableVueDataExpression(expression, visible, component) {
		// An untyped Options API data seed is not an authoritative use-site
		// type once methods or watchers assign another value to it. Completion
		// can still use the indexed seed, but correctness diagnostics must stay
		// conservative without flow-sensitive narrowing.
		return "", false, nil
	}
	resolved, found, err := idx.resolveTwigVueIterableExpressionType(
		expression, visible, component,
	)
	if err != nil || !found || resolved.Type == "" {
		return "", false, err
	}
	return resolved.Type, true, nil
}

func mutableVueDataExpression(
	expression string,
	visible []TwigVueBinding,
	component *VueComponent,
) bool {
	if component == nil {
		return false
	}
	path, matched := vueStaticTemplateExpression(expression)
	if !matched || len(path) == 0 {
		return false
	}
	root := path[0].Name
	for visibleIndex := len(visible) - 1; visibleIndex >= 0; visibleIndex-- {
		if visible[visibleIndex].Name == root {
			return false
		}
	}
	member, found := component.TemplateMember(root)
	if !found || member.Kind != ComponentMemberData {
		return false
	}
	for _, assignment := range component.Assignments {
		if assignment.Target == root {
			return true
		}
	}
	return false
}

// ResolveTwigVueMember resolves the property under the cursor through the
// structural type of a lexical Vue binding. Every intermediate receiver must
// be a statically named field; unresolved chains return a handled result with
// ReceiverFound false so LSP callers do not fall back to component members.
func (idx *AdminComponentIndexer) ResolveTwigVueMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath ...string,
) (*ResolvedTwigVueMember, error) {
	return idx.resolveTwigVueMember(
		root, content, offset, firstOptionalString(templatePath), nil,
	)
}

// ResolveTwigVueMemberForComponent resolves a lexical member against the
// request-local owner contract of an open Vue document.
func (idx *AdminComponentIndexer) ResolveTwigVueMemberForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueMember, error) {
	return idx.resolveTwigVueMember(
		root, content, offset, templatePath, component,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueMember, error) {
	access, found := TwigVueExpressionMemberAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	binding, err := idx.resolveTwigVueBinding(
		root, content, access.RootRange.Start, templatePath, component,
	)
	if err != nil || binding == nil {
		return nil, err
	}
	resolved := &ResolvedTwigVueMember{
		Binding: *binding, Access: access, ReceiverFound: true,
		ReceiverType:    binding.Type,
		ReceiverMembers: append([]TwigVueMember(nil), binding.Members...),
		MembersComplete: binding.MembersComplete,
	}
	contextPath := binding.TypeContextPath
	if access.RootCalled {
		receiverType := VueCallableReturnType(binding.Type)
		if receiverType == "" {
			resolved.ReceiverFound = false
			resolved.ReceiverMembers = nil
			resolved.MembersComplete = false
			return resolved, nil
		}
		shape, resolveErr := idx.ResolveVueType(
			receiverType, contextPath, componentLiveTypeFiles(component)...,
		)
		if resolveErr != nil {
			return nil, resolveErr
		}
		resolved.ReceiverType = receiverType
		resolved.ReceiverMembers = shape.Members
		resolved.MembersComplete = shape.Complete
	}
	for _, segment := range access.Receiver {
		indexType, indexErr := idx.resolveTwigVueIndexExpressionType(
			root, content, segment, templatePath, component,
		)
		if indexErr != nil {
			return nil, indexErr
		}
		receiverType, receiverMembers, membersComplete,
			resolvedContext, receiverFound, resolveErr :=
			idx.resolveTwigVueReceiverSegment(
				resolved.ReceiverType,
				resolved.ReceiverMembers,
				contextPath,
				segment,
				indexType,
				componentLiveTypeFiles(component)...,
			)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !receiverFound {
			resolved.ReceiverFound = false
			resolved.ReceiverMembers = nil
			resolved.MembersComplete = false
			return resolved, nil
		}
		resolved.ReceiverType = receiverType
		resolved.ReceiverMembers = receiverMembers
		resolved.MembersComplete = membersComplete
		contextPath = resolvedContext
	}
	resolved.Member, resolved.MemberFound = twigVueMemberNamed(
		resolved.ReceiverMembers, access.Member,
	)
	return resolved, nil
}

// ResolveTwigVueInstanceMember resolves a property chain rooted in the
// effective component instance for a template. Lexical Vue and scoped-slot
// bindings win before component members, matching Vue's runtime scoping.
func (idx *AdminComponentIndexer) ResolveTwigVueInstanceMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
) (*ResolvedTwigVueInstanceMember, error) {
	return idx.resolveTwigVueInstanceMember(
		root, content, offset, templatePath, nil,
	)
}

// ResolveTwigVueInstanceMemberForComponent resolves an instance member using
// the request-local component assembled from an open Vue SFC.
func (idx *AdminComponentIndexer) ResolveTwigVueInstanceMemberForComponent(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueInstanceMember, error) {
	return idx.resolveTwigVueInstanceMember(
		root, content, offset, templatePath, component,
	)
}

// ResolveTwigVueInstanceMemberAccessForComponent resolves a pre-parsed member
// access after the caller has established that its root is not a lexical Vue
// or scoped-slot binding. Batch diagnostics use this to avoid repeating the
// same syntax lookup for every component-instance access in a document.
func (idx *AdminComponentIndexer) ResolveTwigVueInstanceMemberAccessForComponent(
	root *twigsyntax.Node,
	content []byte,
	access TwigVueMemberAccess,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueInstanceMember, error) {
	return idx.resolveTwigVueInstanceMemberAccess(
		root, content, access, templatePath, component, false,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueInstanceMember(
	root *twigsyntax.Node,
	content []byte,
	offset uint32,
	templatePath string,
	component *VueComponent,
) (*ResolvedTwigVueInstanceMember, error) {
	if idx == nil || root == nil || templatePath == "" {
		return nil, nil
	}
	access, found := TwigVueExpressionMemberAtOffset(root, content, offset)
	if !found {
		return nil, nil
	}
	return idx.resolveTwigVueInstanceMemberAccess(
		root, content, access, templatePath, component, true,
	)
}

func (idx *AdminComponentIndexer) resolveTwigVueInstanceMemberAccess(
	root *twigsyntax.Node,
	content []byte,
	access TwigVueMemberAccess,
	templatePath string,
	component *VueComponent,
	checkLexicalScope bool,
) (*ResolvedTwigVueInstanceMember, error) {
	if checkLexicalScope {
		if binding, bindingFound := TwigVueBindingAtOffset(
			root, content, access.RootRange.Start,
		); bindingFound && binding != nil {
			return nil, nil
		}
		if scope, scopeFound := TwigScopedSlotAtOffset(
			root, access.RootRange.Start,
		); scopeFound {
			for _, binding := range scope.Bindings {
				if binding.LocalName == access.Root {
					return nil, nil
				}
			}
		}
	}
	if component == nil {
		var err error
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil || component == nil {
			return nil, err
		}
	}
	rootMember, rootFound := component.TemplateMember(access.Root)
	if !rootFound {
		rootMember, rootFound = VueBuiltinMember(access.Root)
	}
	if !rootFound {
		return nil, nil
	}
	resolved := &ResolvedTwigVueInstanceMember{
		Component: *component, RootMember: rootMember, Access: access,
		ReceiverFound: true, ReceiverType: rootMember.Type,
	}
	contextPath := rootMember.TypeContextPath
	if contextPath == "" {
		contextPath = rootMember.FilePath
	}
	if contextPath == "" {
		contextPath = component.DefinitionPath
	}
	if contextPath == "" {
		contextPath = component.FilePath
	}
	rootType, callable := twigVueReceiverType(
		rootMember.Type, access.RootCalled,
	)
	if !callable {
		resolved.ReceiverFound = false
		return resolved, nil
	}
	shape, resolveErr := idx.ResolveVueType(
		rootType, contextPath, componentLiveTypeFiles(component)...,
	)
	if resolveErr != nil {
		return nil, resolveErr
	}
	resolved.ReceiverType = rootType
	resolved.ReceiverMembers = shape.Members
	resolved.MembersComplete = shape.Complete && !rootMember.OpenRuntimeShape
	openRuntimeShape := rootMember.OpenRuntimeShape
	indexedRoot := len(access.Receiver) > 0 && access.Receiver[0].Indexed
	if rootMember.Type == "" ||
		len(shape.Members) == 0 && !shape.Complete && !indexedRoot {
		resolved.ReceiverFound = false
		return resolved, nil
	}
	for _, segment := range access.Receiver {
		indexType, indexErr := idx.resolveTwigVueIndexExpressionType(
			root, content, segment, templatePath, component,
		)
		if indexErr != nil {
			return nil, indexErr
		}
		receiverType, receiverMembers, membersComplete,
			resolvedContext, receiverFound, resolveErr :=
			idx.resolveTwigVueReceiverSegment(
				resolved.ReceiverType,
				resolved.ReceiverMembers,
				contextPath,
				segment,
				indexType,
				componentLiveTypeFiles(component)...,
			)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if !receiverFound {
			resolved.ReceiverFound = false
			resolved.ReceiverMembers = nil
			resolved.MembersComplete = false
			return resolved, nil
		}
		resolved.ReceiverType = receiverType
		resolved.ReceiverMembers = receiverMembers
		resolved.MembersComplete = membersComplete && !openRuntimeShape
		contextPath = resolvedContext
	}
	resolved.Member, resolved.MemberFound = twigVueMemberNamed(
		resolved.ReceiverMembers, access.Member,
	)
	return resolved, nil
}

func twigVueReceiverType(memberType string, called bool) (string, bool) {
	if !called {
		return memberType, true
	}
	returnType := VueCallableReturnType(memberType)
	return returnType, returnType != ""
}

func (idx *AdminComponentIndexer) resolveTwigVueReceiverSegment(
	receiverType string,
	receiverMembers []TwigVueMember,
	contextPath string,
	segment TwigVueMemberSegment,
	indexType string,
	liveFiles ...AdminTypeFile,
) (string, []TwigVueMember, bool, string, bool, error) {
	if segment.Indexed {
		indexedReceiverType := receiverType
		if segment.Optional {
			indexedReceiverType = withoutVueNullishType(indexedReceiverType)
		}
		indexedType, indexedContext, found, err :=
			idx.resolveVueIndexedAccessTypeWithIndexType(
				indexedReceiverType,
				segment.IndexExpression,
				indexType,
				contextPath,
				liveFiles...,
			)
		if err != nil || !found {
			return "", nil, false, contextPath, false, err
		}
		if indexedContext != "" {
			contextPath = indexedContext
		}
		shape, err := idx.ResolveVueType(
			indexedType, contextPath, liveFiles...,
		)
		if err != nil {
			return "", nil, false, contextPath, false, err
		}
		return indexedType, shape.Members, shape.Complete,
			contextPath, true, nil
	}
	member, found := twigVueMemberNamed(receiverMembers, segment.Name)
	if !found {
		return "", nil, false, contextPath, false, nil
	}
	if member.DefinitionPath != "" {
		contextPath = member.DefinitionPath
	}
	nextType, callable := twigVueReceiverType(member.Type, segment.Called)
	if !callable {
		return "", nil, false, contextPath, false, nil
	}
	shape, err := idx.ResolveVueType(nextType, contextPath, liveFiles...)
	if err != nil {
		return "", nil, false, contextPath, false, err
	}
	return nextType, shape.Members, shape.Complete, contextPath, true, nil
}

func (idx *AdminComponentIndexer) resolveTwigVueIndexExpressionType(
	root *twigsyntax.Node,
	content []byte,
	segment TwigVueMemberSegment,
	templatePath string,
	component *VueComponent,
) (string, error) {
	if !segment.Indexed {
		return "", nil
	}
	expression := unwrapVueExpressionParentheses(
		strings.TrimSpace(segment.IndexExpression),
	)
	if expression == "" {
		return "", nil
	}
	if staticType := vueStaticIndexExpressionType(expression); staticType != "" {
		return staticType, nil
	}
	if component == nil && templatePath != "" {
		resolvedComponent, err := idx.GetComponentByTemplatePath(templatePath)
		if err != nil {
			return "", err
		}
		component = resolvedComponent
	}
	bindings, err := idx.resolveTwigVueBindings(
		root, content, segment.IndexRange.Start, templatePath, component,
	)
	if err != nil {
		return "", err
	}
	known := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if binding.Name != "" && binding.Type != "" {
			known[binding.Name] = binding.Type
		}
	}
	if component != nil {
		for _, member := range component.TemplateMembers() {
			if member.Name != "" && member.Type != "" {
				known[member.Name] = member.Type
			}
		}
	}
	var resolve func(string) string
	resolve = func(value string) string {
		value = unwrapVueExpressionParentheses(strings.TrimSpace(value))
		if value == "" {
			return ""
		}
		if staticType := vueStaticIndexExpressionType(value); staticType != "" {
			return staticType
		}
		if isSlotIdentifier(value) {
			return known[value]
		}
		if left, right, split := splitVueTopLevelOperator(value, "+"); split {
			leftType := withoutVueNullishType(resolve(left))
			rightType := withoutVueNullishType(resolve(right))
			if leftType == "number" && rightType == "number" {
				return "number"
			}
			if leftType == "string" || rightType == "string" {
				return "string"
			}
		}
		return ""
	}
	if resolved := resolve(expression); resolved != "" {
		return resolved, nil
	}
	if segment.IndexRange.Len() == 0 {
		return "", nil
	}
	memberOffset := segment.IndexRange.End
	if memberOffset > segment.IndexRange.Start {
		memberOffset--
	}
	resolvedBinding, err := idx.resolveTwigVueMember(
		root, content, memberOffset, templatePath, component,
	)
	if err != nil {
		return "", err
	}
	if resolvedBinding != nil && resolvedBinding.MemberFound {
		return resolvedBinding.Member.Type, nil
	}
	if component == nil || templatePath == "" {
		return "", nil
	}
	resolvedInstance, err := idx.resolveTwigVueInstanceMember(
		root, content, memberOffset, templatePath, component,
	)
	if err != nil {
		return "", err
	}
	if resolvedInstance != nil && resolvedInstance.MemberFound {
		return resolvedInstance.Member.Type, nil
	}
	return "", nil
}

func twigVueMemberNamed(
	members []TwigVueMember,
	name string,
) (TwigVueMember, bool) {
	for _, member := range members {
		if member.Name == name {
			return member, true
		}
	}
	return TwigVueMember{}, false
}

func (idx *AdminComponentIndexer) enrichTwigVueBindingWithVisible(
	binding *TwigVueBinding,
	templatePath string,
	visible []TwigVueBinding,
	component *VueComponent,
) error {
	if binding == nil {
		return nil
	}
	if binding.Kind == TwigVueBindingFor {
		return idx.enrichTwigVueForBinding(
			binding, templatePath, visible, component,
		)
	}
	if binding.Kind != TwigVueBindingEvent {
		return nil
	}
	if idx == nil || binding.ComponentName == "" {
		binding.Type = nativeEventPayloadType(binding.EventName)
		return nil
	}
	component, err := idx.GetEffectiveComponent(binding.ComponentName)
	if err != nil {
		return err
	}
	if component == nil {
		binding.Type = nativeEventPayloadType(binding.EventName)
		return nil
	}
	event, found := component.ComponentEvent(binding.EventName)
	if !found {
		binding.Type = nativeEventPayloadType(binding.EventName)
		return nil
	}
	binding.Type = eventPayloadType(event.Type)
	binding.DefinitionPath = event.FilePath
	if binding.DefinitionPath == "" {
		binding.DefinitionPath = component.DefinitionPath
	}
	if binding.DefinitionPath == "" {
		binding.DefinitionPath = component.FilePath
	}
	binding.DefinitionLine = event.Line
	return nil
}

func (idx *AdminComponentIndexer) enrichTwigVueForBinding(
	binding *TwigVueBinding,
	templatePath string,
	visible []TwigVueBinding,
	component *VueComponent,
) error {
	if binding == nil || templatePath == "" {
		return nil
	}
	if component == nil {
		var err error
		component, err = idx.GetComponentByTemplatePath(templatePath)
		if err != nil {
			return err
		}
	}
	resolved, found, err := idx.resolveTwigVueIterableExpressionType(
		binding.Iterable, visible, component,
	)
	if err != nil || !found || resolved.Type == "" {
		return err
	}
	iterableType := resolved.Type
	typeContextPath := resolved.ContextPath
	bindingType := VueIterableBindingType(iterableType, binding.Ordinal)
	shape, resolveErr := idx.ResolveVueType(
		bindingType, typeContextPath, componentLiveTypeFiles(component)...,
	)
	if resolveErr != nil {
		return resolveErr
	}
	membersComplete := shape.Complete &&
		strings.TrimSpace(bindingType) != "unknown" &&
		strings.TrimSpace(bindingType) != "any" &&
		!resolved.OpenRuntimeShape
	if binding.Ordinal == 0 {
		shape.Members = mergeTwigVueMembers(
			shape.Members,
			componentIterableElementMembers(binding.Iterable, component),
		)
	}
	if membersComplete {
		binding.Members = mergeKnownTwigVueMembers(
			shape.Members, binding.Members,
		)
	} else {
		binding.Members = mergeTwigVueMembers(
			shape.Members, binding.Members,
		)
	}
	binding.MembersComplete = membersComplete
	binding.TypeContextPath = typeContextPath
	binding.Type = bindingType
	return nil
}

func componentIterableElementMembers(
	expression string,
	component *VueComponent,
) []TwigVueMember {
	if component == nil {
		return nil
	}
	path, matched := vueStaticTemplateExpression(expression)
	if !matched || len(path) != 1 || path[0].Called || path[0].Optional {
		return nil
	}
	member, found := component.TemplateMember(path[0].Name)
	if !found || len(member.ElementMembers) == 0 {
		return nil
	}
	result := make([]TwigVueMember, 0, len(member.ElementMembers))
	for _, elementMember := range member.ElementMembers {
		result = append(result, TwigVueMember{
			Name: elementMember.Name, Type: elementMember.Type,
			DefinitionPath: elementMember.FilePath,
			DefinitionLine: elementMember.Line,
		})
	}
	return result
}

func (idx *AdminComponentIndexer) resolveTwigVueIterableExpressionType(
	expression string,
	visible []TwigVueBinding,
	component *VueComponent,
) (resolvedVueExpressionType, bool, error) {
	expression = trimVueSourceExpression(expression)
	if expression == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	if left, right, split := splitVueTopLevelOperator(expression, "??"); split {
		leftResolved, leftFound, err := idx.resolveTwigVueIterableExpressionType(
			left, visible, component,
		)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		rightResolved, rightFound, err := idx.resolveTwigVueIterableExpressionType(
			right, visible, component,
		)
		if err != nil {
			return resolvedVueExpressionType{}, false, err
		}
		result := leftResolved
		result.Type = mergeVueNullishTypes(
			leftResolved.Type, rightResolved.Type,
		)
		if result.ContextPath == "" {
			result.ContextPath = rightResolved.ContextPath
		}
		result.OpenRuntimeShape = leftResolved.OpenRuntimeShape ||
			rightResolved.OpenRuntimeShape
		return result, (leftFound || rightFound) && result.Type != "", nil
	}
	contextPath := ""
	if component != nil {
		contextPath = component.DefinitionPath
		if contextPath == "" {
			contextPath = component.FilePath
		}
	}
	if resolved, found, err := idx.resolveVueObjectTransformExpressionType(
		expression,
		contextPath,
		func(argument string) (resolvedVueExpressionType, bool, error) {
			return idx.resolveTwigVueIterableExpressionType(
				argument, visible, component,
			)
		},
	); err != nil || found {
		return resolved, found, err
	}
	if literalType := vueStaticLiteralType(expression); literalType != "" {
		return resolvedVueExpressionType{
			Type: literalType, ContextPath: contextPath,
		}, true, nil
	}
	if literalType := vueExpressionTextType(expression, nil); literalType != "" {
		return resolvedVueExpressionType{
			Type: literalType, ContextPath: contextPath,
		}, true, nil
	}
	iterablePath, matched := vueStaticTemplateExpression(expression)
	if !matched || len(iterablePath) == 0 {
		return resolvedVueExpressionType{}, false, nil
	}
	var rootType, rootContext string
	rootOpenRuntimeShape := false
	for visibleIndex := len(visible) - 1; visibleIndex >= 0; visibleIndex-- {
		candidate := visible[visibleIndex]
		if candidate.Name != iterablePath[0].Name {
			continue
		}
		rootType = candidate.Type
		rootContext = candidate.TypeContextPath
		break
	}
	if rootType == "" && component != nil {
		member, found := component.TemplateMember(iterablePath[0].Name)
		if !found {
			member, found = VueBuiltinMember(iterablePath[0].Name)
		}
		if !found {
			return resolvedVueExpressionType{}, false, nil
		}
		rootType = member.Type
		rootContext = componentMemberTypeContext(member, *component)
		rootOpenRuntimeShape = member.OpenRuntimeShape
	}
	if rootType == "" {
		return resolvedVueExpressionType{}, false, nil
	}
	if iterablePath[0].Called {
		rootType = VueCallableReturnType(rootType)
		if rootType == "" {
			return resolvedVueExpressionType{}, false, nil
		}
	}
	resolved, found, err := idx.resolveVueStaticTypeChainWithOptional(
		rootType, rootContext, iterablePath[1:], iterablePath[0].Optional,
		componentLiveTypeFiles(component)...,
	)
	resolved.OpenRuntimeShape = resolved.OpenRuntimeShape || rootOpenRuntimeShape
	return resolved, found, err
}

func vueStaticTemplateExpression(
	expression string,
) ([]vueStaticExpressionSegment, bool) {
	expression = strings.TrimSpace(expression)
	if left, right, split := splitVueTopLevelOperator(expression, "??"); split &&
		unwrapVueExpressionParentheses(right) == "[]" {
		expression = left
	}
	for len(expression) >= 2 && expression[0] == '(' &&
		matchingSlotDelimiter(expression, 0, '(', ')') == len(expression)-1 {
		expression = strings.TrimSpace(expression[1 : len(expression)-1])
	}
	if expression == "" {
		return nil, false
	}
	if strings.HasPrefix(expression, "this.") {
		return vueStaticThisExpression(expression)
	}
	if !isVueIdentifierStart(expression[0]) {
		return nil, false
	}
	return vueStaticThisExpression("this." + expression)
}

func mergeTwigVueMembers(
	base,
	overlay []TwigVueMember,
) []TwigVueMember {
	result := append([]TwigVueMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[member.Name] = index
	}
	for _, member := range overlay {
		if index, found := positions[member.Name]; found {
			if member.Documentation == "" {
				member.Documentation = result[index].Documentation
			}
			if member.Type == "" {
				member.Type = result[index].Type
			}
			if member.DefinitionPath == "" {
				member.DefinitionPath = result[index].DefinitionPath
				member.DefinitionLine = result[index].DefinitionLine
			}
			if member.DefinitionRange == (AdminSourceRange{}) {
				member.DefinitionRange = result[index].DefinitionRange
			}
			if len(member.NestedMembers) == 0 &&
				!member.NestedComplete {
				member.NestedMembers = result[index].NestedMembers
				member.NestedComplete = result[index].NestedComplete
			}
			result[index] = member
			continue
		}
		positions[member.Name] = len(result)
		result = append(result, member)
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Name < result[right].Name
	})
	return result
}

func mergeKnownTwigVueMembers(
	base,
	overlay []TwigVueMember,
) []TwigVueMember {
	known := make(map[string]bool, len(base))
	for _, member := range base {
		known[member.Name] = true
	}
	filtered := make([]TwigVueMember, 0, len(overlay))
	for _, member := range overlay {
		if known[member.Name] {
			filtered = append(filtered, member)
		}
	}
	return mergeTwigVueMembers(base, filtered)
}

func firstOptionalString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

// GetAllComponentNames returns all registered component names
func (idx *AdminComponentIndexer) GetAllComponentNames() ([]string, error) {
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(components))
	result := make([]string, 0, len(components))
	for _, component := range components {
		if component.Name == "" || seen[component.Name] {
			continue
		}
		seen[component.Name] = true
		result = append(result, component.Name)
	}
	sort.Strings(result)
	return result, nil
}

// GetComponentsByDefinitionPath returns effective components whose inline or
// imported configuration is owned by filePath.
func (idx *AdminComponentIndexer) GetComponentsByDefinitionPath(
	filePath string,
) ([]VueComponent, error) {
	components, err := idx.GetAllComponentsView()
	if err != nil {
		return nil, err
	}
	normalized := normalizeDefinitionPath(filePath)
	names := make(map[string]bool)
	for _, component := range components {
		definitionPath := component.DefinitionPath
		if definitionPath == "" && component.FilePath == filePath {
			definitionPath = component.FilePath
		}
		if normalizeDefinitionPath(definitionPath) == normalized {
			names[component.Name] = true
		}
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)
	result := make([]VueComponent, 0, len(ordered))
	for _, name := range ordered {
		component, resolveErr := idx.GetEffectiveComponent(name)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if component != nil {
			result = append(result, *component)
		}
	}
	return result, nil
}

// GetComponent returns components by name (may have multiple if extended)
func (idx *AdminComponentIndexer) GetComponent(name string) ([]VueComponent, error) {
	components, err := idx.componentIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	return idx.registrationsWithLiveDocuments(components, name), nil
}

// GetComponentDefinition returns the component definition for a given definition path
func (idx *AdminComponentIndexer) GetComponentDefinition(definitionPath string) (*ComponentDefinition, error) {
	if definition, found, shadowed, err := idx.liveLegacyDefinition(
		definitionPath,
	); err != nil || found || shadowed {
		if !found {
			return nil, err
		}
		return &definition, err
	}
	normalizedPath := normalizeDefinitionPath(definitionPath)
	defs, err := idx.definitionIndex.GetValues(normalizedPath)
	if err != nil {
		return nil, err
	}
	if len(defs) == 0 {
		return nil, nil
	}
	definition, err := idx.definitionWithLiveTemplate(defs[0])
	if err != nil {
		return nil, err
	}
	return &definition, nil
}

// GetComponentDefinitionByName returns the inline component definition by component name
func (idx *AdminComponentIndexer) GetComponentDefinitionByName(name string) (*ComponentDefinition, error) {
	components, componentErr := idx.GetComponent(name)
	if componentErr != nil {
		return nil, componentErr
	}
	for _, component := range components {
		if component.InlineDefinition == nil {
			continue
		}
		definition, err := idx.definitionWithLiveTemplate(
			*component.InlineDefinition,
		)
		if err != nil {
			return nil, err
		}
		return &definition, nil
	}
	defs, err := idx.definitionIndex.GetValues(name)
	if err != nil {
		return nil, err
	}
	filtered := defs[:0]
	for _, definition := range defs {
		if !idx.isLiveLegacyDocumentPath(definition.FilePath) {
			filtered = append(filtered, definition)
		}
	}
	defs = filtered
	if len(defs) == 0 {
		return nil, nil
	}
	definition, err := idx.definitionWithLiveTemplate(defs[0])
	if err != nil {
		return nil, err
	}
	return &definition, nil
}

// GetComponentRegistrationWithDefinition attaches the definition owned by one
// concrete registration without applying parents, mixins, or sibling
// overrides. It is useful for source-oriented features which must distinguish
// an inherited template from a template declared by the registration itself.
func (idx *AdminComponentIndexer) GetComponentRegistrationWithDefinition(
	component VueComponent,
) (*VueComponent, error) {
	if err := idx.populateComponentDefinition(&component); err != nil {
		return nil, err
	}
	return &component, nil
}

// GetComponentWithDefinition returns the effective component definition. It
// resolves the extends chain and overlays all registrations and overrides while
// retaining source locations for inherited members.
func (idx *AdminComponentIndexer) GetComponentWithDefinition(name string) ([]VueComponent, error) {
	component, err := idx.GetEffectiveComponent(name)
	if err != nil || component == nil {
		return nil, err
	}
	return []VueComponent{*component}, nil
}

func (idx *AdminComponentIndexer) GetEffectiveComponent(name string) (*VueComponent, error) {
	return idx.effectiveComponent(name, make(map[string]bool))
}

func (idx *AdminComponentIndexer) effectiveComponent(
	name string,
	resolving map[string]bool,
) (*VueComponent, error) {
	if name == "" || resolving[name] {
		return nil, nil
	}
	idx.effectiveCacheMu.RLock()
	epoch := idx.effectiveCacheEpoch
	cached, cachedFound := idx.effectiveCache[name]
	idx.effectiveCacheMu.RUnlock()
	if cachedFound {
		result := cloneVueComponent(cached)
		return &result, nil
	}
	resolving[name] = true
	defer delete(resolving, name)

	components, err := idx.GetComponent(name)
	if err != nil {
		return nil, err
	}
	if len(components) == 0 {
		return nil, nil
	}

	for i := range components {
		if err := idx.populateComponentDefinition(&components[i]); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(components, func(left, right int) bool {
		leftOverride := components[left].Kind == ComponentOverride
		rightOverride := components[right].Kind == ComponentOverride
		if leftOverride != rightOverride {
			return !leftOverride
		}
		return components[left].FilePath < components[right].FilePath
	})

	result := VueComponent{Name: name}
	for index := range components {
		own, mergeErr := idx.componentWithMixins(components[index])
		if mergeErr != nil {
			return nil, mergeErr
		}
		if own.ExtendsComponent != "" {
			ownDeprecated := own.Deprecated
			parent, parentErr := idx.effectiveComponent(own.ExtendsComponent, resolving)
			if parentErr != nil {
				return nil, parentErr
			}
			if parent != nil {
				own = overlayComponents(*parent, own)
				// Extending a deprecated component does not make the child registry
				// name deprecated. Public props retain their own inherited metadata.
				own.Deprecated = ownDeprecated
			}
		}
		result = overlayComponents(result, own)
	}
	if err := idx.enrichEffectiveComponentMemberTypes(&result); err != nil {
		return nil, err
	}
	idx.effectiveCacheMu.Lock()
	if idx.effectiveCacheEpoch == epoch {
		if idx.effectiveCache == nil {
			idx.effectiveCache = make(map[string]VueComponent)
		}
		idx.effectiveCache[name] = cloneVueComponent(result)
	}
	idx.effectiveCacheMu.Unlock()
	return &result, nil
}

func (idx *AdminComponentIndexer) populateComponentDefinition(component *VueComponent) error {
	if component == nil {
		return nil
	}
	if component.InlineDefinition != nil {
		definition, err := idx.definitionWithLiveTemplate(
			*component.InlineDefinition,
		)
		if err != nil {
			return err
		}
		applyDefinition(component, definition)
		return nil
	}
	var definition *ComponentDefinition
	var err error
	if component.DefinitionPath != "" {
		definition, err = idx.GetComponentDefinition(component.DefinitionPath)
		if err != nil {
			return err
		}
	}
	if definition == nil {
		definitions, lookupErr := idx.definitionIndex.GetValues(component.Name)
		if lookupErr != nil {
			return lookupErr
		}
		for index := range definitions {
			if definitions[index].FilePath == component.FilePath {
				definition = &definitions[index]
				break
			}
		}
		if definition == nil && len(definitions) == 1 {
			definition = &definitions[0]
		}
		if definition != nil {
			resolved, definitionErr := idx.definitionWithLiveTemplate(*definition)
			if definitionErr != nil {
				return definitionErr
			}
			definition = &resolved
		}
	}
	if definition != nil {
		applyDefinition(component, *definition)
	}
	return nil
}

func applyDefinition(component *VueComponent, definition ComponentDefinition) {
	if definition.Deprecated != "" {
		component.Deprecated = definition.Deprecated
	}
	component.Props = definition.Props
	component.ModelProp = definition.ModelProp
	component.ModelEvent = definition.ModelEvent
	component.Emits = definition.Emits
	component.Events = definition.Events
	component.Methods = definition.Methods
	component.Computed = definition.Computed
	component.Data = definition.Data
	component.Injected = definition.Injected
	component.Mixins = definition.Mixins
	component.LocalComponents = definition.LocalComponents
	component.LocalDirectives = definition.LocalDirectives
	component.Members = definition.Members
	component.OpenRuntimeMembers = definition.OpenRuntimeMembers
	component.Assignments = definition.Assignments
	component.Slots = definition.Slots
	component.Blocks = definition.Blocks
	component.TemplatePath = definition.TemplatePath
}

func (idx *AdminComponentIndexer) componentWithMixins(
	component VueComponent,
) (VueComponent, error) {
	result := VueComponent{Name: component.Name}
	seen := make(map[string]bool)
	var applyMixin func(string) error
	applyMixin = func(name string) error {
		if name == "" || seen[name] {
			return nil
		}
		seen[name] = true
		mixins, err := idx.GetMixin(name)
		if err != nil {
			return err
		}
		if len(mixins) == 0 {
			result.OpenRuntimeMembers = true
		}
		sort.SliceStable(mixins, func(left, right int) bool {
			return mixins[left].FilePath < mixins[right].FilePath
		})
		for _, mixin := range mixins {
			for _, parent := range mixin.Definition.Mixins {
				if err := applyMixin(parent); err != nil {
					return err
				}
			}
			mixinComponent := VueComponent{Name: component.Name}
			applyDefinition(&mixinComponent, mixin.Definition)
			result = overlayComponents(result, mixinComponent)
		}
		return nil
	}
	for _, mixin := range component.Mixins {
		if err := applyMixin(mixin); err != nil {
			return component, err
		}
	}
	return overlayComponents(result, component), nil
}

// deduplicateComponents merges multiple component entries with the same name
// into a single entry, preferring entries with more complete data
func deduplicateComponents(components []VueComponent) []VueComponent {
	if len(components) <= 1 {
		return components
	}

	// Find the best component (one with the most complete data)
	best := components[0]
	for i := 1; i < len(components); i++ {
		comp := components[i]
		// Prefer component with props defined
		if len(comp.Props) > len(best.Props) {
			best = mergeComponents(best, comp)
		} else if len(comp.Props) < len(best.Props) {
			best = mergeComponents(comp, best)
		} else {
			// Same number of props, prefer one with definition path
			if comp.DefinitionPath != "" && best.DefinitionPath == "" {
				best = mergeComponents(best, comp)
			} else {
				best = mergeComponents(comp, best)
			}
		}
	}

	return []VueComponent{best}
}

// SaveComponentDefinition saves a component definition (primarily for testing)
func (idx *AdminComponentIndexer) SaveComponentDefinition(key string, def ComponentDefinition) error {
	idx.invalidateTemplateComponentCache()
	batchSave := make(map[string]map[string]ComponentDefinition)
	batchSave[def.FilePath] = map[string]ComponentDefinition{
		key: def,
	}
	return idx.definitionIndex.BatchSaveItems(batchSave)
}

// SaveComponent saves a component (primarily for testing)
func (idx *AdminComponentIndexer) SaveComponent(comp VueComponent) error {
	idx.invalidateTemplateComponentCache()
	batchSave := make(map[string]map[string]VueComponent)
	batchSave[comp.FilePath] = map[string]VueComponent{
		comp.Name: comp,
	}
	return idx.componentIndex.BatchSaveItems(batchSave)
}

// mergeComponents merges two components, taking data from 'preferred' when available,
// falling back to 'fallback' for missing data
func mergeComponents(fallback, preferred VueComponent) VueComponent {
	result := preferred
	result.OpenRuntimeMembers = preferred.OpenRuntimeMembers ||
		fallback.OpenRuntimeMembers
	if result.Deprecated == "" {
		result.Deprecated = fallback.Deprecated
	}
	if result.ExtendsComponent == "" {
		result.ExtendsComponent = fallback.ExtendsComponent
	}
	if result.ImportPath == "" {
		result.ImportPath = fallback.ImportPath
	}
	if result.DefinitionPath == "" {
		result.DefinitionPath = fallback.DefinitionPath
	}
	if len(result.Props) == 0 {
		result.Props = fallback.Props
	}
	if result.ModelProp == "" {
		result.ModelProp = fallback.ModelProp
	}
	if result.ModelEvent == "" {
		result.ModelEvent = fallback.ModelEvent
	}
	if len(result.Emits) == 0 {
		result.Emits = fallback.Emits
	}
	if len(result.Events) == 0 {
		result.Events = fallback.Events
	}
	if len(result.Methods) == 0 {
		result.Methods = fallback.Methods
	}
	if len(result.Computed) == 0 {
		result.Computed = fallback.Computed
	}
	if len(result.Data) == 0 {
		result.Data = fallback.Data
	}
	if len(result.Injected) == 0 {
		result.Injected = fallback.Injected
	}
	if len(result.Mixins) == 0 {
		result.Mixins = fallback.Mixins
	}
	if len(result.LocalComponents) == 0 {
		result.LocalComponents = fallback.LocalComponents
	}
	if len(result.LocalDirectives) == 0 {
		result.LocalDirectives = fallback.LocalDirectives
	}
	if len(result.Members) == 0 {
		result.Members = fallback.Members
	}
	if len(result.Assignments) == 0 {
		result.Assignments = fallback.Assignments
	}
	if len(result.Slots) == 0 {
		result.Slots = fallback.Slots
	}
	if len(result.Blocks) == 0 {
		result.Blocks = fallback.Blocks
	}
	if result.TemplatePath == "" {
		result.TemplatePath = fallback.TemplatePath
	}
	return result
}

func overlayComponents(base, overlay VueComponent) VueComponent {
	result := base
	result.OpenRuntimeMembers = result.OpenRuntimeMembers ||
		overlay.OpenRuntimeMembers
	if overlay.Name != "" {
		result.Name = overlay.Name
	}
	if overlay.Deprecated != "" {
		result.Deprecated = overlay.Deprecated
	}
	if overlay.Kind != "" {
		result.Kind = overlay.Kind
	}
	if overlay.TargetComponent != "" {
		result.TargetComponent = overlay.TargetComponent
	}
	if overlay.ExtendsComponent != "" {
		result.ExtendsComponent = overlay.ExtendsComponent
	}
	if overlay.ImportPath != "" {
		result.ImportPath = overlay.ImportPath
	}
	if overlay.FilePath != "" {
		result.FilePath = overlay.FilePath
	}
	if overlay.DefinitionPath != "" {
		result.DefinitionPath = overlay.DefinitionPath
	}
	if overlay.Line != 0 {
		result.Line = overlay.Line
	}
	if overlay.TemplatePath != "" {
		result.TemplatePath = overlay.TemplatePath
	}
	if overlay.ModelProp != "" {
		result.ModelProp = overlay.ModelProp
	}
	if overlay.ModelEvent != "" {
		result.ModelEvent = overlay.ModelEvent
	}
	result.Props = overlayProps(result.Props, overlay.Props)
	result.Emits = overlayNames(result.Emits, overlay.Emits)
	result.Events = overlayEvents(result.Events, overlay.Events)
	result.Methods = overlayNames(result.Methods, overlay.Methods)
	result.Computed = overlayNames(result.Computed, overlay.Computed)
	result.Data = overlayNames(result.Data, overlay.Data)
	result.Injected = overlayNames(result.Injected, overlay.Injected)
	result.Mixins = overlayNames(result.Mixins, overlay.Mixins)
	result.LocalComponents = overlayLocalComponents(
		result.LocalComponents,
		overlay.LocalComponents,
	)
	result.LocalDirectives = overlayLocalDirectives(
		result.LocalDirectives,
		overlay.LocalDirectives,
	)
	result.Members = overlayMembers(result.Members, overlay.Members)
	result.Assignments = append(result.Assignments, overlay.Assignments...)
	result.Slots = overlaySlots(result.Slots, overlay.Slots)
	result.Blocks = overlayBlocks(result.Blocks, overlay.Blocks)
	return result
}

func overlayProps(base, overlay []VueComponentProp) []VueComponentProp {
	result := append([]VueComponentProp(nil), base...)
	positions := make(map[string]int, len(result))
	for index, prop := range result {
		positions[prop.Name] = index
	}
	for _, prop := range overlay {
		if index, exists := positions[prop.Name]; exists {
			result[index] = prop
		} else {
			positions[prop.Name] = len(result)
			result = append(result, prop)
		}
	}
	return result
}

func overlayNames(base, overlay []string) []string {
	result := append([]string(nil), base...)
	seen := make(map[string]bool, len(result))
	for _, name := range result {
		seen[name] = true
	}
	for _, name := range overlay {
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result
}

func overlayLocalComponents(
	base,
	overlay []VueLocalComponent,
) []VueLocalComponent {
	result := append([]VueLocalComponent(nil), base...)
	positions := make(map[string]int, len(result))
	for index, component := range result {
		positions[strings.ToLower(component.Name)] = index
	}
	for _, component := range overlay {
		key := strings.ToLower(component.Name)
		if key == "" {
			continue
		}
		if index, exists := positions[key]; exists {
			result[index] = component
		} else {
			positions[key] = len(result)
			result = append(result, component)
		}
	}
	return result
}

func overlayLocalDirectives(
	base,
	overlay []VueLocalDirective,
) []VueLocalDirective {
	result := append([]VueLocalDirective(nil), base...)
	positions := make(map[string]int, len(result))
	for index, directive := range result {
		positions[strings.ToLower(directive.Name)] = index
	}
	for _, directive := range overlay {
		key := strings.ToLower(directive.Name)
		if key == "" {
			continue
		}
		if index, exists := positions[key]; exists {
			result[index] = directive
		} else {
			positions[key] = len(result)
			result = append(result, directive)
		}
	}
	return result
}

func overlayMembers(base, overlay []VueComponentMember) []VueComponentMember {
	result := append([]VueComponentMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[string(member.Kind)+"\x00"+member.Name] = index
	}
	for _, member := range overlay {
		key := string(member.Kind) + "\x00" + member.Name
		if index, exists := positions[key]; exists {
			if member.Deprecated == "" {
				member.Deprecated = result[index].Deprecated
			}
			result[index] = member
		} else {
			positions[key] = len(result)
			result = append(result, member)
		}
	}
	return result
}

func overlaySlots(base, overlay []VueComponentSlot) []VueComponentSlot {
	result := append([]VueComponentSlot(nil), base...)
	positions := make(map[string]int, len(result))
	for index, slot := range result {
		positions[slot.identityKey()] = index
	}
	for _, slot := range overlay {
		key := slot.identityKey()
		if index, exists := positions[key]; exists {
			current := result[index]
			if slot.FilePath == "" {
				slot.FilePath = current.FilePath
			}
			if slot.Line == 0 {
				slot.Line = current.Line
			}
			if slot.NameRange == (AdminSourceRange{}) &&
				slot.FilePath == current.FilePath {
				slot.NameRange = current.NameRange
			}
			result[index] = slot
		} else {
			positions[key] = len(result)
			result = append(result, slot)
		}
	}
	return result
}

func overlayEvents(base, overlay []VueComponentEvent) []VueComponentEvent {
	result := append([]VueComponentEvent(nil), base...)
	positions := make(map[string]int, len(result))
	for index, event := range result {
		positions[CanonicalEventName(event.Name)] = index
	}
	for _, event := range overlay {
		name := CanonicalEventName(event.Name)
		if name == "" {
			continue
		}
		if index, exists := positions[name]; exists {
			current := result[index]
			if event.Documentation == "" {
				event.Documentation = current.Documentation
			}
			if event.Type == "" {
				event.Type = current.Type
			}
			if event.FilePath == "" {
				event.FilePath = current.FilePath
			}
			if event.Line == 0 {
				event.Line = current.Line
			}
			if event.NameRange == (AdminSourceRange{}) {
				event.NameRange = current.NameRange
			}
			result[index] = event
		} else {
			positions[name] = len(result)
			result = append(result, event)
		}
	}
	return result
}

func overlayBlocks(base, overlay []TwigBlock) []TwigBlock {
	result := append([]TwigBlock(nil), base...)
	positions := make(map[string]int, len(result))
	for index, block := range result {
		positions[block.Name] = index
	}
	for _, block := range overlay {
		if index, exists := positions[block.Name]; exists {
			if block.Deprecated == "" {
				block.Deprecated = result[index].Deprecated
			}
			block.ScopeMembers = overlayBlockScopeMembers(
				result[index].ScopeMembers,
				block.ScopeMembers,
			)
			result[index] = block
		} else {
			positions[block.Name] = len(result)
			result = append(result, block)
		}
	}
	return result
}

func overlayBlockScopeMembers(
	base,
	overlay []TwigBlockScopeMember,
) []TwigBlockScopeMember {
	result := append([]TwigBlockScopeMember(nil), base...)
	positions := make(map[string]int, len(result))
	for index, member := range result {
		positions[member.Name] = index
	}
	for _, member := range overlay {
		if member.Name == "" {
			continue
		}
		if index, exists := positions[member.Name]; exists {
			result[index] = member
		} else {
			positions[member.Name] = len(result)
			result = append(result, member)
		}
	}
	return result
}

// parseComponentRegistrations extracts Shopware.Component.register and extend calls.
func parseComponentRegistrations(root *jssyntax.Node, content []byte, filePath string) []VueComponent {
	return parseComponentRegistrationsWithLineIndex(
		root,
		filePath,
		jssyntax.NewLineIndex(string(content)),
	)
}

func parseComponentRegistrationsWithLineIndex(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponent {
	callNames := []string{
		"Shopware.Component.register",
		"Shopware.Component.extend",
		"Shopware.Component.override",
		"Component.register",
		"Component.extend",
		"Component.override",
	}
	var components []VueComponent
	for _, call := range jsquery.Calls(root, callNames...) {
		name := jsquery.CallName(call)
		line, _ := lineIndex.Position(call.RangeTrimmedTrivia().Start)
		component := VueComponent{FilePath: filePath, Line: int(line) + 1, Kind: ComponentRegister}
		component.Deprecated = componentRegistrationDeprecation(call)
		component.Name = jsquery.StringValue(jsquery.StringArgument(call, 0))
		if component.Name == "" {
			continue
		}

		definitionIndex := 1
		if strings.HasSuffix(name, ".extend") {
			component.Kind = ComponentExtend
			component.ExtendsComponent = jsquery.StringValue(jsquery.StringArgument(call, 1))
			component.TargetComponent = component.ExtendsComponent
			definitionIndex = 2
		} else if strings.HasSuffix(name, ".override") {
			component.Kind = ComponentOverride
			component.TargetComponent = component.Name
		}
		if definition := jsquery.ArgumentExpression(call, definitionIndex); definition != nil {
			switch definition.Kind() {
			case jssyntax.JsObject:
				component.InlineDefinition = parseInlineDefinition(
					root, definition, filePath, lineIndex,
				)
				component.DefinitionPath = filePath
			case jssyntax.JsArrowFunction, jssyntax.JsFunction:
				if importPath := jsquery.DynamicImportPath(definition); importPath != "" {
					component.ImportPath = importPath
					component.DefinitionPath = resolveImportPath(filePath, importPath)
				}
			case jssyntax.JsCallExpression:
				if object := componentDefinitionObject(definition); object != nil {
					component.InlineDefinition = parseInlineDefinition(
						root, object, filePath, lineIndex,
					)
					component.DefinitionPath = filePath
				}
			}
		}
		components = append(components, component)
	}
	for _, component := range parseVueApplicationComponentCollections(
		root, filePath, lineIndex,
	) {
		duplicate := false
		for _, existing := range components {
			if existing.Name == component.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			components = append(components, component)
		}
	}
	return components
}

func componentRegistrationDeprecation(call *jssyntax.Node) string {
	for current := call; current != nil; current = current.Parent() {
		if current.Kind() == jssyntax.JsProgram {
			break
		}
		if deprecation := JavaScriptDeprecation(current); deprecation != "" {
			return deprecation
		}
	}
	return ""
}

// parseVueApplicationComponentCollections recognizes static component maps
// that are deliberately registered on the Vue application after normalizing
// their object keys to kebab-case. Shopware uses this for eager and lazy
// Meteor components, including compound exports that do not have standalone
// declaration files.
func parseVueApplicationComponentCollections(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponent {
	if root == nil {
		return nil
	}
	var result []VueComponent
	for _, declaration := range jsquery.Nodes(
		root, jssyntax.JsVariableDeclaration,
	) {
		nameNode := firstDirectIdentifier(declaration)
		object := firstObject(declaration)
		if nameNode == nil || object == nil {
			continue
		}
		collectionName := jsquery.IdentifierText(nameNode)
		if collectionName == "" ||
			!isVueApplicationComponentCollection(root, collectionName) {
			continue
		}
		for _, property := range jsquery.Properties(object) {
			propertyName := strings.TrimSpace(jsquery.PropertyName(property))
			if propertyName == "" || !isStaticVueIdentifier(propertyName) {
				continue
			}
			value := jsquery.PropertyValue(property)
			importPath := ""
			if value == nil {
				// JavaScript shorthand: { MtButton }.
				importPath = jsquery.ImportPath(root, propertyName)
			} else {
				switch value.Kind() {
				case jssyntax.JsIdentifier:
					importPath = jsquery.ImportPath(
						root, jsquery.IdentifierText(value),
					)
				case jssyntax.JsArrowFunction, jssyntax.JsFunction:
					importPath = jsquery.DynamicImportPath(value)
				default:
					continue
				}
			}
			line := 0
			if lineIndex != nil {
				lineNumber, _ := lineIndex.Position(
					property.RangeTrimmedTrivia().Start,
				)
				line = int(lineNumber) + 1
			}
			result = append(result, VueComponent{
				Name:           CamelToKebab(propertyName),
				FilePath:       filePath,
				DefinitionPath: filePath,
				ImportPath:     importPath,
				Line:           line,
				Kind:           ComponentRegister,
			})
		}
	}
	for _, component := range parseRawVueApplicationComponentCollections(
		root, filePath, lineIndex,
	) {
		duplicate := false
		for _, existing := range result {
			if existing.Name == component.Name {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result = append(result, component)
		}
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Line == result[right].Line {
			return result[left].Name < result[right].Name
		}
		return result[left].Line < result[right].Line
	})
	return result
}

// TypeScript recovery can retain a class method body losslessly even when a
// construct such as `as const` prevents it from producing nested expression
// nodes. Scan only the same strongly identified registration shape so these
// valid declarations remain indexable without broad text heuristics.
func parseRawVueApplicationComponentCollections(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) []VueComponent {
	if root == nil {
		return nil
	}
	source := root.Text()
	collections := rawVueApplicationComponentCollections(source)
	var result []VueComponent
	for collectionName, loopOffset := range collections {
		open := staticVueCollectionObjectStart(
			source[:loopOffset], collectionName,
		)
		if open < 0 {
			continue
		}
		close := balancedBraceEnd(source, open)
		if close <= open || close > loopOffset {
			continue
		}
		for _, segment := range splitTopLevelDeclarations(
			source[open+1:close], open+1,
		) {
			entry, found := rawVueComponentCollectionEntry(segment)
			if !found {
				continue
			}
			line := 0
			if lineIndex != nil {
				lineNumber, _ := lineIndex.Position(uint32(entry.offset))
				line = int(lineNumber) + 1
			}
			importPath := jsquery.ImportPath(root, entry.symbol)
			if importPath == "" {
				importPath = rawDynamicImportPath(entry.expression)
			}
			result = append(result, VueComponent{
				Name:           CamelToKebab(entry.name),
				FilePath:       filePath,
				DefinitionPath: filePath,
				ImportPath:     importPath,
				Line:           line,
				Kind:           ComponentRegister,
			})
		}
	}
	return result
}

func rawVueApplicationComponentCollections(source string) map[string]int {
	result := make(map[string]int)
	const marker = "Object.entries"
	for searchOffset := 0; searchOffset < len(source); {
		relative := strings.Index(source[searchOffset:], marker)
		if relative < 0 {
			break
		}
		position := searchOffset + relative
		searchOffset = position + len(marker)
		if !adminTypeCodePosition(source, position) {
			continue
		}
		cursor := skipJavaScriptSpaces(source, searchOffset)
		if cursor >= len(source) || source[cursor] != '(' {
			continue
		}
		close := matchingSlotDelimiter(source, cursor, '(', ')')
		if close < 0 {
			continue
		}
		collectionName := strings.TrimSpace(source[cursor+1 : close])
		if !isStaticVueIdentifier(collectionName) {
			continue
		}
		cursor = skipJavaScriptSpaces(source, close+1)
		if cursor >= len(source) || source[cursor] != '.' {
			continue
		}
		cursor = skipJavaScriptSpaces(source, cursor+1)
		if !strings.HasPrefix(source[cursor:], "forEach") {
			continue
		}
		cursor = skipJavaScriptSpaces(source, cursor+len("forEach"))
		if cursor >= len(source) || source[cursor] != '(' {
			continue
		}
		callbackClose := matchingSlotDelimiter(source, cursor, '(', ')')
		if callbackClose < 0 {
			continue
		}
		callback := compactJavaScriptText(source[cursor+1 : callbackClose])
		if !strings.Contains(callback, "kebabCase(") ||
			(!strings.Contains(callback, ".component(") &&
				!strings.Contains(callback, "registerAsyncComponent(")) {
			continue
		}
		result[collectionName] = position
	}
	return result
}

func staticVueCollectionObjectStart(source, collectionName string) int {
	for searchEnd := len(source); searchEnd > 0; {
		position := strings.LastIndex(source[:searchEnd], collectionName)
		if position < 0 {
			return -1
		}
		searchEnd = position
		if !adminTypeCodePosition(source, position) ||
			position > 0 && isVueIdentifierPart(source[position-1]) ||
			position+len(collectionName) < len(source) &&
				isVueIdentifierPart(source[position+len(collectionName)]) {
			continue
		}
		before := position
		for before > 0 && isJavaScriptSpace(source[before-1]) {
			before--
		}
		keywordStart := before
		for keywordStart > 0 && isVueIdentifierPart(source[keywordStart-1]) {
			keywordStart--
		}
		if source[keywordStart:before] != "const" &&
			source[keywordStart:before] != "let" {
			continue
		}
		cursor := skipJavaScriptSpaces(source, position+len(collectionName))
		if cursor >= len(source) || source[cursor] != '=' {
			continue
		}
		cursor = skipJavaScriptSpaces(source, cursor+1)
		if cursor < len(source) && source[cursor] == '{' {
			return cursor
		}
	}
	return -1
}

type rawVueComponentEntry struct {
	name       string
	symbol     string
	expression string
	offset     int
}

func rawVueComponentCollectionEntry(
	segment declarationSegment,
) (rawVueComponentEntry, bool) {
	trimmed, skipped := trimDeclarationPrefix(segment.text)
	if trimmed == "" || !isVueIdentifierStart(trimmed[0]) {
		return rawVueComponentEntry{}, false
	}
	end := 1
	for end < len(trimmed) && isVueIdentifierPart(trimmed[end]) {
		end++
	}
	name := trimmed[:end]
	rest := strings.TrimSpace(trimmed[end:])
	entry := rawVueComponentEntry{
		name: name, symbol: name,
		offset: segment.offset + skipped,
	}
	if rest == "" {
		return entry, true
	}
	if rest[0] != ':' {
		return rawVueComponentEntry{}, false
	}
	entry.expression = strings.TrimSpace(rest[1:])
	if entry.expression == "" {
		return rawVueComponentEntry{}, false
	}
	if isStaticVueIdentifier(entry.expression) {
		entry.symbol = entry.expression
	} else {
		entry.symbol = ""
	}
	return entry, true
}

func rawDynamicImportPath(expression string) string {
	position := strings.Index(expression, "import")
	if position < 0 {
		return ""
	}
	cursor := skipJavaScriptSpaces(expression, position+len("import"))
	if cursor >= len(expression) || expression[cursor] != '(' {
		return ""
	}
	cursor = skipJavaScriptSpaces(expression, cursor+1)
	if cursor >= len(expression) ||
		(expression[cursor] != '\'' && expression[cursor] != '"') {
		return ""
	}
	quote := expression[cursor]
	start := cursor + 1
	for cursor = start; cursor < len(expression); cursor++ {
		if expression[cursor] == '\\' {
			cursor++
			continue
		}
		if expression[cursor] == quote {
			return expression[start:cursor]
		}
	}
	return ""
}

func skipJavaScriptSpaces(value string, cursor int) int {
	for cursor < len(value) && isJavaScriptSpace(value[cursor]) {
		cursor++
	}
	return cursor
}

func isVueApplicationComponentCollection(
	root *jssyntax.Node,
	collectionName string,
) bool {
	wantedReceiver := "Object.entries(" + collectionName + ")"
	for _, call := range jsquery.Calls(root) {
		receiver, found := staticCallbackCallReceiver(call, "forEach")
		if !found || compactJavaScriptText(receiver) != wantedReceiver {
			continue
		}
		callback := jsquery.ArgumentExpression(call, 0)
		if callback == nil {
			continue
		}
		text := compactJavaScriptText(callback.Text())
		if !strings.Contains(text, "kebabCase(") {
			continue
		}
		if strings.Contains(text, ".component(") ||
			strings.Contains(text, "registerAsyncComponent(") {
			return true
		}
	}
	return false
}

func parseMixinsAndModules(
	root *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) ([]AdminMixin, []AdminModule) {
	var mixins []AdminMixin
	for _, call := range jsquery.Calls(
		root,
		"Shopware.Mixin.register",
		"Mixin.register",
	) {
		name := jsquery.StringValue(jsquery.StringArgument(call, 0))
		if name == "" {
			continue
		}
		line, _ := lineIndex.Position(call.RangeTrimmedTrivia().Start)
		mixin := AdminMixin{
			Name: name, FilePath: filePath, Line: int(line) + 1,
		}
		if object := componentDefinitionObject(
			jsquery.ArgumentExpression(call, 1),
		); object != nil {
			definition := parseInlineDefinition(
				root, object, filePath, lineIndex,
			)
			if definition != nil {
				mixin.Definition = *definition
			}
		}
		mixins = append(mixins, mixin)
	}

	var modules []AdminModule
	for _, call := range jsquery.Calls(
		root,
		"Shopware.Module.register",
		"Module.register",
	) {
		name := jsquery.StringValue(jsquery.StringArgument(call, 0))
		config := resolveAdminModuleConfig(root, call)
		if name == "" || config == nil {
			continue
		}
		line, _ := lineIndex.Position(call.RangeTrimmedTrivia().Start)
		module := AdminModule{
			Name: name, FilePath: filePath, Line: int(line) + 1,
			DisplayName: stringProperty(config, "name"),
			Type:        stringProperty(config, "type"),
			Title:       stringProperty(config, "title"),
			Description: stringProperty(config, "description"),
		}
		if routesProperty := jsquery.Property(config, "routes"); routesProperty != nil {
			module.Routes = appendAdminModuleRoutes(
				module.Routes,
				root,
				resolveAdminRouteObject(
					root,
					jsquery.PropertyValue(routesProperty),
				),
				strings.ReplaceAll(name, "-", "."),
				lineIndex,
			)
		}
		module.Routes = appendAdminRouteMiddlewareRoutes(
			module.Routes, root, config, lineIndex,
		)
		modules = append(modules, module)
	}
	return mixins, modules
}

func resolveAdminModuleConfig(
	root,
	call *jssyntax.Node,
) *jssyntax.Node {
	expression := jsquery.ArgumentExpression(call, 1)
	if expression == nil {
		return nil
	}
	if expression.Kind() == jssyntax.JsObject {
		return expression
	}
	identifier := strings.TrimSpace(jsquery.IdentifierText(expression))
	if identifier == "" {
		return nil
	}
	declaration, _, found := visibleJavaScriptConstDeclaration(
		call, identifier, root,
	)
	if !found || declaration == nil {
		return nil
	}
	for child := range declaration.ChildNodes() {
		if child.Kind() == jssyntax.JsObject {
			return child
		}
	}
	return nil
}

func appendAdminRouteMiddlewareRoutes(
	destination []AdminModuleRoute,
	root,
	config *jssyntax.Node,
	lineIndex *cst.LineIndex,
) []AdminModuleRoute {
	property := jsquery.Property(config, "routeMiddleware")
	middleware := resolveAdminRouteMiddleware(root, property)
	if middleware == nil {
		return destination
	}
	positions := make(map[string]bool, len(destination))
	for _, route := range destination {
		positions[route.Name] = true
	}
	for _, call := range jsquery.Calls(middleware) {
		callName := jsquery.CallName(call)
		if callName != "children.push" &&
			!strings.HasSuffix(callName, ".children.push") {
			continue
		}
		routeConfig := jsquery.ObjectArgument(call, 0)
		if routeConfig == nil {
			continue
		}
		name := adminStaticStringProperty(root, routeConfig, "name")
		if name == "" || positions[name] {
			continue
		}
		positions[name] = true
		line, _ := lineIndex.Position(routeConfig.RangeTrimmedTrivia().Start)
		destination = append(destination, AdminModuleRoute{
			Name: name, LocalName: adminModuleRouteLocalName(name),
			Path:      stringProperty(routeConfig, "path"),
			Component: adminModuleRouteComponent(routeConfig),
			Line:      int(line) + 1,
		})
	}
	return destination
}

func resolveAdminRouteMiddleware(
	root,
	property *jssyntax.Node,
) *jssyntax.Node {
	if property == nil {
		return nil
	}
	if property.Kind() == jssyntax.JsMethod {
		return property
	}
	value := jsquery.PropertyValue(property)
	if value == nil {
		return nil
	}
	switch value.Kind() {
	case jssyntax.JsFunction, jssyntax.JsArrowFunction, jssyntax.JsMethod:
		return value
	}
	identifier := strings.TrimSpace(jsquery.IdentifierText(value))
	if identifier == "" {
		return nil
	}
	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		if adminJavaScriptFunctionName(function) == identifier {
			return function
		}
	}
	declaration, _, found := visibleJavaScriptConstDeclaration(
		property, identifier, root,
	)
	if !found || declaration == nil {
		return nil
	}
	for child := range declaration.ChildNodes() {
		if child.Kind() == jssyntax.JsFunction ||
			child.Kind() == jssyntax.JsArrowFunction {
			return child
		}
	}
	return nil
}

func adminStaticStringProperty(
	root,
	object *jssyntax.Node,
	name string,
) string {
	property := jsquery.Property(object, name)
	if property == nil {
		return ""
	}
	value := jsquery.PropertyValue(property)
	if direct := jsquery.StringValue(value); direct != "" {
		return direct
	}
	identifier := ""
	if value != nil {
		identifier = strings.TrimSpace(jsquery.IdentifierText(value))
	} else if property.Kind() == jssyntax.JsProperty {
		identifier = jsquery.PropertyName(property)
	}
	if identifier == "" {
		return ""
	}
	expression, found := visibleJavaScriptConstInitializer(
		property, identifier, root,
	)
	if !found {
		return ""
	}
	parsed := javascriptparser.Parse(expression)
	if parsed.Tree == nil || parsed.Tree.Root == nil {
		return ""
	}
	for _, stringNode := range jsquery.Nodes(
		parsed.Tree.Root, jssyntax.JsString,
	) {
		return jsquery.StringValue(stringNode)
	}
	return ""
}

func adminModuleRouteLocalName(name string) string {
	if position := strings.LastIndexByte(name, '.'); position >= 0 {
		return name[position+1:]
	}
	return name
}

func appendAdminModuleRoutes(
	destination []AdminModuleRoute,
	root *jssyntax.Node,
	routesObject *jssyntax.Node,
	prefix string,
	lineIndex *cst.LineIndex,
) []AdminModuleRoute {
	for _, routeProperty := range jsquery.Properties(routesObject) {
		localName := jsquery.PropertyName(routeProperty)
		routeConfig := jsquery.PropertyValue(routeProperty)
		if localName == "" || routeConfig == nil {
			continue
		}
		name := prefix + "." + localName
		routeLine, _ := lineIndex.Position(
			routeProperty.RangeTrimmedTrivia().Start,
		)
		destination = append(destination, AdminModuleRoute{
			Name:      name,
			LocalName: localName,
			Path:      stringProperty(routeConfig, "path"),
			Component: adminModuleRouteComponent(routeConfig),
			Line:      int(routeLine) + 1,
		})
		children := resolveAdminRouteObject(
			root,
			jsquery.PropertyValue(jsquery.Property(routeConfig, "children")),
		)
		destination = appendAdminModuleRoutes(
			destination,
			root,
			children,
			name,
			lineIndex,
		)
	}
	return destination
}

// resolveAdminRouteObject follows the small static factory pattern used by
// core Administration modules, for example `children: detailChildren()` with
// a local `function detailChildren() { return { ... }; }` declaration.
func resolveAdminRouteObject(
	root *jssyntax.Node,
	expression *jssyntax.Node,
) *jssyntax.Node {
	if expression == nil || expression.Kind() == jssyntax.JsObject {
		return expression
	}
	if expression.Kind() != jssyntax.JsCallExpression {
		return nil
	}
	name := jsquery.CallName(expression)
	if name == "" || strings.Contains(name, ".") ||
		len(jsquery.Arguments(expression)) != 0 {
		return nil
	}
	for _, function := range jsquery.Nodes(root, jssyntax.JsFunction) {
		if adminJavaScriptFunctionName(function) != name {
			continue
		}
		for _, object := range jsquery.Nodes(function, jssyntax.JsObject) {
			return object
		}
	}
	return nil
}

func adminJavaScriptFunctionName(function *jssyntax.Node) string {
	if function == nil || function.Kind() != jssyntax.JsFunction {
		return ""
	}
	for child := range function.ChildNodes() {
		if child.Kind() == jssyntax.JsIdentifier {
			return strings.TrimSpace(child.Text())
		}
	}
	return ""
}

func adminModuleRouteComponent(routeConfig *jssyntax.Node) string {
	if component := stringProperty(routeConfig, "component"); component != "" {
		return component
	}
	components := jsquery.PropertyValue(jsquery.Property(routeConfig, "components"))
	return stringProperty(components, "default")
}

func stringProperty(object *jssyntax.Node, name string) string {
	property := jsquery.Property(object, name)
	if property == nil {
		return ""
	}
	return jsquery.StringValue(jsquery.PropertyValue(property))
}

func parseInlineDefinition(
	root,
	object *jssyntax.Node,
	filePath string,
	lineIndex *cst.LineIndex,
) *ComponentDefinition {
	definition := ParseComponentObject(object, filePath, lineIndex)
	if definition == nil {
		return nil
	}
	if root == nil {
		root = object
		for root.Parent() != nil {
			root = root.Parent()
		}
	}
	enrichLocalComponentImports(root, definition)
	if templateImport := jsquery.ImportPath(root, "template"); templateImport != "" {
		definition.TemplatePath = ResolveTemplatePath(filePath, templateImport)
		if result, err := ParseTemplateFromFile(definition.TemplatePath); err == nil {
			definition.Slots = result.Slots
			definition.Blocks = result.Blocks
		}
	}
	return definition
}

// resolveImportPath resolves an import path relative to the registration file
func resolveImportPath(registrationFile, importPath string) string {
	if importPath == "" {
		return ""
	}

	var basePath string

	// If it starts with 'src/', it's an absolute path from the administration root
	if strings.HasPrefix(importPath, "src/") {
		// Find the administration root
		adminIdx := strings.Index(registrationFile, "Resources/app/administration/")
		if adminIdx != -1 {
			adminRoot := registrationFile[:adminIdx+len("Resources/app/administration/")]
			basePath = filepath.Join(adminRoot, importPath)
		} else {
			return importPath
		}
	} else if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		// Handle relative paths
		dir := filepath.Dir(registrationFile)
		basePath = filepath.Join(dir, importPath)
	} else {
		return importPath
	}

	// Try to resolve the actual file
	return resolveJSFile(basePath)
}

// resolveJSFile resolves Administration JavaScript, TypeScript, and Vue SFC
// imports. The historical name is retained because callers also use it for
// store and service module paths.
func resolveJSFile(basePath string) string {
	// If already has extension, return as-is
	if strings.HasSuffix(basePath, ".js") || strings.HasSuffix(basePath, ".ts") ||
		strings.HasSuffix(basePath, ".vue") {
		return basePath
	}

	// Try direct file with extensions
	candidates := []string{
		basePath + ".js",
		basePath + ".ts",
		basePath + ".vue",
		filepath.Join(basePath, "index.js"),
		filepath.Join(basePath, "index.ts"),
		filepath.Join(basePath, "index.vue"),
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	// Fallback: return with /index.js as most common pattern
	return filepath.Join(basePath, "index.js")
}
