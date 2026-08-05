package diagnostics

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	xmlquery "github.com/shopware/shopware-lsp/internal/parser/xml/query"
	xmlsyntax "github.com/shopware/shopware-lsp/internal/parser/xml/syntax"
	yamlquery "github.com/shopware/shopware-lsp/internal/parser/yaml/query"
	yamlsyntax "github.com/shopware/shopware-lsp/internal/parser/yaml/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/suggestion"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	missingMessengerMessageCode       lsp.DiagnosticID = "symfony.messenger.message.missing"
	missingMessengerHandlerMethodCode lsp.DiagnosticID = "symfony.messenger.handler_method.missing"
)

type MessengerAnalyzer struct {
	phpIndex *php.PHPIndex
}

func NewMessengerAnalyzer(
	phpIndex *php.PHPIndex,
) *MessengerAnalyzer {
	return &MessengerAnalyzer{phpIndex: phpIndex}
}

func (p *MessengerAnalyzer) Analyze(
	ctx context.Context,
	document *lsp.TextDocument,
) ([]lsp.Problem, error) {
	if p == nil || p.phpIndex == nil || document == nil ||
		document.SyntaxTree == nil || document.SyntaxTree.Root == nil {
		return nil, nil
	}
	path, _ := uriutil.Path(document.URI)
	validationContext := ctx
	if strings.EqualFold(filepath.Ext(path), ".php") {
		validationContext = p.phpIndex.AddDocumentContext(
			ctx,
			path,
			document.Version,
			document.SyntaxTree.Root,
			document.SyntaxTree.Root,
		)
	}
	var result []lsp.Problem
	for _, node := range messengerDiagnosticNodes(
		path,
		document.SyntaxTree.Root,
	) {
		if ctx.Err() != nil {
			return nil, nil
		}
		reference, found := messenger.ReferenceAt(
			validationContext,
			path,
			document.SyntaxTree.Root,
			node,
		)
		if !found {
			continue
		}
		switch reference.Role {
		case messenger.ReferenceMessage:
			if reference.Name == "" {
				continue
			}
			if _, exists := p.phpIndex.FindClass(
				reference.Name,
			); exists {
				continue
			}
			result = append(result, lsp.Problem{
				Range: valueNodeTextRange(reference.Node, reference.Name),
				Message: fmt.Sprintf(
					"Messenger message class '%s' not found",
					reference.Name,
				),
				Severity: protocol.DiagnosticSeverityWarning,
				Source:   "symfony",
				ID:       missingMessengerMessageCode,
				Payload: map[string]any{
					"suggestions": suggestion.Similar(
						reference.Name,
						p.phpIndex.ClassNamesView(),
					),
				},
			})
		case messenger.ReferenceHandlerMethod:
			result = append(
				result,
				p.handlerDiagnostic(document, reference)...,
			)
		}
	}
	return result, nil
}

func (p *MessengerAnalyzer) handlerDiagnostic(
	_ *lsp.TextDocument,
	reference messenger.Reference,
) []lsp.Problem {
	if reference.Name == "" || reference.Class == "" ||
		len(p.phpIndex.FindMethods(
			reference.Class,
			reference.Name,
		)) != 0 {
		return nil
	}
	if _, exists := p.phpIndex.FindClass(reference.Class); !exists {
		return nil
	}
	methods := messenger.PublicHandlerMethods(
		p.phpIndex,
		reference.Class,
	)
	names := make([]string, 0, len(methods))
	for _, method := range methods {
		names = append(names, method.Name)
	}
	return []lsp.Problem{{
		Range: valueNodeTextRange(reference.Node, reference.Name),
		Message: fmt.Sprintf(
			"Messenger handler method '%s::%s' not found",
			reference.Class,
			reference.Name,
		),
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "symfony",
		ID:       missingMessengerHandlerMethodCode,
		Payload: map[string]any{
			"suggestions": suggestion.Similar(
				reference.Name,
				names,
			),
		},
	}}
}

func messengerDiagnosticNodes(
	path string,
	root *cst.Node,
) []*cst.Node {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".php":
		values := phpquery.Nodes(root, phpsyntax.PhpString)
		result := make([]*cst.Node, len(values))
		copy(result, values)
		return result
	case ".yaml", ".yml":
		values := yamlquery.Nodes(root, yamlsyntax.YamlScalar)
		result := make([]*cst.Node, len(values))
		copy(result, values)
		return result
	case ".xml":
		values := xmlquery.Nodes(root, xmlsyntax.XmlAttribute)
		result := make([]*cst.Node, len(values))
		copy(result, values)
		return result
	default:
		return nil
	}
}
