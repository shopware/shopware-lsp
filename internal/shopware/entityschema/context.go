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

// ScanPluginSchema imports the literal portion of every entity definition in
// the plugin. Unsupported custom expressions remain visible as locked fields.
func ScanPluginSchema(pluginRoot string) (Schema, []ScannedDefinition, error) {
	return ScanPluginSchemaWithLookup(pluginRoot, nil)
}

// ScanPluginSchemaWithLookup augments plugin-local relation targets with the
// workspace DAL index. The importer still falls back to Shopware's class-name
// conventions when an external target is unavailable.
func ScanPluginSchemaWithLookup(pluginRoot string, external RelationLookup) (Schema, []ScannedDefinition, error) {
	var sources []struct{ path, source string }
	err := filepath.WalkDir(filepath.Join(pluginRoot, "src"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == "node_modules" || entry.Name() == "var" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".php" || !strings.HasSuffix(entry.Name(), "Definition.php") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(content), "EntityDefinition") && strings.Contains(string(content), "defineFields") {
			sources = append(sources, struct{ path, source string }{path, string(content)})
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return EmptySchema(), nil, nil
	}
	if err != nil {
		return Schema{}, nil, err
	}
	targets := make(map[string]RelationTarget)
	for _, source := range sources {
		spec, importErr := ImportDefinition(source.source, nil)
		if importErr != nil {
			continue
		}
		target := RelationTarget{
			DefinitionClass: spec.DefinitionClass, EntityClass: spec.EntityClass,
			CollectionClass: spec.CollectionClass, EntityName: spec.EntityName, FileURI: source.path,
		}
		for _, field := range spec.Fields {
			if field.Kind == FieldVersion {
				target.VersionAware = true
			}
			if field.Kind == FieldLocked || field.StorageName == "" || field.Kind == FieldOneToMany || field.Kind == FieldManyToMany || field.UsesExistingColumn {
				continue
			}
			target.Fields = append(target.Fields, RelationTargetField{PropertyName: field.PropertyName, StorageName: field.StorageName, Primary: field.Primary || field.Kind == FieldID || field.Kind == FieldVersion})
		}
		targets[spec.DefinitionClass] = target
	}
	lookup := func(class string) (RelationTarget, bool) {
		target, ok := targets[strings.Trim(class, `\`)]
		if ok || external == nil {
			return target, ok
		}
		return external(class)
	}
	schema := EmptySchema()
	var definitions []ScannedDefinition
	for _, source := range sources {
		spec, importErr := ImportDefinition(source.source, lookup)
		if importErr != nil {
			continue
		}
		entity, schemaErr := SchemaFromSpec(spec)
		if schemaErr != nil {
			return Schema{}, nil, fmt.Errorf("import %s: %w", source.path, schemaErr)
		}
		schema.Entities[entity.Name] = entity
		definitions = append(definitions, ScannedDefinition{Path: source.path, Spec: spec})
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Spec.EntityName < definitions[j].Spec.EntityName })
	return schema.Normalize(), definitions, nil
}
