package inspections

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/diagnostics"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phprewrite "github.com/shopware/shopware-lsp/internal/php/rewrite"
	"github.com/shopware/shopware-lsp/internal/rewrite"
)

const declarationMigrationFixID lsp.FixID = "shopware-rector-declaration-migration"

type declarationMigrationFix struct{}

func (declarationMigrationFix) ID() lsp.FixID { return declarationMigrationFixID }

func (declarationMigrationFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	title := "Shopware: Update declaration"
	switch payload.Kind {
	case "interface-class", "interface-parameter", "interface-property":
		title = "Shopware: Replace interface with abstract class"
	case "parameter-add":
		title = "Shopware: Add required method parameter"
	case "parameter-type", "return-type":
		title = "Shopware: Update native type"
	}
	return lsp.FixPresentation{
		Title:      title,
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixEager,
	}, payload.Safe && payload.Rule == "declaration-migration", err
}

func (declarationMigrationFix) Build(
	_ context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[diagnostics.ShopwareMigrationPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if !payload.Safe || payload.Rule != "declaration-migration" || payload.Replacement == "" {
		return rewrite.WorkspacePlan{}, fmt.Errorf("declaration migration is no longer safe")
	}
	element, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	editor := phprewrite.NewEditor(
		fixContext.Document.Source,
		fixContext.Document.SyntaxTree.Root,
	)
	switch payload.Kind {
	case "interface-class":
		class := ancestorNode(element, phpsyntax.PhpClassDeclaration)
		if class == nil || payload.Original == "" {
			return rewrite.WorkspacePlan{}, fmt.Errorf("interface migration class changed")
		}
		parent, referenceErr := editor.ClassReference(strings.Trim(payload.Replacement, "\\"))
		if referenceErr != nil {
			return rewrite.WorkspacePlan{}, referenceErr
		}
		extends := phpquery.DirectChild(class, phpsyntax.PhpExtendsClause)
		implements := phpquery.DirectChild(class, phpsyntax.PhpImplementsClause)
		implementedNames := directMigrationChildren(implements, phpsyntax.PhpName)
		switch {
		case extends != nil:
			removed, removeErr := editor.RemoveImplements(class, payload.Original)
			if removeErr != nil {
				return rewrite.WorkspacePlan{}, removeErr
			}
			if !removed {
				return rewrite.WorkspacePlan{}, fmt.Errorf("implemented interface changed")
			}
		case len(implementedNames) == 1 && implements != nil:
			if err := editor.ReplaceRange(
				implements.RangeTrimmedTrivia(),
				"extends "+parent,
			); err != nil {
				return rewrite.WorkspacePlan{}, err
			}
		default:
			if err := editor.SetExtends(class, parent); err != nil {
				return rewrite.WorkspacePlan{}, err
			}
			removed, removeErr := editor.RemoveImplements(class, payload.Original)
			if removeErr != nil {
				return rewrite.WorkspacePlan{}, removeErr
			}
			if !removed {
				return rewrite.WorkspacePlan{}, fmt.Errorf("implemented interface changed")
			}
		}
	case "interface-parameter", "parameter-type":
		parameter := ancestorNode(element, phpsyntax.PhpParameter)
		if parameter == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf("parameter type target changed")
		}
		typeText, referenceErr := migrationTypeReference(editor, payload.Replacement)
		if referenceErr != nil {
			return rewrite.WorkspacePlan{}, referenceErr
		}
		if err := editor.SetParameterType(parameter, typeText); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "interface-property":
		property := ancestorNode(element, phpsyntax.PhpPropertyDeclaration)
		if property == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf("property type target changed")
		}
		typeText, referenceErr := migrationTypeReference(editor, payload.Replacement)
		if referenceErr != nil {
			return rewrite.WorkspacePlan{}, referenceErr
		}
		if err := editor.SetPropertyType(property, typeText); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "parameter-add":
		method := ancestorNode(element, phpsyntax.PhpMethodDeclaration)
		if method == nil || payload.ArgumentIndex < 0 {
			return rewrite.WorkspacePlan{}, fmt.Errorf("method parameter target changed")
		}
		if err := editor.InsertParameter(method, payload.ArgumentIndex, payload.Replacement); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	case "return-type":
		method := ancestorNode(element, phpsyntax.PhpMethodDeclaration)
		if method == nil {
			return rewrite.WorkspacePlan{}, fmt.Errorf("return type target changed")
		}
		typeText, referenceErr := migrationTypeReference(editor, payload.Replacement)
		if referenceErr != nil {
			return rewrite.WorkspacePlan{}, referenceErr
		}
		if err := editor.SetReturnType(method, typeText); err != nil {
			return rewrite.WorkspacePlan{}, err
		}
	default:
		return rewrite.WorkspacePlan{}, fmt.Errorf("unknown declaration migration %q", payload.Kind)
	}
	return finishPHPRewrite(fixContext, editor)
}

func migrationTypeReference(
	editor *phprewrite.Editor,
	typeText string,
) (string, error) {
	typeText = strings.TrimSpace(typeText)
	if typeText == "" {
		return "", fmt.Errorf("migration type is empty")
	}
	if !strings.Contains(strings.TrimPrefix(typeText, "?"), "\\") {
		return typeText, nil
	}
	nullable := strings.HasPrefix(typeText, "?")
	class := strings.Trim(strings.TrimPrefix(typeText, "?"), "\\")
	reference, err := editor.ClassReference(class)
	if err != nil {
		return "", err
	}
	if nullable {
		return "?" + reference, nil
	}
	return reference, nil
}

func directMigrationChildren(
	node *phpsyntax.Node,
	kind phpsyntax.Kind,
) []*phpsyntax.Node {
	if node == nil {
		return nil
	}
	var result []*phpsyntax.Node
	for index := 0; index < node.ChildCount(); index++ {
		child, ok := node.Child(index).(*phpsyntax.Node)
		if ok && child.Kind() == kind {
			result = append(result, child)
		}
	}
	return result
}
