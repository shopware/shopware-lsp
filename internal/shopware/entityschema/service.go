package entityschema

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	yamlparser "github.com/shopware/shopware-lsp/internal/parser/yaml"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
)

// PatchServiceConfiguration registers a definition while preserving the
// surrounding configuration file. When no file exists, callers pass an empty
// source and the desired target path.
func PatchServiceConfiguration(path, source, definitionClass string) (string, error) {
	return PatchTaggedServiceConfiguration(path, source, definitionClass, "shopware.entity.definition")
}

// PatchTaggedServiceConfiguration registers a DAL definition or extension
// with its exact Shopware service tag while preserving the surrounding file.
func PatchTaggedServiceConfiguration(path, source, className, tag string) (string, error) {
	className = strings.Trim(className, `\ `)
	if className == "" {
		return "", fmt.Errorf("entity service class is empty")
	}
	if tag != "shopware.entity.definition" && tag != "shopware.entity.extension" && tag != "shopware.bulk.entity.extension" {
		return "", fmt.Errorf("unsupported entity service tag %q", tag)
	}
	if strings.Contains(source, className) && strings.Contains(source, tag) {
		return source, nil
	}
	var result string
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xml":
		if strings.TrimSpace(source) == "" {
			result = `<?xml version="1.0" ?>
<container xmlns="http://symfony.com/schema/dic/services"
           xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
           xsi:schemaLocation="http://symfony.com/schema/dic/services https://symfony.com/schema/dic/services/services-1.0.xsd">
    <services>
        <service id="` + className + `">
            <tag name="` + tag + `"/>
        </service>
    </services>
</container>
`
			break
		}
		marker := "</services>"
		position := strings.LastIndex(source, marker)
		if position < 0 {
			return "", fmt.Errorf("services XML has no services element")
		}
		indent := indentationBefore(source, position) + "    "
		entry := indent + `<service id="` + className + `">` + "\n" +
			indent + `    <tag name="` + tag + `"/>` + "\n" +
			indent + `</service>` + "\n"
		result = source[:position] + entry + source[position:]
	case ".yaml", ".yml":
		if strings.TrimSpace(source) == "" {
			result = "services:\n  " + className + ":\n    tags:\n      - { name: " + tag + " }\n"
		} else if !hasYAMLServicesRoot(source) {
			return "", fmt.Errorf("services YAML has no top-level services mapping")
		} else {
			position := yamlServicesEnd(source)
			prefix := ""
			if position > 0 && source[position-1] != '\n' {
				prefix = "\n"
			}
			entry := prefix + "  " + className + ":\n    tags:\n      - { name: " + tag + " }\n"
			result = source[:position] + entry + source[position:]
		}
	case ".php":
		if strings.TrimSpace(source) == "" {
			return "", fmt.Errorf("cannot create PHP service configuration without its closure skeleton")
		}
		position := strings.LastIndex(source, "};")
		if position < 0 {
			return "", fmt.Errorf("services PHP has no closing configurator closure")
		}
		indent := indentationBefore(source, position) + "    "
		entry := indent + `$services->set(\` + className + `::class)->tag('` + tag + `');` + "\n"
		result = source[:position] + entry + source[position:]
	default:
		return "", fmt.Errorf("unsupported service configuration %s", filepath.Base(path))
	}
	registry := language.DefaultRegistry()
	_, parsed, ok := registry.ParsePath(path, result)
	if ok && len(parsed.Errors) != 0 {
		return "", fmt.Errorf("patched service configuration is invalid: %s", parsed.Errors[0].Message())
	}
	return result, nil
}

// RemoveServiceConfiguration removes an exact, explicitly configured service
// class while preserving unrelated service configuration. Resource-based
// discovery needs no edit: once the class file is removed, the resource no
// longer discovers it.
func RemoveServiceConfiguration(path, source, className string) (string, error) {
	className = strings.Trim(className, `\ `)
	if className == "" {
		return "", fmt.Errorf("entity service class is empty")
	}
	if strings.TrimSpace(source) == "" {
		return source, nil
	}
	start, end, found := 0, 0, false
	switch strings.ToLower(filepath.Ext(path)) {
	case ".xml":
		parsed := xmlparser.Parse(source)
		if parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Errors) != 0 {
			return "", fmt.Errorf("services XML cannot be parsed")
		}
		for _, service := range xmlquery.Elements(parsed.Tree.Root, "service") {
			if strings.Trim(xmlquery.AttributeValue(xmlquery.Attribute(service, "id")), `\ `) != className {
				continue
			}
			start, end, found = int(service.Range().Start), int(service.Range().End), true
			break
		}
	case ".yaml", ".yml":
		parsed := yamlparser.Parse(source)
		if parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Errors) != 0 {
			return "", fmt.Errorf("services YAML cannot be parsed")
		}
		root := yamlquery.RootValue(parsed.Tree.Root)
		services := yamlquery.Property(root, "services")
		if pair := yamlquery.PropertyPair(services, className); pair != nil {
			start, end, found = int(pair.Range().Start), int(pair.Range().End), true
		}
	case ".php":
		parsed := phpparser.Parse(source)
		if parsed.Tree == nil || parsed.Tree.Root == nil || len(parsed.Errors) != 0 {
			return "", fmt.Errorf("services PHP cannot be parsed")
		}
		for _, statement := range phpquery.ExpressionStatements(parsed.Tree.Root) {
			if !phpServiceStatementRegisters(statement.Text(), className) {
				continue
			}
			start, end, found = int(statement.Range().Start), int(statement.Range().End), true
			break
		}
	default:
		return "", fmt.Errorf("unsupported service configuration %s", filepath.Base(path))
	}
	if !found {
		return source, nil
	}
	start, end = wholeLineRange(source, start, end)
	result := source[:start] + source[end:]
	registry := language.DefaultRegistry()
	_, parsed, ok := registry.ParsePath(path, result)
	if ok && len(parsed.Errors) != 0 {
		return "", fmt.Errorf("removed service configuration is invalid: %s", parsed.Errors[0].Message())
	}
	return result, nil
}

func phpServiceStatementRegisters(source, className string) bool {
	compact := strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "").Replace(source)
	classReference := `\` + className + "::class"
	return strings.Contains(compact, "$services->set("+classReference) ||
		strings.Contains(compact, "$services->set('"+className+"'") ||
		strings.Contains(compact, `$services->set("`+className+`"`)
}

func wholeLineRange(source string, start, end int) (int, int) {
	lineStart := strings.LastIndex(source[:start], "\n") + 1
	lineEnd := len(source)
	if relative := strings.IndexByte(source[end:], '\n'); relative >= 0 {
		lineEnd = end + relative + 1
	}
	if strings.TrimSpace(source[lineStart:start]) == "" && strings.TrimSpace(source[end:lineEnd]) == "" {
		return lineStart, lineEnd
	}
	return start, end
}

func indentationBefore(source string, offset int) string {
	start := strings.LastIndex(source[:offset], "\n") + 1
	return source[start:offset][:len(source[start:offset])-len(strings.TrimLeft(source[start:offset], " \t"))]
}

func hasYAMLServicesRoot(source string) bool {
	for _, line := range strings.Split(source, "\n") {
		if strings.TrimSpace(line) == "services:" && len(line) == len(strings.TrimLeft(line, " \t")) {
			return true
		}
	}
	return false
}

func yamlServicesEnd(source string) int {
	offset := 0
	inServices := false
	for _, line := range strings.SplitAfter(source, "\n") {
		plain := strings.TrimSuffix(line, "\n")
		trimmed := strings.TrimSpace(plain)
		rootLevel := len(plain) == len(strings.TrimLeft(plain, " \t"))
		if !inServices {
			if rootLevel && trimmed == "services:" {
				inServices = true
			}
			offset += len(line)
			continue
		}
		if rootLevel && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			return offset
		}
		offset += len(line)
	}
	return len(source)
}
