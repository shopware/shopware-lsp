package codeaction

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	jsonquery "github.com/shopware/shopware-lsp/internal/parser/json/query"
	jsonsyntax "github.com/shopware/shopware-lsp/internal/parser/json/syntax"
)

const copySnippetUsageAction = "shopware.copySnippetUsage"

type SnippetCopyCodeActionProvider struct{}

func NewSnippetCopyCodeActionProvider() *SnippetCopyCodeActionProvider {
	return &SnippetCopyCodeActionProvider{}
}

func (*SnippetCopyCodeActionProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorExtract}
}

func (*SnippetCopyCodeActionProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || request == nil || request.Document == nil ||
		request.Node == nil || request.Document.SyntaxLanguage != language.JSON ||
		!isShopwareSnippetJSON(request.Document.URI) {
		return nil
	}
	key := jsonSnippetKey(request.Node)
	if key == "" {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Shopware: Copy snippet usage for '" + key + "'",
		Kind:  protocol.CodeActionRefactorExtract,
		Command: &protocol.CommandAction{
			Title:     "Copy Shopware snippet usage",
			Command:   copySnippetUsageAction,
			Arguments: []any{key},
		},
	}}
}

func isShopwareSnippetJSON(uri string) bool {
	path := strings.ToLower(filepath.ToSlash(uri))
	return strings.Contains(path, "/resources/snippet/") ||
		strings.Contains(path, "/resources/app/administration/") &&
			strings.Contains(path, "/snippet/")
}

func jsonSnippetKey(node *jsonsyntax.Node) string {
	var pair *jsonsyntax.Node
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == jsonsyntax.JsonPair {
			pair = current
			break
		}
	}
	if pair == nil {
		return ""
	}
	var parts []string
	for current := pair; current != nil; {
		key := jsonquery.StringValue(jsonquery.PairKey(current))
		if key != "" {
			parts = append([]string{key}, parts...)
		}
		parentObject := current.Parent()
		if parentObject == nil || parentObject.Kind() != jsonsyntax.JsonObject {
			break
		}
		parentPair := parentObject.Parent()
		if parentPair == nil || parentPair.Kind() != jsonsyntax.JsonPair {
			break
		}
		current = parentPair
	}
	return strings.Join(parts, ".")
}

var _ lsp.ActionProvider = (*SnippetCopyCodeActionProvider)(nil)
