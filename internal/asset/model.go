// Package asset indexes Symfony public assets and Webpack Encore entries and
// provides editor-independent reference extraction for Twig and PHP.
package asset

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type Kind uint8

const (
	PublicFile Kind = iota
	ManifestAsset
	EncoreEntry
	ImportmapModule
	ViteEntry
	AsseticNamedAsset
)

func (kind Kind) String() string {
	switch kind {
	case ManifestAsset:
		return "manifest asset"
	case EncoreEntry:
		return "Encore entry"
	case ImportmapModule:
		return "AssetMapper module"
	case ViteEntry:
		return "Vite entry"
	case AsseticNamedAsset:
		return "Assetic named asset"
	default:
		return "public asset"
	}
}

type Resource struct {
	Name       string
	File       string
	Target     string
	Kind       Kind
	Range      cst.TextRange
	URL        string
	Version    string
	ModuleType string
	Entrypoint bool
}

type Usage struct {
	Name    string
	Package string
	Kind    ReferenceKind
	File    string
	Range   cst.TextRange
}

// Package describes one named Symfony Asset package. BasePath is the
// workspace-public prefix used to translate a logical package path to an
// indexed resource path when that mapping is statically known.
type Package struct {
	Name     string
	BasePath string
	File     string
	Range    cst.TextRange
	Inferred bool
}

func normalizeName(value string) string {
	value = strings.ReplaceAll(strings.TrimSpace(value), `\`, "/")
	value = strings.TrimLeft(value, "/")
	for strings.Contains(value, "//") {
		value = strings.ReplaceAll(value, "//", "/")
	}
	return value
}
