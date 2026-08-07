package codeaction

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/language"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/uriutil"
)

const (
	generateTwigExtendsAction = "shopware.symfony.generateTwigExtends"
	generateTwigBlocksAction  = "shopware.symfony.generateTwigBlocks"

	twigExtendsCandidatesCommand = "shopware/symfony/twig/extends/candidates"
	generateTwigExtendsCommand   = "shopware/symfony/twig/extends/generate"
	twigBlockCandidatesCommand   = "shopware/symfony/twig/blocks/candidates"
	generateTwigBlocksCommand    = "shopware/symfony/twig/blocks/generate"
)

// TwigTemplateGeneratorProvider ports the reference plugin's Twig Extends and
// Block Overwrite generators using the persistent template and inheritance
// indexes. Editor prompts remain thin; candidate validation stays server-side.
type TwigTemplateGeneratorProvider struct {
	twigIndex *twig.TwigIndexer
}

func NewTwigTemplateGeneratorProvider(
	twigIndex *twig.TwigIndexer,
) *TwigTemplateGeneratorProvider {
	return &TwigTemplateGeneratorProvider{twigIndex: twigIndex}
}

func (p *TwigTemplateGeneratorProvider) GetCodeActionKinds() []protocol.CodeActionKind {
	return []protocol.CodeActionKind{protocol.CodeActionRefactorRewrite}
}

func (p *TwigTemplateGeneratorProvider) GetCodeActions(
	ctx context.Context,
	request *lsp.CodeActionRequest,
) []protocol.CodeAction {
	if ctx.Err() != nil || p == nil || p.twigIndex == nil ||
		request == nil || request.CodeActionParams == nil ||
		request.Document == nil ||
		request.Document.SyntaxLanguage != language.Twig {
		return nil
	}
	path := twigGeneratorPath(request.Document.URI)
	if isAdministrationTwigPath(path) {
		return nil
	}
	var current *twig.TwigFile
	var err error
	if request.Document.SyntaxTree != nil && request.Document.LineIndex != nil {
		current, err = twig.ParseTwigTree(
			path,
			request.Document.SyntaxTree,
			request.Document.LineIndex,
		)
	} else {
		current, err = twig.ParseTwig(path, request.Document.Text)
	}
	if err != nil || current == nil {
		return nil
	}
	var result []protocol.CodeAction
	if current.ExtendsFile == "" {
		hasCandidate, candidateErr := p.twigIndex.HasOtherTemplateFile(path)
		if candidateErr == nil && hasCandidate {
			result = append(result, protocol.CodeAction{
				Title: "Symfony: Add Twig extends",
				Kind:  protocol.CodeActionRefactorRewrite,
				Command: &protocol.CommandAction{
					Title:     "Symfony: Add Twig extends",
					Command:   generateTwigExtendsAction,
					Arguments: []any{request.TextDocument.URI},
				},
			})
		}
		return result
	}
	blocks, blockErr := p.twigBlockCandidates(current)
	if blockErr == nil && len(blocks) != 0 {
		result = append(result, protocol.CodeAction{
			Title: "Symfony: Override Twig blocks",
			Kind:  protocol.CodeActionRefactorRewrite,
			Command: &protocol.CommandAction{
				Title:     "Symfony: Override Twig blocks",
				Command:   generateTwigBlocksAction,
				Arguments: []any{request.TextDocument.URI},
			},
		})
	}
	return result
}

func (p *TwigTemplateGeneratorProvider) GetCommands(
	_ context.Context,
) map[string]lsp.CommandFunc {
	return map[string]lsp.CommandFunc{
		twigExtendsCandidatesCommand: p.getTwigExtendsCandidates,
		generateTwigExtendsCommand:   p.generateTwigExtends,
		twigBlockCandidatesCommand:   p.getTwigBlockCandidates,
		generateTwigBlocksCommand:    p.generateTwigBlocks,
	}
}

type twigTemplateGeneratorRequest struct {
	FileURI        string   `json:"fileUri"`
	Source         string   `json:"source,omitempty"`
	Template       string   `json:"template,omitempty"`
	SelectedBlocks []string `json:"selectedBlocks,omitempty"`
}

type twigTemplateCandidatesResponse struct {
	Templates []string `json:"templates"`
}

type twigBlockCandidatesResponse struct {
	Parent string   `json:"parent"`
	Blocks []string `json:"blocks"`
}

type twigTemplateGenerationResponse struct {
	Content string `json:"content"`
}

func (p *TwigTemplateGeneratorProvider) getTwigExtendsCandidates(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	params, err := decodeTwigTemplateGeneratorRequest(ctx, raw)
	if err != nil {
		return nil, err
	}
	templates, err := p.twigExtendsCandidates(
		twigGeneratorPath(params.FileURI),
	)
	if err != nil {
		return nil, err
	}
	return twigTemplateCandidatesResponse{Templates: templates}, nil
}

func (p *TwigTemplateGeneratorProvider) generateTwigExtends(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	params, err := decodeTwigTemplateGeneratorRequest(ctx, raw)
	if err != nil {
		return nil, err
	}
	path := twigGeneratorPath(params.FileURI)
	current, err := twig.ParseTwig(path, []byte(params.Source))
	if err != nil {
		return nil, fmt.Errorf("parse current Twig template: %w", err)
	}
	if current.ExtendsFile != "" {
		return nil, fmt.Errorf(
			"template already extends %q",
			current.ExtendsFile,
		)
	}
	templates, err := p.twigExtendsCandidates(path)
	if err != nil {
		return nil, err
	}
	selected := strings.TrimPrefix(
		strings.TrimSpace(params.Template),
		"/",
	)
	if !containsExactString(templates, selected) {
		return nil, fmt.Errorf(
			"twig template %q is no longer available",
			params.Template,
		)
	}
	return twigTemplateGenerationResponse{
		Content: "{% extends '" + escapeTwigSingleQuoted(selected) + "' %}\n",
	}, nil
}

func (p *TwigTemplateGeneratorProvider) getTwigBlockCandidates(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	params, err := decodeTwigTemplateGeneratorRequest(ctx, raw)
	if err != nil {
		return nil, err
	}
	current, err := twig.ParseTwig(
		twigGeneratorPath(params.FileURI),
		[]byte(params.Source),
	)
	if err != nil {
		return nil, fmt.Errorf("parse current Twig template: %w", err)
	}
	blocks, err := p.twigBlockCandidates(current)
	if err != nil {
		return nil, err
	}
	return twigBlockCandidatesResponse{
		Parent: current.ExtendsFile,
		Blocks: blocks,
	}, nil
}

func (p *TwigTemplateGeneratorProvider) generateTwigBlocks(
	ctx context.Context,
	raw *json.RawMessage,
) (interface{}, error) {
	params, err := decodeTwigTemplateGeneratorRequest(ctx, raw)
	if err != nil {
		return nil, err
	}
	current, err := twig.ParseTwig(
		twigGeneratorPath(params.FileURI),
		[]byte(params.Source),
	)
	if err != nil {
		return nil, fmt.Errorf("parse current Twig template: %w", err)
	}
	available, err := p.twigBlockCandidates(current)
	if err != nil {
		return nil, err
	}
	availableSet := make(map[string]string, len(available))
	for _, name := range available {
		availableSet[strings.ToLower(name)] = name
	}
	seen := make(map[string]struct{}, len(params.SelectedBlocks))
	var selected []string
	for _, name := range params.SelectedBlocks {
		key := strings.ToLower(strings.TrimSpace(name))
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		canonical, exists := availableSet[key]
		if !exists {
			return nil, fmt.Errorf(
				"twig block %q is no longer available from %s",
				name,
				current.ExtendsFile,
			)
		}
		seen[key] = struct{}{}
		selected = append(selected, canonical)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one Twig block")
	}
	sort.SliceStable(selected, func(left, right int) bool {
		return strings.ToLower(selected[left]) <
			strings.ToLower(selected[right])
	})
	blocks := make([]string, 0, len(selected))
	for index, name := range selected {
		body := ""
		if index == 0 {
			body = "$0"
		}
		blocks = append(
			blocks,
			"{% block "+name+" %}\n    "+body+"\n{% endblock %}",
		)
	}
	return twigTemplateGenerationResponse{
		Content: strings.Join(blocks, "\n\n") + "\n",
	}, nil
}

func (p *TwigTemplateGeneratorProvider) twigExtendsCandidates(
	currentPath string,
) ([]string, error) {
	if p == nil || p.twigIndex == nil {
		return nil, fmt.Errorf("twig template generator is unavailable")
	}
	templates, err := p.twigIndex.GetAllTemplateFiles()
	if err != nil {
		return nil, err
	}
	currentNames := make(map[string]struct{})
	for _, name := range twig.TemplateNames(currentPath) {
		currentNames[strings.ToLower(name)] = struct{}{}
	}
	unique := make(map[string]string, len(templates))
	for _, template := range templates {
		template = strings.TrimPrefix(strings.TrimSpace(template), "/")
		if template == "" {
			continue
		}
		key := strings.ToLower(template)
		if _, current := currentNames[key]; current {
			continue
		}
		unique[key] = template
	}
	result := make([]string, 0, len(unique))
	for _, template := range unique {
		result = append(result, template)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func (p *TwigTemplateGeneratorProvider) twigBlockCandidates(
	current *twig.TwigFile,
) ([]string, error) {
	if p == nil || p.twigIndex == nil {
		return nil, fmt.Errorf("twig template generator is unavailable")
	}
	if current == nil || current.ExtendsFile == "" {
		return nil, nil
	}
	blocks, err := p.twigIndex.GetTemplateBlocks(current.ExtendsFile)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]string, len(blocks))
	for _, block := range blocks {
		key := strings.ToLower(block.Name)
		if _, overridden := current.Blocks[block.Name]; overridden {
			continue
		}
		overriddenFold := false
		for name := range current.Blocks {
			if strings.EqualFold(name, block.Name) {
				overriddenFold = true
				break
			}
		}
		if overriddenFold {
			continue
		}
		if _, exists := unique[key]; !exists {
			unique[key] = block.Name
		}
	}
	result := make([]string, 0, len(unique))
	for _, name := range unique {
		result = append(result, name)
	}
	sort.SliceStable(result, func(left, right int) bool {
		return strings.ToLower(result[left]) <
			strings.ToLower(result[right])
	})
	return result, nil
}

func decodeTwigTemplateGeneratorRequest(
	ctx context.Context,
	raw *json.RawMessage,
) (twigTemplateGeneratorRequest, error) {
	var params twigTemplateGeneratorRequest
	if err := decodeSymfonyGeneratorRequest(raw, &params); err != nil {
		return params, err
	}
	if err := ctx.Err(); err != nil {
		return params, err
	}
	if strings.TrimSpace(params.FileURI) == "" {
		return params, fmt.Errorf("missing Twig file URI")
	}
	return params, nil
}

func twigGeneratorPath(uri string) string {
	if path, err := uriutil.Path(uri); err == nil {
		return path
	}
	return uri
}

func containsExactString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ lsp.ActionProvider = (*TwigTemplateGeneratorProvider)(nil)
var _ lsp.CommandProvider = (*TwigTemplateGeneratorProvider)(nil)
