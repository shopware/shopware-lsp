//go:build integration

package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/lsp"
	lspcompletion "github.com/shopware/shopware-lsp/internal/lsp/completion"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	lspsemantic "github.com/shopware/shopware-lsp/internal/lsp/semantic"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/stretchr/testify/require"
)

func realWorldPHPAttributeCompletions(
	phpIndex *php.PHPIndex,
	source,
	needle string,
) []protocol.CompletionItem {
	document := lsp.NewTextDocument(
		"file:///real-world-php-attribute.php",
		source,
		1,
	)
	offset := strings.LastIndex(source, needle) + len(needle)
	if offset < len(needle) {
		return nil
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	return lspcompletion.NewPHPAttributeCompletionProvider(
		phpIndex,
	).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}

func realWorldTwigCompletions(
	root string,
	twigIndex *twig.TwigIndexer,
	phpIndex *php.PHPIndex,
	source,
	needle string,
) []protocol.CompletionItem {
	document := lsp.NewTextDocument(
		"file:///real-world-loop.html.twig",
		source,
		1,
	)
	offset := strings.LastIndex(source, needle) + len(needle)
	if offset < len(needle) {
		return nil
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	nodeOffset := offset
	if nodeOffset > 0 {
		nodeOffset--
	}
	node := document.SyntaxTree.Root.NodeAtOffset(uint32(nodeOffset))
	return lspcompletion.NewTwigCompletionProvider(
		root,
		twigIndex,
		nil,
		phpIndex,
	).GetCompletions(
		context.Background(),
		&lsp.CompletionRequest{
			CompletionParams: params,
			SyntaxContext: lsp.SyntaxContext{
				Document:        document,
				Language:        document.SyntaxLanguage,
				DocumentContent: document.Text,
				DocumentTree:    document.SyntaxTree,
				LineIndex:       document.LineIndex,
				Root:            document.SyntaxTree.Root,
				Node:            node,
			},
		},
	)
}

func realWorldCompletionLabels(
	items []protocol.CompletionItem,
) []string {
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item.Label)
	}
	sort.Strings(result)
	return result
}

func realWorldCompletionRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.CompletionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	if node == nil && offset > 0 {
		node = document.SyntaxTree.Root.NodeAtOffset(offset - 1)
	}
	return &lsp.CompletionRequest{
		CompletionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}

func realWorldSignatureRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.SignatureHelpRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.SignatureHelpParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	if node == nil && offset > 0 {
		node = document.SyntaxTree.Root.NodeAtOffset(offset - 1)
	}
	return &lsp.SignatureHelpRequest{
		SignatureHelpParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document: document, DocumentContent: document.Text,
			DocumentTree: document.SyntaxTree, LineIndex: document.LineIndex,
			Root: document.SyntaxTree.Root, Node: node,
		},
	}
}

func realWorldCompletionByLabel(
	t *testing.T,
	items []protocol.CompletionItem,
	label string,
) protocol.CompletionItem {
	t.Helper()
	for _, item := range items {
		if item.Label == label {
			return item
		}
	}
	t.Fatalf("completion %q not found in %v", label,
		realWorldCompletionLabels(items))
	return protocol.CompletionItem{}
}

func realWorldTwigUXSemanticTokenSnapshot(
	t *testing.T,
	ctx context.Context,
) []string {
	t.Helper()
	document := lsp.NewTextDocument(
		"file:///real-world-ux-annotations.html.twig",
		`{# @prop title string Visible title #}
{# @block content Main content #}`,
		1,
	)
	tokens, err := lspsemantic.NewTwigUXToolkitProvider().
		GetSemanticTokens(
			ctx,
			&lsp.SemanticTokensRequest{Document: document},
		)
	require.NoError(t, err)
	require.Len(t, tokens, 5)
	result := make([]string, 0, len(tokens))
	for _, token := range tokens {
		result = append(
			result,
			fmt.Sprintf(
				"%d:%s",
				token.Type,
				string(document.Text[token.Range.Start:token.Range.End]),
			),
		)
	}
	require.Equal(t, []string{
		fmt.Sprintf("%d:@prop", protocol.SemanticTokenKeyword),
		fmt.Sprintf("%d:title", protocol.SemanticTokenProperty),
		fmt.Sprintf("%d:string", protocol.SemanticTokenType),
		fmt.Sprintf("%d:@block", protocol.SemanticTokenKeyword),
		fmt.Sprintf("%d:content", protocol.SemanticTokenProperty),
	}, result)
	return result
}

func realWorldDefinitionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
	offset uint32,
) *lsp.DefinitionRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.DefinitionParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.DefinitionRequest{
		DefinitionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            node,
		},
	}
}

func realWorldReferenceRequest(
	document *lsp.TextDocument,
	offset uint32,
	includeDeclaration bool,
) *lsp.ReferenceRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.ReferenceParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.Context.IncludeDeclaration = includeDeclaration
	return &lsp.ReferenceRequest{
		ReferenceParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			Token:           document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func realWorldRenameRequest(
	document *lsp.TextDocument,
	offset uint32,
	newName string,
) *lsp.RenameRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.RenameParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	params.NewName = newName
	return &lsp.RenameRequest{
		RenameParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			Token:           document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func realWorldHoverRequest(
	document *lsp.TextDocument,
	offset uint32,
) *lsp.HoverRequest {
	line, character := document.LineIndex.PositionUTF16(offset)
	params := &protocol.HoverParams{}
	params.TextDocument.URI = document.URI
	params.Position.Line = int(line)
	params.Position.Character = int(character)
	return &lsp.HoverRequest{
		HoverParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node:            document.SyntaxTree.Root.NodeAtOffset(offset),
			Token:           document.SyntaxTree.Root.TokenAtOffset(offset),
		},
	}
}

func requireRouteDefinitionPath(
	t *testing.T,
	locations []protocol.Location,
	path string,
) {
	t.Helper()
	requireLocationURI(t, locations, path)
}

func requireLocationURI(
	t *testing.T,
	locations []protocol.Location,
	path string,
) {
	t.Helper()
	uri := uriutil.FileURI(path)
	for _, location := range locations {
		if location.URI == uri {
			return
		}
	}
	t.Fatalf("location %q not found in %#v", uri, locations)
}

func realWorldCodeActionRequest(
	t *testing.T,
	document *lsp.TextDocument,
	needle string,
) *lsp.CodeActionRequest {
	t.Helper()
	require.NotNil(t, document)
	require.NotNil(t, document.SyntaxTree)
	require.NotNil(t, document.SyntaxTree.Root)
	offset := strings.Index(document.Source, needle)
	require.NotEqual(t, -1, offset)
	line, character := document.LineIndex.PositionUTF16(uint32(offset))
	params := &protocol.CodeActionParams{
		Range: protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character + uint32(len(needle))),
			},
		},
	}
	params.TextDocument.URI = document.URI
	return &lsp.CodeActionRequest{
		CodeActionParams: params,
		SyntaxContext: lsp.SyntaxContext{
			Document:        document,
			Language:        document.SyntaxLanguage,
			DocumentContent: document.Text,
			DocumentTree:    document.SyntaxTree,
			LineIndex:       document.LineIndex,
			Root:            document.SyntaxTree.Root,
			Node: document.SyntaxTree.Root.NodeAtOffset(
				uint32(offset),
			),
		},
	}
}
