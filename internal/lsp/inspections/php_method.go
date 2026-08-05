package inspections

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	phpcst "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/rewrite"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

var phpIdentifierPattern = regexp.MustCompile(`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`)

type phpMethodPayload struct {
	ClassName       string               `json:"className"`
	MethodName      string               `json:"methodName"`
	Parameters      []string             `json:"parameters,omitempty"`
	TypedParameters []phpMethodParameter `json:"typedParameters,omitempty"`
}

type phpMethodParameter struct {
	Name  string   `json:"name"`
	Types []string `json:"types,omitempty"`
}

type phpMethodFix struct {
	id          lsp.FixID
	titlePrefix string
	phpIndex    *php.PHPIndex
}

func (f phpMethodFix) ID() lsp.FixID { return f.id }

func (f phpMethodFix) Present(
	_ context.Context,
	fixContext lsp.FixContext,
) (lsp.FixPresentation, bool, error) {
	payload, err := lsp.DecodeBoundFixPayload[phpMethodPayload](fixContext)
	if err != nil || f.phpIndex == nil || payload.ClassName == "" ||
		!phpIdentifierPattern.MatchString(payload.MethodName) {
		return lsp.FixPresentation{}, false, err
	}
	for _, parameter := range payload.Parameters {
		if !phpIdentifierPattern.MatchString(strings.TrimPrefix(parameter, "$")) {
			return lsp.FixPresentation{}, false, nil
		}
	}
	for _, parameter := range payload.TypedParameters {
		if !phpIdentifierPattern.MatchString(strings.TrimPrefix(parameter.Name, "$")) {
			return lsp.FixPresentation{}, false, nil
		}
		for _, typeName := range parameter.Types {
			if !validPHPClassName(typeName) {
				return lsp.FixPresentation{}, false, nil
			}
		}
	}
	return lsp.FixPresentation{
		Title: fmt.Sprintf(
			"Symfony: Create %s method '%s'",
			f.titlePrefix,
			payload.MethodName,
		),
		Kind:       protocol.CodeActionQuickFix,
		Preferred:  true,
		Resolution: lsp.FixLazy,
	}, true, nil
}

func (f phpMethodFix) Build(
	ctx context.Context,
	fixContext lsp.FixContext,
) (rewrite.WorkspacePlan, error) {
	payload, err := lsp.DecodeBoundFixPayload[phpMethodPayload](fixContext)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if _, err := fixContext.Anchor.Resolve(
		fixContext.Document.URI,
		fixContext.Document.Version,
		fixContext.Document.SyntaxLanguage,
		fixContext.Document.SyntaxTree,
	); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	symbol, found := f.phpIndex.FindClass(payload.ClassName)
	if !found || symbol.Path == "" || strings.HasPrefix(symbol.Path, "phpstub://") {
		return rewrite.WorkspacePlan{}, fmt.Errorf("class %q is not editable", payload.ClassName)
	}
	targetURI := uriutil.FileURI(symbol.Path)
	target, err := fixContext.Documents.ResolveDocument(ctx, targetURI)
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	if target.Document.SyntaxTree == nil || target.Document.SyntaxTree.Root == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("target class has no PHP syntax tree")
	}
	class := phpClassForSymbol(target.Document, payload.ClassName, symbol.Range.Start)
	body := phpquery.ClassBody(class)
	if body == nil {
		return rewrite.WorkspacePlan{}, fmt.Errorf("class %q has no body", payload.ClassName)
	}
	for _, method := range phpquery.Methods(class) {
		if strings.EqualFold(phpquery.MethodName(method), payload.MethodName) {
			return rewrite.WorkspacePlan{}, fmt.Errorf(
				"method %s::%s already exists",
				payload.ClassName,
				payload.MethodName,
			)
		}
	}
	methodText := phpMethodFragment(
		payload.MethodName,
		payload.Parameters,
		payload.TypedParameters,
	)
	if err := validatePHPMethodFragment(methodText, payload.MethodName); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	offset, insertion, ok := phpMethodInsertion(target.Document.Source, class, body, methodText)
	if !ok {
		return rewrite.WorkspacePlan{}, fmt.Errorf("class closing brace was not found")
	}
	builder := rewrite.NewBuilder(target.Document.Source)
	if err := builder.Insert(offset, insertion); err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	edits, err := builder.Finish()
	if err != nil {
		return rewrite.WorkspacePlan{}, err
	}
	return rewrite.WorkspacePlan{Documents: []rewrite.DocumentPlan{
		rewrite.NewDocumentPlan(targetURI, target.Version, target.Document.Source, edits),
	}}, nil
}

func phpClassForSymbol(
	document *lsp.TextDocument,
	className string,
	oldOffset uint32,
) *cst.Node {
	shortName := className
	if index := strings.LastIndex(shortName, `\`); index >= 0 {
		shortName = shortName[index+1:]
	}
	var best *cst.Node
	bestDistance := ^uint32(0)
	for _, class := range phpquery.Classes(document.SyntaxTree.Root) {
		if !strings.EqualFold(phpquery.ClassName(class), shortName) {
			continue
		}
		distance := class.Range().Start
		if distance > oldOffset {
			distance -= oldOffset
		} else {
			distance = oldOffset - distance
		}
		if best == nil || distance < bestDistance {
			best = class
			bestDistance = distance
		}
	}
	return best
}

func phpMethodFragment(
	methodName string,
	parameters []string,
	typed []phpMethodParameter,
) string {
	normalized := make([]string, 0, len(parameters)+len(typed))
	for _, parameter := range parameters {
		parameter = strings.TrimSpace(parameter)
		if parameter != "" && !strings.HasPrefix(parameter, "$") {
			parameter = "$" + parameter
		}
		normalized = append(normalized, parameter)
	}
	for _, parameter := range typed {
		name := strings.TrimSpace(parameter.Name)
		if name != "" && !strings.HasPrefix(name, "$") {
			name = "$" + name
		}
		types := make([]string, 0, len(parameter.Types))
		for _, typeName := range parameter.Types {
			typeName = strings.Trim(strings.TrimSpace(typeName), `\`)
			if typeName != "" {
				types = append(types, `\`+typeName)
			}
		}
		if len(types) != 0 {
			name = strings.Join(types, "|") + " " + name
		}
		normalized = append(normalized, name)
	}
	return fmt.Sprintf(
		"public function %s(%s)\n{\n}",
		methodName,
		strings.Join(normalized, ", "),
	)
}

func validPHPClassName(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), `\`)
	if value == "" {
		return false
	}
	for _, part := range strings.Split(value, `\`) {
		if !phpIdentifierPattern.MatchString(part) {
			return false
		}
	}
	return true
}

func validatePHPMethodFragment(fragment, methodName string) error {
	parsed := phpcst.Parse("<?php\nclass Fragment {\n" + fragment + "\n}\n")
	if len(parsed.Errors) != 0 || parsed.Tree == nil || parsed.Tree.Root == nil {
		return fmt.Errorf("generated PHP method is not syntactically valid")
	}
	classes := phpquery.Classes(parsed.Tree.Root)
	if len(classes) != 1 {
		return fmt.Errorf("generated PHP method has no class context")
	}
	methods := phpquery.Methods(classes[0])
	if len(methods) != 1 || phpquery.MethodName(methods[0]) != methodName {
		return fmt.Errorf("generated PHP fragment is not method %q", methodName)
	}
	return nil
}

func phpMethodInsertion(
	source string,
	class,
	body *cst.Node,
	fragment string,
) (uint32, string, bool) {
	if class == nil || body == nil {
		return 0, "", false
	}
	bodyText := body.Text()
	closingIndex := strings.LastIndexByte(bodyText, '}')
	if closingIndex < 0 {
		return 0, "", false
	}
	closingOffset := body.Range().Start + uint32(closingIndex)
	classIndent := sourceIndentationAt(source, class.Range().Start)
	memberIndent := classIndent + "    "
	if methods := phpquery.Methods(class); len(methods) != 0 {
		if inferred := sourceIndentationAt(source, methods[0].Range().Start); inferred != "" {
			memberIndent = inferred
		}
	}
	lines := strings.Split(fragment, "\n")
	for index := range lines {
		lines[index] = memberIndent + lines[index]
	}
	block := strings.Join(lines, "\n")
	bodyContent := ""
	if closingIndex > 0 {
		bodyContent = strings.TrimSpace(bodyText[1:closingIndex])
	}
	lineStart := sourceLineStart(source, closingOffset)
	if strings.TrimSpace(source[lineStart:closingOffset]) == "" {
		prefix := ""
		if bodyContent != "" {
			prefix = "\n"
		}
		return lineStart, prefix + block + "\n", true
	}
	prefix := "\n"
	if bodyContent == "" {
		prefix = "\n"
	}
	return closingOffset, prefix + block + "\n" + classIndent, true
}

func sourceIndentationAt(source string, offset uint32) string {
	start := sourceLineStart(source, offset)
	end := start
	for int(end) < len(source) {
		switch source[end] {
		case ' ', '\t':
			end++
		default:
			return source[start:end]
		}
	}
	return source[start:end]
}

func sourceLineStart(source string, offset uint32) uint32 {
	if int(offset) > len(source) {
		offset = uint32(len(source))
	}
	for offset > 0 && source[offset-1] != '\n' {
		offset--
	}
	return offset
}
