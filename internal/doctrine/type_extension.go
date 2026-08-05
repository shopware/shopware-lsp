package doctrine

import (
	"strings"

	"github.com/shopware/shopware-lsp/internal/php/inference"
	"github.com/shopware/shopware-lsp/internal/php/resolver"
	"github.com/shopware/shopware-lsp/internal/php/semantic"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

const (
	objectRepositoryClass = "Doctrine\\Persistence\\ObjectRepository"
	queryBuilderClass     = "Doctrine\\ORM\\QueryBuilder"
)

var (
	doctrineObjectRepositoryType = types.Named(objectRepositoryClass)
	doctrineManagerTypes         = []types.Type{
		types.Named("Doctrine\\Persistence\\ObjectManager"),
		types.Named("Doctrine\\Persistence\\ManagerRegistry"),
		types.Named("Doctrine\\Common\\Persistence\\ObjectManager"),
		types.Named("Doctrine\\Common\\Persistence\\ManagerRegistry"),
		types.Named("Doctrine\\ORM\\EntityManagerInterface"),
		types.Named("Doctrine\\ORM\\EntityManager"),
	}
)

type PHPTypeExtension struct {
	index *Index
}

func NewPHPTypeExtension(index *Index) *PHPTypeExtension {
	return &PHPTypeExtension{index: index}
}

func (extension *PHPTypeExtension) InferCall(
	context inference.CallContext,
) (semantic.TypeFact, bool) {
	if extension == nil {
		return semantic.TypeFact{}, false
	}
	lowerName := canonicalDoctrineMethod(context.Name)
	if lowerName == "" {
		return semantic.TypeFact{}, false
	}
	if isManagerExtensionMethod(lowerName) {
		if entity, found := extension.managerEntityArgument(context); found {
			switch lowerName {
			case "getrepository":
				return doctrineFact(
					extension.repositoryType(entity),
					"Doctrine repository entity",
				), true
			case "find":
				return doctrineFact(
					types.Nullable(entity),
					"Doctrine object manager result",
				), true
			case "getreference", "getpartialreference":
				return doctrineFact(
					entity,
					"Doctrine entity reference",
				), true
			case "getclassmetadata":
				return doctrineFact(
					types.Named(
						"Doctrine\\Persistence\\Mapping\\ClassMetadata",
						entity,
					),
					"Doctrine class metadata",
				), true
			}
		}
	}
	if !isRepositoryExtensionMethod(lowerName) {
		return semantic.TypeFact{}, false
	}
	entity, found := extension.repositoryEntity(context)
	if !found {
		return semantic.TypeFact{}, false
	}
	switch lowerName {
	case "find", "findoneby":
		return doctrineFact(
			types.Nullable(entity),
			"Doctrine repository result",
		), true
	case "findall", "findby":
		return doctrineFact(
			types.List(entity),
			"Doctrine repository result collection",
		), true
	case "count":
		return doctrineFact(types.Int(), "Doctrine repository count"), true
	case "createquerybuilder":
		return doctrineFact(
			types.Named(queryBuilderClass, entity),
			"Doctrine entity query builder",
		), true
	default:
		if extension.hasConcreteRepositoryMethod(context) {
			return semantic.TypeFact{}, false
		}
		switch lowerName {
		case "findoneby*":
			return doctrineFact(
				types.Nullable(entity),
				"Doctrine magic repository result",
			), true
		case "findby*":
			return doctrineFact(
				types.List(entity),
				"Doctrine magic repository result collection",
			), true
		case "countby*":
			return doctrineFact(
				types.Int(),
				"Doctrine magic repository count",
			), true
		default:
			return semantic.TypeFact{}, false
		}
	}
}

func canonicalDoctrineMethod(name string) string {
	switch len(name) {
	case 4:
		if strings.EqualFold(name, "find") {
			return "find"
		}
	case 5:
		if strings.EqualFold(name, "count") {
			return "count"
		}
	case 6:
		if strings.EqualFold(name, "findby") {
			return "findby"
		}
	case 7:
		if strings.EqualFold(name, "findall") {
			return "findall"
		}
	case 9:
		if strings.EqualFold(name, "findoneby") {
			return "findoneby"
		}
	case 12:
		if strings.EqualFold(name, "getreference") {
			return "getreference"
		}
	case 13:
		if strings.EqualFold(name, "getrepository") {
			return "getrepository"
		}
	case 16:
		if strings.EqualFold(name, "getclassmetadata") {
			return "getclassmetadata"
		}
	case 18:
		if strings.EqualFold(name, "createquerybuilder") {
			return "createquerybuilder"
		}
	case 19:
		if strings.EqualFold(name, "getpartialreference") {
			return "getpartialreference"
		}
	}
	switch {
	case len(name) > len("findoneby") &&
		strings.EqualFold(name[:len("findoneby")], "findoneby"):
		return "findoneby*"
	case len(name) > len("findby") &&
		strings.EqualFold(name[:len("findby")], "findby"):
		return "findby*"
	case len(name) > len("countby") &&
		strings.EqualFold(name[:len("countby")], "countby"):
		return "countby*"
	default:
		return ""
	}
}

func isManagerExtensionMethod(name string) bool {
	switch name {
	case "getrepository":
		return true
	case "find", "getreference", "getpartialreference", "getclassmetadata":
		return true
	default:
		return false
	}
}

func isRepositoryExtensionMethod(name string) bool {
	switch name {
	case "find", "findoneby", "findall", "findby", "count",
		"createquerybuilder":
		return true
	}
	return len(name) > len("findoneby") && strings.HasPrefix(name, "findoneby") ||
		len(name) > len("findby") && strings.HasPrefix(name, "findby") ||
		len(name) > len("countby") && strings.HasPrefix(name, "countby")
}

func (extension *PHPTypeExtension) repositoryType(entity types.Type) types.Type {
	if extension != nil && extension.index != nil &&
		entity.Kind() == types.ObjectKind && entity.Name() != "" {
		if model, found, err := extension.index.Model(entity.Name()); err == nil && found && model.Repository != "" {
			return types.Named(model.Repository)
		}
	}
	return types.Named(objectRepositoryClass, entity)
}

func (extension *PHPTypeExtension) hasConcreteRepositoryMethod(
	context inference.CallContext,
) bool {
	if context.Snapshot == nil ||
		context.Receiver.Kind() != types.ObjectKind {
		return false
	}
	members := (resolver.MemberResolver{
		Snapshot: context.Snapshot,
	}).Methods(context.Receiver, context.Name)
	for _, member := range members {
		owner, found := context.Snapshot.Symbol(member.Symbol.Container)
		if !found {
			continue
		}
		switch strings.ToLower(owner.FullyQualified) {
		case "doctrine\\persistence\\objectrepository",
			"doctrine\\common\\persistence\\objectrepository",
			"doctrine\\orm\\entityrepository",
			"doctrine\\bundle\\doctrinebundle\\repository\\serviceentityrepository":
			continue
		default:
			return true
		}
	}
	return false
}

func (extension *PHPTypeExtension) managerEntityArgument(
	context inference.CallContext,
) (types.Type, bool) {
	if len(context.Arguments) == 0 || !isDoctrineManager(context) {
		return types.Unknown(), false
	}
	argument := context.Arguments[0].Type
	if argument.Kind() == types.ClassStringKind &&
		len(argument.Arguments()) != 0 {
		entity := argument.Arguments()[0]
		return entity, entity.Kind() == types.ObjectKind &&
			entity.Name() != ""
	}
	if argument.Kind() != types.LiteralStringKind ||
		extension.index == nil {
		return types.Unknown(), false
	}
	className, found, err := extension.index.ResolveModelName(argument.Name())
	if err != nil || !found {
		return types.Unknown(), false
	}
	return types.Named(className), true
}

func isDoctrineManager(context inference.CallContext) bool {
	if context.Snapshot == nil || context.Receiver.IsUnknown() {
		return false
	}
	relations := context.Snapshot.Relations()
	for _, managerType := range doctrineManagerTypes {
		if relations.IsSubtype(context.Receiver, managerType) {
			return true
		}
	}
	return false
}

func (extension *PHPTypeExtension) repositoryEntity(
	context inference.CallContext,
) (types.Type, bool) {
	if context.Receiver.Kind() == types.ObjectKind {
		arguments := context.Receiver.Arguments()
		if len(arguments) != 0 && isObjectRepositoryType(context) {
			entity := arguments[0]
			if entity.Kind() == types.ObjectKind && entity.Name() != "" {
				return entity, true
			}
		}
		if extension.index != nil && context.Receiver.Name() != "" {
			model, found, err := extension.index.ModelForRepository(
				context.Receiver.Name(),
			)
			if err == nil && found {
				return types.Named(model.Class), true
			}
		}
	}
	return types.Unknown(), false
}

func isObjectRepositoryType(context inference.CallContext) bool {
	if context.Receiver.Kind() != types.ObjectKind {
		return false
	}
	switch strings.ToLower(context.Receiver.Name()) {
	case "doctrine\\persistence\\objectrepository",
		"doctrine\\common\\persistence\\objectrepository",
		"doctrine\\orm\\entityrepository",
		"doctrine\\bundle\\doctrinebundle\\repository\\serviceentityrepository":
		return true
	}
	if context.Snapshot == nil {
		return false
	}
	return context.Snapshot.Relations().IsSubtype(
		context.Receiver,
		doctrineObjectRepositoryType,
	)
}

func doctrineFact(value types.Type, reason string) semantic.TypeFact {
	return semantic.TypeFact{
		Type:       value,
		Confidence: semantic.DeclaredConfidence,
		Source:     semantic.FrameworkSource,
		Reason:     reason,
	}
}
