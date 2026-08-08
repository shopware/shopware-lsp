package diagnostics

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const ServicesXMLDeprecatedCode lsp.DiagnosticID = "symfony.services_xml.deprecated"

type ServicesXMLDeprecationPayload struct {
	Convertible bool `json:"convertible"`
}

// ServicesXMLDeprecationAnalyzer mirrors shopware-cli's platform-plugin
// validation. Only convention-loaded Resources/config/services.xml files are
// flagged; arbitrary XML files may be loaded explicitly and cannot be safely
// renamed without updating their loader.
type ServicesXMLDeprecationAnalyzer struct{}

func NewServicesXMLDeprecationAnalyzer() *ServicesXMLDeprecationAnalyzer {
	return &ServicesXMLDeprecationAnalyzer{}
}

func (*ServicesXMLDeprecationAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if document == nil || document.SyntaxTree == nil ||
		document.SyntaxTree.Root == nil || !isConventionLoadedServicesXML(document.URI) {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	convertible := false
	if container, err := symfony.ParseServicesXML(document.Text); err == nil {
		_, conversionErr := symfony.ConvertContainerToYAML(container)
		convertible = conversionErr == nil
	}

	element := cst.Element(document.SyntaxTree.Root)
	rng := cst.TextRange{End: min(uint32(len(document.Source)), 1)}
	containers := xmlquery.Elements(document.SyntaxTree.Root, "container")
	if len(containers) != 0 {
		element = containers[0]
		rng = containers[0].Range()
		if offset := strings.Index(containers[0].Text(), "container"); offset >= 0 {
			rng.Start += uint32(offset)
			rng.End = rng.Start + uint32(len("container"))
		}
	}

	return []lsp.Problem{{
		ID:       ServicesXMLDeprecatedCode,
		Range:    rng,
		Element:  element,
		Message:  "Symfony services.xml is deprecated; migrate to services.yaml",
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "symfony",
		Tags:     []protocol.DiagnosticTag{protocol.DiagnosticTagDeprecated},
		Payload:  ServicesXMLDeprecationPayload{Convertible: convertible},
	}}, nil
}

func isConventionLoadedServicesXML(uri string) bool {
	path, err := uriutil.Path(uri)
	if err != nil || !strings.EqualFold(filepath.Base(path), "services.xml") {
		return false
	}
	normalized := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	return strings.HasSuffix(normalized, "/resources/config/services.xml")
}
