package entityschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
)

type PluginContext struct {
	Root              string   `json:"-"`
	RootURI           string   `json:"rootUri"`
	ComposerName      string   `json:"composerName"`
	PluginClass       string   `json:"pluginClass"`
	SourceRoot        string   `json:"-"`
	SourceRootURI     string   `json:"sourceRootUri"`
	BaseNamespace     string   `json:"baseNamespace"`
	Namespace         string   `json:"namespace"`
	ShopwareVersion   string   `json:"shopwareVersion,omitempty"`
	ServiceURIs       []string `json:"serviceUris,omitempty"`
	SnapshotDirectory string   `json:"-"`
}

type composerFile struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Require  map[string]string `json:"require"`
	Autoload struct {
		PSR4 map[string]json.RawMessage `json:"psr-4"`
	} `json:"autoload"`
	Extra struct {
		PluginClass string `json:"shopware-plugin-class"`
	} `json:"extra"`
}

// FindPluginContext locates the nearest Composer package without escaping the
// workspace. Entity snapshots are deliberately scoped to this package.
func FindPluginContext(workspaceRoot, selectedDirectory string) (PluginContext, error) {
	workspaceRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return PluginContext{}, err
	}
	selectedDirectory, err = filepath.Abs(selectedDirectory)
	if err != nil {
		return PluginContext{}, err
	}
	root := selectedDirectory
	for withinDirectory(workspaceRoot, root) {
		composerPath := filepath.Join(root, "composer.json")
		if content, readErr := os.ReadFile(composerPath); readErr == nil {
			context, parseErr := parsePluginContext(root, selectedDirectory, content)
			if parseErr != nil {
				return PluginContext{}, parseErr
			}
			return context, nil
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return PluginContext{}, readErr
		}
		parent := filepath.Dir(root)
		if parent == root {
			break
		}
		root = parent
	}
	return PluginContext{}, fmt.Errorf("no plugin composer.json contains %s", selectedDirectory)
}

func parsePluginContext(root, selected string, content []byte) (PluginContext, error) {
	var composer composerFile
	if err := json.Unmarshal(content, &composer); err != nil {
		return PluginContext{}, fmt.Errorf("parse plugin composer.json: %w", err)
	}
	if composer.Name == "" {
		return PluginContext{}, errors.New("plugin composer.json has no package name")
	}
	if composer.Extra.PluginClass == "" && composer.Type != "shopware-platform-plugin" {
		return PluginContext{}, errors.New("selected Composer package is not a Shopware plugin")
	}
	type mapping struct{ namespace, root string }
	var mappings []mapping
	for namespace, raw := range composer.Autoload.PSR4 {
		var paths []string
		var single string
		if json.Unmarshal(raw, &single) == nil {
			paths = []string{single}
		} else if err := json.Unmarshal(raw, &paths); err != nil {
			return PluginContext{}, fmt.Errorf("parse PSR-4 mapping %s: %w", namespace, err)
		}
		for _, relative := range paths {
			mappings = append(mappings, mapping{
				namespace: strings.Trim(namespace, `\`),
				root:      filepath.Clean(filepath.Join(root, filepath.FromSlash(relative))),
			})
		}
	}
	sort.SliceStable(mappings, func(i, j int) bool { return len(mappings[i].root) > len(mappings[j].root) })
	var selectedMapping *mapping
	for index := range mappings {
		if withinDirectory(mappings[index].root, selected) {
			selectedMapping = &mappings[index]
			break
		}
	}
	if selectedMapping == nil {
		for index := range mappings {
			if filepath.Base(mappings[index].root) == "src" {
				selectedMapping = &mappings[index]
				break
			}
		}
	}
	if selectedMapping == nil {
		return PluginContext{}, errors.New("plugin has no usable PSR-4 source mapping")
	}
	namespace := selectedMapping.namespace
	if relative, relErr := filepath.Rel(selectedMapping.root, selected); relErr == nil && relative != "." && !strings.HasPrefix(relative, "..") {
		namespace += `\` + strings.ReplaceAll(filepath.ToSlash(relative), "/", `\`)
	}
	context := PluginContext{
		Root: root, ComposerName: composer.Name, PluginClass: strings.Trim(composer.Extra.PluginClass, `\`),
		SourceRoot: selectedMapping.root, BaseNamespace: selectedMapping.namespace, Namespace: strings.Trim(namespace, `\`),
		ShopwareVersion:   composer.Require["shopware/core"],
		SnapshotDirectory: filepath.Join(root, filepath.FromSlash(SnapshotRelativeDirectory)),
	}
	for _, name := range []string{"services.yaml", "services.yml", "services.php", "services.xml"} {
		path := filepath.Join(selectedMapping.root, "Resources", "config", name)
		if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
			context.ServiceURIs = append(context.ServiceURIs, path)
		}
	}
	return context, nil
}

func withinDirectory(root, candidate string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type ScannedDefinition struct {
	Path string
	Spec EntitySpec
}

// ScanPluginSchema imports the literal portion of every entity definition and
// EntityExtension and BulkEntityExtension in the plugin. Unsupported field
// expressions remain locked.
func ScanPluginSchema(pluginRoot string) (Schema, []ScannedDefinition, error) {
	return ScanPluginSchemaWithLookup(pluginRoot, nil)
}

// ScanPluginSchemaWithLookup augments plugin-local relation targets with the
// workspace DAL index. The importer still falls back to Shopware's class-name
// conventions when an external target is unavailable.
func ScanPluginSchemaWithLookup(pluginRoot string, external RelationLookup) (Schema, []ScannedDefinition, error) {
	var declarations []classBasedDeclaration
	err := filepath.WalkDir(filepath.Join(pluginRoot, "src"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "var" ||
				entry.Name() == "Test" || entry.Name() == "Tests" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".php" {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		parsed := phpparser.ParseBytes(content)
		if parsed.Tree == nil || parsed.Tree.Root == nil {
			return nil
		}
		root := parsed.Tree.Root
		namespace := strings.Trim(phpquery.Namespace(root), `\`)
		resolve := importClassResolver(root)
		for _, class := range phpquery.Classes(root) {
			name := phpquery.ClassName(class)
			if name == "" {
				continue
			}
			declaration := classBasedDeclaration{
				path: path, source: string(content), class: qualify(namespace, name), abstract: phpquery.IsAbstract(class),
			}
			for _, parent := range phpquery.ClassExtends(class) {
				declaration.parents = append(declaration.parents, resolve(parent))
			}
			declarations = append(declarations, declaration)
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return EmptySchema(), nil, nil
	}
	if err != nil {
		return Schema{}, nil, err
	}
	return scanClassBasedDeclarations(pluginRoot, declarations, external)
}

// IndexedClassDeclaration is the class metadata needed to discover class-based
// DAL definitions without scanning the filesystem. Paths and inheritance come
// from the production PHP semantic catalog.
type IndexedClassDeclaration struct {
	Path     string
	Class    string
	Parents  []string
	Abstract bool
	Source   string
}

// ScanIndexedPluginSchema imports only class-based DAL candidates selected by
// the PHP semantic index. Stale deleted index entries are ignored while other
// read failures remain visible to the caller.
func ScanIndexedPluginSchema(
	pluginRoot string,
	indexed []IndexedClassDeclaration,
	external RelationLookup,
) (Schema, []ScannedDefinition, error) {
	return scanIndexedPluginSchema(pluginRoot, indexed, external, true)
}

// ScanIndexedPluginSchemaSources is the production request path. Every
// semantically selected DAL class must have source captured by SourceIndex;
// missing source is an incomplete index, never permission to read from disk.
func ScanIndexedPluginSchemaSources(
	pluginRoot string,
	indexed []IndexedClassDeclaration,
	external RelationLookup,
) (Schema, []ScannedDefinition, error) {
	return scanIndexedPluginSchema(pluginRoot, indexed, external, false)
}

func scanIndexedPluginSchema(
	pluginRoot string,
	indexed []IndexedClassDeclaration,
	external RelationLookup,
	allowDisk bool,
) (Schema, []ScannedDefinition, error) {
	return scanIndexedPluginSchemaWithEnricher(pluginRoot, indexed, external, allowDisk, nil)
}

func scanIndexedPluginSchemaWithEnricher(
	pluginRoot string,
	indexed []IndexedClassDeclaration,
	external RelationLookup,
	allowDisk bool,
	enrich func(*EntitySpec),
) (Schema, []ScannedDefinition, error) {
	declarations := make([]classBasedDeclaration, 0, len(indexed))
	for _, candidate := range indexed {
		if candidate.Path == "" || candidate.Class == "" ||
			!withinDirectory(pluginRoot, candidate.Path) {
			continue
		}
		declarations = append(declarations, classBasedDeclaration{
			path:     filepath.Clean(candidate.Path),
			source:   candidate.Source,
			class:    strings.Trim(candidate.Class, `\`),
			parents:  append([]string(nil), candidate.Parents...),
			abstract: candidate.Abstract,
		})
	}
	return scanClassBasedDeclarationsWithEnricher(pluginRoot, declarations, external, allowDisk, enrich)
}

func scanClassBasedDeclarations(
	pluginRoot string,
	declarations []classBasedDeclaration,
	external RelationLookup,
	allowDisk ...bool,
) (Schema, []ScannedDefinition, error) {
	readMissingSources := len(allowDisk) == 0 || allowDisk[0]
	return scanClassBasedDeclarationsWithEnricher(pluginRoot, declarations, external, readMissingSources, nil)
}

func scanClassBasedDeclarationsWithEnricher(
	pluginRoot string,
	declarations []classBasedDeclaration,
	external RelationLookup,
	readMissingSources bool,
	enrich func(*EntitySpec),
) (Schema, []ScannedDefinition, error) {
	kinds := resolveClassBasedKinds(declarations)
	if err := hydrateClassBasedSources(declarations, kinds, readMissingSources); err != nil {
		return Schema{}, nil, err
	}
	targets := classBasedRelationTargets(declarations, kinds, enrich)
	lookup := combinedRelationLookup(targets, external)
	definitions, definitionIndexes := importScannedClassBasedDefinitions(declarations, kinds, lookup, enrich)
	attachScannedTranslations(definitions, definitionIndexes, declarations, kinds, lookup, enrich)
	schema := EmptySchema()
	for _, definition := range definitions {
		if schemaErr := MergeSpecSchema(&schema, definition.Spec); schemaErr != nil {
			return Schema{}, nil, fmt.Errorf("import %s: %w", definition.Path, schemaErr)
		}
	}
	sort.Slice(definitions, func(i, j int) bool {
		if definitions[i].Spec.EntityName != definitions[j].Spec.EntityName {
			return definitions[i].Spec.EntityName < definitions[j].Spec.EntityName
		}
		return definitions[i].Spec.DefinitionClass < definitions[j].Spec.DefinitionClass
	})
	return schema.Normalize(), definitions, nil
}

func hydrateClassBasedSources(declarations []classBasedDeclaration, kinds map[string]string, readMissingSources bool) error {
	sources := make(map[string]string)
	for index := range declarations {
		declaration := &declarations[index]
		if declaration.abstract || kinds[strings.ToLower(declaration.class)] == "" || declaration.source != "" {
			continue
		}
		if !readMissingSources {
			return fmt.Errorf("indexed DAL class source is unavailable for %s", declaration.class)
		}
		source, found := sources[declaration.path]
		if !found {
			content, readErr := os.ReadFile(declaration.path)
			if errors.Is(readErr, os.ErrNotExist) {
				continue
			}
			if readErr != nil {
				return fmt.Errorf("read indexed DAL class %s: %w", declaration.path, readErr)
			}
			source = string(content)
			sources[declaration.path] = source
		}
		declaration.source = source
	}
	return nil
}

func classBasedRelationTargets(
	declarations []classBasedDeclaration,
	kinds map[string]string,
	enrich func(*EntitySpec),
) map[string]RelationTarget {
	targets := make(map[string]RelationTarget)
	for _, declaration := range declarations {
		kind := kinds[strings.ToLower(declaration.class)]
		if declaration.abstract || declaration.source == "" || kind != classBasedEntity && kind != classBasedMapping {
			continue
		}
		spec, importErr := importDefinitionClass(declaration.source, nil, declaration.class, DefinitionKind(kind))
		if importErr != nil {
			continue
		}
		if enrich != nil {
			enrich(&spec)
		}
		target := RelationTarget{
			DefinitionClass: spec.DefinitionClass, EntityClass: spec.EntityClass,
			CollectionClass: spec.CollectionClass, EntityName: spec.EntityName, FileURI: declaration.path,
			InheritanceAware: spec.InheritanceAware,
		}
		if spec.DefinitionBehavior != nil && spec.DefinitionBehavior.VersionAware != nil {
			target.VersionAware = *spec.DefinitionBehavior.VersionAware
		}
		for _, field := range schemaDefinitionFields(spec) {
			if field.Kind == FieldVersion {
				if spec.DefinitionBehavior == nil || spec.DefinitionBehavior.VersionAware == nil {
					target.VersionAware = true
				}
			}
			if field.Kind == FieldLocked || field.StorageName == "" || field.Kind == FieldOneToMany || field.Kind == FieldManyToMany || field.UsesExistingColumn {
				continue
			}
			target.Fields = append(target.Fields, RelationTargetField{PropertyName: field.PropertyName, StorageName: field.StorageName, Primary: field.Primary || field.Kind == FieldID || field.Kind == FieldVersion})
		}
		targets[spec.DefinitionClass] = target
		if spec.EntityName != "" {
			targets[spec.EntityName] = target
		}
	}
	return targets
}

func combinedRelationLookup(targets map[string]RelationTarget, external RelationLookup) RelationLookup {
	lookup := func(class string) (RelationTarget, bool) {
		target, ok := targets[strings.Trim(class, `\`)]
		if ok || external == nil {
			return target, ok
		}
		return external(class)
	}
	return lookup
}

func importScannedClassBasedDefinitions(
	declarations []classBasedDeclaration,
	kinds map[string]string,
	lookup RelationLookup,
	enrich func(*EntitySpec),
) ([]ScannedDefinition, map[string]int) {
	var definitions []ScannedDefinition
	definitionIndexes := make(map[string]int)
	for _, declaration := range declarations {
		if declaration.abstract || declaration.source == "" {
			continue
		}
		kind := kinds[strings.ToLower(declaration.class)]
		if kind == "" || kind == classBasedTranslation {
			continue
		}
		spec, importErr := importClassBasedSpec(declaration.source, declaration.class, kind, lookup)
		if importErr != nil {
			continue
		}
		if enrich != nil {
			enrich(&spec)
		}
		if spec.DefinitionKind == DefinitionEntity || spec.DefinitionKind == DefinitionMapping {
			definitionIndexes[spec.DefinitionClass] = len(definitions)
		}
		definitions = append(definitions, ScannedDefinition{Path: declaration.path, Spec: spec})
	}
	return definitions, definitionIndexes
}

func attachScannedTranslations(
	definitions []ScannedDefinition,
	definitionIndexes map[string]int,
	declarations []classBasedDeclaration,
	kinds map[string]string,
	lookup RelationLookup,
	enrich func(*EntitySpec),
) {
	for _, declaration := range declarations {
		if declaration.abstract || declaration.source == "" || kinds[strings.ToLower(declaration.class)] != classBasedTranslation {
			continue
		}
		translation, importErr := importTranslationClass(declaration.source, lookup, declaration.class)
		if importErr != nil {
			continue
		}
		index, found := definitionIndexes[translation.Spec.ParentDefinitionClass]
		if !found {
			continue
		}
		parentTranslation := definitions[index].Spec.Translation
		if parentTranslation == nil || !strings.EqualFold(strings.Trim(parentTranslation.DefinitionClass, `\`), strings.Trim(translation.Spec.DefinitionClass, `\`)) {
			continue
		}
		translation.Spec.DefinitionURI = declaration.path
		translationDirectory := filepath.Dir(declaration.path)
		translation.Spec.EntityURI = filepath.Join(translationDirectory, ShortClass(translation.Spec.EntityClass)+".php")
		translation.Spec.CollectionURI = filepath.Join(translationDirectory, ShortClass(translation.Spec.CollectionClass)+".php")
		definitions[index].Spec = AttachTranslation(definitions[index].Spec, translation)
		if enrich != nil {
			enrich(&definitions[index].Spec)
		}
	}
}

const (
	classBasedEntity        = "entity"
	classBasedMapping       = "mapping"
	classBasedTranslation   = "translation"
	classBasedExtension     = "extension"
	classBasedBulkExtension = "bulk-extension"
)

type classBasedDeclaration struct {
	path, source, class string
	parents             []string
	abstract            bool
}

func resolveClassBasedKinds(declarations []classBasedDeclaration) map[string]string {
	byClass := make(map[string]classBasedDeclaration, len(declarations))
	for _, declaration := range declarations {
		byClass[strings.ToLower(strings.Trim(declaration.class, `\`))] = declaration
	}
	result := make(map[string]string, len(declarations))
	resolving := make(map[string]bool)
	var resolve func(string) string
	resolve = func(class string) string {
		key := strings.ToLower(strings.Trim(class, `\`))
		if kind, found := result[key]; found {
			return kind
		}
		if resolving[key] {
			return ""
		}
		resolving[key] = true
		defer delete(resolving, key)
		switch key {
		case strings.ToLower(`Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition`):
			return classBasedTranslation
		case strings.ToLower(`Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition`):
			return classBasedMapping
		case strings.ToLower(`Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`):
			return classBasedEntity
		case strings.ToLower(`Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension`):
			return classBasedBulkExtension
		case strings.ToLower(`Shopware\Core\Framework\DataAbstractionLayer\EntityExtension`):
			return classBasedExtension
		}
		declaration, found := byClass[key]
		if !found || isRuntimeClassBasedTemplate(ShortClass(declaration.class)) {
			result[key] = ""
			return ""
		}
		for _, parent := range declaration.parents {
			if kind := resolve(parent); kind != "" {
				result[key] = kind
				return kind
			}
		}
		result[key] = ""
		return ""
	}
	for _, declaration := range declarations {
		result[strings.ToLower(strings.Trim(declaration.class, `\`))] = resolve(declaration.class)
	}
	return result
}

func isRuntimeClassBasedTemplate(name string) bool {
	switch name {
	case "AttributeEntityDefinition", "AttributeMappingDefinition", "AttributeTranslationDefinition",
		"DynamicEntityDefinition", "DynamicMappingEntityDefinition", "DynamicTranslationEntityDefinition",
		"FilteredBulkEntityExtension":
		return true
	default:
		return false
	}
}

func importClassBasedSpec(source, class, kind string, lookup RelationLookup) (EntitySpec, error) {
	switch kind {
	case classBasedEntity, classBasedMapping:
		return importDefinitionClass(source, lookup, class, DefinitionKind(kind))
	case classBasedExtension:
		return importExtensionClass(source, lookup, class)
	case classBasedBulkExtension:
		return importBulkExtensionClass(source, lookup, class)
	default:
		return EntitySpec{}, fmt.Errorf("unsupported class-based DAL kind %q", kind)
	}
}
