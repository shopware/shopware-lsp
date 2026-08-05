package completion

import (
	"context"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTranslationCompletionUsesTwigDefaultAndExplicitDomains(t *testing.T) {
	idx := translationCompletionFixture(t)
	provider := NewTranslationCompletionProvider(idx, nil)

	tests := []struct {
		source string
		needle string
		label  string
	}{
		{
			`{% trans_default_domain 'admin' %}{{ ''|trans }}`,
			"''",
			"admin.dashboard",
		},
		{
			`{{ ''|trans({}, 'admin') }}`,
			"''",
			"admin.dashboard",
		},
		{
			`{{ ''|transchoice(2, {}, 'messages') }}`,
			"''",
			"hello.world",
		},
	}
	for _, test := range tests {
		document := lsp.NewTextDocument(
			"file:///project/template.twig",
			test.source,
			1,
		)
		offset := uint32(strings.Index(test.source, test.needle) + 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		items := provider.GetCompletions(
			context.Background(),
			translationCompletionRequest(document, node),
		)
		item := requireCompletion(t, items, test.label)
		assert.Equal(t, int(protocol.ValueCompletion), item.Kind)
	}
}

func TestTranslationDomainCompletionInTwigDefaultDomainTag(t *testing.T) {
	idx := translationCompletionFixture(t)
	provider := NewTranslationCompletionProvider(idx, nil)
	source := `{% trans_default_domain '' %}`
	document := lsp.NewTextDocument(
		"file:///project/template.twig",
		source,
		1,
	)
	node := document.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.Index(source, "''") + 1),
	)
	items := provider.GetCompletions(
		context.Background(),
		translationCompletionRequest(document, node),
	)
	item := requireCompletion(t, items, "admin")
	assert.Equal(t, int(protocol.ModuleCompletion), item.Kind)
	assert.Equal(t, "1 translation key", item.Detail)
}

func TestPHPTranslationCompletionRequiresTranslatorType(t *testing.T) {
	idx := translationCompletionFixture(t)
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/TranslatorInterface.php",
		[]byte(`<?php
namespace Symfony\Contracts\Translation;
interface TranslatorInterface {
    public function trans(string $id, array $parameters = [], ?string $domain = null): string;
}
`),
	)))

	source := `<?php
namespace App;
use Symfony\Contracts\Translation\TranslatorInterface;
function translate(TranslatorInterface $translator): string {
    return $translator->trans('');
}
`
	document := lsp.NewTextDocument(
		"file:///project/Controller.php",
		source,
		1,
	)
	node := document.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.LastIndex(source, "''") + 1),
	)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		"/project/Controller.php",
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := NewTranslationCompletionProvider(
		idx,
		phpIndex,
	).GetCompletions(
		ctx,
		translationCompletionRequest(document, node),
	)
	requireCompletion(t, items, "hello.world")

	untypedSource := `<?php $logger->trans('');`
	untypedDocument := lsp.NewTextDocument(
		"file:///project/Logger.php",
		untypedSource,
		1,
	)
	untypedNode := untypedDocument.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.Index(untypedSource, "''") + 1),
	)
	untypedContext := phpIndex.AddDocumentContext(
		context.Background(),
		"/project/Logger.php",
		1,
		untypedNode,
		untypedDocument.SyntaxTree.Root,
	)
	assert.Empty(t, NewTranslationCompletionProvider(
		idx,
		phpIndex,
	).GetCompletions(
		untypedContext,
		translationCompletionRequest(untypedDocument, untypedNode),
	))
}

func TestValidatorMessageCompletionUsesValidatorsDomain(t *testing.T) {
	idx := translationCompletionFixture(t)
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/Constraint.php",
		[]byte(`<?php
namespace Symfony\Component\Validator;
class Constraint {}
`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/NotBlank.php",
		[]byte(`<?php
namespace Symfony\Component\Validator\Constraints;
class NotBlank extends \Symfony\Component\Validator\Constraint {
    public string $message;
}
`),
	)))
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/vendor/ValidatorContext.php",
		[]byte(`<?php
namespace Symfony\Component\Validator\Context;
interface ExecutionContextInterface {
    public function addViolation(string $message): void;
    public function buildViolation(string $message): object;
}
namespace Symfony\Component\Validator\Violation;
interface ConstraintViolationBuilderInterface {
    public function setTranslationDomain(string $domain): self;
}
`),
	)))

	for _, source := range []string{
		`<?php
use Symfony\Component\Validator\Constraints\NotBlank;
#[NotBlank(message: '')]
function validate(): void {}
`,
		`<?php
use Symfony\Component\Validator\Constraints\NotBlank;
new NotBlank(['message' => '']);
`,
		`<?php
class UniqueName extends \Symfony\Component\Validator\Constraint {
    public string $message = '';
}
`,
		`<?php
use Symfony\Component\Validator\Context\ExecutionContextInterface;
function validate(ExecutionContextInterface $context): void {
    $context->buildViolation('');
}
`,
	} {
		document := lsp.NewTextDocument(
			"file:///project/Validation.php",
			source,
			1,
		)
		offset := uint32(strings.LastIndex(source, "''") + 1)
		node := document.SyntaxTree.Root.NodeAtOffset(offset)
		ctx := phpIndex.AddDocumentContext(
			context.Background(),
			"/project/Validation.php",
			1,
			node,
			document.SyntaxTree.Root,
		)
		items := NewTranslationCompletionProvider(
			idx,
			phpIndex,
		).GetCompletions(
			ctx,
			translationCompletionRequest(document, node),
		)
		requireCompletion(t, items, "validator.message")
	}

	domainSource := `<?php
use Symfony\Component\Validator\Violation\ConstraintViolationBuilderInterface;
function configure(ConstraintViolationBuilderInterface $builder): void {
    $builder->setTranslationDomain('');
}
`
	document := lsp.NewTextDocument(
		"file:///project/Violation.php",
		domainSource,
		1,
	)
	offset := uint32(strings.LastIndex(domainSource, "''") + 1)
	node := document.SyntaxTree.Root.NodeAtOffset(offset)
	ctx := phpIndex.AddDocumentContext(
		context.Background(),
		"/project/Violation.php",
		1,
		node,
		document.SyntaxTree.Root,
	)
	items := NewTranslationCompletionProvider(idx, phpIndex).GetCompletions(
		ctx,
		translationCompletionRequest(document, node),
	)
	requireCompletion(t, items, "validators")
	requireCompletion(t, items, "admin")
}

func TestPHPDocTranslationAssistantTagCompletion(t *testing.T) {
	idx := translationCompletionFixture(t)
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
		"/project/TranslationAssistant.php",
		[]byte(`<?php
/**
 * @param string $key #TranslationKey
 * @param string $domain #TranslationDomain
 */
function resolve_translation(string $key, string $domain): void {}

/** @param string $domain #TranslationDomain */
function resolve_domain(string $domain): void {}

/** @param string $key #TranslationKey */
function resolve_default_translation(string $key): void {}
`),
	)))
	provider := NewTranslationCompletionProvider(idx, phpIndex)
	for _, fixture := range []struct {
		name     string
		source   string
		partial  string
		expected string
	}{
		{
			name: "key with named sibling domain",
			source: `<?php resolve_translation(
    domain: 'admin',
    key: 'admin.',
);`,
			partial:  "admin.",
			expected: "admin.dashboard",
		},
		{
			name:     "domain",
			source:   "<?php resolve_domain('adm');",
			partial:  "adm",
			expected: "admin",
		},
		{
			name:     "default messages domain",
			source:   "<?php resolve_default_translation('hello.');",
			partial:  "hello.",
			expected: "hello.world",
		},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			document := lsp.NewTextDocument(
				"file:///project/Usage.php",
				fixture.source,
				1,
			)
			offset := uint32(
				strings.LastIndex(fixture.source, fixture.partial) +
					len(fixture.partial),
			)
			node := document.SyntaxTree.Root.NodeAtOffset(offset)
			ctx := phpIndex.AddDocumentContext(
				context.Background(),
				"/project/Usage.php",
				document.Version,
				node,
				document.SyntaxTree.Root,
			)
			item := requireCompletion(
				t,
				provider.GetCompletions(
					ctx,
					translationCompletionRequest(document, node),
				),
				fixture.expected,
			)
			edit, ok := item.TextEdit.(protocol.TextEdit)
			require.True(t, ok)
			assert.Equal(t, fixture.expected, edit.NewText)
			assert.Equal(
				t,
				fixture.partial,
				completionRangeText(document, edit.Range),
			)
		})
	}
}

func TestTranslationPlaceholderCompletion(t *testing.T) {
	idx := translationCompletionFixture(t)
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/placeholders.en.yaml",
		[]byte("greeting: 'Hello %name%, {count, plural, one {item} other {items}}'\n"),
	)))
	provider := NewTranslationCompletionProvider(idx, nil)
	source := `{{ 'greeting'|trans({'': name}, 'placeholders') }}`
	document := lsp.NewTextDocument(
		"file:///project/template.twig",
		source,
		1,
	)
	node := document.SyntaxTree.Root.NodeAtOffset(
		uint32(strings.Index(source, "''") + 1),
	)
	items := provider.GetCompletions(
		context.Background(),
		translationCompletionRequest(document, node),
	)
	requireCompletion(t, items, "%name%")
	requireCompletion(t, items, "count")
}

func translationCompletionFixture(t *testing.T) *translation.Index {
	t.Helper()
	idx, err := translation.NewIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, idx.Close()) })
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/messages.en.yaml",
		[]byte("hello.world: Hello world\n"),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/admin.en.yaml",
		[]byte("admin.dashboard: Dashboard\n"),
	)))
	require.NoError(t, idx.Index(indexer.NewParsedFile(
		"/project/translations/validators.en.yaml",
		[]byte("validator.message: Invalid value\n"),
	)))
	return idx
}

func translationCompletionRequest(
	document *lsp.TextDocument,
	node *cst.Node,
) *lsp.CompletionRequest {
	params := &protocol.CompletionParams{}
	params.TextDocument.URI = document.URI
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
