package scaffold

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

var shopwareScaffoldNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
var shopwareJavaScriptIdentifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

type ShopwareRequest struct {
	Kind         string         `json:"kind"`
	DirectoryURI string         `json:"directoryUri"`
	Name         string         `json:"name"`
	Options      map[string]any `json:"options,omitempty"`
}

type ShopwareResponse struct {
	Edit            *protocol.WorkspaceEdit `json:"edit"`
	PrimaryFileURI  string                  `json:"primaryFileUri"`
	ShopwareVersion string                  `json:"shopwareVersion,omitempty"`
}

type generatedFile struct {
	path    string
	content string
}

func (p *Provider) createShopware(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	if p == nil {
		return nil, fmt.Errorf("shopware scaffold generator is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var request ShopwareRequest
	if raw == nil {
		return nil, fmt.Errorf("missing Shopware scaffold request")
	}
	if err := json.Unmarshal(*raw, &request); err != nil {
		return nil, fmt.Errorf("invalid Shopware scaffold request: %w", err)
	}
	directory, err := uriutil.Path(request.DirectoryURI)
	if err != nil {
		return nil, fmt.Errorf("resolve scaffold directory: %w", err)
	}
	directory, err = p.validatedDirectory(directory)
	if err != nil {
		return nil, err
	}
	request.Kind = strings.ToLower(strings.TrimSpace(request.Kind))
	request.Name = strings.TrimSpace(request.Name)
	if !shopwareScaffoldNamePattern.MatchString(request.Name) {
		return nil, fmt.Errorf("invalid Shopware scaffold name %q", request.Name)
	}
	files, primary, err := p.shopwareFiles(directory, request)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("shopware scaffold %q produced no files", request.Kind)
	}
	seen := make(map[string]struct{}, len(files))
	plan := rewrite.WorkspacePlan{}
	for _, file := range files {
		file.path = filepath.Clean(file.path)
		if _, duplicate := seen[file.path]; duplicate {
			return nil, fmt.Errorf("duplicate scaffold target %s", file.path)
		}
		seen[file.path] = struct{}{}
		if err := ensureScaffoldTargetAvailable(file.path); err != nil {
			return nil, err
		}
		if err := validateGeneratedFile(file.path, file.content); err != nil {
			return nil, err
		}
		plan.Creates = append(plan.Creates, rewrite.CreateFilePlan{
			URI: uriutil.FileURI(file.path), Content: file.content,
		})
	}
	if request.Kind == "admin-component" || request.Kind == "admin-module" {
		mainPath := filepath.Join(directory, "main.js")
		if _, statErr := os.Stat(mainPath); os.IsNotExist(statErr) {
			content, contentErr := mainJSImportContent(directory, primary)
			if contentErr != nil {
				return nil, contentErr
			}
			if _, duplicate := seen[mainPath]; duplicate {
				return nil, fmt.Errorf("duplicate scaffold target %s", mainPath)
			}
			if err := ensureScaffoldTargetAvailable(mainPath); err != nil {
				return nil, err
			}
			plan.Creates = append(plan.Creates, rewrite.CreateFilePlan{
				URI: uriutil.FileURI(mainPath), Content: content,
			})
		} else if statErr != nil {
			return nil, statErr
		} else if document, found, documentErr := mainJSImportPlan(directory, request, primary); documentErr != nil {
			return nil, documentErr
		} else if found {
			plan.Documents = append(plan.Documents, document)
		}
	}
	edit, err := plan.WorkspaceEdit()
	if err != nil {
		return nil, err
	}
	return ShopwareResponse{
		Edit:            edit,
		PrimaryFileURI:  uriutil.FileURI(primary),
		ShopwareVersion: p.shopwareVersion(),
	}, nil
}

func (p *Provider) shopwareFiles(directory string, request ShopwareRequest) ([]generatedFile, string, error) {
	name := request.Name
	className := pascalName(name)
	namespaceFallback := className
	if request.Kind != "plugin" && p.phpIndex != nil {
		if inferred, err := namespaceForDirectory(p.phpIndex.Project(), directory); err == nil && inferred != "" {
			namespaceFallback = inferred
		}
	}
	namespace := optionString(request.Options, "namespace", namespaceFallback)
	version := p.shopwareVersion()
	if version == "" {
		version = "~6.7"
	}
	switch request.Kind {
	case "plugin":
		root := filepath.Join(directory, className)
		description := optionString(request.Options, "description", "Shopware extension "+className)
		author := optionString(request.Options, "author", "Acme")
		license := optionString(request.Options, "license", "MIT")
		packageName := optionString(request.Options, "package", strings.ToLower(className)+"/"+strings.ToLower(className))
		composer, _ := json.MarshalIndent(map[string]any{
			"name": packageName, "description": description,
			"type": "shopware-platform-plugin", "license": license,
			"authors":  []map[string]string{{"name": author}},
			"require":  map[string]string{"shopware/core": version},
			"autoload": map[string]any{"psr-4": map[string]string{namespace + `\`: "src/"}},
			"extra": map[string]any{
				"shopware-plugin-class": namespace + `\` + className,
				"label":                 map[string]string{"de-DE": className, "en-GB": className},
				"description":           map[string]string{"de-DE": description, "en-GB": description},
				"manufacturerLink":      map[string]string{"de-DE": "https://example.com", "en-GB": "https://example.com"},
				"supportLink":           map[string]string{"de-DE": "https://example.com", "en-GB": "https://example.com"},
			},
		}, "", "  ")
		primary := filepath.Join(root, "src", className+".php")
		return []generatedFile{
			{path: filepath.Join(root, "composer.json"), content: string(composer) + "\n"},
			{path: primary, content: pluginPHP(namespace, className)},
		}, primary, nil
	case "system-config":
		primary := filepath.Join(directory, "Resources", "config", "config.xml")
		return []generatedFile{{path: primary, content: systemConfigXML()}}, primary, nil
	case "scheduled-task":
		interval := optionInt(request.Options, "interval", 300)
		taskName := optionString(request.Options, "taskName", strings.ToLower(strings.ReplaceAll(name, "_", ".")))
		root := filepath.Join(directory, "ScheduledTask")
		primary := filepath.Join(root, className+"Task.php")
		return []generatedFile{
			{path: primary, content: scheduledTaskPHP(namespace, className, taskName, interval)},
			{path: filepath.Join(root, className+"TaskHandler.php"), content: scheduledTaskHandlerPHP(namespace, className)},
		}, primary, nil
	case "migration":
		timestamp := optionString(request.Options, "timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		migrationClass := "Migration" + timestamp + className
		primary := filepath.Join(directory, "Migration", migrationClass+".php")
		return []generatedFile{{path: primary, content: migrationPHP(namespace, migrationClass, timestamp)}}, primary, nil
	case "event-listener":
		eventClass := strings.Trim(optionString(request.Options, "event", ""), `\ `)
		if eventClass == "" || !strings.Contains(eventClass, `\`) {
			return nil, "", fmt.Errorf("event-listener scaffold requires an event class")
		}
		eventShort := eventClass
		if separator := strings.LastIndex(eventShort, `\`); separator >= 0 {
			eventShort = eventShort[separator+1:]
		}
		listenerClass := pascalName(name)
		primary := filepath.Join(directory, listenerClass+".php")
		return []generatedFile{{
			path:    primary,
			content: eventListenerPHP(namespace, listenerClass, eventClass, eventShort),
		}}, primary, nil
	case "app":
		root := filepath.Join(directory, name)
		primary := filepath.Join(root, "manifest.xml")
		return []generatedFile{{path: primary, content: appManifestXML(
			name,
			optionString(request.Options, "label", name),
			optionString(request.Options, "author", "Acme"),
			optionString(request.Options, "license", "MIT"),
		)}}, primary, nil
	case "app-custom-entities":
		primary := filepath.Join(directory, "Resources", "entities.xml")
		return []generatedFile{{path: primary, content: appEntitiesXML(name)}}, primary, nil
	case "app-cms":
		primary := filepath.Join(directory, "Resources", "cms.xml")
		return []generatedFile{{path: primary, content: appCMSXML()}}, primary, nil
	case "app-script":
		hook := optionString(request.Options, "hook", name)
		primary := filepath.Join(directory, "Resources", "scripts", hook, name+".twig")
		return []generatedFile{{path: primary, content: appScriptTwig(hook)}}, primary, nil
	case "admin-component":
		mode := strings.ToLower(optionString(request.Options, "mode", "register"))
		if mode != "register" && mode != "extend" && mode != "override" {
			return nil, "", fmt.Errorf("unsupported Administration component mode %q", mode)
		}
		target := optionString(request.Options, "target", "")
		if mode != "register" && !shopwareScaffoldNamePattern.MatchString(target) {
			return nil, "", fmt.Errorf("invalid Administration target component %q", target)
		}
		rootKind := "component"
		if mode == "override" {
			rootKind = "override"
			name = target
		}
		root := filepath.Join(directory, rootKind, name)
		primary := filepath.Join(root, "index.js")
		generateAssets := mode == "register"
		generateTwig := optionBool(request.Options, "generateTwig", generateAssets)
		generateSCSS := optionBool(request.Options, "generateScss", generateAssets)
		files := []generatedFile{{
			path:    primary,
			content: adminComponentJS(name, target, mode, generateTwig, generateSCSS, request.Options),
		}}
		if generateTwig {
			files = append(files, generatedFile{path: filepath.Join(root, name+".html.twig"), content: adminComponentTwig(name)})
		}
		if generateSCSS {
			files = append(files, generatedFile{path: filepath.Join(root, name+".scss"), content: "." + name + " {\n}\n"})
		}
		return files, primary, nil
	case "admin-module":
		root := filepath.Join(directory, "module", name)
		primary := filepath.Join(root, "index.js")
		return []generatedFile{
			{path: primary, content: adminModuleJS(name, request.Options)},
			{path: filepath.Join(root, "snippet", "en-GB.json"), content: adminModuleSnippet(name)},
			{path: filepath.Join(root, "snippet", "de-DE.json"), content: adminModuleSnippet(name)},
		}, primary, nil
	case "cms-block":
		return cmsBlockFiles(directory, name, optionString(request.Options, "category", "text"))
	case "cms-element":
		return cmsElementFiles(directory, name)
	default:
		return nil, "", fmt.Errorf("unsupported Shopware scaffold kind %q", request.Kind)
	}
}

func validateGeneratedFile(path, source string) error {
	registry := language.DefaultRegistry()
	_, result, ok := registry.ParsePath(path, source)
	if !ok {
		return nil
	}
	if len(result.Errors) != 0 {
		return fmt.Errorf("generated %s scaffold is invalid: %s", filepath.Ext(path), result.Errors[0].Message())
	}
	return nil
}

func mainJSImportPlan(directory string, request ShopwareRequest, primary string) (rewrite.DocumentPlan, bool, error) {
	mainPath := filepath.Join(directory, "main.js")
	source, err := os.ReadFile(mainPath)
	if os.IsNotExist(err) {
		return rewrite.DocumentPlan{}, false, nil
	}
	if err != nil {
		return rewrite.DocumentPlan{}, false, err
	}
	relative, err := filepath.Rel(directory, filepath.Dir(primary))
	if err != nil {
		return rewrite.DocumentPlan{}, false, err
	}
	importPath := "./" + filepath.ToSlash(relative)
	if strings.Contains(string(source), "'"+importPath+"'") || strings.Contains(string(source), `"`+importPath+`"`) {
		return rewrite.DocumentPlan{}, false, nil
	}
	prefix := ""
	if len(source) > 0 && source[len(source)-1] != '\n' {
		prefix = "\n"
	}
	builder := rewrite.NewBuilder(string(source))
	if err := builder.Insert(uint32(len(source)), prefix+"import '"+importPath+"';\n"); err != nil {
		return rewrite.DocumentPlan{}, false, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.DocumentPlan{}, false, err
	}
	return rewrite.NewDocumentPlan(uriutil.FileURI(mainPath), nil, string(source), edits), true, nil
}

func mainJSImportContent(directory, primary string) (string, error) {
	relative, err := filepath.Rel(directory, filepath.Dir(primary))
	if err != nil {
		return "", err
	}
	return "import './" + filepath.ToSlash(relative) + "';\n", nil
}

func (p *Provider) shopwareVersion() string {
	data, err := os.ReadFile(filepath.Join(p.root, "composer.json"))
	if err != nil {
		return ""
	}
	var composer struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(data, &composer) != nil {
		return ""
	}
	return composer.Require["shopware/core"]
}

func optionString(options map[string]any, key, fallback string) string {
	if value, ok := options[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func optionBool(options map[string]any, key string, fallback bool) bool {
	if value, ok := options[key].(bool); ok {
		return value
	}
	return fallback
}

func optionInt(options map[string]any, key string, fallback int) int {
	switch value := options[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return fallback
	}
}

func pascalName(name string) string {
	var result []rune
	upper := true
	for _, current := range name {
		if current == '-' || current == '_' {
			upper = true
			continue
		}
		if upper {
			current = unicode.ToUpper(current)
			upper = false
		}
		result = append(result, current)
	}
	return string(result)
}

func normalizeTwigName(name string) string { return strings.ReplaceAll(name, "-", "_") }

func pluginPHP(namespace, class string) string {
	return fmt.Sprintf("<?php declare(strict_types=1);\n\nnamespace %s;\n\nuse Shopware\\Core\\Framework\\Plugin;\n\nclass %s extends Plugin\n{\n}\n", namespace, class)
}

func systemConfigXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<config xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
        xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/System/SystemConfig/Schema/config.xsd">
    <card>
        <title>Configuration</title>
        <input-field type="text">
            <name>example</name>
            <label>Example</label>
        </input-field>
    </card>
</config>
`
}

func scheduledTaskPHP(namespace, name, taskName string, interval int) string {
	return fmt.Sprintf("<?php declare(strict_types=1);\n\nnamespace %s\\ScheduledTask;\n\nuse Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTask;\n\nclass %sTask extends ScheduledTask\n{\n    public static function getTaskName(): string\n    {\n        return '%s';\n    }\n\n    public static function getDefaultInterval(): int\n    {\n        return %d;\n    }\n}\n", namespace, name, taskName, interval)
}

func scheduledTaskHandlerPHP(namespace, name string) string {
	return fmt.Sprintf("<?php declare(strict_types=1);\n\nnamespace %s\\ScheduledTask;\n\nuse Shopware\\Core\\Framework\\MessageQueue\\ScheduledTask\\ScheduledTaskHandler;\n\nclass %sTaskHandler extends ScheduledTaskHandler\n{\n    public static function getHandledMessages(): iterable\n    {\n        return [%sTask::class];\n    }\n\n    public function run(): void\n    {\n    }\n}\n", namespace, name, name)
}

func migrationPHP(namespace, class, timestamp string) string {
	return fmt.Sprintf("<?php declare(strict_types=1);\n\nnamespace %s\\Migration;\n\nuse Doctrine\\DBAL\\Connection;\nuse Shopware\\Core\\Framework\\Migration\\MigrationStep;\n\nclass %s extends MigrationStep\n{\n    public function getCreationTimestamp(): int\n    {\n        return %s;\n    }\n\n    public function update(Connection $connection): void\n    {\n    }\n\n    public function updateDestructive(Connection $connection): void\n    {\n    }\n}\n", namespace, class, timestamp)
}

func eventListenerPHP(namespace, class, eventClass, eventShort string) string {
	return fmt.Sprintf("<?php declare(strict_types=1);\n\nnamespace %s;\n\nuse %s;\nuse Symfony\\Component\\EventDispatcher\\Attribute\\AsEventListener;\n\n#[AsEventListener]\nclass %s\n{\n    public function __invoke(%s $event): void\n    {\n    }\n}\n", namespace, eventClass, class, eventShort)
}

func appManifestXML(name, label, author, license string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<manifest xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Manifest/Schema/manifest-3.0.xsd">
    <meta>
        <name>%s</name>
        <label>%s</label>
        <label lang="de-DE">%s</label>
        <description>%s</description>
        <description lang="de-DE">%s</description>
        <author>%s</author>
        <version>1.0.0</version>
        <license>%s</license>
    </meta>
</manifest>
`, name, label, label, label, label, author, license)
}

func appEntitiesXML(name string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<entities xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
          xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/System/CustomEntity/Xml/entity-1.0.xsd">
    <entity name="custom_entity_%s">
        <fields/>
    </entity>
</entities>
`, strings.ToLower(strings.ReplaceAll(name, "-", "_")))
}

func appCMSXML() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<cms xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
     xsi:noNamespaceSchemaLocation="https://raw.githubusercontent.com/shopware/shopware/trunk/src/Core/Framework/App/Cms/Schema/cms-1.0.xsd">
    <blocks/>
</cms>
`
}

func appScriptTwig(hook string) string {
	return fmt.Sprintf("{# Script hook: %s #}\n{# @var services \\Shopware\\Core\\Framework\\Script\\ServiceStubs #}\n\n{# Your script code #}\n", hook)
}

func adminComponentJS(
	name,
	target,
	mode string,
	twig,
	scss bool,
	options map[string]any,
) string {
	imports := ""
	fields := ""
	if twig {
		imports += "import template from './" + name + ".html.twig';\n"
		fields += "    template,\n"
	}
	if scss {
		imports += "import './" + name + ".scss';\n"
	}
	if method := optionString(options, "method", ""); method != "" {
		if shopwareJavaScriptIdentifierPattern.MatchString(method) {
			fields += adminOverrideMethodJS(
				method,
				optionString(options, "methodGroup", ""),
				optionString(options, "parameters", ""),
			)
		}
	}
	call := "Component.register('" + name + "', {"
	switch mode {
	case "extend":
		call = "Component.extend('" + name + "', '" + target + "', {"
	case "override":
		call = "Component.override('" + target + "', {"
	}
	return imports + "\nconst { Component } = Shopware;\n\n" + call + "\n" + fields + "});\n"
}

func adminOverrideMethodJS(method, group, parameters string) string {
	parameterNames := make([]string, 0)
	for _, parameter := range strings.Split(parameters, ",") {
		parameter = strings.TrimSpace(parameter)
		if parameter != "" && shopwareJavaScriptIdentifierPattern.MatchString(parameter) {
			parameterNames = append(parameterNames, parameter)
		}
	}
	parameters = strings.Join(parameterNames, ", ")
	forward := ""
	if parameters != "" {
		forward = ", " + parameters
	}
	methodBody := fmt.Sprintf("%s(%s) {\n            return this.$super('%s'%s);\n        },\n", method, parameters, method, forward)
	if group != "" && shopwareJavaScriptIdentifierPattern.MatchString(group) {
		return "    " + group + ": {\n        " + methodBody + "    },\n"
	}
	return "    " + strings.ReplaceAll(methodBody, "\n        ", "\n    ")
}

func adminComponentTwig(name string) string {
	return "{% block " + normalizeTwigName(name) + " %}\n<div class=\"" + name + "\">\n</div>\n{% endblock %}\n"
}

func adminModuleJS(name string, options map[string]any) string {
	typeName := optionString(options, "type", "plugin")
	color := optionString(options, "color", "#189EFF")
	icon := optionString(options, "icon", "regular-cube")
	prefix := strings.ReplaceAll(name, "-", ".")
	return fmt.Sprintf("const { Module } = Shopware;\n\nModule.register('%s', {\n    type: '%s',\n    title: '%s.general.title',\n    description: '%s.general.description',\n    color: '%s',\n    icon: '%s',\n    routes: {\n        index: { component: '%s-index', path: 'index' },\n    },\n});\n\n// Route name: %s.index\n", name, typeName, name, name, color, icon, name, prefix)
}

func adminModuleSnippet(name string) string {
	data, _ := json.MarshalIndent(map[string]any{name: map[string]any{"general": map[string]string{"title": name, "description": name}}}, "", "  ")
	return string(data) + "\n"
}

func cmsBlockFiles(directory, name, category string) ([]generatedFile, string, error) {
	root := filepath.Join(directory, "module", "sw-cms", "blocks", name)
	primary := filepath.Join(root, "index.js")
	normalized := normalizeTwigName(name)
	return []generatedFile{
		{path: primary, content: fmt.Sprintf("import './component';\nimport './preview';\n\nShopware.Service('cmsService').registerCmsBlock({ name: '%s', label: 'sw-cms.blocks.%s.%s.label', category: '%s', component: 'sw-cms-block-%s', previewComponent: 'sw-cms-preview-%s', slots: { content: 'text' } });\n", name, category, name, category, name, name)},
		{path: filepath.Join(root, "component", "index.js"), content: "import template from './sw-cms-block-" + name + ".html.twig';\nShopware.Component.register('sw-cms-block-" + name + "', { template });\n"},
		{path: filepath.Join(root, "component", "sw-cms-block-"+name+".html.twig"), content: "{% block sw_cms_block_" + normalized + " %}\n<div class=\"sw-cms-block-" + name + "\">" + name + "</div>\n{% endblock %}\n"},
		{path: filepath.Join(root, "preview", "index.js"), content: "import template from './sw-cms-preview-" + name + ".html.twig';\nShopware.Component.register('sw-cms-preview-" + name + "', { template });\n"},
		{path: filepath.Join(root, "preview", "sw-cms-preview-"+name+".html.twig"), content: "<div class=\"sw-cms-preview-" + name + "\">" + name + "</div>\n"},
	}, primary, nil
}

func cmsElementFiles(directory, name string) ([]generatedFile, string, error) {
	root := filepath.Join(directory, "module", "sw-cms", "elements", name)
	primary := filepath.Join(root, "index.js")
	normalized := normalizeTwigName(name)
	return []generatedFile{
		{path: primary, content: fmt.Sprintf("import './component';\nimport './config';\nimport './preview';\n\nShopware.Service('cmsService').registerCmsElement({ name: '%s', label: 'sw-cms.elements.%s.label', component: 'sw-cms-el-component-%s', configComponent: 'sw-cms-el-config-%s', previewComponent: 'sw-cms-el-preview-%s', defaultConfig: {}, defaultData: {} });\n", name, name, name, name, name)},
		{path: filepath.Join(root, "component", "index.js"), content: "import template from './sw-cms-el-component-" + name + ".html.twig';\nShopware.Component.register('sw-cms-el-component-" + name + "', { template });\n"},
		{path: filepath.Join(root, "component", "sw-cms-el-component-"+name+".html.twig"), content: "{% block sw_cms_el_component_" + normalized + " %}<div>" + name + "</div>{% endblock %}\n"},
		{path: filepath.Join(root, "config", "index.js"), content: "Shopware.Component.register('sw-cms-el-config-" + name + "', {});\n"},
		{path: filepath.Join(root, "preview", "index.js"), content: "Shopware.Component.register('sw-cms-el-preview-" + name + "', {});\n"},
	}, primary, nil
}
