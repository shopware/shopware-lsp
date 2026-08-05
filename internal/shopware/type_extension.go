// Package shopware contains Shopware-specific semantic extensions layered on
// top of the framework-neutral PHP engine.
package shopware

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

var (
	systemConfigServiceType = types.Named(
		"Shopware\\Core\\System\\SystemConfig\\SystemConfigService",
	)
	entityCollectionType = types.Named(
		"Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityCollection",
	)
	entityType = types.Named(
		"Shopware\\Core\\Framework\\DataAbstractionLayer\\Entity",
	)
	entitySearchResultType = types.Named(
		"Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\EntitySearchResult",
	)
	entityRepositoryType = types.Named(
		"Shopware\\Core\\Framework\\DataAbstractionLayer\\EntityRepository",
	)
	featureType = types.Named(
		"Shopware\\Core\\Framework\\Feature",
	)
	idSearchResultType = types.Named(
		"Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\IdSearchResult",
	)
	aggregationResultCollectionType = types.Named(
		"Shopware\\Core\\Framework\\DataAbstractionLayer\\Search\\AggregationResult\\AggregationResultCollection",
	)
)

type PHPTypeExtension struct{}

func NewPHPTypeExtension() *PHPTypeExtension {
	return &PHPTypeExtension{}
}

func (e *PHPTypeExtension) InferCall(
	context inference.CallContext,
) (semantic.TypeFact, bool) {
	lowerName := canonicalShopwareMethod(context.Name)
	if lowerName == "" {
		return semantic.TypeFact{}, false
	}
	if isSystemConfigMethod(lowerName) {
		if result, ok := systemConfigType(context, lowerName); ok {
			return frameworkFact(result, "Shopware system config"), true
		}
	}
	if isCollectionMethod(lowerName) {
		if result, ok := collectionType(context, lowerName); ok {
			return frameworkFact(result, "Shopware DAL collection"), true
		}
	}
	if isRepositoryMethod(lowerName) {
		if result, ok := repositoryType(context, lowerName); ok {
			return frameworkFact(result, "Shopware DAL repository"), true
		}
	}
	if lowerName == "isactive" && isReceiver(context, featureType) {
		return frameworkFact(types.Bool(), "Shopware feature flag"), true
	}
	return semantic.TypeFact{}, false
}

func canonicalShopwareMethod(name string) string {
	switch len(name) {
	case 3:
		if strings.EqualFold(name, "get") {
			return "get"
		}
		if strings.EqualFold(name, "set") {
			return "set"
		}
	case 4:
		if strings.EqualFold(name, "last") {
			return "last"
		}
		if strings.EqualFold(name, "sort") {
			return "sort"
		}
	case 5:
		if strings.EqualFold(name, "first") {
			return "first"
		}
		if strings.EqualFold(name, "slice") {
			return "slice"
		}
		if strings.EqualFold(name, "count") {
			return "count"
		}
	case 6:
		if strings.EqualFold(name, "getint") {
			return "getint"
		}
		if strings.EqualFold(name, "delete") {
			return "delete"
		}
		if strings.EqualFold(name, "filter") {
			return "filter"
		}
		if strings.EqualFold(name, "search") {
			return "search"
		}
	case 7:
		if strings.EqualFold(name, "getbool") {
			return "getbool"
		}
	case 8:
		if strings.EqualFold(name, "getfloat") {
			return "getfloat"
		}
		if strings.EqualFold(name, "isactive") {
			return "isactive"
		}
	case 9:
		if strings.EqualFold(name, "getstring") {
			return "getstring"
		}
		if strings.EqualFold(name, "getdomain") {
			return "getdomain"
		}
		if strings.EqualFold(name, "searchids") {
			return "searchids"
		}
		if strings.EqualFold(name, "aggregate") {
			return "aggregate"
		}
	case 11:
		if strings.EqualFold(name, "getelements") {
			return "getelements"
		}
	}
	return ""
}

func isSystemConfigMethod(name string) bool {
	switch name {
	case "get", "getint", "getstring", "getfloat", "getbool", "getdomain",
		"set", "delete":
		return true
	default:
		return false
	}
}

func isCollectionMethod(name string) bool {
	switch name {
	case "first", "last", "get", "getelements", "filter", "sort", "slice",
		"count":
		return true
	default:
		return false
	}
}

func isRepositoryMethod(name string) bool {
	switch name {
	case "search", "searchids", "aggregate":
		return true
	default:
		return false
	}
}

func systemConfigType(
	context inference.CallContext,
	name string,
) (types.Type, bool) {
	if !isReceiver(context, systemConfigServiceType) {
		return types.Unknown(), false
	}
	switch name {
	case "get":
		return types.Mixed(), true
	case "getint":
		return types.Int(), true
	case "getstring":
		return types.String(), true
	case "getfloat":
		return types.Float(), true
	case "getbool":
		return types.Bool(), true
	case "getdomain":
		return types.Array(types.String(), types.Mixed()), true
	case "set", "delete":
		return types.Void(), true
	default:
		return types.Unknown(), false
	}
}

func repositoryType(
	context inference.CallContext,
	name string,
) (types.Type, bool) {
	if !isReceiver(context, entityRepositoryType) {
		return types.Unknown(), false
	}
	entityCollection := types.Object()
	if args := context.Receiver.Arguments(); len(args) > 0 {
		entityCollection = args[0]
	}
	switch name {
	case "search":
		return types.Named(entitySearchResultType.Name(), entityCollection), true
	case "searchids":
		return idSearchResultType, true
	case "aggregate":
		return aggregationResultCollectionType, true
	default:
		return types.Unknown(), false
	}
}

func collectionType(
	context inference.CallContext,
	name string,
) (types.Type, bool) {
	if !isReceiver(context, entityCollectionType) &&
		!isReceiver(context, entitySearchResultType) {
		return types.Unknown(), false
	}
	element := collectionElementType(context)
	switch name {
	case "first", "last", "get":
		return types.Nullable(element), true
	case "getelements":
		return types.Array(types.ArrayKey(), element), true
	case "filter", "sort", "slice":
		return context.Receiver, true
	case "count":
		return types.Int(), true
	default:
		return types.Unknown(), false
	}
}

func collectionElementType(context inference.CallContext) types.Type {
	if context.Receiver.Kind() == types.UnionKind {
		relations := types.Relations{}
		if context.Snapshot != nil {
			relations = context.Snapshot.Relations()
		}
		elements := types.NewJoiner(relations, types.Never())
		for index := 0; index < context.Receiver.ArgumentCount(); index++ {
			arm := context
			arm.Receiver = context.Receiver.Argument(index)
			elements.Add(collectionElementType(arm))
		}
		return elements.Value()
	}
	if context.Snapshot == nil {
		return directCollectionElementType(context.Receiver)
	}
	receiver := context.Receiver
	if context.Snapshot.Relations().IsSubtype(receiver, entitySearchResultType) &&
		receiver.ArgumentCount() > 0 {
		receiver = receiver.Argument(0)
	}
	if projected, ok := context.Snapshot.AsSupertype(
		receiver,
		entityCollectionType.Name(),
	); ok {
		if projected.ArgumentCount() > 0 {
			return projected.Argument(0)
		}
		// A raw EntityCollection still carries its declared template bound.
		// Returning object here loses the fact that every element is a Struct
		// and causes invalid addExtension diagnostics after dynamic creation.
		return entityType
	}
	return directCollectionElementType(receiver)
}

func directCollectionElementType(receiver types.Type) types.Type {
	if receiver.ArgumentCount() > 0 {
		return receiver.Argument(0)
	}
	return types.Object()
}

func isReceiver(context inference.CallContext, target types.Type) bool {
	if context.Receiver.Kind() == types.ObjectKind &&
		strings.EqualFold(context.Receiver.Name(), target.Name()) {
		return true
	}
	return context.Snapshot != nil &&
		context.Snapshot.Relations().IsSubtype(context.Receiver, target)
}

func frameworkFact(value types.Type, reason string) semantic.TypeFact {
	return semantic.TypeFact{
		Type:       value,
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.FrameworkSource,
		Reason:     reason,
	}
}
