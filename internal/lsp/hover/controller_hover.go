package hover

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type ControllerHoverProvider struct {
	services *symfony.ServiceIndex
	php      *php.PHPIndex
}

func NewControllerHoverProvider(
	services *symfony.ServiceIndex,
	phpIndex *php.PHPIndex,
) *ControllerHoverProvider {
	return &ControllerHoverProvider{services: services, php: phpIndex}
}

func (p *ControllerHoverProvider) GetHover(
	_ context.Context,
	request *lsp.HoverRequest,
) (*protocol.Hover, error) {
	if p == nil || p.php == nil || request == nil || request.Node == nil ||
		request.LineIndex == nil ||
		!strings.EqualFold(filepath.Ext(request.TextDocument.URI), ".twig") {
		return nil, nil
	}
	reference, ok := symfony.TwigControllerReferenceAt(request.Node)
	if !ok {
		return nil, nil
	}
	resolution, err := symfony.ResolveControllerReference(
		reference.ControllerReference,
		p.services,
		p.php,
	)
	if err != nil || !resolution.TargetExists {
		return nil, err
	}
	var markdown strings.Builder
	fmt.Fprintf(
		&markdown,
		"**Symfony controller** `%s`\n\n",
		strings.ReplaceAll(reference.Value, "`", "\\`"),
	)
	if resolution.MethodDeclared {
		markdown.WriteString("```php\n")
		markdown.WriteString(controllerHoverSignature(
			resolution.Class,
			resolution.Method,
		))
		markdown.WriteString("\n```")
		if resolution.Method.DocSummary != "" {
			fmt.Fprintf(
				&markdown,
				"\n\n%s",
				resolution.Method.DocSummary,
			)
		}
		if !resolution.MethodFound {
			markdown.WriteString("\n\n**Not callable:** method is not public.")
		}
	} else if resolution.ClassFound {
		fmt.Fprintf(
			&markdown,
			"Resolved class: `%s`\n\nMethod `%s()` is not declared.",
			resolution.Class.FullyQualified,
			reference.Method,
		)
	}
	rng := controllerHoverRange(reference.Range, request.LineIndex)
	return &protocol.Hover{
		Contents: protocol.MarkupContent{
			Kind:  protocol.Markdown,
			Value: markdown.String(),
		},
		Range: &rng,
	}, nil
}

func controllerHoverSignature(
	class,
	method semantic.Symbol,
) string {
	var result strings.Builder
	result.WriteString(visibilityName(method.Visibility))
	result.WriteString(" function ")
	result.WriteString(strings.TrimLeft(class.FullyQualified, `\`))
	result.WriteString("::")
	result.WriteString(method.Name)
	result.WriteByte('(')
	for index, parameter := range method.Parameters {
		if index != 0 {
			result.WriteString(", ")
		}
		if !parameter.Type.IsUnknown() {
			result.WriteString(parameter.Type.String())
			result.WriteByte(' ')
		}
		result.WriteString(parameter.Name)
		if parameter.Optional {
			result.WriteString(" = …")
		}
	}
	result.WriteByte(')')
	if !method.ReturnType.IsUnknown() {
		result.WriteString(": ")
		result.WriteString(method.ReturnType.String())
	}
	return result.String()
}

func visibilityName(visibility semantic.Visibility) string {
	switch visibility {
	case semantic.Protected:
		return "protected"
	case semantic.Private:
		return "private"
	default:
		return "public"
	}
}

func controllerHoverRange(
	rng cst.TextRange,
	lineIndex *cst.LineIndex,
) protocol.Range {
	startLine, startCharacter := lineIndex.PositionUTF16(rng.Start)
	endLine, endCharacter := lineIndex.PositionUTF16(rng.End)
	return protocol.Range{
		Start: protocol.Position{
			Line:      int(startLine),
			Character: int(startCharacter),
		},
		End: protocol.Position{
			Line:      int(endLine),
			Character: int(endCharacter),
		},
	}
}
