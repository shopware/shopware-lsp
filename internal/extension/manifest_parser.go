package extension

import (
	"strings"

	xmlparser "github.com/shopware/shopware-lsp/internal/parser/xml"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
)

// ManifestMeta represents metadata from a Shopware app manifest.xml file.
type ManifestMeta struct {
	Name        string
	Label       string
	Description string
	Author      string
	Copyright   string
	Version     string
	License     string
	Path        string
	Permissions []AppPermission
}

// ParseManifestXml parses a Shopware app manifest.xml file and extracts its
// metadata using the shared error-tolerant XML backend.
func ParseManifestXml(path string, data []byte) (*ManifestMeta, error) {
	tree := xmlparser.Parse(string(data)).Tree
	return ParseManifestTree(path, tree)
}

func ParseManifestTree(path string, tree *xmlsyntax.Tree) (*ManifestMeta, error) {
	if tree == nil || tree.Root == nil {
		return nil, nil
	}
	manifests := xmlquery.Elements(tree.Root, "manifest")
	if len(manifests) == 0 {
		return nil, nil
	}

	meta := xmlquery.ChildElement(manifests[0], "meta")
	if meta == nil {
		return nil, nil
	}

	manifest := &ManifestMeta{Path: path}
	for _, child := range xmlquery.ChildElements(meta) {
		value := strings.TrimSpace(xmlquery.TextContent(child))
		switch xmlquery.ElementName(child) {
		case "name":
			manifest.Name = value
		case "label":
			manifest.Label = value
		case "description":
			manifest.Description = value
		case "author":
			manifest.Author = value
		case "copyright":
			manifest.Copyright = value
		case "version":
			manifest.Version = value
		case "license":
			manifest.License = value
		}
	}
	if permissions := xmlquery.ChildElement(manifests[0], "permissions"); permissions != nil {
		lines := xmlsyntax.NewLineIndex(tree.Source)
		for _, permission := range xmlquery.ChildElements(permissions) {
			operation := xmlquery.ElementName(permission)
			entity := strings.TrimSpace(xmlquery.TextContent(permission))
			if entity == "" {
				continue
			}
			line, _ := lines.Position(permission.RangeTrimmedTrivia().Start)
			manifest.Permissions = append(manifest.Permissions, AppPermission{
				Operation: operation,
				Entity:    entity,
				Line:      int(line) + 1,
			})
		}
	}

	return manifest, nil
}
