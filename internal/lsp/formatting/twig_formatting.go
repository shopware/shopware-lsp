package formatting

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/twigfmt"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

type TwigProvider struct{}

func NewTwigProvider() *TwigProvider { return &TwigProvider{} }

func (*TwigProvider) FormatDocument(
	ctx context.Context,
	request *lsp.DocumentFormattingRequest,
) (string, bool, error) {
	if request == nil || request.Document == nil ||
		request.Document.SyntaxLanguage != language.Twig ||
		request.Document.SyntaxTree == nil {
		return "", false, nil
	}
	if err := ctx.Err(); err != nil {
		return "", true, err
	}

	path, err := uriutil.Path(request.Document.URI)
	if err != nil {
		return "", true, err
	}
	formatted, err := twigfmt.FormatTree(
		request.Document.SyntaxTree,
		twigfmt.Options{
			InsertSpaces:            request.Options.InsertSpaces,
			TabSize:                 request.Options.TabSize,
			TwigBlockIndentChildren: !isAdministrationTemplate(path),
		},
	)
	return formatted, true, err
}

func isAdministrationTemplate(path string) bool {
	normalized := filepath.ToSlash(filepath.Clean(path))
	return strings.Contains(normalized, "/Resources/app/administration/")
}
