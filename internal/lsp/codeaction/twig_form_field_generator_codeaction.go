package codeaction

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	generateTwigFormFieldsAction   = "shopware.symfony.generateTwigFormFields"
	twigFormFieldCandidatesCommand = "shopware/symfony/twig/form/fields/candidates"
	generateTwigFormFieldsCommand  = "shopware/symfony/twig/form/fields/generate"
)

var twigFormIdentifierPattern = regexp.MustCompile(
	`^[A-Za-z_\x80-\xff][A-Za-z0-9_\x80-\xff]*$`,
)

// TwigFormFieldGeneratorProvider ports the reference plugin's Twig form-row
// generator. FormView provenance is indexed from controller createForm()
// expressions, keeping each template variable associated with its FormType.
type TwigFormFieldGeneratorProvider struct {
	forms    *form.Index
	phpIndex *php.PHPIndex
}

func NewTwigFormFieldGeneratorProvider(
	forms *form.Index,
	phpIndex *php.PHPIndex,
) *TwigFormFieldGeneratorProvider {
	return &TwigFormFieldGeneratorProvider{
		forms:    forms,
		phpIndex: phpIndex,
	}
}

func (p *TwigFormFieldGeneratorProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *TwigFormFieldGeneratorProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.forms == nil ||
		p.phpIndex == nil || request == nil ||
		request.CodeActionParams == nil || request.Document == nil ||
		request.Document.SyntaxLanguage != language.Twig {
		return nil
	}
	candidates, err := p.twigFormCandidates(request.Document.URI)
	if err != nil || len(candidates) == 0 {
		return nil
	}
	return []protocol.CodeAction{{
		Title: "Symfony: Generate Twig form rows",
		Kind:  protocol.CodeActionRefactorRewrite,
		Command: &protocol.CommandAction{
			Title:     "Symfony: Generate Twig form rows",
			Command:   generateTwigFormFieldsAction,
			Arguments: []any{request.TextDocument.URI},
		},
	}}
}

func (p *TwigFormFieldGeneratorProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		twigFormFieldCandidatesCommand: p.getTwigFormFieldCandidates,
		generateTwigFormFieldsCommand:  p.generateTwigFormFields,
	}
}

type twigFormFieldRequest struct {
	FileURI        string   `json:"fileUri"`
	Variable       string   `json:"variable,omitempty"`
	FormType       string   `json:"formType,omitempty"`
	SelectedFields []string `json:"selectedFields,omitempty"`
}

type twigFormCandidate struct {
	Variable string   `json:"variable"`
	FormType string   `json:"formType"`
	Fields   []string `json:"fields"`
}

type twigFormFieldCandidatesResponse struct {
	Forms []twigFormCandidate `json:"forms"`
}

type twigFormFieldGenerationResponse struct {
	Content string `json:"content"`
}

func (p *TwigFormFieldGeneratorProvider) getTwigFormFieldCandidates(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var params twigFormFieldRequest
	if err := decodeSymfonyGeneratorRequest(raw, &params); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	candidates, err := p.twigFormCandidates(params.FileURI)
	if err != nil {
		return nil, err
	}
	return twigFormFieldCandidatesResponse{Forms: candidates}, nil
}

func (p *TwigFormFieldGeneratorProvider) generateTwigFormFields(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	var params twigFormFieldRequest
	if err := decodeSymfonyGeneratorRequest(raw, &params); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !twigFormIdentifierPattern.MatchString(params.Variable) {
		return nil, fmt.Errorf("invalid Twig form variable %q", params.Variable)
	}
	candidates, err := p.twigFormCandidates(params.FileURI)
	if err != nil {
		return nil, err
	}
	var selectedForm *twigFormCandidate
	for index := range candidates {
		if candidates[index].Variable == params.Variable &&
			strings.EqualFold(
				candidates[index].FormType,
				strings.Trim(params.FormType, `\`),
			) {
			selectedForm = &candidates[index]
			break
		}
	}
	if selectedForm == nil {
		return nil, fmt.Errorf(
			"twig variable %q is no longer associated with form type %q",
			params.Variable,
			params.FormType,
		)
	}
	available := make(map[string]string, len(selectedForm.Fields))
	for _, field := range selectedForm.Fields {
		available[strings.ToLower(field)] = field
	}
	var selected []string
	seen := make(map[string]struct{}, len(params.SelectedFields))
	for _, field := range params.SelectedFields {
		key := strings.ToLower(strings.TrimSpace(field))
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		canonical, exists := available[key]
		if !exists {
			return nil, fmt.Errorf(
				"form field %q is no longer available on %s",
				field,
				selectedForm.FormType,
			)
		}
		seen[key] = struct{}{}
		selected = append(selected, canonical)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one Twig form field")
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return strings.ToLower(selected[left]) <
			strings.ToLower(selected[right])
	})
	rows := make([]string, 0, len(selected))
	for _, field := range selected {
		access := params.Variable + "." + field
		if !twigFormIdentifierPattern.MatchString(field) {
			access = "attribute(" + params.Variable + ", '" +
				escapeTwigSingleQuoted(field) + "')"
		}
		rows = append(rows, "{{ form_row("+access+") }}")
	}
	return twigFormFieldGenerationResponse{
		Content: strings.Join(rows, "\n") + "\n",
	}, nil
}

func (p *TwigFormFieldGeneratorProvider) twigFormCandidates(
	fileURI string,
) ([]twigFormCandidate, error) {
	if p == nil || p.forms == nil || p.phpIndex == nil {
		return nil, fmt.Errorf("twig form field generator is unavailable")
	}
	path, err := uriutil.Path(fileURI)
	if err != nil {
		return nil, nil
	}
	variables, err := p.phpIndex.TwigTemplateVariables(
		twig.TemplateNames(path)...,
	)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]twigFormCandidate)
	for _, variable := range variables {
		if !twigFormIdentifierPattern.MatchString(variable.Name) {
			continue
		}
		for _, formType := range variable.FormTypes {
			current, found, typeErr := p.forms.GetType(formType)
			if typeErr != nil {
				return nil, typeErr
			}
			if !found {
				continue
			}
			fields, fieldErr := p.forms.EffectiveFields(current.Class)
			if fieldErr != nil {
				return nil, fieldErr
			}
			names := make([]string, 0, len(fields))
			seenFields := make(map[string]struct{}, len(fields))
			for _, field := range fields {
				key := strings.ToLower(field.Name)
				if field.Name == "" {
					continue
				}
				if _, duplicate := seenFields[key]; duplicate {
					continue
				}
				seenFields[key] = struct{}{}
				names = append(names, field.Name)
			}
			if len(names) == 0 {
				continue
			}
			sort.SliceStable(names, func(left, right int) bool {
				return strings.ToLower(names[left]) <
					strings.ToLower(names[right])
			})
			key := strings.ToLower(variable.Name) + "\x00" +
				strings.ToLower(current.Class)
			unique[key] = twigFormCandidate{
				Variable: variable.Name,
				FormType: current.Class,
				Fields:   names,
			}
		}
	}
	result := make([]twigFormCandidate, 0, len(unique))
	for _, candidate := range unique {
		result = append(result, candidate)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].Variable != result[right].Variable {
			return strings.ToLower(result[left].Variable) <
				strings.ToLower(result[right].Variable)
		}
		return strings.ToLower(result[left].FormType) <
			strings.ToLower(result[right].FormType)
	})
	return result, nil
}

func escapeTwigSingleQuoted(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

var _ lsp.ActionProvider = (*TwigFormFieldGeneratorProvider)(nil)
var _ lsp.CommandProvider = (*TwigFormFieldGeneratorProvider)(nil)
