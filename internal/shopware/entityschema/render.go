package entityschema

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

func CompleteSpec(spec EntitySpec) EntitySpec {
	namespace := strings.Trim(spec.Namespace, `\ `)
	base := spec.ClassName
	if spec.DefinitionClass == "" {
		spec.DefinitionClass = qualify(namespace, base+"Definition")
	}
	if spec.EntityClass == "" {
		spec.EntityClass = qualify(namespace, base+"Entity")
	}
	if spec.CollectionClass == "" {
		spec.CollectionClass = qualify(namespace, base+"Collection")
	}
	for index := range spec.Fields {
		field := &spec.Fields[index]
		if field.ID == "" {
			field.ID = fmt.Sprintf("field-%d", index+1)
		}
		if field.TargetDefinitionClass != "" && field.TargetEntityName == "" {
			field.TargetEntityName = inferEntityName(field.TargetDefinitionClass)
		}
		if field.Kind == FieldID {
			field.PropertyName = "id"
			field.StorageName = "id"
			field.Required = true
			field.Primary = true
			field.Editable = true
		}
		if field.Kind == FieldAutoIncrement {
			field.PropertyName = "autoIncrement"
			field.StorageName = "auto_increment"
			field.Required = true
			field.Editable = true
		}
		if field.Kind == FieldVersion {
			field.PropertyName = "versionId"
			field.StorageName = "version_id"
			field.Required = true
			field.Primary = true
			field.Editable = true
			if field.MigrationDefault == "" {
				field.MigrationDefault = "UNHEX('0fa91ce3e96a4bc2be4bd9ce752c3425')"
			}
		}
		if field.Kind == FieldReferenceVersion && field.StorageName == "" && field.TargetEntityName != "" {
			field.StorageName = field.TargetEntityName + "_version_id"
			field.PropertyName = camelizeStorageName(field.StorageName)
		}
		if field.Kind == FieldCreatedAt {
			field.PropertyName = "createdAt"
			field.StorageName = "created_at"
			field.Required = true
			field.Editable = true
			if field.MigrationDefault == "" {
				field.MigrationDefault = "CURRENT_TIMESTAMP(3)"
			}
		}
		if field.Kind == FieldUpdatedAt {
			field.PropertyName = "updatedAt"
			field.StorageName = "updated_at"
			field.Required = false
			field.Editable = true
		}
		if field.Kind == FieldManyToOne || field.Kind == FieldOneToOne {
			if !field.UsesExistingColumn && field.ForeignKeyPropertyName == "" {
				field.ForeignKeyPropertyName = field.PropertyName + "Id"
			}
			if field.ReferenceField == "" {
				field.ReferenceField = "id"
			}
			if field.ReferenceStorageName == "" {
				field.ReferenceStorageName = "id"
			}
		}
		if field.Kind == FieldOneToMany {
			if field.ReferenceStorageName == "" {
				field.ReferenceStorageName = "id"
			}
			if field.SourceColumn == "" {
				field.SourceColumn = "id"
			}
		}
		if field.Kind == FieldManyToMany {
			if field.ReferenceField == "" {
				field.ReferenceField = "id"
			}
			if field.SourceColumn == "" {
				field.SourceColumn = "id"
			}
		}
	}
	return spec
}

func RenderDefinition(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	imports := newImportTable(definitionImports(spec))
	var fields []string
	for _, field := range spec.Fields {
		rendered, err := renderDefinitionField(field, imports)
		if err != nil {
			return "", err
		}
		fields = append(fields, rendered...)
	}
	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(strings.Trim(spec.Namespace, `\`))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(spec.DefinitionClass))
	builder.WriteString(" extends EntityDefinition\n{\n")
	builder.WriteString("    final public const ENTITY_NAME = '")
	builder.WriteString(escapePHPSingle(spec.EntityName))
	builder.WriteString("';\n\n")
	builder.WriteString("    public function getEntityName(): string\n    {\n        return self::ENTITY_NAME;\n    }\n\n")
	builder.WriteString("    public function getEntityClass(): string\n    {\n        return ")
	builder.WriteString(imports.Ref(spec.EntityClass))
	builder.WriteString("::class;\n    }\n\n")
	builder.WriteString("    public function getCollectionClass(): string\n    {\n        return ")
	builder.WriteString(imports.Ref(spec.CollectionClass))
	builder.WriteString("::class;\n    }\n\n")
	builder.WriteString("    protected function defineFields(): FieldCollection\n    {\n        return new FieldCollection([\n")
	for _, field := range fields {
		lines := strings.Split(strings.TrimSpace(field), "\n")
		for _, line := range lines {
			builder.WriteString("            ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("        ]);\n    }\n}\n")
	return builder.String(), nil
}

func RenderEntity(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	imports := newImportTable(entityImports(spec))
	var properties, methods []string
	for _, field := range spec.Fields {
		fieldProperties, fieldMethods, err := renderEntityField(field, imports)
		if err != nil {
			return "", err
		}
		properties = append(properties, fieldProperties...)
		methods = append(methods, fieldMethods...)
	}
	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(strings.Trim(spec.Namespace, `\`))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(spec.EntityClass))
	builder.WriteString(" extends Entity\n{\n    use EntityIdTrait;\n")
	for _, property := range properties {
		builder.WriteString("\n    ")
		builder.WriteString(property)
		builder.WriteByte('\n')
	}
	for _, method := range methods {
		builder.WriteString("\n")
		for _, line := range strings.Split(method, "\n") {
			builder.WriteString("    ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("}\n")
	return builder.String(), nil
}

func RenderCollection(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	return fmt.Sprintf(`<?php declare(strict_types=1);

namespace %s;

use Shopware\Core\Framework\DataAbstractionLayer\EntityCollection;

/**
 * @extends EntityCollection<%s>
 */
class %s extends EntityCollection
{
    public function getApiAlias(): string
    {
        return '%s_collection';
    }

    protected function getExpectedClass(): string
    {
        return %s::class;
    }
}
`, strings.Trim(spec.Namespace, `\`), ShortClass(spec.EntityClass),
		ShortClass(spec.CollectionClass), escapePHPSingle(spec.EntityName),
		ShortClass(spec.EntityClass)), nil
}

func RenderMigration(namespace, className string, timestamp int64, statements []string) string {
	var body strings.Builder
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		body.WriteString("        $connection->executeStatement(<<<'SQL'\n")
		for _, line := range strings.Split(statement, "\n") {
			body.WriteString("            ")
			body.WriteString(line)
			body.WriteByte('\n')
		}
		body.WriteString("            SQL);\n")
	}
	return fmt.Sprintf(`<?php declare(strict_types=1);

namespace %s\Migration;

use Doctrine\DBAL\Connection;
use Shopware\Core\Framework\Migration\MigrationStep;

class %s extends MigrationStep
{
    public function getCreationTimestamp(): int
    {
        return %d;
    }

    public function update(Connection $connection): void
    {
%s    }
}
`, strings.Trim(namespace, `\`), className, timestamp, body.String())
}

func renderDefinitionField(field FieldSpec, imports *importTable) ([]string, error) {
	if field.Kind == FieldLocked {
		if strings.TrimSpace(field.Raw) == "" {
			return nil, nil
		}
		return []string{strings.TrimSpace(field.Raw)}, nil
	}
	flags := func(values ...string) string {
		var active []string
		for _, value := range values {
			if value != "" {
				active = append(active, value)
			}
		}
		if len(active) == 0 {
			return ""
		}
		return "->addFlags(" + strings.Join(active, ", ") + ")"
	}
	required := ""
	if field.Required {
		required = "new Required()"
	}
	apiAware := ""
	if field.APIAware {
		apiAware = "new ApiAware()"
	}
	ranking := ""
	if field.SearchRanking > 0 {
		ranking = fmt.Sprintf("new SearchRanking(%s)", strconv.FormatFloat(field.SearchRanking, 'g', -1, 64))
	}
	storage := quotePHP(field.StorageName)
	property := quotePHP(field.PropertyName)
	fieldFlagValues := func(values ...string) []string {
		result := append([]string(nil), values...)
		if field.Primary && field.Kind != FieldID && field.Kind != FieldVersion {
			result = append(result, "new PrimaryKey()")
		}
		return append(result, field.PreservedFlags...)
	}
	associationFlagValues := func(values ...string) []string {
		return append(values, field.AssociationFlags...)
	}
	fieldExpression := func(base string, values ...string) string {
		return withModifiers(base, field.ModifiersBeforeFlags, flags(fieldFlagValues(values...)...), field.ModifiersAfterFlags)
	}
	associationExpression := func(base string, values ...string) string {
		return withModifiers(base, field.AssociationBeforeFlags, flags(associationFlagValues(values...)...), field.AssociationAfterFlags)
	}
	associationAPI := ""
	if field.AssociationAPIAware {
		associationAPI = "new ApiAware()"
	}
	associationRanking := ""
	if field.AssociationSearchRank > 0 {
		associationRanking = fmt.Sprintf("new SearchRanking(%s)", strconv.FormatFloat(field.AssociationSearchRank, 'g', -1, 64))
	}
	switch field.Kind {
	case FieldID:
		return []string{fieldExpression("(new IdField('id', 'id'))", "new Required()", "new PrimaryKey()") + ","}, nil
	case FieldString:
		arguments := storage + ", " + property
		if field.MaxLength > 0 && field.MaxLength != 255 {
			arguments += ", " + strconv.Itoa(field.MaxLength)
		}
		return []string{fieldExpression("(new StringField("+arguments+"))", required, apiAware, ranking) + ","}, nil
	case FieldLongText:
		return []string{fieldExpression("(new LongTextField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldInt:
		arguments := storage + ", " + property
		if field.Min != nil || field.Max != nil {
			arguments += ", " + nullableInt(field.Min) + ", " + nullableInt(field.Max)
		}
		return []string{fieldExpression("(new IntField("+arguments+"))", required, apiAware) + ","}, nil
	case FieldFloat:
		return []string{fieldExpression("(new FloatField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldBool:
		return []string{fieldExpression("(new BoolField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldDate:
		return []string{fieldExpression("(new DateField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldDateTime:
		return []string{fieldExpression("(new DateTimeField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldJSON:
		return []string{fieldExpression("(new JsonField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldList:
		arguments := storage + ", " + property
		if field.ElementTypeClass != "" {
			arguments += ", " + imports.Ref(field.ElementTypeClass) + "::class"
		}
		return []string{fieldExpression("(new ListField("+arguments+"))", required, apiAware) + ","}, nil
	case FieldObject:
		return []string{fieldExpression("(new ObjectField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldBlob:
		return []string{fieldExpression("(new BlobField("+storage+", "+property+"))", required, apiAware) + ","}, nil
	case FieldAutoIncrement:
		return []string{fieldExpression("(new AutoIncrementField())", apiAware) + ","}, nil
	case FieldCreatedAt:
		return []string{fieldExpression("(new CreatedAtField())") + ","}, nil
	case FieldUpdatedAt:
		return []string{fieldExpression("(new UpdatedAtField())") + ","}, nil
	case FieldVersion:
		return []string{fieldExpression("(new VersionField())", apiAware) + ","}, nil
	case FieldReferenceVersion:
		target := imports.Ref(field.TargetDefinitionClass)
		return []string{fieldExpression("(new ReferenceVersionField("+target+"::class, "+storage+"))", required, apiAware) + ","}, nil
	case FieldManyToOne, FieldOneToOne:
		target := imports.Ref(field.TargetDefinitionClass)
		var result []string
		if !field.UsesExistingColumn {
			result = append(result, fieldExpression("(new FkField("+storage+", "+quotePHP(field.ForeignKeyPropertyName)+", "+target+"::class))", required, apiAware)+",")
		}
		var association string
		if field.Kind == FieldOneToOne {
			association = "new OneToOneAssociationField(" + property + ", " + storage + ", " + quotePHP(defaultString(field.ReferenceField, "id")) + ", " + target + "::class, false)"
		} else {
			association = "new ManyToOneAssociationField(" + property + ", " + storage + ", " + target + "::class, " + quotePHP(defaultString(field.ReferenceField, "id")) + ", false)"
		}
		deleteFlag := ""
		switch field.DeleteBehavior {
		case DeleteCascade:
			deleteFlag = "new CascadeDelete()"
		case DeleteSetNull:
			deleteFlag = "new SetNullOnDelete()"
		case DeleteRestrict:
			deleteFlag = "new RestrictDelete()"
		}
		association = associationExpression("("+association+")", deleteFlag, associationAPI, associationRanking)
		return append(result, association+","), nil
	case FieldOneToMany:
		target := imports.Ref(field.TargetDefinitionClass)
		arguments := property + ", " + target + "::class, " + quotePHP(field.ReferenceStorageName)
		if field.SourceColumn != "" && field.SourceColumn != "id" {
			arguments += ", " + quotePHP(field.SourceColumn)
		}
		expression := "new OneToManyAssociationField(" + arguments + ")"
		deleteFlag := ""
		switch field.DeleteBehavior {
		case DeleteCascade:
			deleteFlag = "new CascadeDelete()"
		case DeleteSetNull:
			deleteFlag = "new SetNullOnDelete()"
		case DeleteRestrict:
			deleteFlag = "new RestrictDelete()"
		}
		expression = associationExpression("("+expression+")", deleteFlag, associationAPI, associationRanking)
		return []string{expression + ","}, nil
	case FieldManyToMany:
		target := imports.Ref(field.TargetDefinitionClass)
		mapping := imports.Ref(field.MappingDefinitionClass)
		expression := "new ManyToManyAssociationField(" + property + ", " + target + "::class, " + mapping + "::class, " + quotePHP(field.MappingLocalColumn) + ", " + quotePHP(field.MappingReferenceColumn) + ", " + quotePHP(defaultString(field.SourceColumn, "id")) + ", " + quotePHP(defaultString(field.ReferenceField, "id")) + ")"
		return []string{associationExpression("("+expression+")", associationAPI, associationRanking) + ","}, nil
	default:
		return nil, fmt.Errorf("unsupported field kind %q", field.Kind)
	}
}

func renderEntityField(field FieldSpec, imports *importTable) ([]string, []string, error) {
	if field.Kind == FieldID || field.Kind == FieldVersion || field.Kind == FieldReferenceVersion ||
		field.Kind == FieldCreatedAt || field.Kind == FieldUpdatedAt || field.Kind == FieldLocked {
		return nil, nil, nil
	}
	if field.Kind == FieldManyToOne || field.Kind == FieldOneToOne {
		fkType := "string"
		fkDefault := ""
		fkReturn := fkType
		if !field.Required {
			fkType = "?string"
			fkReturn = "?string"
			fkDefault = " = null"
		}
		target := imports.Ref(field.TargetEntityClass)
		properties := []string{"protected ?" + target + " $" + field.PropertyName + " = null;"}
		methods := accessors(field.PropertyName, "?"+target, false)
		if !field.UsesExistingColumn {
			properties = append([]string{"protected " + fkType + " $" + field.ForeignKeyPropertyName + fkDefault + ";"}, properties...)
			methods = append(accessors(field.ForeignKeyPropertyName, fkReturn, false), methods...)
		}
		return properties, methods, nil
	}
	if field.Kind == FieldOneToMany || field.Kind == FieldManyToMany {
		target := imports.Ref(field.TargetCollectionClass)
		return []string{"protected ?" + target + " $" + field.PropertyName + " = null;"}, accessors(field.PropertyName, "?"+target, false), nil
	}
	phpType, boolean, err := entityPHPType(field.Kind)
	if err != nil {
		return nil, nil, err
	}
	defaultValue := ""
	returnType := phpType
	if !field.Required {
		phpType = "?" + phpType
		returnType = phpType
		defaultValue = " = null"
	}
	property := "protected " + phpType + " $" + field.PropertyName + defaultValue + ";"
	return []string{property}, accessors(field.PropertyName, returnType, boolean), nil
}

func accessors(property, phpType string, boolean bool) []string {
	method := "get" + exported(property)
	if boolean {
		method = "is" + exported(property)
	}
	getter := fmt.Sprintf("public function %s(): %s\n{\n    return $this->%s;\n}", method, phpType, property)
	setter := fmt.Sprintf("public function set%s(%s $%s): void\n{\n    $this->%s = $%s;\n}", exported(property), phpType, property, property, property)
	return []string{getter, setter}
}

func entityPHPType(kind FieldKind) (string, bool, error) {
	switch kind {
	case FieldString, FieldLongText:
		return "string", false, nil
	case FieldBlob:
		return "object", false, nil
	case FieldInt, FieldAutoIncrement:
		return "int", false, nil
	case FieldFloat:
		return "float", false, nil
	case FieldBool:
		return "bool", true, nil
	case FieldDate, FieldDateTime:
		return `\DateTimeInterface`, false, nil
	case FieldJSON, FieldList, FieldObject:
		return "array", false, nil
	default:
		return "", false, fmt.Errorf("field kind %q has no PHP entity type", kind)
	}
}

func definitionImports(spec EntitySpec) []string {
	imports := []string{
		`Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`,
		`Shopware\Core\Framework\DataAbstractionLayer\FieldCollection`,
		spec.EntityClass, spec.CollectionClass,
	}
	fieldClasses := map[FieldKind]string{
		FieldID: `Shopware\Core\Framework\DataAbstractionLayer\Field\IdField`, FieldString: `Shopware\Core\Framework\DataAbstractionLayer\Field\StringField`,
		FieldLongText: `Shopware\Core\Framework\DataAbstractionLayer\Field\LongTextField`, FieldInt: `Shopware\Core\Framework\DataAbstractionLayer\Field\IntField`,
		FieldFloat: `Shopware\Core\Framework\DataAbstractionLayer\Field\FloatField`, FieldBool: `Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField`,
		FieldDate: `Shopware\Core\Framework\DataAbstractionLayer\Field\DateField`, FieldDateTime: `Shopware\Core\Framework\DataAbstractionLayer\Field\DateTimeField`,
		FieldJSON: `Shopware\Core\Framework\DataAbstractionLayer\Field\JsonField`, FieldList: `Shopware\Core\Framework\DataAbstractionLayer\Field\ListField`,
		FieldObject: `Shopware\Core\Framework\DataAbstractionLayer\Field\ObjectField`, FieldBlob: `Shopware\Core\Framework\DataAbstractionLayer\Field\BlobField`,
		FieldAutoIncrement: `Shopware\Core\Framework\DataAbstractionLayer\Field\AutoIncrementField`, FieldCreatedAt: `Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedAtField`,
		FieldUpdatedAt: `Shopware\Core\Framework\DataAbstractionLayer\Field\UpdatedAtField`, FieldVersion: `Shopware\Core\Framework\DataAbstractionLayer\Field\VersionField`,
		FieldReferenceVersion: `Shopware\Core\Framework\DataAbstractionLayer\Field\ReferenceVersionField`,
		FieldManyToOne:        `Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField`, FieldOneToOne: `Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField`,
		FieldOneToMany: `Shopware\Core\Framework\DataAbstractionLayer\Field\OneToManyAssociationField`, FieldManyToMany: `Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToManyAssociationField`,
	}
	need := make(map[string]struct{})
	for _, field := range spec.Fields {
		if class := fieldClasses[field.Kind]; class != "" {
			need[class] = struct{}{}
		}
		if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && !field.UsesExistingColumn {
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\FkField`] = struct{}{}
			need[field.TargetDefinitionClass] = struct{}{}
		}
		if field.Kind == FieldOneToMany || field.Kind == FieldManyToOne || field.Kind == FieldOneToOne {
			need[field.TargetDefinitionClass] = struct{}{}
			switch field.DeleteBehavior {
			case DeleteCascade:
				need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\CascadeDelete`] = struct{}{}
			case DeleteSetNull:
				need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SetNullOnDelete`] = struct{}{}
			case DeleteRestrict:
				need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\RestrictDelete`] = struct{}{}
			}
		}
		if field.Kind == FieldManyToMany {
			need[field.TargetDefinitionClass] = struct{}{}
			need[field.MappingDefinitionClass] = struct{}{}
		}
		if field.Kind == FieldReferenceVersion {
			need[field.TargetDefinitionClass] = struct{}{}
		}
		if field.Kind == FieldList && field.ElementTypeClass != "" {
			need[field.ElementTypeClass] = struct{}{}
		}
		if field.Required || field.Kind == FieldID {
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required`] = struct{}{}
		}
		if field.Kind == FieldID || (field.Primary && field.Kind != FieldVersion) {
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey`] = struct{}{}
		}
		if field.APIAware || field.AssociationAPIAware {
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware`] = struct{}{}
		}
		if field.SearchRanking > 0 || field.AssociationSearchRank > 0 {
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SearchRanking`] = struct{}{}
		}
	}
	for class := range need {
		imports = append(imports, class)
	}
	return imports
}

func entityImports(spec EntitySpec) []string {
	imports := []string{`Shopware\Core\Framework\DataAbstractionLayer\Entity`, `Shopware\Core\Framework\DataAbstractionLayer\EntityIdTrait`}
	for _, field := range spec.Fields {
		if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && field.TargetEntityClass != "" {
			imports = append(imports, field.TargetEntityClass)
		}
		if (field.Kind == FieldOneToMany || field.Kind == FieldManyToMany) && field.TargetCollectionClass != "" {
			imports = append(imports, field.TargetCollectionClass)
		}
	}
	return imports
}

type importTable struct {
	classes    []string
	shortCount map[string]int
}

func newImportTable(classes []string) *importTable {
	seen := make(map[string]struct{})
	result := &importTable{shortCount: make(map[string]int)}
	for _, class := range classes {
		class = strings.Trim(class, `\ `)
		if class == "" {
			continue
		}
		if _, duplicate := seen[class]; duplicate {
			continue
		}
		seen[class] = struct{}{}
		result.classes = append(result.classes, class)
		result.shortCount[strings.ToLower(ShortClass(class))]++
	}
	sort.Strings(result.classes)
	return result
}

func (t *importTable) Ref(class string) string {
	class = strings.Trim(class, `\ `)
	if t.shortCount[strings.ToLower(ShortClass(class))] > 1 {
		return `\` + class
	}
	return ShortClass(class)
}

func (t *importTable) Render() string {
	var builder strings.Builder
	for _, class := range t.classes {
		if t.shortCount[strings.ToLower(ShortClass(class))] > 1 {
			continue
		}
		builder.WriteString("use ")
		builder.WriteString(class)
		builder.WriteString(";\n")
	}
	return builder.String()
}

func qualify(namespace, class string) string {
	if namespace == "" {
		return class
	}
	return namespace + `\` + class
}

func exported(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func quotePHP(value string) string { return "'" + escapePHPSingle(value) + "'" }
func escapePHPSingle(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `'`, `\'`)
}
func nullableInt(value *int) string {
	if value == nil {
		return "null"
	}
	return strconv.Itoa(*value)
}
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func inferEntityName(definitionClass string) string {
	name := strings.TrimSuffix(ShortClass(definitionClass), "Definition")
	var builder strings.Builder
	for index, current := range name {
		if unicode.IsUpper(current) && index > 0 {
			builder.WriteByte('_')
		}
		builder.WriteRune(unicode.ToLower(current))
	}
	return builder.String()
}

func withModifiers(base string, before []string, flagSuffix string, after []string) string {
	var builder strings.Builder
	builder.WriteString(base)
	for _, modifier := range before {
		builder.WriteString(strings.TrimSpace(modifier))
	}
	builder.WriteString(flagSuffix)
	for _, modifier := range after {
		builder.WriteString(strings.TrimSpace(modifier))
	}
	return builder.String()
}
