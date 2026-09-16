package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	"github.com/shopware/shopware-lsp/internal/uriutil"
	"github.com/sourcegraph/jsonrpc2"
)

var benchmarkCompletionItems []protocol.CompletionItem
var benchmarkHoverResult *protocol.Hover
var benchmarkLocations []protocol.Location
var benchmarkWorkspaceSymbols []protocol.SymbolInformation
var benchmarkDiagnostics protocol.DiagnosticResult

// BenchmarkSyntheticWorkspaceIndexing measures the production indexing
// pipeline — discovery, parsing, indexer preparation, and SQLite commits —
// against a deterministic multi-domain project. "cold" pays for workspace
// construction and a fresh cache per iteration, "warm_rescan" is the no-op
// editor restart, and "single_file_change" is the incremental save path.
func BenchmarkSyntheticWorkspaceIndexing(b *testing.B) {
	ctx := context.Background()
	root, fileCount := writeSyntheticBenchmarkProject(b)

	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		b.ReportMetric(float64(fileCount), "files/op")
		for b.Loop() {
			b.Setenv("SHOPWARE_LSP_CACHE_DIR", b.TempDir())
			server := lsp.NewServer(nil, root, "benchmark")
			workspace, err := NewWorkspace(ctx, root, server)
			if err != nil {
				b.Fatal(err)
			}
			if err := workspace.Scanner().IndexAll(ctx); err != nil {
				b.Fatal(err)
			}
			if err := workspace.Close(); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("warm_rescan", func(b *testing.B) {
		b.Setenv("SHOPWARE_LSP_CACHE_DIR", b.TempDir())
		server := lsp.NewServer(nil, root, "benchmark")
		workspace, err := NewWorkspace(ctx, root, server)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() {
			if err := workspace.Close(); err != nil {
				b.Error(err)
			}
		})
		if err := workspace.Scanner().IndexAll(ctx); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ReportMetric(float64(fileCount), "files/op")
		for b.Loop() {
			if err := workspace.Scanner().IndexAll(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("single_file_change", func(b *testing.B) {
		b.Setenv("SHOPWARE_LSP_CACHE_DIR", b.TempDir())
		server := lsp.NewServer(nil, root, "benchmark")
		workspace, err := NewWorkspace(ctx, root, server)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() {
			if err := workspace.Close(); err != nil {
				b.Error(err)
			}
		})
		if err := workspace.Scanner().IndexAll(ctx); err != nil {
			b.Fatal(err)
		}
		target := filepath.Join(root, "src", "Service", "ProductService00.php")
		content, err := os.ReadFile(target)
		if err != nil {
			b.Fatal(err)
		}
		original := string(content)
		variant := original + "\n// benchmark variant\n"
		b.Cleanup(func() {
			if err := os.WriteFile(target, content, 0o644); err != nil {
				b.Error(err)
			}
		})
		b.ReportAllocs()
		iteration := 0
		for b.Loop() {
			current := original
			if iteration%2 == 1 {
				current = variant
			}
			iteration++
			if err := os.WriteFile(target, []byte(current), 0o644); err != nil {
				b.Fatal(err)
			}
			if err := workspace.Scanner().IndexFiles(ctx, []string{target}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkSyntheticWorkspaceRequests measures user-facing request latency
// end to end: JSON-RPC transport, dispatch, provider fan-out, and protocol
// conversion against an indexed workspace. Every sub-benchmark asserts a
// non-empty result before the timed loop so a silently broken provider
// cannot masquerade as a fast one.
func BenchmarkSyntheticWorkspaceRequests(b *testing.B) {
	ctx := context.Background()
	root, _ := writeSyntheticBenchmarkProject(b)
	b.Setenv("SHOPWARE_LSP_CACHE_DIR", b.TempDir())

	server := lsp.NewServer(nil, root, "benchmark")
	server.SetWorkspaceFactory(func(
		ctx context.Context,
		root string,
		current *lsp.Server,
	) (lsp.WorkspaceRuntime, error) {
		return NewWorkspace(ctx, root, current)
	})

	serverSide, clientSide := net.Pipe()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- server.Start(serverSide, serverSide)
	}()

	indexingDone := make(chan struct{}, 1)
	published := make(chan string, 16)
	client := jsonrpc2.NewConn(
		ctx,
		jsonrpc2.NewBufferedStream(clientSide, jsonrpc2.VSCodeObjectCodec{}),
		jsonrpc2.HandlerWithError(func(
			_ context.Context,
			_ *jsonrpc2.Conn,
			request *jsonrpc2.Request,
		) (interface{}, error) {
			switch request.Method {
			case "shopware/indexingCompleted":
				select {
				case indexingDone <- struct{}{}:
				default:
				}
			case "textDocument/publishDiagnostics":
				if request.Params == nil {
					return nil, nil
				}
				var params struct {
					URI string `json:"uri"`
				}
				if err := json.Unmarshal(*request.Params, &params); err == nil {
					select {
					case published <- params.URI:
					default:
					}
				}
			}
			return nil, nil
		}).SuppressErrClosed(),
	)
	b.Cleanup(func() {
		_ = client.Close()
		_ = clientSide.Close()
		_ = serverSide.Close()
		select {
		case err := <-serverDone:
			if err != nil {
				b.Error(err)
			}
		case <-time.After(5 * time.Second):
			b.Error("benchmark server did not stop")
		}
		if err := server.CloseAll(); err != nil {
			b.Error(err)
		}
	})

	var initializeResult map[string]any
	if err := client.Call(ctx, "initialize", map[string]any{
		"rootUri":      uriutil.FileURI(root),
		"capabilities": map[string]any{},
	}, &initializeResult); err != nil {
		b.Fatal(err)
	}
	if err := client.Notify(ctx, "initialized", struct{}{}); err != nil {
		b.Fatal(err)
	}
	select {
	case <-indexingDone:
	case <-time.After(60 * time.Second):
		b.Fatal("synthetic workspace indexing did not finish")
	}

	controller := openBenchmarkDocument(
		b,
		client,
		filepath.Join(root, "src", "Controller", "CheckoutController.php"),
	)
	template := openBenchmarkDocument(
		b,
		client,
		filepath.Join(root, "templates", "checkout", "summary.html.twig"),
	)
	waitForBenchmarkDiagnostics(b, published, controller.URI, template.URI)

	phpCompletion := &protocol.CompletionParams{}
	phpCompletion.TextDocument.URI = controller.URI
	phpCompletion.Position = benchmarkPosition(
		b, controller, "$this->basket->\n", len("$this->basket->"),
	)
	twigCompletion := &protocol.CompletionParams{}
	twigCompletion.TextDocument.URI = template.URI
	twigCompletion.Position = benchmarkPosition(
		b, template, "{{ product.na }}", len("{{ product."),
	)
	phpHover := &protocol.HoverParams{}
	phpHover.TextDocument.URI = controller.URI
	phpHover.Position = benchmarkPosition(
		b, controller, "private BasketService $basket", len("private Basket"),
	)
	phpDefinition := &protocol.DefinitionParams{}
	phpDefinition.TextDocument.URI = controller.URI
	phpDefinition.Position = phpHover.Position
	twigDiagnostics := &protocol.DiagnosticParams{}
	twigDiagnostics.TextDocument.URI = template.URI
	symbolQuery := &protocol.WorkspaceSymbolParams{Query: "ProductService"}

	var completions protocol.CompletionList
	if err := client.Call(ctx, "textDocument/completion", phpCompletion, &completions); err != nil {
		b.Fatal(err)
	}
	if len(completions.Items) == 0 {
		b.Fatal("PHP member completion returned no items")
	}
	var twigCompletions protocol.CompletionList
	if err := client.Call(ctx, "textDocument/completion", twigCompletion, &twigCompletions); err != nil {
		b.Fatal(err)
	}
	if len(twigCompletions.Items) == 0 {
		b.Fatal("Twig member completion returned no items")
	}
	var hover *protocol.Hover
	if err := client.Call(ctx, "textDocument/hover", phpHover, &hover); err != nil {
		b.Fatal(err)
	}
	if hover == nil {
		b.Fatal("PHP class hover returned no result")
	}
	var definitions []protocol.Location
	if err := client.Call(ctx, "textDocument/definition", phpDefinition, &definitions); err != nil {
		b.Fatal(err)
	}
	if len(definitions) == 0 {
		b.Fatal("PHP class definition returned no locations")
	}
	var symbols []protocol.SymbolInformation
	if err := client.Call(ctx, "workspace/symbol", symbolQuery, &symbols); err != nil {
		b.Fatal(err)
	}
	if len(symbols) == 0 {
		b.Fatal("workspace symbol search returned no results")
	}

	b.Run("completion/php_member", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var result protocol.CompletionList
			if err := client.Call(ctx, "textDocument/completion", phpCompletion, &result); err != nil {
				b.Fatal(err)
			}
			benchmarkCompletionItems = result.Items
		}
	})
	b.Run("completion/twig_member", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var result protocol.CompletionList
			if err := client.Call(ctx, "textDocument/completion", twigCompletion, &result); err != nil {
				b.Fatal(err)
			}
			benchmarkCompletionItems = result.Items
		}
	})
	b.Run("hover/php_class", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var result *protocol.Hover
			if err := client.Call(ctx, "textDocument/hover", phpHover, &result); err != nil {
				b.Fatal(err)
			}
			benchmarkHoverResult = result
		}
	})
	b.Run("definition/php_class", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var result []protocol.Location
			if err := client.Call(ctx, "textDocument/definition", phpDefinition, &result); err != nil {
				b.Fatal(err)
			}
			benchmarkLocations = result
		}
	})
	b.Run("diagnostic/twig", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var result protocol.DiagnosticResult
			if err := client.Call(ctx, "textDocument/diagnostic", twigDiagnostics, &result); err != nil {
				b.Fatal(err)
			}
			benchmarkDiagnostics = result
		}
	})
	b.Run("workspace_symbol", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			var result []protocol.SymbolInformation
			if err := client.Call(ctx, "workspace/symbol", symbolQuery, &result); err != nil {
				b.Fatal(err)
			}
			benchmarkWorkspaceSymbols = result
		}
	})
}

func openBenchmarkDocument(
	b *testing.B,
	client *jsonrpc2.Conn,
	path string,
) *lsp.TextDocument {
	b.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	document := lsp.NewTextDocument(uriutil.FileURI(path), string(source), 1)
	if err := client.Notify(context.Background(), "textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":     document.URI,
			"version": document.Version,
			"text":    document.Source,
		},
	}); err != nil {
		b.Fatal(err)
	}
	return document
}

func waitForBenchmarkDiagnostics(
	b *testing.B,
	published <-chan string,
	uris ...string,
) {
	b.Helper()
	pending := make(map[string]bool, len(uris))
	for _, uri := range uris {
		pending[uri] = true
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for len(pending) > 0 {
		select {
		case uri := <-published:
			delete(pending, uri)
		case <-timer.C:
			b.Fatalf("timed out waiting for benchmark diagnostics: %v", pending)
		}
	}
}

func benchmarkPosition(
	b *testing.B,
	document *lsp.TextDocument,
	needle string,
	delta int,
) protocol.Position {
	b.Helper()
	offset := strings.Index(document.Source, needle)
	if offset < 0 {
		b.Fatalf("needle %q not found in %s", needle, document.URI)
	}
	line, character := document.LineIndex.PositionUTF16(uint32(offset + delta))
	return protocol.Position{Line: int(line), Character: int(character)}
}

// writeSyntheticBenchmarkProject materializes a deterministic multi-domain
// project: content depends only on the file index, so CodSpeed comparisons
// across revisions measure the same workload.
func writeSyntheticBenchmarkProject(b *testing.B) (string, int) {
	b.Helper()
	root := b.TempDir()
	fileCount := 0
	write := func(path, content string) {
		b.Helper()
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
		fileCount++
	}

	write("composer.json", `{"name": "app/benchmark", "require": {"php": ">=8.3"}}`)

	for i := range 20 {
		write(fmt.Sprintf("src/Entity/Product%02d.php", i), fmt.Sprintf(`<?php
namespace App\Entity;

class Product%02d
{
    public string $id;
    public string $name;
    public float $price;
    public bool $active;

    public function getId(): string
    {
        return $this->id;
    }

    public function getName(): string
    {
        return $this->name;
    }

    public function getPrice(): float
    {
        return $this->price;
    }

    public function isActive(): bool
    {
        return $this->active;
    }
}
`, i))
	}

	write("src/Entity/Product.php", `<?php
namespace App\Entity;

class Product
{
    public string $id;
    public string $name;
    public float $price;

    public function getId(): string
    {
        return $this->id;
    }

    public function getName(): string
    {
        return $this->name;
    }

    public function getPrice(): float
    {
        return $this->price;
    }
}
`)

	for i := range 40 {
		write(fmt.Sprintf("src/Service/ProductService%02d.php", i), fmt.Sprintf(`<?php
namespace App\Service;

use App\Entity\Product%02d;

class ProductService%02d
{
    /** @var Product%02d[] */
    private array $products = [];

    public function add(Product%02d $product): void
    {
        $this->products[$product->getId()] = $product;
    }

    public function find(string $id): ?Product%02d
    {
        return $this->products[$id] ?? null;
    }

    public function describe(string $id): string
    {
        $product = $this->find($id);
        if ($product === null) {
            return 'missing';
        }

        return $product->getName() . ':' . $product->getPrice();
    }
}
`, i, i, i, i, i))
	}

	write("src/Service/BasketService.php", `<?php
namespace App\Service;

use App\Entity\Product;

class BasketService
{
    /** @var Product[] */
    private array $products = [];

    public function addProduct(Product $product): void
    {
        $this->products[$product->getId()] = $product;
    }

    public function total(): float
    {
        $total = 0.0;
        foreach ($this->products as $product) {
            $total += $product->getPrice();
        }

        return $total;
    }

    public function count(): int
    {
        return count($this->products);
    }
}
`)

	for i := range 12 {
		write(fmt.Sprintf("src/Controller/PageController%02d.php", i), fmt.Sprintf(`<?php
namespace App\Controller;

use App\Entity\Product%02d;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\Attribute\Route;

class PageController%02d
{
    #[Route('/page/%02d', name: 'app.page.%02d')]
    public function show(Product%02d $product): Response
    {
        return $this->render('pages/page%02d.html.twig', [
            'product' => $product,
        ]);
    }
}
`, i, i, i, i, i, i))
	}

	write("src/Controller/CheckoutController.php", `<?php
namespace App\Controller;

use App\Entity\Product;
use App\Service\BasketService;
use Symfony\Component\HttpFoundation\Response;
use Symfony\Component\Routing\Attribute\Route;

class CheckoutController
{
    public function __construct(private BasketService $basket)
    {
    }

    #[Route('/checkout/summary', name: 'app.checkout.summary')]
    public function summary(Product $product): Response
    {
        $this->basket->addProduct($product);

        return $this->render('checkout/summary.html.twig', [
            'product' => $product,
            'basket' => $this->basket,
        ]);
    }

    public function completion(): void
    {
        $this->basket->
    }
}
`)

	for i := range 8 {
		write(fmt.Sprintf("src/Subscriber/ProductSubscriber%02d.php", i), fmt.Sprintf(`<?php
namespace App\Subscriber;

use App\Service\ProductService%02d;

class ProductSubscriber%02d
{
    public function __construct(private ProductService%02d $products)
    {
    }

    public static function getSubscribedEvents(): array
    {
        return ['product.loaded' => 'onProductLoaded'];
    }

    public function onProductLoaded(string $id): void
    {
        $this->products->find($id);
    }
}
`, i, i, i))
	}

	var services strings.Builder
	services.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n")
	services.WriteString("<container xmlns=\"http://symfony.com/schema/dic/services\">\n    <services>\n")
	for i := range 30 {
		fmt.Fprintf(
			&services,
			"        <service id=\"App\\Service\\ProductService%02d\" class=\"App\\Service\\ProductService%02d\" public=\"true\"/>\n",
			i,
			i,
		)
	}
	for i := range 8 {
		fmt.Fprintf(
			&services,
			"        <service id=\"app.subscriber.%02d\" class=\"App\\Subscriber\\ProductSubscriber%02d\">\n"+
				"            <argument type=\"service\" id=\"App\\Service\\ProductService%02d\"/>\n"+
				"            <tag name=\"kernel.event_subscriber\"/>\n"+
				"        </service>\n",
			i,
			i,
			i,
		)
	}
	services.WriteString("    </services>\n</container>\n")
	write("config/services.xml", services.String())

	write("config/routes.yaml", `app_controllers:
    resource: ../src/Controller/
    type: attribute
`)
	write("config/packages/framework.yaml", `framework:
    secret: benchmark
`)
	write("config/packages/app.yaml", `parameters:
    app.page_size: 25
`)

	write("templates/base.html.twig", `<!DOCTYPE html>
<html>
    <body>
        {% block content %}{% endblock %}
    </body>
</html>
`)

	for i := range 12 {
		id := fmt.Sprintf("%02d", i)
		write(
			"templates/pages/page"+id+".html.twig",
			"{% extends 'base.html.twig' %}\n\n"+
				"{% block content %}\n"+
				"    <h1>{{ 'page.title."+id+"'|trans }}</h1>\n"+
				"    <p>{{ product.name }} — {{ product.price|number_format(2) }}</p>\n"+
				"{% endblock %}\n",
		)
	}

	write("templates/checkout/summary.html.twig", `{% extends 'base.html.twig' %}

{% block content %}
    <h1>{{ 'checkout.summary.title'|trans }}</h1>
    <p>{{ product.name }}</p>
    {{ product.na }}
{% endblock %}
`)

	var translations strings.Builder
	translations.WriteString("checkout.summary.title: Order summary\n")
	for i := range 12 {
		fmt.Fprintf(&translations, "page.title.%02d: Page %02d\n", i, i)
	}
	write("translations/messages.en.yaml", translations.String())
	write("translations/messages.de.yaml", "checkout.summary.title: Bestellübersicht\n")

	return root, fileCount
}
