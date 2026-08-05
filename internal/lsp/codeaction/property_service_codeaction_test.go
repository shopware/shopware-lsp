package codeaction

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPropertyServiceCodeActionCreatesPromotedConstructor(t *testing.T) {
	fixture := newPropertyServiceFixture(t)
	source := `<?php
namespace App\Service;
class Consumer
{
    public function run(): void
    {
        $this->productRepository->findAll();
    }
}`
	request := propertyServiceCodeActionRequest(
		source,
		"productRepository",
	)

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Symfony: Inject ProductRepository as $productRepository",
		actions[0].Title,
	)
	assert.Equal(t, protocol.CodeActionRefactorRewrite, actions[0].Kind)
	updated := applyPropertyServiceAction(t, request, actions[0])
	assert.Contains(
		t,
		updated,
		"use App\\Repository\\ProductRepository;",
	)
	assert.Contains(t, updated, `public function __construct(
        private readonly ProductRepository $productRepository,
    ) {
    }`)
	assert.Contains(
		t,
		updated,
		"$this->productRepository->findAll();",
	)
	require.Empty(
		t,
		lsp.NewTextDocument(request.Document.URI, updated, 2).ParseErrors,
	)
}

func TestPropertyServiceCodeActionExtendsPromotedConstructorBeforeOptional(
	t *testing.T,
) {
	fixture := newPropertyServiceFixture(t)
	source := `<?php
namespace App\Service;
use Psr\Log\LoggerInterface;
class Consumer
{
    public function __construct(
        private readonly LoggerInterface $logger,
        private ?string $name = null,
    ) {
    }

    public function run(): void
    {
        $this->productRepository->findAll();
    }
}`
	request := propertyServiceCodeActionRequest(
		source,
		"productRepository",
	)

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	updated := applyPropertyServiceAction(t, request, actions[0])
	assert.Contains(t, updated, `private readonly LoggerInterface $logger,
        private readonly ProductRepository $productRepository,
        private ?string $name = null,`)
	require.Empty(
		t,
		lsp.NewTextDocument(request.Document.URI, updated, 2).ParseErrors,
	)
}

func TestPropertyServiceCodeActionPreservesTraditionalInjectionStyle(
	t *testing.T,
) {
	fixture := newPropertyServiceFixture(t)
	source := `<?php
namespace App\Service;
class Consumer
{
    private string $name;

    public function __construct(string $name = '')
    {
        $this->name = $name;
    }

    public function run(): void
    {
        $this->productRepository->findAll();
    }
}`
	request := propertyServiceCodeActionRequest(
		source,
		"productRepository",
	)

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	updated := applyPropertyServiceAction(t, request, actions[0])
	assert.Contains(
		t,
		updated,
		"private ProductRepository $productRepository;",
	)
	assert.Contains(
		t,
		updated,
		"public function __construct(ProductRepository "+
			"$productRepository, string $name = '')",
	)
	assert.Contains(t, updated, `{
        $this->productRepository = $productRepository;
        $this->name = $name;
    }`)
	require.Empty(
		t,
		lsp.NewTextDocument(request.Document.URI, updated, 2).ParseErrors,
	)
}

func TestPropertyServiceCodeActionHandlesReadonlyClass(t *testing.T) {
	fixture := newPropertyServiceFixture(t)
	source := `<?php
namespace App\Service;
readonly class Consumer
{
    public function run(): void
    {
        $this->productRepository->findAll();
    }
}`
	request := propertyServiceCodeActionRequest(
		source,
		"productRepository",
	)

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	updated := applyPropertyServiceAction(t, request, actions[0])
	assert.Contains(
		t,
		updated,
		"private ProductRepository $productRepository,",
	)
	assert.NotContains(
		t,
		updated,
		"private readonly ProductRepository $productRepository,",
	)
}

func TestPropertyServiceCodeActionRespectsPHPLanguageLevel(t *testing.T) {
	for _, test := range []struct {
		name       string
		version    string
		expected   string
		unexpected string
	}{
		{
			name:    "PHP 7.4 traditional injection",
			version: "7.4",
			expected: "private ProductRepository $productRepository;\n\n" +
				"    public function __construct(",
			unexpected: "private readonly ProductRepository",
		},
		{
			name:       "PHP 8.0 promotion without readonly",
			version:    "8.0",
			expected:   "private ProductRepository $productRepository,",
			unexpected: "private readonly ProductRepository",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPropertyServiceFixture(t)
			projectRoot := t.TempDir()
			require.NoError(t, os.WriteFile(
				filepath.Join(projectRoot, "composer.json"),
				[]byte(`{"require":{"php":"`+test.version+`"}}`),
				0o600,
			))
			require.NoError(
				t,
				fixture.phpIndex.ConfigureProject(projectRoot),
			)
			source := `<?php
namespace App\Service;
class Consumer
{
    public function run(): void
    {
        $this->productRepository->findAll();
    }
}`
			request := propertyServiceCodeActionRequest(
				source,
				"productRepository",
			)
			actions := fixture.provider.GetCodeActions(
				context.Background(),
				request,
			)
			require.Len(t, actions, 1)
			updated := applyPropertyServiceAction(
				t,
				request,
				actions[0],
			)
			assert.Contains(t, updated, test.expected)
			assert.NotContains(t, updated, test.unexpected)
			require.Empty(
				t,
				lsp.NewTextDocument(
					request.Document.URI,
					updated,
					2,
				).ParseErrors,
			)
		})
	}
}

func TestPropertyServiceCodeActionUsesAliasAndCalledMethod(t *testing.T) {
	fixture := newPropertyServiceFixture(t)
	source := `<?php
namespace App\Service;
class Consumer
{
    public function run(): string
    {
        return $this->router->generate('home');
    }
}`
	request := propertyServiceCodeActionRequest(source, "router")

	actions := fixture.provider.GetCodeActions(
		context.Background(),
		request,
	)

	require.Len(t, actions, 1)
	assert.Equal(
		t,
		"Symfony: Inject UrlGeneratorInterface as $router",
		actions[0].Title,
	)
	updated := applyPropertyServiceAction(t, request, actions[0])
	assert.Contains(
		t,
		updated,
		"use Symfony\\Component\\Routing\\Generator\\"+
			"UrlGeneratorInterface;",
	)
	assert.Contains(
		t,
		updated,
		"private readonly UrlGeneratorInterface $router,",
	)
}

func TestPropertyServiceCodeActionRejectsDeclaredInheritedAndNonServiceProperties(
	t *testing.T,
) {
	fixture := newPropertyServiceFixture(t)
	for _, test := range []struct {
		name        string
		className   string
		declaration string
		expression  string
		needle      string
	}{
		{
			name:      "declared property",
			className: "Consumer",
			declaration: "private \\App\\Repository\\ProductRepository " +
				"$productRepository;",
			expression: "$this->productRepository->findAll();",
			needle:     "productRepository->",
		},
		{
			name:       "inherited property",
			className:  "Consumer extends BaseConsumer",
			expression: "$this->productRepository->findAll();",
			needle:     "productRepository",
		},
		{
			name:       "different receiver",
			className:  "Consumer",
			expression: "$other->productRepository->findAll();",
			needle:     "productRepository",
		},
		{
			name:       "short property",
			className:  "Consumer",
			expression: "$this->em->flush();",
			needle:     "em",
		},
		{
			name:       "class is not a service",
			className:  "NotAService",
			expression: "$this->productRepository->findAll();",
			needle:     "productRepository",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := `<?php
namespace App\Service;
class ` + test.className + `
{
    ` + test.declaration + `
    public function run(): void
    {
        ` + test.expression + `
    }
}`
			request := propertyServiceCodeActionRequest(
				source,
				test.needle,
			)
			assert.Empty(t, fixture.provider.GetCodeActions(
				context.Background(),
				request,
			))
		})
	}
}

func TestPropertyServiceCandidateNormalizationAndRanking(t *testing.T) {
	fixture := newPropertyServiceFixture(t)

	candidates := propertyServiceCandidates(
		fixture.phpIndex,
		"fooBarLogger",
		"info",
	)
	require.NotEmpty(t, candidates)
	assert.Equal(t, "Psr\\Log\\LoggerInterface", candidates[0].className)

	candidates = propertyServiceCandidates(
		fixture.phpIndex,
		"longClassNameServiceFactory",
		"",
	)
	require.NotEmpty(t, candidates)
	assert.Equal(
		t,
		"App\\Service\\FoobarLongClassNameServiceFactory",
		candidates[0].className,
	)
	assert.Empty(t, propertyServiceCandidates(
		fixture.phpIndex,
		"serviceFactory",
		"",
	))
}

type propertyServiceFixture struct {
	provider     *PropertyServiceCodeActionProvider
	phpIndex     *php.PHPIndex
	serviceIndex *symfony.ServiceIndex
}

func newPropertyServiceFixture(t *testing.T) propertyServiceFixture {
	t.Helper()
	root := t.TempDir()
	phpIndex, err := php.NewPHPIndex(t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, phpIndex.Close()) })
	for path, source := range map[string]string{
		"/project/src/Repository/ProductRepository.php": `<?php
namespace App\Repository;
class ProductRepository
{
    public function findAll(): array { return []; }
}
`,
		"/project/src/Service/BaseConsumer.php": `<?php
namespace App\Service;
class BaseConsumer
{
    protected \App\Repository\ProductRepository $productRepository;
}
`,
		"/project/src/Service/Factory.php": `<?php
namespace App\Service;
class FoobarLongClassNameServiceFactory {}
`,
		"/vendor/Psr/Log/LoggerInterface.php": `<?php
namespace Psr\Log;
interface LoggerInterface
{
    public function info(string $message): void;
}
`,
		"/vendor/Symfony/UrlGeneratorInterface.php": `<?php
namespace Symfony\Component\Routing\Generator;
interface UrlGeneratorInterface
{
    public function generate(string $name): string;
}
`,
	} {
		require.NoError(t, phpIndex.Index(indexer.NewParsedFile(
			path,
			[]byte(source),
		)))
	}
	serviceIndex, err := symfony.NewServiceIndex(
		root,
		t.TempDir(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serviceIndex.Close()) })
	require.NoError(t, serviceIndex.Index(indexer.NewParsedFile(
		root+"/config/services.yaml",
		[]byte(`services:
  app.consumer:
    class: App\Service\Consumer
`),
	)))
	return propertyServiceFixture{
		provider: NewPropertyServiceCodeActionProvider(
			phpIndex,
			serviceIndex,
		),
		phpIndex:     phpIndex,
		serviceIndex: serviceIndex,
	}
}

func propertyServiceCodeActionRequest(
	source,
	needle string,
) *lsp.CodeActionRequest {
	document := lsp.NewTextDocument(
		"file:///project/src/Service/Consumer.php",
		source,
		1,
	)
	offset := strings.LastIndex(source, needle)
	params := &protocol.CodeActionParams{}
	params.TextDocument.URI = document.URI
	if offset >= 0 {
		line, character := document.LineIndex.PositionUTF16(
			uint32(offset),
		)
		params.Range = protocol.Range{
			Start: protocol.Position{
				Line:      int(line),
				Character: int(character),
			},
			End: protocol.Position{
				Line:      int(line),
				Character: int(character) + len(needle),
			},
		}
	}
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
				uint32(max(offset, 0)),
			),
		},
	}
}

func applyPropertyServiceAction(
	t *testing.T,
	request *lsp.CodeActionRequest,
	action protocol.CodeAction,
) string {
	t.Helper()
	require.NotNil(t, action.Edit)
	edits := append(
		[]protocol.TextEdit(nil),
		action.Edit.Changes[request.TextDocument.URI]...,
	)
	type offsetEdit struct {
		start   uint32
		end     uint32
		newText string
	}
	offsets := make([]offsetEdit, 0, len(edits))
	for _, edit := range edits {
		offsets = append(offsets, offsetEdit{
			start: request.Document.LineIndex.OffsetUTF16(
				uint32(edit.Range.Start.Line),
				uint32(edit.Range.Start.Character),
			),
			end: request.Document.LineIndex.OffsetUTF16(
				uint32(edit.Range.End.Line),
				uint32(edit.Range.End.Character),
			),
			newText: edit.NewText,
		})
	}
	sort.Slice(offsets, func(left, right int) bool {
		if offsets[left].start != offsets[right].start {
			return offsets[left].start > offsets[right].start
		}
		return offsets[left].end > offsets[right].end
	})
	updated := request.Document.Source
	for _, edit := range offsets {
		require.LessOrEqual(t, edit.start, edit.end)
		require.LessOrEqual(t, int(edit.end), len(updated))
		updated = updated[:edit.start] + edit.newText + updated[edit.end:]
	}
	return updated
}
