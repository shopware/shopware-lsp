package entityschema

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
)

// PatchServiceConfiguration registers a definition while preserving the
// surrounding configuration file. When no file exists, callers pass an empty
// source and an XML target path.
func PatchServiceConfiguration(path, source, definitionClass string) (string, error) {
	definitionClass = strings.Trim(definitionClass, `\ `)
	if definitionClass == "" {
		return "", fmt.Errorf("entity definition service class is empty")
	}
	if strings.Contains(source, definitionClass) && strings.Contains(source, "shopware.entity.definition") {
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
        <service id="` + definitionClass + `">
            <tag name="shopware.entity.definition"/>
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
		entry := indent + `<service id="` + definitionClass + `">` + "\n" +
			indent + `    <tag name="shopware.entity.definition"/>` + "\n" +
			indent + `</service>` + "\n"
		result = source[:position] + entry + source[position:]
	case ".yaml", ".yml":
		if strings.TrimSpace(source) == "" {
			result = "services:\n  " + definitionClass + ":\n    tags:\n      - { name: shopware.entity.definition }\n"
		} else if !hasYAMLServicesRoot(source) {
			return "", fmt.Errorf("services YAML has no top-level services mapping")
		} else {
			position := yamlServicesEnd(source)
			prefix := ""
			if position > 0 && source[position-1] != '\n' {
				prefix = "\n"
			}
			entry := prefix + "  " + definitionClass + ":\n    tags:\n      - { name: shopware.entity.definition }\n"
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
		entry := indent + `$services->set(\` + definitionClass + `::class)->tag('shopware.entity.definition');` + "\n"
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
