package diagnostics

import (
	"context"
	"fmt"
	"strings"

	"github.com/shopware/shopware-lsp/internal/lsp"
	"github.com/shopware/shopware-lsp/internal/lsp/protocol"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
)

const (
	apiClassRenameCode        lsp.DiagnosticID = "shopware.migration.api.class.rename"
	apiMethodRenameCode       lsp.DiagnosticID = "shopware.migration.api.method.rename"
	apiStaticMethodRenameCode lsp.DiagnosticID = "shopware.migration.api.static_method.rename"
	apiConstantRenameCode     lsp.DiagnosticID = "shopware.migration.api.constant.rename"
	apiPropertyMigrationCode  lsp.DiagnosticID = "shopware.migration.api.property"
	apiExceptionFactoryCode   lsp.DiagnosticID = "shopware.migration.api.exception_factory"
)

type shopwareMigrationSince struct {
	minor int
	patch int
}

func (v shopwareMigrationSince) active(analyzer *ShopwareMigrationAnalyzer) bool {
	return analyzer != nil && analyzer.version.AtLeast(6, v.minor, v.patch)
}

func (v shopwareMigrationSince) label() string {
	if v.patch == 0 {
		return fmt.Sprintf("6.%d", v.minor)
	}
	return fmt.Sprintf("6.%d.%d", v.minor, v.patch)
}

type apiClassMigration struct {
	since shopwareMigrationSince
	from  string
	to    string
}

type apiMemberMigration struct {
	since    shopwareMigrationSince
	owner    string
	from     string
	to       string
	newOwner string
}

type apiFactoryMigration struct {
	since  shopwareMigrationSince
	from   string
	to     string
	method string
}

var apiClassMigrations = []apiClassMigration{
	{shopwareMigrationSince{5, 0}, "League\\Flysystem\\FilesystemInterface", "League\\Flysystem\\FilesystemOperator"},
	{shopwareMigrationSince{5, 0}, "League\\Flysystem\\AdapterInterface", "League\\Flysystem\\FilesystemAdapter"},
	{shopwareMigrationSince{5, 0}, "League\\Flysystem\\Memory\\MemoryAdapter", "League\\Flysystem\\InMemory\\InMemoryFilesystemAdapter"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Adapter\\Asset\\ThemeAssetPackage", "Shopware\\Storefront\\Theme\\ThemeAssetPackage"},
	{shopwareMigrationSince{5, 0}, "Maltyxx\\ImagesGenerator\\ImagesGeneratorProvider", "bheller\\ImagesGenerator\\ImagesGeneratorProvider"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Event\\BusinessEventInterface", "Shopware\\Core\\Framework\\Event\\FlowEventAware"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Event\\MailActionInterface", "Shopware\\Core\\Framework\\Event\\MailAware"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Log\\LogAwareBusinessEventInterface", "Shopware\\Core\\Framework\\Log\\LogAware"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Event\\ProductExportContentTypeEvent", "Shopware\\Core\\Content\\ProductExport\\Event\\ProductExportContentTypeEvent"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Page\\Product\\Review\\MatrixElement", "Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\MatrixElement"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Page\\Product\\Review\\RatingMatrix", "Shopware\\Core\\Content\\Product\\SalesChannel\\Review\\RatingMatrix"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Storefront\\Page\\Address\\Listing\\AddressListingCriteriaEvent", "Shopware\\Core\\Checkout\\Customer\\Event\\AddressListingCriteriaEvent"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Administration\\Service\\AdminOrderCartService", "Shopware\\Core\\Checkout\\Cart\\ApiOrderCartService"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\System\\User\\Service\\UserProvisioner", "Shopware\\Core\\Maintenance\\User\\Service\\UserProvisioner"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepositoryInterface", "Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\System\\SalesChannel\\Entity\\SalesChannelRepositoryInterface", "Shopware\\Core\\System\\SalesChannel\\Entity\\SalesChannelRepository"},
	{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Adapter\\Console\\ShopwareStyle", "Symfony\\Component\\Console\\Style\\SymfonyStyle"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\DataAbstractionLayer\\Event\\BeforeDeleteEvent", "Shopware\\Core\\Framework\\DataAbstractionLayer\\Event\\EntityDeleteEvent"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\Api\\Exception\\ExceptionFailedException", "Shopware\\Core\\Framework\\Api\\Exception\\ExpectationFailedException"},
}

var apiMethodMigrations = buildAPIMethodMigrations()
var apiMethodMigrationsByName = indexAPIMemberMigrations(apiMethodMigrations)

func buildAPIMethodMigrations() []apiMemberMigration {
	result := []apiMemberMigration{
		{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Framework\\Adapter\\Twig\\EntityTemplateLoader", "clearInternalCache", "reset", ""},
		{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping", "getDefault", "getDefaultValue", ""},
		{shopwareMigrationSince{5, 0}, "Shopware\\Core\\Content\\ImportExport\\Processing\\Mapping\\Mapping", "getMappedDefault", "getDefaultValue", ""},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Framework\\Indexing\\IndexerOffset", "setNextDefinition", "selectNextDefinition", ""},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Framework\\Indexing\\IndexerOffset", "setNextLanguage", "selectNextLanguage", ""},
	}
	flysystemClasses := []string{
		"League\\Flysystem\\FilesystemOperator",
		"League\\Flysystem\\Filesystem",
		"League\\Flysystem\\FilesystemInterface",
		"League\\Flysystem\\FilesystemAdapter",
	}
	flysystemMethods := [][2]string{
		{"rename", "move"},
		{"createDir", "createDirectory"},
		{"deleteDir", "deleteDirectory"},
		{"update", "write"},
		{"updateStream", "writeStream"},
		{"put", "write"},
		{"putStream", "writeStream"},
		{"getTimestamp", "lastModified"},
		{"has", "fileExists"},
		{"getMimetype", "mimeType"},
		{"getSize", "fileSize"},
		{"getVisibility", "visibility"},
	}
	for _, class := range flysystemClasses {
		for _, method := range flysystemMethods {
			result = append(result, apiMemberMigration{
				since: shopwareMigrationSince{5, 0},
				owner: class,
				from:  method[0],
				to:    method[1],
			})
		}
	}
	return result
}

var apiStaticMethodMigrations = []apiMemberMigration{
	{
		since:    shopwareMigrationSince{6, 0},
		owner:    "Shopware\\Core\\Framework\\DataAbstractionLayer\\FieldSerializer\\JsonFieldSerializer",
		from:     "encodeJson",
		to:       "encode",
		newOwner: "Shopware\\Core\\Framework\\Util\\Json",
	},
}
var apiStaticMethodMigrationsByName = indexAPIMemberMigrations(apiStaticMethodMigrations)

var apiPropertyMigrations = []apiMemberMigration{
	{
		since: shopwareMigrationSince{5, 0},
		owner: "Shopware\\Core\\Content\\Flow\\Dispatching\\FlowState",
		from:  "sequenceId",
		to:    "getSequenceId",
	},
}
var apiPropertyMigrationsByName = indexAPIMemberMigrations(apiPropertyMigrations)

var apiFactoryMigrations = []apiFactoryMigration{
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\Routing\\Exception\\InvalidRequestParameterException", "Shopware\\Core\\Framework\\Routing\\RoutingException", "invalidRequestParameter"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\Routing\\Exception\\MissingRequestParameterException", "Shopware\\Core\\Framework\\Routing\\RoutingException", "missingRequestParameter"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\Routing\\Exception\\LanguageNotFoundException", "Shopware\\Core\\Framework\\Routing\\RoutingException", "languageNotFound"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\DataAbstractionLayer\\Exception\\InvalidSerializerFieldException", "Shopware\\Core\\Framework\\DataAbstractionLayer\\DataAbstractionLayerException", "invalidSerializerField"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Framework\\DataAbstractionLayer\\Exception\\VersionMergeAlreadyLockedException", "Shopware\\Core\\Framework\\DataAbstractionLayer\\DataAbstractionLayerException", "versionMergeAlreadyLocked"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Exception\\UnsupportedElasticsearchDefinitionException", "Shopware\\Elasticsearch\\ElasticsearchException", "unsupportedElasticsearchDefinition"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Exception\\ElasticsearchIndexingException", "Shopware\\Elasticsearch\\ElasticsearchException", "indexingError"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Exception\\ServerNotAvailableException", "Shopware\\Elasticsearch\\ElasticsearchException", "serverNotAvailable"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Content\\ProductExport\\Exception\\EmptyExportException", "Shopware\\Core\\Content\\ProductExport\\ProductExportException", "productExportNotFound"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Content\\ProductExport\\Exception\\RenderFooterException", "Shopware\\Core\\Content\\ProductExport\\ProductExportException", "renderFooterException"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Content\\ProductExport\\Exception\\RenderHeaderException", "Shopware\\Core\\Content\\ProductExport\\ProductExportException", "renderHeaderException"},
	{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Content\\ProductExport\\Exception\\RenderProductException", "Shopware\\Core\\Content\\ProductExport\\ProductExportException", "renderProductException"},
}

var apiConstantMigrations = buildAPIConstantMigrations()
var apiConstantMigrationsByName = indexAPIMemberMigrations(apiConstantMigrations)

func buildAPIConstantMigrations() []apiMemberMigration {
	result := []apiMemberMigration{
		{shopwareMigrationSince{6, 0}, "Shopware\\Core\\Checkout\\Cart", "CHECKOUT_ORDER_PLACED", "CHECKOUT_ORDER_PLACED", "Shopware\\Core\\Framework\\Event\\BusinessEvents"},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition", "KEYWORD_FIELD", "KEYWORD_FIELD", "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition"},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition", "BOOLEAN_FIELD", "BOOLEAN_FIELD", "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition"},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition", "FLOAT_FIELD", "FLOAT_FIELD", "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition"},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition", "INT_FIELD", "INT_FIELD", "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition"},
		{shopwareMigrationSince{6, 0}, "Shopware\\Elasticsearch\\Product\\ElasticsearchProductDefinition", "SEARCH_FIELD", "SEARCH_FIELD", "Shopware\\Elasticsearch\\Framework\\AbstractElasticsearchDefinition"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Content\\MailTemplate\\Subscriber\\MailSendSubscriberConfig", "MAIL_CONFIG_EXTENSION", "MAIL_CONFIG_EXTENSION", "Shopware\\Core\\Content\\Flow\\Dispatching\\Action\\SendMailAction"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Content\\MailTemplate\\Subscriber\\MailSendSubscriberConfig", "ACTION_NAME", "ACTION_NAME", "Shopware\\Core\\Content\\Flow\\Dispatching\\Action\\SendMailAction"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Content\\MailTemplate\\MailTemplateActions", "MAIL_TEMPLATE_MAIL_SEND_ACTION", "ACTION_NAME", "Shopware\\Core\\Content\\Flow\\Dispatching\\Action\\SendMailAction"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber", "STATE_LOGGED_IN", "STATE_LOGGED_IN", "Shopware\\Core\\Framework\\Adapter\\Cache\\CacheStateSubscriber"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber", "STATE_CART_FILLED", "STATE_CART_FILLED", "Shopware\\Core\\Framework\\Adapter\\Cache\\CacheStateSubscriber"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber", "CURRENCY_COOKIE", "CURRENCY_COOKIE", "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber", "CONTEXT_CACHE_COOKIE", "CONTEXT_CACHE_COOKIE", "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber", "SYSTEM_STATE_COOKIE", "SYSTEM_STATE_COOKIE", "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator"},
		{shopwareMigrationSince{7, 0}, "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\CacheResponseSubscriber", "INVALIDATION_STATES_HEADER", "INVALIDATION_STATES_HEADER", "Shopware\\Core\\Framework\\Adapter\\Cache\\Http\\HttpCacheKeyGenerator"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Cart\\AbstractCartPersister", "PERSIST_CART_ERROR_PERMISSION", "PERSIST_CART_ERROR", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Cart\\Delivery\\DeliveryProcessor", "SKIP_DELIVERY_PRICE_RECALCULATION", "SKIP_PRODUCT_STOCK_VALIDATION", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Cart\\Delivery\\DeliveryProcessor", "SKIP_DELIVERY_TAX_RECALCULATION", "SKIP_DELIVERY_TAX_RECALCULATION", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector", "SKIP_PROMOTION", "SKIP_PROMOTION", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector", "SKIP_AUTOMATIC_PROMOTIONS", "SKIP_AUTOMATIC_PROMOTIONS", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector", "PIN_MANUAL_PROMOTIONS", "PIN_MANUAL_PROMOTIONS", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Checkout\\Promotion\\Cart\\PromotionCollector", "PIN_AUTOMATIC_PROMOTIONS", "PIN_AUTOMATIC_PROMOTIONS", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor", "ALLOW_PRODUCT_PRICE_OVERWRITES", "ALLOW_PRODUCT_PRICE_OVERWRITES", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor", "ALLOW_PRODUCT_LABEL_OVERWRITES", "ALLOW_PRODUCT_LABEL_OVERWRITES", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor", "SKIP_PRODUCT_RECALCULATION", "SKIP_PRODUCT_RECALCULATION", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor", "SKIP_PRODUCT_STOCK_VALIDATION", "SKIP_PRODUCT_STOCK_VALIDATION", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
		{shopwareMigrationSince{8, 0}, "Shopware\\Core\\Content\\Product\\Cart\\ProductCartProcessor", "KEEP_INACTIVE_PRODUCT", "KEEP_INACTIVE_PRODUCT", "Shopware\\Core\\Checkout\\CheckoutPermissions"},
	}
	for _, class := range []string{
		"League\\Flysystem\\FilesystemOperator",
		"League\\Flysystem\\Filesystem",
		"League\\Flysystem\\FilesystemInterface",
		"League\\Flysystem\\FilesystemAdapter",
	} {
		result = append(result,
			apiMemberMigration{shopwareMigrationSince{5, 0}, class, "VISIBILITY_PUBLIC", "PUBLIC", "League\\Flysystem\\Visibility"},
			apiMemberMigration{shopwareMigrationSince{5, 0}, class, "VISIBILITY_PRIVATE", "PRIVATE", "League\\Flysystem\\Visibility"},
		)
	}
	return result
}

type apiClassOccurrence struct {
	reference     semantic.Reference
	rule          apiClassMigration
	node          *phpsyntax.Node
	inUse         bool
	explicitAlias bool
}

func (p *ShopwareMigrationAnalyzer) apiMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	source string,
) []lsp.Problem {
	result := p.apiClassMigrationProblems(ctx, root, document, source)
	result = append(result, p.apiMemberMigrationProblems(ctx, root, document, snapshot, source)...)
	result = append(result, p.apiFactoryMigrationProblems(ctx, root, source)...)
	return result
}

func (p *ShopwareMigrationAnalyzer) apiClassMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	source string,
) []lsp.Problem {
	active := make(map[string]apiClassMigration)
	for _, rule := range apiClassMigrations {
		if rule.since.active(p) {
			active[strings.ToLower(rule.from)] = rule
		}
	}
	var occurrences []apiClassOccurrence
	imports := make(map[string]bool)
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind != phpresolver.ClassImport {
				continue
			}
			rule, found := active[strings.ToLower(strings.Trim(imported.Target, "\\"))]
			if !found {
				continue
			}
			rng, found := phpUseTargetRange(declaration, imported.Target, source)
			if !found {
				continue
			}
			occurrences = append(occurrences, apiClassOccurrence{
				reference: semantic.Reference{Name: imported.Target, Range: rng},
				rule:      rule,
				node:      declaration,
				inUse:     true,
				explicitAlias: !strings.EqualFold(
					imported.Alias,
					shortPHPName(imported.Target),
				),
			})
			imports[strings.ToLower(rule.from)] = true
		}
	}
	for _, reference := range document.References {
		if ctx.Err() != nil {
			return nil
		}
		if reference.Kind != semantic.ClassName {
			continue
		}
		rule, found := classMigrationForReference(reference, active)
		if !found || !validSourceRange(reference.Range.Start, reference.Range.End, source) {
			continue
		}
		node := root.NodeAtOffset(reference.Range.Start)
		if node == nil {
			continue
		}
		inUse := ancestorPHPKind(node, phpsyntax.PhpUseDeclaration) != nil
		if inUse {
			imports[strings.ToLower(rule.from)] = true
		}
		occurrences = append(occurrences, apiClassOccurrence{
			reference: reference,
			rule:      rule,
			node:      node,
			inUse:     inUse,
		})
	}
	result := make([]lsp.Problem, 0, len(occurrences))
	for _, occurrence := range occurrences {
		if imports[strings.ToLower(occurrence.rule.from)] && !occurrence.inUse {
			continue
		}
		edits, safe := apiClassMigrationEdits(root, occurrence, occurrences, source)
		replacement := ""
		original := ""
		if len(edits) > 0 {
			replacement = edits[0].Replacement
			original = edits[0].Original
		}
		problem := apiMigrationProblem(
			apiClassRenameCode,
			occurrence.reference.Range,
			occurrence.node,
			occurrence.rule.since,
			"class",
			occurrence.rule.from,
			occurrence.rule.to,
			original,
			replacement,
			safe,
		)
		payload := problem.Payload.(ShopwareMigrationPayload)
		payload.Edits = edits
		problem.Payload = payload
		result = append(result, problem)
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) apiFactoryMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	source string,
) []lsp.Problem {
	resolver := php.NewNameResolver(root)
	var result []lsp.Problem
	for _, creation := range phpquery.ObjectCreations(root) {
		if ctx.Err() != nil {
			return result
		}
		class := strings.Trim(resolver.Resolve(phpquery.ObjectClassName(creation)), "\\")
		for _, rule := range apiFactoryMigrations {
			if !rule.since.active(p) || !strings.EqualFold(class, rule.from) {
				continue
			}
			arguments := phpquery.DirectChild(creation, phpsyntax.PhpArgumentList)
			if arguments == nil {
				break
			}
			rng := creation.RangeTrimmedTrivia()
			if !validSourceRange(rng.Start, rng.End, source) {
				break
			}
			result = append(result, apiMigrationProblem(
				apiExceptionFactoryCode,
				rng,
				creation,
				rule.since,
				"exception-factory",
				"new "+rule.from,
				rule.to+"::"+rule.method,
				source[rng.Start:rng.End],
				"\\"+rule.to+"::"+rule.method+arguments.Text(),
				true,
			))
			break
		}
	}
	return result
}

func apiClassMigrationEdits(
	root *phpsyntax.Node,
	occurrence apiClassOccurrence,
	occurrences []apiClassOccurrence,
	source string,
) ([]ShopwareMigrationEdit, bool) {
	rng := occurrence.reference.Range
	if !validSourceRange(rng.Start, rng.End, source) {
		return nil, false
	}
	original := source[rng.Start:rng.End]
	if !occurrence.inUse {
		return []ShopwareMigrationEdit{{
			Start:       rng.Start,
			End:         rng.End,
			Original:    original,
			Replacement: "\\" + occurrence.rule.to,
		}}, true
	}
	use := ancestorPHPKind(occurrence.node, phpsyntax.PhpUseDeclaration)
	if use == nil || strings.Contains(use.Text(), "{") {
		return nil, false
	}
	edits := []ShopwareMigrationEdit{{
		Start:       rng.Start,
		End:         rng.End,
		Original:    original,
		Replacement: occurrence.rule.to,
	}}
	oldShort := shortPHPName(occurrence.rule.from)
	newShort := shortPHPName(occurrence.rule.to)
	if strings.EqualFold(oldShort, newShort) || occurrence.explicitAlias {
		return edits, true
	}
	if phpImportAliasConflict(root, use, newShort) {
		edits[0].Replacement += " as " + oldShort
		return edits, true
	}
	for _, other := range occurrences {
		if other.inUse || !strings.EqualFold(other.rule.from, occurrence.rule.from) {
			continue
		}
		otherRange := other.reference.Range
		if !validSourceRange(otherRange.Start, otherRange.End, source) {
			return nil, false
		}
		otherOriginal := source[otherRange.Start:otherRange.End]
		otherReplacement := newShort
		if strings.Contains(otherOriginal, "\\") {
			otherReplacement = "\\" + occurrence.rule.to
		}
		edits = append(edits, ShopwareMigrationEdit{
			Start:       otherRange.Start,
			End:         otherRange.End,
			Original:    otherOriginal,
			Replacement: otherReplacement,
		})
	}
	return edits, true
}

func phpImportAliasConflict(
	root *phpsyntax.Node,
	current *phpsyntax.Node,
	alias string,
) bool {
	for _, declaration := range phpquery.UseDeclarations(root) {
		if declaration == current {
			continue
		}
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind == phpresolver.ClassImport && strings.EqualFold(imported.Alias, alias) {
				return true
			}
		}
	}
	return false
}

func phpUseTargetRange(
	declaration *phpsyntax.Node,
	target string,
	source string,
) (phpsyntax.TextRange, bool) {
	if declaration == nil || target == "" {
		return phpsyntax.TextRange{}, false
	}
	text := declaration.Text()
	index := strings.Index(strings.ToLower(text), strings.ToLower(strings.Trim(target, "\\")))
	if index < 0 {
		return phpsyntax.TextRange{}, false
	}
	if index > 0 && text[index-1] == '\\' {
		index--
	}
	start := declaration.Range().Start + uint32(index)
	end := start + uint32(len(strings.Trim(target, "\\")))
	if text[index] == '\\' {
		end++
	}
	if !validSourceRange(start, end, source) {
		return phpsyntax.TextRange{}, false
	}
	return phpsyntax.TextRange{Start: start, End: end}, true
}

func (p *ShopwareMigrationAnalyzer) apiMemberMigrationProblems(
	ctx context.Context,
	root *phpsyntax.Node,
	document *semantic.Document,
	snapshot *semantic.Snapshot,
	source string,
) []lsp.Problem {
	var result []lsp.Problem
	for _, reference := range document.References {
		if ctx.Err() != nil {
			return result
		}
		if reference.Kind != semantic.MemberName ||
			!validSourceRange(reference.Range.Start, reference.Range.End, source) {
			continue
		}
		node := root.NodeAtOffset(reference.Range.Start)
		if node == nil {
			continue
		}
		if reference.TargetKind == semantic.PropertySymbol && !reference.Static {
			if rule, found := p.matchAPIMemberRule(reference, snapshot, apiPropertyMigrationsByName); found {
				result = append(result, apiMigrationProblem(
					apiPropertyMigrationCode,
					reference.Range,
					node,
					rule.since,
					"property",
					rule.owner+"::$"+rule.from,
					rule.owner+"::"+rule.to+"()",
					source[reference.Range.Start:reference.Range.End],
					rule.to+"()",
					!reference.Write,
				))
			}
			continue
		}
		if reference.TargetKind == semantic.MethodSymbol && !reference.Static {
			if rule, found := p.matchAPIMemberRule(reference, snapshot, apiMethodMigrationsByName); found {
				result = append(result, apiMigrationProblem(
					apiMethodRenameCode,
					reference.Range,
					node,
					rule.since,
					"method",
					rule.owner+"::"+rule.from,
					rule.owner+"::"+rule.to,
					source[reference.Range.Start:reference.Range.End],
					rule.to,
					true,
				))
			}
			continue
		}
		if reference.TargetKind == semantic.MethodSymbol && reference.Static {
			if rule, found := p.matchAPIMemberRule(reference, snapshot, apiStaticMethodMigrationsByName); found {
				call := ancestorPHPKind(node, phpsyntax.PhpScopedCall)
				arguments := phpquery.DirectChild(call, phpsyntax.PhpArgumentList)
				if call == nil || arguments == nil {
					continue
				}
				rng := call.RangeTrimmedTrivia()
				result = append(result, apiMigrationProblem(
					apiStaticMethodRenameCode,
					rng,
					call,
					rule.since,
					"static-method",
					rule.owner+"::"+rule.from,
					rule.newOwner+"::"+rule.to,
					source[rng.Start:rng.End],
					"\\"+rule.newOwner+"::"+rule.to+arguments.Text(),
					true,
				))
			}
			continue
		}
		if reference.TargetKind == semantic.ClassConstantSymbol && reference.Static {
			if rule, found := p.matchAPIMemberRule(reference, snapshot, apiConstantMigrationsByName); found {
				access := ancestorPHPScopedAccess(node)
				if access == nil {
					continue
				}
				rng := access.RangeTrimmedTrivia()
				result = append(result, apiMigrationProblem(
					apiConstantRenameCode,
					rng,
					access,
					rule.since,
					"constant",
					rule.owner+"::"+rule.from,
					rule.newOwner+"::"+rule.to,
					source[rng.Start:rng.End],
					"\\"+rule.newOwner+"::"+rule.to,
					true,
				))
			}
		}
	}
	return result
}

func (p *ShopwareMigrationAnalyzer) matchAPIMemberRule(
	reference semantic.Reference,
	snapshot *semantic.Snapshot,
	rulesByName map[string][]apiMemberMigration,
) (apiMemberMigration, bool) {
	for _, rule := range rulesByName[strings.ToLower(reference.Name)] {
		if rule.since.active(p) &&
			phpTypeIsSubtype(reference.Receiver, snapshot, rule.owner) {
			return rule, true
		}
	}
	return apiMemberMigration{}, false
}

func indexAPIMemberMigrations(
	rules []apiMemberMigration,
) map[string][]apiMemberMigration {
	result := make(map[string][]apiMemberMigration)
	for _, rule := range rules {
		key := strings.ToLower(rule.from)
		result[key] = append(result[key], rule)
	}
	return result
}

func apiMigrationProblem(
	code lsp.DiagnosticID,
	rng phpsyntax.TextRange,
	element *phpsyntax.Node,
	since shopwareMigrationSince,
	kind string,
	from string,
	to string,
	original string,
	replacement string,
	safe bool,
) lsp.Problem {
	return lsp.Problem{
		ID:       code,
		Range:    rng,
		Element:  element,
		Message:  "Shopware " + since.label() + ": replace " + from + " with " + to,
		Severity: protocol.DiagnosticSeverityWarning,
		Source:   "shopware-rector",
		Payload: ShopwareMigrationPayload{
			Rule:        "api-rename",
			Kind:        kind,
			Safe:        safe,
			Original:    original,
			Replacement: replacement,
			Start:       rng.Start,
			End:         rng.End,
		},
	}
}

func classMigrationForReference(
	reference semantic.Reference,
	active map[string]apiClassMigration,
) (apiClassMigration, bool) {
	for index := 0; index < reference.QualifiedNameCount(); index++ {
		if rule, found := active[strings.ToLower(strings.Trim(reference.QualifiedNameAt(index), "\\"))]; found {
			return rule, true
		}
	}
	if rule, found := active[strings.ToLower(strings.Trim(reference.Name, "\\"))]; found {
		return rule, true
	}
	return apiClassMigration{}, false
}

func ancestorPHPKind(node *phpsyntax.Node, kind phpsyntax.Kind) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		if current.Kind() == kind {
			return current
		}
	}
	return nil
}

func ancestorPHPScopedAccess(node *phpsyntax.Node) *phpsyntax.Node {
	for current := node; current != nil; current = current.Parent() {
		switch current.Kind() {
		case phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess:
			if current.ChildTokenOfKind(phpsyntax.TkScopeResolution) != nil {
				return current
			}
		}
	}
	return nil
}

func validSourceRange(start, end uint32, source string) bool {
	return start < end && end <= uint32(len(source))
}
