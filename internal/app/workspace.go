package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/shopware/shopware-lsp/internal/admin"
	"github.com/shopware/shopware-lsp/internal/appscript"
	"github.com/shopware/shopware-lsp/internal/asset"
	"github.com/shopware/shopware-lsp/internal/console"
	"github.com/shopware/shopware-lsp/internal/doctrine"
	"github.com/shopware/shopware-lsp/internal/environment"
	"github.com/shopware/shopware-lsp/internal/event"
	"github.com/shopware/shopware-lsp/internal/extension"
	"github.com/shopware/shopware-lsp/internal/feature"
	"github.com/shopware/shopware-lsp/internal/form"
	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/messenger"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/security"
	"github.com/shopware/shopware-lsp/internal/serializer"
	"github.com/shopware/shopware-lsp/internal/shopware"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
	"github.com/shopware/shopware-lsp/internal/snippet"
	"github.com/shopware/shopware-lsp/internal/stimulus"
	"github.com/shopware/shopware-lsp/internal/symfony"
	"github.com/shopware/shopware-lsp/internal/symfonyconfig"
	"github.com/shopware/shopware-lsp/internal/systemconfig"
	"github.com/shopware/shopware-lsp/internal/theme"
	"github.com/shopware/shopware-lsp/internal/translation"
	"github.com/shopware/shopware-lsp/internal/twig"
	"github.com/shopware/shopware-lsp/internal/twigcomponent"
)

type Workspace struct {
	root         string
	cacheDir     string
	scanner      *indexer.FileScanner
	store        *indexer.Store
	symbols      *indexer.WorkspaceSymbolCatalog
	indexers     []indexer.Indexer
	initialForce bool
	closeOnce    sync.Once
	closeErr     error
}

func NewWorkspace(_ context.Context, root string, server *lsp.Server) (_ *Workspace, returnErr error) {
	cacheDir, err := projectCacheFolder(root)
	if err != nil {
		return nil, fmt.Errorf("resolve cache directory: %w", err)
	}
	cacheCleared, err := indexer.CheckAndMigrateCache(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("migrate index cache: %w", err)
	}

	workspace := &Workspace{root: root, cacheDir: cacheDir, initialForce: cacheCleared}
	defer func() {
		if returnErr != nil {
			_ = workspace.Close()
		}
	}()

	workspace.store, err = indexer.NewStore(filepath.Join(cacheDir, "indexes.db"))
	if err != nil {
		return nil, fmt.Errorf("create index store: %w", err)
	}
	workspace.symbols, err = indexer.NewWorkspaceSymbolCatalog(workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create workspace symbol catalog: %w", err)
	}

	workspace.scanner, err = indexer.NewFileScanner(root, filepath.Join(cacheDir, "file_scanner.db"), workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create file scanner: %w", err)
	}
	workspace.scanner.SetWorkspaceSymbolCatalog(workspace.symbols)

	phpIndex, err := php.NewPHPIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create PHP index: %w", err)
	}
	initializationOptions := server.InitializationOptions()
	if err := phpIndex.ConfigureProjectWithExtensions(
		root,
		initializationOptions.PHPExtensions,
		initializationOptions.DisabledPHPExtensions,
	); err != nil {
		return nil, fmt.Errorf("configure PHP project: %w", err)
	}
	phpIndex.RegisterTypeExtension(inference.FakerTypes)
	phpIndex.RegisterTypeExtension(shopware.NewPHPTypeExtension())
	phpIndex.RegisterTypeExtension(event.NewPHPTypeExtension())
	workspace.indexers = append(workspace.indexers, phpIndex)

	serviceIndex, err := symfony.NewServiceIndex(root, cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create service index: %w", err)
	}
	serviceIndex.SetPHPIndex(phpIndex)
	phpIndex.RegisterTypeExtension(symfony.NewPHPTypeExtension(serviceIndex))
	workspace.indexers = append(workspace.indexers, serviceIndex)
	routeIndex, err := symfony.NewProjectRouteIndexer(
		root,
		cacheDir,
		workspace.store,
	)
	if err != nil {
		return nil, fmt.Errorf("create route index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, routeIndex)
	routeUsageIndex, err := symfony.NewRouteUsageIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create route usage index: %w", err)
	}
	routeUsageIndex.SetPHPIndex(phpIndex)
	workspace.indexers = append(workspace.indexers, routeUsageIndex)
	consoleIndex, err := console.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Console index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, consoleIndex)
	eventIndex, err := event.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create event index: %w", err)
	}
	eventIndex.SetPHPIndex(phpIndex)
	workspace.indexers = append(workspace.indexers, eventIndex)
	messengerIndex, err := messenger.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Messenger index: %w", err)
	}
	messengerIndex.SetPHPIndex(phpIndex)
	workspace.indexers = append(workspace.indexers, messengerIndex)
	environmentIndex, err := environment.NewIndex(
		cacheDir,
		workspace.store,
	)
	if err != nil {
		return nil, fmt.Errorf("create environment index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, environmentIndex)
	formIndex, err := form.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create form index: %w", err)
	}
	formIndex.SetPHPIndex(phpIndex)
	workspace.indexers = append(workspace.indexers, formIndex)
	securityIndex, err := security.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Security index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, securityIndex)
	configurationIndex, err := symfonyconfig.NewIndex(
		cacheDir,
		workspace.store,
	)
	if err != nil {
		return nil, fmt.Errorf("create Symfony configuration index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, configurationIndex)
	serializerIndex, err := serializer.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Serializer index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, serializerIndex)
	doctrineIndex, err := doctrine.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Doctrine index: %w", err)
	}
	doctrineIndex.SetNamespaceAliasProvider(serviceIndex)
	phpIndex.RegisterTypeExtension(doctrine.NewPHPTypeExtension(doctrineIndex))
	workspace.indexers = append(workspace.indexers, doctrineIndex)
	assetIndex, err := asset.NewIndex(root, cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create asset index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, assetIndex)
	stimulusIndex, err := stimulus.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Stimulus index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, stimulusIndex)
	twigIndex, err := twig.NewTwigIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Twig index: %w", err)
	}
	twigIndex.SetDependencies(phpIndex, serviceIndex)
	workspace.indexers = append(workspace.indexers, twigIndex)
	twigComponentIndex, err := twigcomponent.NewIndex(
		cacheDir,
		workspace.store,
	)
	if err != nil {
		return nil, fmt.Errorf("create Twig component index: %w", err)
	}
	twigComponentIndex.SetDependencies(
		phpIndex,
		serviceIndex,
		twigIndex,
	)
	workspace.indexers = append(
		workspace.indexers,
		twigComponentIndex,
	)
	snippetIndex, err := snippet.NewSnippetIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create snippet index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, snippetIndex)
	translationIndex, err := translation.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create translation index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, translationIndex)
	featureIndex, err := feature.NewFeatureIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create feature index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, featureIndex)
	systemConfigIndex, err := systemconfig.NewSystemConfigIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create system config index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, systemConfigIndex)
	themeIndex, err := theme.NewThemeConfigIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create theme index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, themeIndex)
	extensionIndex, err := extension.NewExtensionIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create extension index: %w", err)
	}
	extensionIndex.SetPHPIndex(phpIndex)
	workspace.indexers = append(workspace.indexers, extensionIndex)
	adminIndex, err := admin.NewAdminComponentIndexer(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create administration index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, adminIndex)
	dalIndex, err := shopwaredal.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create Shopware DAL index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, dalIndex)
	appScriptIndex, err := appscript.NewIndex(cacheDir, workspace.store)
	if err != nil {
		return nil, fmt.Errorf("create App script index: %w", err)
	}
	workspace.indexers = append(workspace.indexers, appScriptIndex)
	for _, idx := range workspace.indexers {
		workspace.scanner.AddIndexer(idx)
	}

	registerFeatures(server, root, workspaceServices{
		symbols:        workspace.symbols,
		services:       serviceIndex,
		routes:         routeIndex,
		routeUsage:     routeUsageIndex,
		console:        consoleIndex,
		doctrine:       doctrineIndex,
		assets:         assetIndex,
		events:         eventIndex,
		messenger:      messengerIndex,
		environment:    environmentIndex,
		forms:          formIndex,
		security:       securityIndex,
		configuration:  configurationIndex,
		serializer:     serializerIndex,
		stimulus:       stimulusIndex,
		php:            phpIndex,
		twig:           twigIndex,
		twigComponents: twigComponentIndex,
		snippets:       snippetIndex,
		translations:   translationIndex,
		features:       featureIndex,
		systemConfig:   systemConfigIndex,
		theme:          themeIndex,
		extensions:     extensionIndex,
		admin:          adminIndex,
		dal:            dalIndex,
		appScripts:     appScriptIndex,
	})

	return workspace, nil
}

func (w *Workspace) Root() string                  { return w.root }
func (w *Workspace) Scanner() *indexer.FileScanner { return w.scanner }
func (w *Workspace) InitialForceReindex() bool     { return w.initialForce }

func (w *Workspace) Close() error {
	w.closeOnce.Do(func() {
		var closeErrors []error
		if w.scanner != nil {
			closeErrors = append(closeErrors, w.scanner.Close())
		}
		for index := len(w.indexers) - 1; index >= 0; index-- {
			if err := w.indexers[index].Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("%s: %w", w.indexers[index].ID(), err))
			}
		}
		if w.store != nil {
			if err := w.store.Close(); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("index store: %w", err))
			}
		}
		w.closeErr = errors.Join(closeErrors...)
	})
	return w.closeErr
}
