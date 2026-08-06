package entityschema

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	phpparser "github.com/shopware/shopware-lsp/internal/parser/php"
	phpquery "github.com/shopware/shopware-lsp/internal/parser/php/query"
	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	phpresolver "github.com/shopware/shopware-lsp/internal/php/resolver"
)

var (
	importedEntityNamePattern = regexp.MustCompile(`(?m)\bENTITY_NAME\s*=\s*['"]([^'"]+)['"]`)
	importedAcronymBoundary   = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	importedWordBoundary      = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

type RelationTarget struct {
	DefinitionClass string                `json:"definitionClass"`
	EntityClass     string                `json:"entityClass"`
	CollectionClass string                `json:"collectionClass"`
	EntityName      string                `json:"entityName"`
	FileURI         string                `json:"fileUri,omitempty"`
	Fields          []RelationTargetField `json:"fields,omitempty"`
	VersionAware    bool                  `json:"versionAware,omitempty"`
}

type RelationTargetField struct {
	PropertyName string `json:"propertyName"`
	StorageName  string `json:"storageName"`
	Primary      bool   `json:"primary,omitempty"`
}

type RelationLookup func(definitionClass string) (RelationTarget, bool)

func ImportDefinition(source string, lookup RelationLookup) (EntitySpec, error) {
	tree := phpparser.Parse(source)
	if len(tree.Errors) != 0 || tree.Tree == nil || tree.Tree.Root == nil {
		return EntitySpec{}, fmt.Errorf("parse entity definition: PHP source contains syntax errors")
	}
	root := tree.Tree.Root
	var definition *phpsyntax.Node
	for _, class := range phpquery.Classes(root) {
		for _, parent := range phpquery.ClassExtends(class) {
			short := ShortClass(parent)
			if short == "EntityDefinition" {
				definition = class
				break
			}
		}
		if definition != nil {
			break
		}
	}
	if definition == nil {
		return EntitySpec{}, fmt.Errorf("PHP file contains no concrete EntityDefinition")
	}
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	className := phpquery.ClassName(definition)
	baseName := strings.TrimSuffix(className, "Definition")
	resolve := importClassResolver(root)
	spec := EntitySpec{
		Mode: "edit", Namespace: namespace, ClassName: baseName,
		DefinitionClass: qualify(namespace, className),
		EntityClass:     qualify(namespace, baseName+"Entity"),
		CollectionClass: qualify(namespace, baseName+"Collection"),
		CreateMigration: true,
	}
	if match := importedEntityNamePattern.FindStringSubmatch(definition.Text()); len(match) > 1 {
		spec.EntityName = match[1]
	}
	var defineFields *phpsyntax.Node
	for _, method := range phpquery.Methods(definition) {
		switch phpquery.MethodName(method) {
		case "getEntityName":
			if spec.EntityName == "" {
				for _, value := range phpquery.Nodes(method, phpsyntax.PhpString) {
					if literal := phpquery.StringValue(value); literal != "" {
						spec.EntityName = literal
						break
					}
				}
			}
		case "getEntityClass":
			if class := returnedImportedClass(method, resolve); class != "" {
				spec.EntityClass = class
			}
		case "getCollectionClass":
			if class := returnedImportedClass(method, resolve); class != "" {
				spec.CollectionClass = class
			}
		case "defineFields":
			defineFields = method
		}
	}
	if spec.EntityName == "" {
		return EntitySpec{}, fmt.Errorf("entity definition has no literal entity name")
	}
	if defineFields == nil {
		return EntitySpec{}, fmt.Errorf("entity definition has no defineFields method")
	}
	fields, err := importFields(defineFields, resolve, lookup)
	if err != nil {
		return EntitySpec{}, err
	}
	spec.Fields = fields
	return CompleteSpec(spec), nil
}

type importedForeignKey struct {
	field FieldSpec
	raw   string
}

func importFields(method *phpsyntax.Node, resolve func(string) string, lookup RelationLookup) ([]FieldSpec, error) {
	collections := phpquery.ObjectCreations(method, "FieldCollection")
	if len(collections) == 0 {
		return nil, fmt.Errorf("defineFields does not return a literal FieldCollection")
	}
	arrays := phpquery.Arrays(collections[0])
	if len(arrays) == 0 {
		return nil, fmt.Errorf("defineFields FieldCollection has no literal array")
	}
	var fields []FieldSpec
	foreignKeys := make(map[string]importedForeignKey)
	for itemIndex, item := range phpquery.ArrayItems(arrays[0]) {
		value := phpquery.ArrayItemValue(item)
		if value == nil {
			continue
		}
		creations := phpquery.ObjectCreations(value)
		if len(creations) == 0 {
			fields = append(fields, lockedField(itemIndex, item.Text()))
			continue
		}
		creation := creations[0]
		kindName := ShortClass(phpquery.ObjectClassName(creation))
		flags := importedFlags(creations)
		modifiers := importedModifiers(value, creation)
		raw := strings.TrimSpace(item.Text())
		id := fmt.Sprintf("field-%d", itemIndex+1)
		switch kindName {
		case "IdField":
			fields = append(fields, withFieldModifiers(FieldSpec{ID: id, Kind: FieldID, PropertyName: "id", StorageName: "id", Required: true, PreservedFlags: flags.preserved, Editable: true, Raw: raw}, modifiers))
		case "StringField", "LongTextField", "IntField", "FloatField", "BoolField", "DateField", "DateTimeField", "JsonField", "ListField", "ObjectField", "BlobField":
			kind := importedScalarKind(kindName)
			field := FieldSpec{
				ID: id, Kind: kind,
				StorageName:  importedStringArgument(creation, 0),
				PropertyName: importedStringArgument(creation, 1),
				Required:     flags.required, Primary: flags.primary, APIAware: flags.apiAware, SearchRanking: flags.ranking,
				PreservedFlags: flags.preserved,
				Editable:       true, Raw: raw,
			}
			if kind == FieldString {
				field.MaxLength = importedIntArgument(creation, 2, 255)
			}
			if kind == FieldInt {
				field.Min = importedOptionalIntArgument(creation, 2)
				field.Max = importedOptionalIntArgument(creation, 3)
			}
			if kind == FieldList {
				field.ElementTypeClass = resolve(importedClassArgument(creation, 2))
			}
			field = withFieldModifiers(field, modifiers)
			fields = append(fields, field)
		case "AutoIncrementField":
			fields = append(fields, withFieldModifiers(FieldSpec{ID: id, Kind: FieldAutoIncrement, PropertyName: "autoIncrement", StorageName: "auto_increment", Required: true, Primary: flags.primary, APIAware: flags.apiAware, PreservedFlags: flags.preserved, Editable: true, Raw: raw}, modifiers))
		case "CreatedAtField":
			fields = append(fields, withFieldModifiers(FieldSpec{ID: id, Kind: FieldCreatedAt, PropertyName: "createdAt", StorageName: "created_at", Required: true, PreservedFlags: flags.preserved, Editable: true, Raw: raw}, modifiers))
		case "UpdatedAtField":
			fields = append(fields, withFieldModifiers(FieldSpec{ID: id, Kind: FieldUpdatedAt, PropertyName: "updatedAt", StorageName: "updated_at", PreservedFlags: flags.preserved, Editable: true, Raw: raw}, modifiers))
		case "VersionField":
			fields = append(fields, withFieldModifiers(FieldSpec{ID: id, Kind: FieldVersion, PropertyName: "versionId", StorageName: "version_id", Required: true, APIAware: flags.apiAware, PreservedFlags: flags.preserved, Editable: true, Raw: raw}, modifiers))
		case "ReferenceVersionField":
			storage := importedStringArgument(creation, 1)
			targetClass := resolve(importedClassArgument(creation, 0))
			field := FieldSpec{
				ID: id, Kind: FieldReferenceVersion,
				StorageName:           storage,
				PropertyName:          camelizeStorageName(storage),
				TargetDefinitionClass: targetClass,
				Required:              flags.required,
				Primary:               flags.primary,
				APIAware:              flags.apiAware,
				SearchRanking:         flags.ranking,
				PreservedFlags:        flags.preserved,
				Editable:              true,
				Raw:                   raw,
			}
			if target, found := lookupRelation(lookup, targetClass); found {
				enrichRelation(&field, target)
				if field.StorageName == "" {
					field.StorageName = target.EntityName + "_version_id"
					field.PropertyName = camelizeStorageName(field.StorageName)
				}
			}
			fields = append(fields, withFieldModifiers(field, modifiers))
		case "FkField":
			storage := importedStringArgument(creation, 0)
			foreignKeys[storage] = importedForeignKey{field: withFieldModifiers(FieldSpec{
				ID: id, Kind: FieldManyToOne, StorageName: storage,
				ForeignKeyPropertyName: importedStringArgument(creation, 1),
				TargetDefinitionClass:  resolve(importedClassArgument(creation, 2)),
				ReferenceField:         defaultString(importedStringArgument(creation, 3), "id"),
				Required:               flags.required, Primary: flags.primary, APIAware: flags.apiAware,
				PreservedFlags: flags.preserved, Editable: true,
			}, modifiers), raw: raw}
		case "ManyToOneAssociationField":
			storage := importedStringArgument(creation, 1)
			foreignKey, found := foreignKeys[storage]
			if !found {
				fields = append(fields, lockedField(itemIndex, raw))
				continue
			}
			field := foreignKey.field
			field.PropertyName = importedStringArgument(creation, 0)
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 2))
			field.ReferenceField = defaultString(importedStringArgument(creation, 3), "id")
			field.ReferenceStorageName = field.ReferenceField
			field.DeleteBehavior = importedDeleteBehavior(creations)
			associationFlags := importedFlags(creations)
			field.AssociationFlags = associationFlags.preserved
			field.AssociationAPIAware = associationFlags.apiAware
			field.AssociationSearchRank = associationFlags.ranking
			field = withAssociationModifiers(field, modifiers)
			field.Raw = foreignKey.raw + ",\n" + raw
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			fields = append(fields, field)
			delete(foreignKeys, storage)
		case "OneToOneAssociationField":
			storage := importedStringArgument(creation, 1)
			foreignKey, found := foreignKeys[storage]
			if !found {
				field := FieldSpec{
					ID:                    id,
					Kind:                  FieldOneToOne,
					PropertyName:          importedStringArgument(creation, 0),
					StorageName:           storage,
					ReferenceField:        defaultString(importedStringArgument(creation, 2), "id"),
					ReferenceStorageName:  defaultString(importedStringArgument(creation, 2), "id"),
					TargetDefinitionClass: resolve(importedClassArgument(creation, 3)),
					DeleteBehavior:        importedDeleteBehavior(creations),
					AssociationFlags:      flags.preserved,
					AssociationAPIAware:   flags.apiAware,
					AssociationSearchRank: flags.ranking,
					UsesExistingColumn:    true,
					Editable:              true,
					Raw:                   raw,
				}
				field = withAssociationModifiers(field, modifiers)
				if target, targetFound := lookupRelation(lookup, field.TargetDefinitionClass); targetFound {
					enrichRelation(&field, target)
				}
				fields = append(fields, field)
				continue
			}
			field := foreignKey.field
			field.Kind = FieldOneToOne
			field.PropertyName = importedStringArgument(creation, 0)
			field.ReferenceField = defaultString(importedStringArgument(creation, 2), "id")
			field.ReferenceStorageName = field.ReferenceField
			field.TargetDefinitionClass = resolve(importedClassArgument(creation, 3))
			field.DeleteBehavior = importedDeleteBehavior(creations)
			field.AssociationFlags = flags.preserved
			field.AssociationAPIAware = flags.apiAware
			field.AssociationSearchRank = flags.ranking
			field = withAssociationModifiers(field, modifiers)
			field.Raw = foreignKey.raw + ",\n" + raw
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			fields = append(fields, field)
			delete(foreignKeys, storage)
		case "OneToManyAssociationField":
			field := FieldSpec{
				ID: id, Kind: FieldOneToMany,
				PropertyName:          importedStringArgument(creation, 0),
				TargetDefinitionClass: resolve(importedClassArgument(creation, 1)),
				ReferenceStorageName:  importedStringArgument(creation, 2),
				SourceColumn:          defaultString(importedStringArgument(creation, 3), "id"),
				DeleteBehavior:        importedDeleteBehavior(creations),
				AssociationFlags:      flags.preserved,
				AssociationAPIAware:   flags.apiAware,
				AssociationSearchRank: flags.ranking,
				Editable:              true, Raw: raw,
			}
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			field = withAssociationModifiers(field, modifiers)
			fields = append(fields, field)
		case "ManyToManyAssociationField":
			field := FieldSpec{
				ID:                     id,
				Kind:                   FieldManyToMany,
				PropertyName:           importedStringArgument(creation, 0),
				TargetDefinitionClass:  resolve(importedClassArgument(creation, 1)),
				MappingDefinitionClass: resolve(importedClassArgument(creation, 2)),
				MappingLocalColumn:     importedStringArgument(creation, 3),
				MappingReferenceColumn: importedStringArgument(creation, 4),
				SourceColumn:           defaultString(importedStringArgument(creation, 5), "id"),
				ReferenceField:         defaultString(importedStringArgument(creation, 6), "id"),
				AssociationFlags:       flags.preserved,
				AssociationAPIAware:    flags.apiAware,
				AssociationSearchRank:  flags.ranking,
				Editable:               true,
				Raw:                    raw,
			}
			if target, found := lookupRelation(lookup, field.TargetDefinitionClass); found {
				enrichRelation(&field, target)
			}
			field = withAssociationModifiers(field, modifiers)
			fields = append(fields, field)
		default:
			fields = append(fields, lockedField(itemIndex, raw))
		}
	}
	foreignKeyNames := make([]string, 0, len(foreignKeys))
	for storage := range foreignKeys {
		foreignKeyNames = append(foreignKeyNames, storage)
	}
	sort.Strings(foreignKeyNames)
	for _, storage := range foreignKeyNames {
		foreignKey := foreignKeys[storage]
		fields = append(fields, FieldSpec{ID: foreignKey.field.ID, Kind: FieldLocked, Editable: false, Raw: foreignKey.raw})
	}
	return fields, nil
}

func importedScalarKind(name string) FieldKind {
	switch name {
	case "StringField":
		return FieldString
	case "LongTextField":
		return FieldLongText
	case "IntField":
		return FieldInt
	case "FloatField":
		return FieldFloat
	case "BoolField":
		return FieldBool
	case "DateField":
		return FieldDate
	case "DateTimeField":
		return FieldDateTime
	case "JsonField":
		return FieldJSON
	case "ListField":
		return FieldList
	case "ObjectField":
		return FieldObject
	case "BlobField":
		return FieldBlob
	default:
		return FieldLocked
	}
}

type importedFlagSet struct {
	required  bool
	primary   bool
	apiAware  bool
	ranking   float64
	preserved []string
}

type importedModifierSet struct {
	beforeFlags []string
	afterFlags  []string
}

func importedModifiers(value, creation *phpsyntax.Node) importedModifierSet {
	calls := phpquery.Calls(value)
	sort.SliceStable(calls, func(i, j int) bool {
		if calls[i].Range().End != calls[j].Range().End {
			return calls[i].Range().End < calls[j].Range().End
		}
		return calls[i].Range().Start > calls[j].Range().Start
	})
	var result importedModifierSet
	seenFlags := false
	for _, call := range calls {
		if call.Kind() != phpsyntax.PhpMemberCall || call.Range().Start > creation.Range().Start || call.Range().End < creation.Range().End {
			continue
		}
		method := phpquery.CallMethodName(call)
		if method == "addFlags" {
			seenFlags = true
			continue
		}
		suffix := importedCallSuffix(call, method)
		if suffix == "" {
			continue
		}
		if seenFlags {
			result.afterFlags = append(result.afterFlags, suffix)
		} else {
			result.beforeFlags = append(result.beforeFlags, suffix)
		}
	}
	return result
}

func importedCallSuffix(call *phpsyntax.Node, method string) string {
	if method == "" {
		return ""
	}
	text := call.Text()
	index := strings.LastIndex(text, "->"+method)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(text[index:])
}

func withFieldModifiers(field FieldSpec, modifiers importedModifierSet) FieldSpec {
	field.ModifiersBeforeFlags = append([]string(nil), modifiers.beforeFlags...)
	field.ModifiersAfterFlags = append([]string(nil), modifiers.afterFlags...)
	return field
}

func withAssociationModifiers(field FieldSpec, modifiers importedModifierSet) FieldSpec {
	field.AssociationBeforeFlags = append([]string(nil), modifiers.beforeFlags...)
	field.AssociationAfterFlags = append([]string(nil), modifiers.afterFlags...)
	return field
}

func importedFlags(creations []*phpsyntax.Node) importedFlagSet {
	var result importedFlagSet
	for _, creation := range creations[1:] {
		switch ShortClass(phpquery.ObjectClassName(creation)) {
		case "Required":
			result.required = true
		case "PrimaryKey":
			result.primary = true
		case "ApiAware":
			result.apiAware = true
		case "SearchRanking":
			result.ranking = importedFloatArgument(creation, 0, 0)
		case "CascadeDelete", "SetNullOnDelete", "RestrictDelete":
			// Represented structurally by the field kind or relation behavior.
		default:
			result.preserved = append(result.preserved, strings.TrimSpace(creation.Text()))
		}
	}
	return result
}

func camelizeStorageName(value string) string {
	parts := strings.Split(value, "_")
	if len(parts) == 0 {
		return value
	}
	for index := 1; index < len(parts); index++ {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "")
}

func importedDeleteBehavior(creations []*phpsyntax.Node) DeleteBehavior {
	for _, creation := range creations[1:] {
		switch ShortClass(phpquery.ObjectClassName(creation)) {
		case "CascadeDelete":
			return DeleteCascade
		case "SetNullOnDelete":
			return DeleteSetNull
		case "RestrictDelete":
			return DeleteRestrict
		}
	}
	return ""
}

func importedStringArgument(creation *phpsyntax.Node, index int) string {
	return phpquery.StringValue(phpquery.StringArgument(creation, index))
}

func importedClassArgument(creation *phpsyntax.Node, index int) string {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) {
		return ""
	}
	return phpquery.ClassConstantName(arguments[index])
}

func importedIntArgument(creation *phpsyntax.Node, index, fallback int) int {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) {
		return fallback
	}
	value, err := strconv.Atoi(strings.TrimSpace(arguments[index].Text()))
	if err != nil {
		return fallback
	}
	return value
}

func importedFloatArgument(creation *phpsyntax.Node, index int, fallback float64) float64 {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) {
		return fallback
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(arguments[index].Text()), 64)
	if err != nil {
		return fallback
	}
	return value
}

func importedOptionalIntArgument(creation *phpsyntax.Node, index int) *int {
	arguments := phpquery.Arguments(creation)
	if index < 0 || index >= len(arguments) || strings.EqualFold(strings.TrimSpace(arguments[index].Text()), "null") {
		return nil
	}
	value, err := strconv.Atoi(strings.TrimSpace(arguments[index].Text()))
	if err != nil {
		return nil
	}
	return &value
}

func returnedImportedClass(method *phpsyntax.Node, resolve func(string) string) string {
	for _, access := range phpquery.Nodes(method, phpsyntax.PhpScopedAccess, phpsyntax.PhpMemberAccess) {
		if className := phpquery.ClassConstantName(access); className != "" {
			return resolve(className)
		}
	}
	return ""
}

func importClassResolver(root *phpsyntax.Node) func(string) string {
	namespace := strings.Trim(phpquery.Namespace(root), `\`)
	aliases := make(map[string]string)
	for _, declaration := range phpquery.UseDeclarations(root) {
		for _, imported := range phpresolver.ParseUseDeclaration(declaration.Text()) {
			if imported.Kind == phpresolver.ClassImport {
				aliases[strings.ToLower(imported.Alias)] = strings.Trim(imported.Target, `\`)
			}
		}
	}
	return func(name string) string {
		name = strings.Trim(strings.TrimSpace(name), `\`)
		if name == "" {
			return ""
		}
		parts := strings.SplitN(name, `\`, 2)
		if target, found := aliases[strings.ToLower(parts[0])]; found {
			if len(parts) > 1 {
				return target + `\` + parts[1]
			}
			return target
		}
		if namespace != "" {
			return namespace + `\` + name
		}
		return name
	}
}

func lookupRelation(lookup RelationLookup, class string) (RelationTarget, bool) {
	if lookup != nil {
		if target, found := lookup(class); found {
			return target, true
		}
	}
	class = strings.Trim(class, `\`)
	short := ShortClass(class)
	if !strings.HasSuffix(short, "Definition") {
		return RelationTarget{}, false
	}
	base := strings.TrimSuffix(short, "Definition")
	namespace := strings.TrimSuffix(class, short)
	entityName := importedAcronymBoundary.ReplaceAllString(base, `${1}_${2}`)
	entityName = importedWordBoundary.ReplaceAllString(entityName, `${1}_${2}`)
	return RelationTarget{
		DefinitionClass: class,
		EntityClass:     namespace + base + "Entity",
		CollectionClass: namespace + base + "Collection",
		EntityName:      strings.ToLower(entityName),
		Fields:          []RelationTargetField{{PropertyName: "id", StorageName: "id", Primary: true}},
	}, true
}

func enrichRelation(field *FieldSpec, target RelationTarget) {
	field.TargetEntityClass = target.EntityClass
	field.TargetCollectionClass = target.CollectionClass
	field.TargetEntityName = target.EntityName
	if field.Kind == FieldOneToMany {
		return
	}
	if field.ReferenceField == "" {
		field.ReferenceField = "id"
	}
	if field.ReferenceStorageName == "" {
		field.ReferenceStorageName = "id"
	}
	for _, candidate := range target.Fields {
		if candidate.PropertyName == field.ReferenceField || candidate.Primary {
			field.ReferenceStorageName = candidate.StorageName
			if candidate.PropertyName == field.ReferenceField {
				break
			}
		}
	}
}

func lockedField(index int, raw string) FieldSpec {
	return FieldSpec{ID: fmt.Sprintf("locked-%d", index+1), Kind: FieldLocked, Editable: false, Raw: strings.TrimSpace(raw)}
}
