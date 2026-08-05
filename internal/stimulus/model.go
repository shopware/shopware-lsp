package stimulus

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/parser/cst"
)

type SourceKind uint8

const (
	JavaScriptSource SourceKind = iota
	RegisteredSource
	ControllersJSONSource
)

func (kind SourceKind) String() string {
	switch kind {
	case JavaScriptSource:
		return "JavaScript controller"
	case RegisteredSource:
		return "registered controller"
	case ControllersJSONSource:
		return "controllers.json"
	default:
		return "Stimulus controller"
	}
}

type Controller struct {
	Name         string
	OriginalName string
	File         string
	Range        cst.TextRange
	Source       SourceKind
}

func (controller Controller) TwigName() string {
	if controller.OriginalName != "" {
		return controller.OriginalName
	}
	return controller.Name
}

type Usage struct {
	Name  string
	File  string
	Range cst.TextRange
}

type UsageCatalog struct {
	File   string
	Usages []Usage
}

type Reference struct {
	Name  string
	Range cst.TextRange
	Twig  bool
}

func NormalizeName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "@", "")
	return strings.ReplaceAll(name, "/", "--")
}
