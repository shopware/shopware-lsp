package twig

import (
	"bytes"
	"errors"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shopware/shopware-lsp/internal/indexer"
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php"
	"github.com/shopware/shopware-lsp/internal/symfony"
)

type TwigIndexer struct {
	twigFileIndex          *indexer.DataIndexer[TwigFile]
	twigBlockIndex         *indexer.DataIndexer[TwigBlock]
	twigBlockHashIndex     *indexer.DataIndexer[TwigBlockHash]
	twigFunctionIndex      *indexer.DataIndexer[TwigFunction]
	twigFilterIndex        *indexer.DataIndexer[TwigFilter]
	twigTestIndex          *indexer.DataIndexer[TwigTest]
	twigOperatorIndex      *indexer.DataIndexer[TwigOperator]
	twigTagIndex           *indexer.DataIndexer[TwigTag]
	twigMacroIndex         *indexer.DataIndexer[MacroCatalog]
	twigMacroUsageIndex    *indexer.DataIndexer[MacroUsageRecord]
	templateVariableIndex  *indexer.DataIndexer[TemplateVariableCatalog]
	twigGlobalIndex        *indexer.DataIndexer[GlobalCatalog]
	templateReferenceIndex *indexer.DataIndexer[TemplateReferenceCatalog]
	constantUsageIndex     *indexer.DataIndexer[ConstantUsageCatalog]
	phpUsageIndex          *indexer.DataIndexer[PHPUsageCatalog]
	extensionUsageIndex    *indexer.DataIndexer[ExtensionUsageCatalog]

	dependenciesMu sync.RWMutex
	phpIndex       *php.PHPIndex
	serviceIndex   *symfony.ServiceIndex

	globalPathsMu sync.RWMutex
	globalPaths   map[string]struct{}
}

func NewTwigIndexer(configDir string, stores ...*indexer.Store) (_ *TwigIndexer, returnErr error) {
	var opened []interface{ Close() error }
	defer func() {
		if returnErr != nil {
			for _, repository := range opened {
				_ = repository.Close()
			}
		}
	}()

	twigFileIndex, err := indexer.NewRepository[TwigFile](path.Join(configDir, "twig_file.index"), "twig.files", stores...)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigFileIndex)

	twigBlockIndex, err := indexer.NewRepository[TwigBlock](path.Join(configDir, "twig_block.index"), "twig.blocks", stores...)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigBlockIndex)

	twigBlockHashIndex, err := indexer.NewRepository[TwigBlockHash](path.Join(configDir, "twig_block_hash.index"), "twig.block_hashes", stores...)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigBlockHashIndex)

	twigFunctionIndex, err := indexer.NewRepository[TwigFunction](path.Join(configDir, "twig_function.index"), "twig.functions", stores...)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigFunctionIndex)

	twigFilterIndex, err := indexer.NewRepository[TwigFilter](path.Join(configDir, "twig_filter.index"), "twig.filters", stores...)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigFilterIndex)

	twigTestIndex, err := indexer.NewRepository[TwigTest](
		path.Join(configDir, "twig_test.index"),
		"twig.tests",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigTestIndex)

	twigOperatorIndex, err := indexer.NewRepository[TwigOperator](
		path.Join(configDir, "twig_operator.index"),
		"twig.operators",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigOperatorIndex)

	twigTagIndex, err := indexer.NewRepository[TwigTag](
		path.Join(configDir, "twig_tag.index"),
		"twig.tags",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigTagIndex)

	twigMacroIndex, err := indexer.NewRepository[MacroCatalog](
		path.Join(configDir, "twig_macro.index"),
		"twig.macros",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigMacroIndex)

	twigMacroUsageIndex, err := indexer.NewRepository[MacroUsageRecord](
		path.Join(configDir, "twig_macro.index"),
		"twig.macro_usages",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigMacroUsageIndex)

	templateVariableIndex, err := indexer.NewRepository[TemplateVariableCatalog](
		path.Join(configDir, "twig_template_variable.index"),
		"twig.template_variables",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, templateVariableIndex)

	twigGlobalIndex, err := indexer.NewRepository[GlobalCatalog](
		path.Join(configDir, "twig_global.index"),
		"twig.globals",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, twigGlobalIndex)
	globalPaths, err := twigGlobalIndex.GetAllFilePaths()
	if err != nil {
		return nil, err
	}
	globalPathSet := make(map[string]struct{}, len(globalPaths))
	for _, globalPath := range globalPaths {
		globalPathSet[globalPath] = struct{}{}
	}

	templateReferenceIndex, err := indexer.NewRepository[TemplateReferenceCatalog](
		path.Join(configDir, "twig_template_reference.index"),
		"twig.template_references",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, templateReferenceIndex)

	constantUsageIndex, err := indexer.NewRepository[ConstantUsageCatalog](
		path.Join(configDir, "twig_constant_usage.index"),
		"twig.constant_usages",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, constantUsageIndex)

	phpUsageIndex, err := indexer.NewRepository[PHPUsageCatalog](
		path.Join(configDir, "twig_php_usage.index"),
		"twig.php_usages",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, phpUsageIndex)

	extensionUsageIndex, err := indexer.NewRepository[ExtensionUsageCatalog](
		path.Join(configDir, "twig_extension_usage.index"),
		"twig.extension_usages",
		stores...,
	)
	if err != nil {
		return nil, err
	}
	opened = append(opened, extensionUsageIndex)

	return &TwigIndexer{
		twigFileIndex:          twigFileIndex,
		twigBlockIndex:         twigBlockIndex,
		twigBlockHashIndex:     twigBlockHashIndex,
		twigFunctionIndex:      twigFunctionIndex,
		twigFilterIndex:        twigFilterIndex,
		twigTestIndex:          twigTestIndex,
		twigOperatorIndex:      twigOperatorIndex,
		twigTagIndex:           twigTagIndex,
		twigMacroIndex:         twigMacroIndex,
		twigMacroUsageIndex:    twigMacroUsageIndex,
		templateVariableIndex:  templateVariableIndex,
		twigGlobalIndex:        twigGlobalIndex,
		templateReferenceIndex: templateReferenceIndex,
		constantUsageIndex:     constantUsageIndex,
		phpUsageIndex:          phpUsageIndex,
		extensionUsageIndex:    extensionUsageIndex,
		globalPaths:            globalPathSet,
	}, nil
}

func (idx *TwigIndexer) SetDependencies(
	phpIndex *php.PHPIndex,
	serviceIndex *symfony.ServiceIndex,
) {
	if idx == nil {
		return
	}
	idx.dependenciesMu.Lock()
	idx.phpIndex = phpIndex
	idx.serviceIndex = serviceIndex
	idx.dependenciesMu.Unlock()
}

func (idx *TwigIndexer) ID() string {
	return "twig.indexer"
}

func (idx *TwigIndexer) Index(file *indexer.ParsedFile) error {
	switch file.Extension() {
	case ".twig":
		return idx.indexTwig(file)
	case ".php":
		if err := idx.indexExtension(file); err != nil {
			return err
		}
		if err := idx.indexPHPGlobals(file); err != nil {
			return err
		}
		return idx.saveTemplateReferences(
			file,
			PHPTemplateReferences(file.Path, file.SyntaxTree().Root),
		)
	case ".yaml", ".yml":
		return idx.indexYAMLGlobals(file)
	default:
		return nil
	}
}

func (idx *TwigIndexer) indexYAMLGlobals(
	file *indexer.ParsedFile,
) error {
	candidate := bytes.Contains(file.Content, []byte("twig")) &&
		bytes.Contains(file.Content, []byte("globals"))
	if !candidate {
		return idx.clearGlobalsIfIndexed(file)
	}
	tree := file.SyntaxTree()
	if tree == nil {
		return nil
	}
	return idx.saveGlobals(
		file,
		GlobalsInYAML(file.Path, tree.Root),
	)
}

func (idx *TwigIndexer) indexPHPGlobals(
	file *indexer.ParsedFile,
) error {
	if !bytes.Contains(file.Content, []byte("getGlobals")) {
		return idx.clearGlobalsIfIndexed(file)
	}
	tree := file.SyntaxTree()
	if tree == nil {
		return nil
	}
	phpIndex, _ := idx.dependencies()
	var globals []Global
	if phpIndex != nil {
		globals = GlobalsInPHPExtension(
			file.Path,
			tree.Root,
			phpIndex.AnalyzeDocument(file.Path, 0, tree.Root),
		)
	} else {
		globals = GlobalsInPHPExtension(file.Path, tree.Root, nil)
	}
	return idx.saveGlobals(file, globals)
}

func (idx *TwigIndexer) clearGlobalsIfIndexed(
	file *indexer.ParsedFile,
) error {
	if !idx.hasGlobalPath(file.Path) {
		return nil
	}
	return idx.saveGlobals(file, nil)
}

func (idx *TwigIndexer) saveGlobals(
	file *indexer.ParsedFile,
	globals []Global,
) error {
	write := map[string]map[string]GlobalCatalog{file.Path: {}}
	if len(globals) != 0 {
		write[file.Path]["globals"] = GlobalCatalog{
			File:    file.Path,
			Globals: globals,
		}
	}
	if err := idx.twigGlobalIndex.BatchSaveItemsIn(
		file.Mutation(),
		write,
	); err != nil {
		return err
	}
	return idx.publishGlobalPath(
		file.Path,
		len(globals) != 0,
		file.Mutation(),
	)
}

func (idx *TwigIndexer) indexTwig(parsed *indexer.ParsedFile) error {
	path := parsed.Path
	if strings.Contains(path, "Resources/app/administration") || strings.Contains(path, "Migration/Fixtures") || strings.Contains(path, ".phpdoc/template") {
		return errors.Join(
			idx.twigFileIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]TwigFile{path: {}}),
			idx.twigBlockIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]TwigBlock{path: {}}),
			idx.twigBlockHashIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]TwigBlockHash{path: {}}),
			idx.twigMacroIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]MacroCatalog{path: {}}),
			idx.twigMacroUsageIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]MacroUsageRecord{path: {}}),
			idx.templateVariableIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]TemplateVariableCatalog{path: {}}),
			idx.templateReferenceIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]TemplateReferenceCatalog{path: {}}),
			idx.constantUsageIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]ConstantUsageCatalog{path: {}}),
			idx.phpUsageIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]PHPUsageCatalog{path: {}}),
			idx.extensionUsageIndex.BatchSaveItemsIn(parsed.Mutation(), map[string]map[string]ExtensionUsageCatalog{path: {}}),
		)
	}

	file, err := ParseTwigTree(path, parsed.SyntaxTree(), parsed.LineIndex())
	if err != nil {
		return err
	}

	// Use batch save for twig files
	twigFiles := make(map[string]map[string]TwigFile)
	twigFiles[path] = make(map[string]TwigFile)
	for _, name := range TemplateNames(path) {
		twigFiles[path][name] = *file
	}

	if err := idx.twigFileIndex.BatchSaveItemsIn(parsed.Mutation(), twigFiles); err != nil {
		return err
	}

	twigBlocks := make(map[string]map[string]TwigBlock)
	twigBlocks[file.Path] = make(map[string]TwigBlock)

	isStorefrontTemplate := IsStorefrontTemplate(path)

	twigBlockHashes := map[string]map[string]TwigBlockHash{
		file.Path: {},
	}
	if isStorefrontTemplate {
		twigBlockHashes[file.Path] = make(map[string]TwigBlockHash)
	}

	for _, block := range file.Blocks {
		if _, ok := twigBlocks[file.Path][block.Name]; !ok {
			twigBlocks[file.Path][block.Name] = block
		}

		if isStorefrontTemplate {
			blockHash := TwigBlockHash{
				Name:         block.Name,
				RelativePath: ConvertToRelativePath(path),
				AbsolutePath: path,
				Hash:         block.Hash,
				Text:         block.Text,
				Deprecation:  block.Deprecation,
			}
			twigBlockHashes[file.Path][block.Name] = blockHash
		}
	}

	if err := idx.twigBlockIndex.BatchSaveItemsIn(parsed.Mutation(), twigBlocks); err != nil {
		return err
	}

	if err := idx.twigBlockHashIndex.BatchSaveItemsIn(parsed.Mutation(), twigBlockHashes); err != nil {
		return err
	}

	macros := MacrosInDocument(path, parsed.SyntaxTree().Root)
	macroCatalogs := map[string]map[string]MacroCatalog{path: {}}
	if len(macros) != 0 {
		for _, template := range TemplateNames(path) {
			macroCatalogs[path][normalizeMacroTemplate(template)] = MacroCatalog{
				Template: template,
				FilePath: path,
				Macros:   macros,
			}
		}
	}
	if err := idx.twigMacroIndex.BatchSaveItemsIn(
		parsed.Mutation(),
		macroCatalogs,
	); err != nil {
		return err
	}

	templateVariables := TemplateInputVariablesInDocument(
		path,
		parsed.SyntaxTree().Root,
	)
	templateDependencies := TemplateDependenciesInDocument(
		parsed.SyntaxTree().Root,
	)
	variableCatalogs := map[string]map[string]TemplateVariableCatalog{
		path: {},
	}
	for _, template := range TemplateNames(path) {
		variableCatalogs[path][normalizeMacroTemplate(template)] =
			TemplateVariableCatalog{
				Template:     template,
				FilePath:     path,
				Variables:    templateVariables,
				Dependencies: templateDependencies,
			}
	}
	if err := idx.templateVariableIndex.BatchSaveItemsIn(
		parsed.Mutation(),
		variableCatalogs,
	); err != nil {
		return err
	}

	usageRecords := map[string]map[string]MacroUsageRecord{path: {}}
	for _, reference := range MacroReferencesInDocument(
		path,
		parsed.SyntaxTree().Root,
	) {
		if reference.Role != MacroUsageReference {
			continue
		}
		for _, template := range reference.Templates {
			key := macroUsageKey(template, reference.Name)
			record := usageRecords[path][key]
			if record.Template == "" {
				record = MacroUsageRecord{
					Template: template,
					Name:     reference.Name,
					FilePath: path,
				}
			}
			record.Usages = append(record.Usages, MacroUsage{
				Template: template,
				Name:     reference.Name,
				FilePath: path,
				Range:    reference.Range,
			})
			usageRecords[path][key] = record
		}
	}
	if err := idx.twigMacroUsageIndex.BatchSaveItemsIn(
		parsed.Mutation(),
		usageRecords,
	); err != nil {
		return err
	}
	if err := idx.saveTemplateReferences(
		parsed,
		TwigTemplateReferences(path, parsed.SyntaxTree().Root),
	); err != nil {
		return err
	}
	phpIndex, _ := idx.dependencies()
	resolver := (PHPAccessResolver{
		PHP:  phpIndex,
		Twig: idx,
	}).forDocument(parsed.SyntaxTree().Root)
	if err := idx.saveConstantReferences(
		parsed,
		ConstantReferencesInDocument(
			path,
			parsed.SyntaxTree().Root,
			resolver,
		),
	); err != nil {
		return err
	}
	if err := idx.saveExtensionUsages(
		parsed,
		ExtensionUsagesInDocument(
			path,
			parsed.SyntaxTree().Root,
		),
	); err != nil {
		return err
	}
	if err := idx.savePHPUsageReferences(
		parsed,
		PHPUsageReferencesInDocument(
			path,
			parsed.SyntaxTree().Root,
			resolver,
		),
	); err != nil {
		return err
	}
	addTemplateWorkspaceSymbols(parsed, file, macros)
	return nil
}

func (idx *TwigIndexer) saveTemplateReferences(
	file *indexer.ParsedFile,
	references []TemplateReference,
) error {
	write := map[string]map[string]TemplateReferenceCatalog{
		file.Path: {},
	}
	for _, reference := range references {
		key := normalizeTemplateReference(reference.Template)
		if key == "" {
			continue
		}
		catalog := write[file.Path][key]
		if catalog.FilePath == "" {
			catalog = TemplateReferenceCatalog{FilePath: file.Path}
		}
		catalog.References = append(catalog.References, reference)
		write[file.Path][key] = catalog
	}
	return idx.templateReferenceIndex.BatchSaveItemsIn(file.Mutation(), write)
}

func (idx *TwigIndexer) saveConstantReferences(
	file *indexer.ParsedFile,
	references []ConstantReference,
) error {
	write := map[string]map[string]ConstantUsageCatalog{
		file.Path: {},
	}
	for _, reference := range references {
		key := ConstantReferenceKey(reference)
		if key == "" {
			continue
		}
		catalog := write[file.Path][key]
		if catalog.Key == "" {
			catalog.Key = key
		}
		catalog.References = append(catalog.References, reference)
		write[file.Path][key] = catalog
	}
	return idx.constantUsageIndex.BatchSaveItemsIn(
		file.Mutation(),
		write,
	)
}

func (idx *TwigIndexer) savePHPUsageReferences(
	file *indexer.ParsedFile,
	references []PHPUsageReference,
) error {
	write := map[string]map[string]PHPUsageCatalog{
		file.Path: {},
	}
	for _, reference := range references {
		key := PHPUsageReferenceKey(reference)
		if key == "" {
			continue
		}
		catalog := write[file.Path][key]
		if catalog.Key == "" {
			catalog.Key = key
		}
		catalog.References = append(catalog.References, reference)
		write[file.Path][key] = catalog
	}
	return idx.phpUsageIndex.BatchSaveItemsIn(
		file.Mutation(),
		write,
	)
}

func (idx *TwigIndexer) saveExtensionUsages(
	file *indexer.ParsedFile,
	usages []ExtensionUsage,
) error {
	write := map[string]map[string]ExtensionUsageCatalog{
		file.Path: {},
	}
	for _, usage := range usages {
		key := ExtensionUsageKey(usage.Kind, usage.Name)
		if key == "" {
			continue
		}
		catalog := write[file.Path][key]
		if catalog.Key == "" {
			catalog.Key = key
		}
		catalog.Usages = append(catalog.Usages, usage)
		write[file.Path][key] = catalog
	}
	return idx.extensionUsageIndex.BatchSaveItemsIn(
		file.Mutation(),
		write,
	)
}

func (idx *TwigIndexer) indexExtension(file *indexer.ParsedFile) error {
	path := file.Path
	fileContent := file.Content
	root := file.SyntaxTree().Root
	callableCandidate := isTwigCallableDefinitionCandidate(fileContent)
	tokenParserCandidate := isTwigTokenParserCandidate(fileContent)
	var lineIndex *cst.LineIndex
	if callableCandidate || tokenParserCandidate {
		lineIndex = file.LineIndex()
	}
	var functions []TwigFunction
	var filters []TwigFilter
	var tests []TwigTest
	if callableCandidate {
		var err error
		functions, filters, tests, err = parseTwigExtensionTreeAll(
			path,
			root,
			fileContent,
			lineIndex,
		)
		if err != nil {
			return err
		}
	}

	functionsMap := map[string]map[string]TwigFunction{path: {}}
	filtersMap := map[string]map[string]TwigFilter{path: {}}
	testsMap := map[string]map[string]TwigTest{path: {}}
	operatorsMap := map[string]map[string]TwigOperator{path: {}}
	tagsMap := map[string]map[string]TwigTag{path: {}}

	for _, function := range functions {
		if _, ok := functionsMap[function.FilePath]; !ok {
			functionsMap[function.FilePath] = make(map[string]TwigFunction)
		}
		functionsMap[function.FilePath][function.Name] = function
	}

	for _, filter := range filters {
		if _, ok := filtersMap[filter.FilePath]; !ok {
			filtersMap[filter.FilePath] = make(map[string]TwigFilter)
		}
		filtersMap[filter.FilePath][filter.Name] = filter
	}
	for _, test := range tests {
		if _, ok := testsMap[test.FilePath]; !ok {
			testsMap[test.FilePath] = make(map[string]TwigTest)
		}
		testsMap[test.FilePath][test.Name] = test
	}
	for _, operator := range ParseTwigOperators(path, root, fileContent) {
		operatorsMap[operator.FilePath][operator.Name] = operator
	}
	if tokenParserCandidate {
		for _, tag := range ParseTwigTokenParsers(
			path,
			root,
			fileContent,
			lineIndex,
		) {
			tagsMap[tag.FilePath][tag.Name] = tag
		}
	}

	if err := idx.twigFunctionIndex.BatchSaveItemsIn(file.Mutation(), functionsMap); err != nil {
		return err
	}

	if err := idx.twigFilterIndex.BatchSaveItemsIn(file.Mutation(), filtersMap); err != nil {
		return err
	}

	if err := idx.twigTestIndex.BatchSaveItemsIn(
		file.Mutation(),
		testsMap,
	); err != nil {
		return err
	}

	if err := idx.twigOperatorIndex.BatchSaveItemsIn(
		file.Mutation(),
		operatorsMap,
	); err != nil {
		return err
	}

	if err := idx.twigTagIndex.BatchSaveItemsIn(file.Mutation(), tagsMap); err != nil {
		return err
	}
	addExtensionWorkspaceSymbols(file, functions, filters)
	return nil
}

func (idx *TwigIndexer) RemovedFiles(paths []string) error {
	err := errors.Join(
		idx.twigFileIndex.BatchDeleteByFilePaths(paths),
		idx.twigBlockIndex.BatchDeleteByFilePaths(paths),
		idx.twigBlockHashIndex.BatchDeleteByFilePaths(paths),
		idx.twigFunctionIndex.BatchDeleteByFilePaths(paths),
		idx.twigFilterIndex.BatchDeleteByFilePaths(paths),
		idx.twigTestIndex.BatchDeleteByFilePaths(paths),
		idx.twigOperatorIndex.BatchDeleteByFilePaths(paths),
		idx.twigTagIndex.BatchDeleteByFilePaths(paths),
		idx.twigMacroIndex.BatchDeleteByFilePaths(paths),
		idx.twigMacroUsageIndex.BatchDeleteByFilePaths(paths),
		idx.templateVariableIndex.BatchDeleteByFilePaths(paths),
		idx.twigGlobalIndex.BatchDeleteByFilePaths(paths),
		idx.templateReferenceIndex.BatchDeleteByFilePaths(paths),
		idx.constantUsageIndex.BatchDeleteByFilePaths(paths),
		idx.phpUsageIndex.BatchDeleteByFilePaths(paths),
		idx.extensionUsageIndex.BatchDeleteByFilePaths(paths),
	)
	if err == nil {
		idx.removeGlobalPaths(paths)
	}
	return err
}

func (idx *TwigIndexer) RemovedFilesIn(paths []string, mutation *indexer.Mutation) error {
	err := errors.Join(
		idx.twigFileIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigBlockIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigBlockHashIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigFunctionIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigFilterIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigTestIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigOperatorIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigTagIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigMacroIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigMacroUsageIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.templateVariableIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.twigGlobalIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.templateReferenceIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.constantUsageIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.phpUsageIndex.BatchDeleteByFilePathsIn(mutation, paths),
		idx.extensionUsageIndex.BatchDeleteByFilePathsIn(mutation, paths),
	)
	if err != nil {
		return err
	}
	if mutation == nil {
		idx.removeGlobalPaths(paths)
		return nil
	}
	return mutation.AfterCommit(func() { idx.removeGlobalPaths(paths) })
}

func (idx *TwigIndexer) Close() error {
	return errors.Join(
		idx.twigBlockIndex.Close(),
		idx.twigBlockHashIndex.Close(),
		idx.twigFileIndex.Close(),
		idx.twigFunctionIndex.Close(),
		idx.twigFilterIndex.Close(),
		idx.twigTestIndex.Close(),
		idx.twigOperatorIndex.Close(),
		idx.twigTagIndex.Close(),
		idx.twigMacroIndex.Close(),
		idx.twigMacroUsageIndex.Close(),
		idx.templateVariableIndex.Close(),
		idx.twigGlobalIndex.Close(),
		idx.templateReferenceIndex.Close(),
		idx.constantUsageIndex.Close(),
		idx.phpUsageIndex.Close(),
		idx.extensionUsageIndex.Close(),
	)
}

func (idx *TwigIndexer) Clear() error {
	err := errors.Join(
		idx.twigBlockIndex.Clear(),
		idx.twigBlockHashIndex.Clear(),
		idx.twigFileIndex.Clear(),
		idx.twigFunctionIndex.Clear(),
		idx.twigFilterIndex.Clear(),
		idx.twigTestIndex.Clear(),
		idx.twigOperatorIndex.Clear(),
		idx.twigTagIndex.Clear(),
		idx.twigMacroIndex.Clear(),
		idx.twigMacroUsageIndex.Clear(),
		idx.templateVariableIndex.Clear(),
		idx.twigGlobalIndex.Clear(),
		idx.templateReferenceIndex.Clear(),
		idx.constantUsageIndex.Clear(),
		idx.phpUsageIndex.Clear(),
		idx.extensionUsageIndex.Clear(),
	)
	if err == nil {
		idx.resetGlobalPaths()
	}
	return err
}

func (idx *TwigIndexer) ClearIn(mutation *indexer.Mutation) error {
	err := errors.Join(
		idx.twigBlockIndex.ClearIn(mutation),
		idx.twigBlockHashIndex.ClearIn(mutation),
		idx.twigFileIndex.ClearIn(mutation),
		idx.twigFunctionIndex.ClearIn(mutation),
		idx.twigFilterIndex.ClearIn(mutation),
		idx.twigTestIndex.ClearIn(mutation),
		idx.twigOperatorIndex.ClearIn(mutation),
		idx.twigTagIndex.ClearIn(mutation),
		idx.twigMacroIndex.ClearIn(mutation),
		idx.twigMacroUsageIndex.ClearIn(mutation),
		idx.templateVariableIndex.ClearIn(mutation),
		idx.twigGlobalIndex.ClearIn(mutation),
		idx.templateReferenceIndex.ClearIn(mutation),
		idx.constantUsageIndex.ClearIn(mutation),
		idx.phpUsageIndex.ClearIn(mutation),
		idx.extensionUsageIndex.ClearIn(mutation),
	)
	if err != nil {
		return err
	}
	if mutation == nil {
		idx.resetGlobalPaths()
		return nil
	}
	return mutation.AfterCommit(idx.resetGlobalPaths)
}

func (idx *TwigIndexer) hasGlobalPath(path string) bool {
	idx.globalPathsMu.RLock()
	defer idx.globalPathsMu.RUnlock()
	_, exists := idx.globalPaths[path]
	return exists
}

func (idx *TwigIndexer) publishGlobalPath(
	path string,
	present bool,
	mutation *indexer.Mutation,
) error {
	publish := func() {
		idx.globalPathsMu.Lock()
		defer idx.globalPathsMu.Unlock()
		if present {
			idx.globalPaths[path] = struct{}{}
		} else {
			delete(idx.globalPaths, path)
		}
	}
	if mutation == nil {
		publish()
		return nil
	}
	return mutation.AfterCommit(publish)
}

func (idx *TwigIndexer) removeGlobalPaths(paths []string) {
	idx.globalPathsMu.Lock()
	defer idx.globalPathsMu.Unlock()
	for _, path := range paths {
		delete(idx.globalPaths, path)
	}
}

func (idx *TwigIndexer) resetGlobalPaths() {
	idx.globalPathsMu.Lock()
	defer idx.globalPathsMu.Unlock()
	clear(idx.globalPaths)
}

func (idx *TwigIndexer) GetConstantReferences(
	target ConstantReference,
) ([]ConstantReference, error) {
	if idx == nil || idx.constantUsageIndex == nil {
		return nil, nil
	}
	key := ConstantReferenceKey(target)
	if key == "" {
		return nil, nil
	}
	catalogs, err := idx.constantUsageIndex.GetValues(key)
	if err != nil {
		return nil, err
	}
	var result []ConstantReference
	for _, catalog := range catalogs {
		result = append(result, catalog.References...)
	}
	return uniqueConstantReferences(result), nil
}

func (idx *TwigIndexer) GetPHPUsageReferences(
	target PHPUsageReference,
) ([]PHPUsageReference, error) {
	if idx == nil || idx.phpUsageIndex == nil {
		return nil, nil
	}
	key := PHPUsageReferenceKey(target)
	if key == "" {
		return nil, nil
	}
	catalogs, err := idx.phpUsageIndex.GetValues(key)
	if err != nil {
		return nil, err
	}
	var result []PHPUsageReference
	for _, catalog := range catalogs {
		result = append(result, catalog.References...)
	}
	return uniquePHPUsageReferences(result), nil
}

func (idx *TwigIndexer) GetExtensionUsages(
	kind ExtensionUsageKind,
	name string,
) ([]ExtensionUsage, error) {
	if idx == nil || idx.extensionUsageIndex == nil {
		return nil, nil
	}
	key := ExtensionUsageKey(kind, name)
	if key == "" {
		return nil, nil
	}
	catalogs, err := idx.extensionUsageIndex.GetValues(key)
	if err != nil {
		return nil, err
	}
	var result []ExtensionUsage
	for _, catalog := range catalogs {
		result = append(result, catalog.Usages...)
	}
	return uniqueExtensionUsages(result), nil
}

func (idx *TwigIndexer) GetTemplateReferences(
	templates ...string,
) ([]TemplateReference, error) {
	if idx == nil || idx.templateReferenceIndex == nil {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var result []TemplateReference
	for _, template := range templates {
		catalogs, err := idx.templateReferenceIndex.GetValues(
			normalizeTemplateReference(template),
		)
		if err != nil {
			return nil, err
		}
		for _, catalog := range catalogs {
			for _, reference := range catalog.References {
				key := reference.FilePath + "\x00" +
					reference.Range.String() + "\x00" +
					reference.Template
				if _, duplicate := seen[key]; duplicate {
					continue
				}
				seen[key] = struct{}{}
				result = append(result, reference)
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func (idx *TwigIndexer) GetTemplateReferencesByPath(
	filePath string,
) ([]TemplateReference, error) {
	if idx == nil || idx.templateReferenceIndex == nil {
		return nil, nil
	}
	catalogs, err := idx.templateReferenceIndex.GetValuesByPath(filePath)
	if err != nil {
		return nil, err
	}
	var result []TemplateReference
	for _, catalog := range catalogs {
		result = append(result, catalog.References...)
	}
	return uniqueTemplateReferences(result), nil
}

func (idx *TwigIndexer) GetAllTemplateFiles() ([]string, error) {
	return idx.twigFileIndex.GetAllKeys()
}

// HasOtherTemplateFile answers the cheap availability question used while
// presenting an extends action. Full candidate materialization stays deferred
// until the user invokes the action.
func (idx *TwigIndexer) HasOtherTemplateFile(
	currentPath string,
) (bool, error) {
	if idx == nil || idx.twigFileIndex == nil {
		return false, nil
	}
	return idx.twigFileIndex.HasAnyKeyExceptFold(
		TemplateNames(currentPath)...,
	)
}

func (idx *TwigIndexer) GetAllTwigFunctions() ([]TwigFunction, error) {
	return idx.twigFunctionIndex.GetAllValues()
}

func (idx *TwigIndexer) GetTwigFunction(name string) ([]TwigFunction, error) {
	return idx.twigFunctionIndex.GetValues(name)
}

func (idx *TwigIndexer) TwigFunctionDeprecation(
	name string,
) (bool, string, error) {
	functions, err := idx.GetTwigFunction(name)
	if err != nil || len(functions) == 0 {
		return false, "", err
	}
	message := ""
	for _, function := range functions {
		if !function.Deprecated {
			return false, "", nil
		}
		if message == "" {
			message = function.Deprecation
		}
	}
	return true, message, nil
}

func (idx *TwigIndexer) GetTwigFilter(name string) ([]TwigFilter, error) {
	return idx.twigFilterIndex.GetValues(name)
}

func (idx *TwigIndexer) TwigFilterDeprecation(
	name string,
) (bool, string, error) {
	filters, err := idx.GetTwigFilter(name)
	if err != nil || len(filters) == 0 {
		return false, "", err
	}
	message := ""
	for _, filter := range filters {
		if !filter.Deprecated {
			return false, "", nil
		}
		if message == "" {
			message = filter.Deprecation
		}
	}
	return true, message, nil
}

func (idx *TwigIndexer) GetAllTwigFilters() ([]TwigFilter, error) {
	values, err := idx.twigFilterIndex.GetAllValues()
	if err != nil {
		return nil, err
	}

	values = append(values, TwigFilter{Name: "raw", Usage: "raw()"})

	return values, nil
}

func (idx *TwigIndexer) GetTwigTest(name string) ([]TwigTest, error) {
	if idx == nil || idx.twigTestIndex == nil || name == "" {
		return nil, nil
	}
	return idx.twigTestIndex.GetValues(name)
}

func (idx *TwigIndexer) GetAllTwigTests() ([]TwigTest, error) {
	if idx == nil || idx.twigTestIndex == nil {
		return nil, nil
	}
	return idx.twigTestIndex.GetAllValues()
}

func (idx *TwigIndexer) TwigTestDeprecation(
	name string,
) (bool, string, error) {
	tests, err := idx.GetTwigTest(name)
	if err != nil || len(tests) == 0 {
		return false, "", err
	}
	message := ""
	for _, test := range tests {
		if !test.Deprecated {
			return false, "", nil
		}
		if message == "" {
			message = test.Deprecation
		}
	}
	return true, message, nil
}

func (idx *TwigIndexer) GetTwigOperator(
	name string,
) ([]TwigOperator, error) {
	if idx == nil || idx.twigOperatorIndex == nil || name == "" {
		return nil, nil
	}
	return idx.twigOperatorIndex.GetValues(name)
}

func (idx *TwigIndexer) GetAllTwigOperators() ([]TwigOperator, error) {
	if idx == nil || idx.twigOperatorIndex == nil {
		return nil, nil
	}
	return idx.twigOperatorIndex.GetAllValues()
}

func (idx *TwigIndexer) GetTwigTag(name string) ([]TwigTag, error) {
	if idx == nil || idx.twigTagIndex == nil || name == "" {
		return nil, nil
	}
	return idx.twigTagIndex.GetValues(name)
}

func (idx *TwigIndexer) GetAllTwigTags() ([]TwigTag, error) {
	if idx == nil || idx.twigTagIndex == nil {
		return nil, nil
	}
	return idx.twigTagIndex.GetAllValues()
}

// ResolveTwigTag resolves an opening tag or its conventional end-prefixed
// closing form. Exact tag declarations win over end-tag normalization.
func (idx *TwigIndexer) ResolveTwigTag(
	name string,
) ([]TwigTag, string, error) {
	tags, err := idx.GetTwigTag(name)
	if err != nil || len(tags) > 0 {
		return tags, name, err
	}
	if len(name) <= 3 || !strings.EqualFold(name[:3], "end") {
		return nil, name, nil
	}
	base := name[3:]
	tags, err = idx.GetTwigTag(base)
	return tags, base, err
}

func (idx *TwigIndexer) TwigTagDeprecation(
	name string,
) (bool, string, error) {
	tags, _, err := idx.ResolveTwigTag(name)
	if err != nil || len(tags) == 0 {
		return false, "", err
	}
	message := ""
	for _, tag := range tags {
		if !tag.Deprecated {
			return false, "", nil
		}
		if message == "" {
			message = tag.Deprecation
		}
	}
	return true, message, nil
}

func (idx *TwigIndexer) GetAllGlobals() ([]Global, error) {
	if idx == nil {
		return nil, nil
	}
	catalogs, err := idx.twigGlobalIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	var result []Global
	for _, catalog := range catalogs {
		result = append(result, catalog.Globals...)
	}
	_, serviceIndex := idx.dependencies()
	if serviceIndex != nil {
		for _, value := range serviceIndex.GetTwigGlobals() {
			global := Global{
				Name:      value.Name,
				Value:     value.Value,
				ServiceID: value.ServiceID,
				File:      value.Path,
				Range:     value.Range,
				Source:    ContainerGlobalSource,
			}
			if global.ServiceID == "" && global.Value != "" {
				global.Type = "string"
			}
			result = append(result, global)
		}
	}
	for index := range result {
		result[index] = idx.resolveGlobal(result[index])
	}
	result = uniqueGlobals(result)
	sort.Slice(result, func(left, right int) bool {
		if comparison := compareFold(
			result[left].Name,
			result[right].Name,
		); comparison != 0 {
			return comparison < 0
		}
		if result[left].Source != result[right].Source {
			return result[left].Source < result[right].Source
		}
		if result[left].File != result[right].File {
			return result[left].File < result[right].File
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func (idx *TwigIndexer) GetGlobals(name string) ([]Global, error) {
	globals, err := idx.GetAllGlobals()
	if err != nil {
		return nil, err
	}
	var result []Global
	for _, global := range globals {
		if global.Name == name {
			result = append(result, global)
		}
	}
	return result, nil
}

func (idx *TwigIndexer) resolveGlobal(global Global) Global {
	if global.Type != "" || global.ServiceID == "" {
		return global
	}
	_, serviceIndex := idx.dependencies()
	if serviceIndex == nil {
		return global
	}
	current := global.ServiceID
	visited := make(map[string]struct{})
	for depth := 0; depth < 16 && current != ""; depth++ {
		key := strings.ToLower(current)
		if _, exists := visited[key]; exists {
			break
		}
		visited[key] = struct{}{}
		service, found, err := serviceIndex.GetServiceByID(current)
		if err != nil || !found {
			break
		}
		if service.Class != "" {
			global.Type = strings.TrimPrefix(service.Class, "\\")
			break
		}
		current = service.AliasTarget
	}
	return global
}

func (idx *TwigIndexer) dependencies() (
	*php.PHPIndex,
	*symfony.ServiceIndex,
) {
	idx.dependenciesMu.RLock()
	defer idx.dependenciesMu.RUnlock()
	return idx.phpIndex, idx.serviceIndex
}

func (idx *TwigIndexer) GetMacros(template string) ([]Macro, error) {
	catalogs, err := idx.twigMacroIndex.GetValues(
		normalizeMacroTemplate(template),
	)
	if err != nil {
		return nil, err
	}
	var result []Macro
	for _, catalog := range catalogs {
		result = append(result, catalog.Macros...)
	}
	sortMacros(result)
	return result, nil
}

func (idx *TwigIndexer) FindMacro(
	template,
	name string,
) ([]Macro, error) {
	macros, err := idx.GetMacros(template)
	if err != nil {
		return nil, err
	}
	var result []Macro
	for _, macro := range macros {
		if strings.EqualFold(macro.Name, name) {
			result = append(result, macro)
		}
	}
	return result, nil
}

func (idx *TwigIndexer) GetMacroUsages(
	template,
	name string,
) ([]MacroUsage, error) {
	records, err := idx.twigMacroUsageIndex.GetValues(
		macroUsageKey(template, name),
	)
	if err != nil {
		return nil, err
	}
	var result []MacroUsage
	for _, record := range records {
		result = append(result, record.Usages...)
	}
	sortMacroUsages(result)
	return result, nil
}

func (idx *TwigIndexer) GetAllMacros() ([]Macro, error) {
	catalogs, err := idx.twigMacroIndex.GetAllValues()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var result []Macro
	for _, catalog := range catalogs {
		for _, macro := range catalog.Macros {
			key := macro.FilePath + ":" + macro.NameRange.String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, macro)
		}
	}
	sortMacros(result)
	return result, nil
}

// GetTemplateVariables resolves the effective input contract for one or more
// Twig template names. Contracts are followed through extends and
// context-preserving include/embed edges, with explicitly supplied child
// parameters removed.
func (idx *TwigIndexer) GetTemplateVariables(
	templates ...string,
) ([]TemplateVariable, error) {
	if idx == nil || idx.templateVariableIndex == nil {
		return nil, nil
	}
	var result []TemplateVariable
	seenVariables := make(map[string]struct{})
	visiting := make(map[string]struct{})
	visited := make(map[string]struct{})
	var visit func(string, map[string]struct{}, int) error
	visit = func(
		template string,
		omitted map[string]struct{},
		depth int,
	) error {
		if depth > 32 {
			return nil
		}
		template = normalizeMacroTemplate(template)
		if template == "" {
			return nil
		}
		omittedNames := make([]string, 0, len(omitted))
		for name := range omitted {
			omittedNames = append(omittedNames, name)
		}
		sort.Strings(omittedNames)
		state := template + "\x00" + strings.Join(omittedNames, "\x00")
		if _, complete := visited[state]; complete {
			return nil
		}
		if _, cycle := visiting[state]; cycle {
			return nil
		}
		visiting[state] = struct{}{}
		defer delete(visiting, state)

		catalogs, err := idx.templateVariableIndex.GetValues(template)
		if err != nil {
			return err
		}
		for _, catalog := range catalogs {
			for _, variable := range catalog.Variables {
				if _, excluded := omitted[variable.Name]; excluded {
					continue
				}
				key := variable.FilePath + "\x00" +
					variable.Range.String() + "\x00" + variable.Name
				if _, exists := seenVariables[key]; exists {
					continue
				}
				seenVariables[key] = struct{}{}
				result = append(result, variable)
			}
			for _, dependency := range catalog.Dependencies {
				if !dependency.Propagate {
					continue
				}
				childOmitted := make(
					map[string]struct{},
					len(omitted)+len(dependency.Provided),
				)
				for name := range omitted {
					childOmitted[name] = struct{}{}
				}
				for _, name := range dependency.Provided {
					childOmitted[name] = struct{}{}
				}
				if err := visit(
					dependency.Template,
					childOmitted,
					depth+1,
				); err != nil {
					return err
				}
			}
		}
		visited[state] = struct{}{}
		return nil
	}
	for _, template := range templates {
		if err := visit(template, nil, 0); err != nil {
			return nil, err
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Name != result[right].Name {
			return compareFold(
				result[left].Name,
				result[right].Name,
			) < 0
		}
		if result[left].FilePath != result[right].FilePath {
			return result[left].FilePath < result[right].FilePath
		}
		return result[left].Range.Start < result[right].Range.Start
	})
	return result, nil
}

func (idx *TwigIndexer) FindTemplateVariable(
	name string,
	templates ...string,
) ([]TemplateVariable, error) {
	variables, err := idx.GetTemplateVariables(templates...)
	if err != nil {
		return nil, err
	}
	var result []TemplateVariable
	for _, variable := range variables {
		if variable.Name == name {
			result = append(result, variable)
		}
	}
	return result, nil
}

func (idx *TwigIndexer) GetTwigFilesByRelPath(relPath string) ([]TwigFile, error) {
	return idx.twigFileIndex.GetValues(relPath)
}

func (idx *TwigIndexer) GetTwigFileByPath(
	path string,
) (TwigFile, bool, error) {
	if idx == nil || idx.twigFileIndex == nil || path == "" {
		return TwigFile{}, false, nil
	}
	files, err := idx.twigFileIndex.GetValuesByPath(path)
	if err != nil {
		return TwigFile{}, false, err
	}
	for _, file := range files {
		if filepath.Clean(file.Path) == filepath.Clean(path) {
			return file, true, nil
		}
	}
	return TwigFile{}, false, nil
}

func (idx *TwigIndexer) GetTwigBlockHashes(blockName string) ([]TwigBlockHash, error) {
	return idx.twigBlockHashIndex.GetValues(blockName)
}

func (idx *TwigIndexer) GetTwigBlockHashByPath(blockName, relativePath string) (*TwigBlockHash, error) {
	blockhashes, err := idx.twigBlockHashIndex.GetValues(blockName)
	if err != nil {
		return nil, err
	}

	for _, hash := range blockhashes {
		if hash.RelativePath == relativePath {
			return &hash, nil
		}
	}

	return nil, nil
}

func (idx *TwigIndexer) GetAllTwigBlockHashes() ([]TwigBlockHash, error) {
	return idx.twigBlockHashIndex.GetAllValues()
}
