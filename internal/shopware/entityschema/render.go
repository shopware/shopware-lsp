package entityschema

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/shopware/shopware-lsp/internal/php/project"
)

func CompleteSpec(spec EntitySpec) EntitySpec {
	if spec.DefinitionKind == "" {
		spec.DefinitionKind = DefinitionEntity
	}
	namespace := strings.Trim(spec.Namespace, `\ `)
	base := spec.ClassName
	completeDefinitionIdentity(&spec, namespace, base)
	completeBulkExtensions(&spec)
	completeDefinitionBehaviorFields(spec.DefinitionBehavior, "")
	completeHierarchyFields(&spec)
	completeOwnedFields(&spec)
	completeFieldModifications(&spec)
	if spec.DefinitionKind == DefinitionEntity {
		spec = completeTranslationSpec(spec, namespace, base)
		if spec.Translation != nil {
			completeDefinitionBehaviorFields(spec.Translation.DefinitionBehavior, "translation-")
		}
	}
	return spec
}

func completeDefinitionIdentity(spec *EntitySpec, namespace, base string) {
	if spec.DefinitionClass == "" {
		suffix := "Definition"
		switch spec.DefinitionKind {
		case DefinitionExtension:
			suffix = "Extension"
		case DefinitionBulkExtension:
			suffix = "BulkEntityExtension"
		}
		spec.DefinitionClass = qualify(namespace, base+suffix)
	}
	if spec.DefinitionKind == DefinitionExtension || spec.DefinitionKind == DefinitionBulkExtension {
		spec.EntityClass = ""
		spec.CollectionClass = ""
		if spec.Mode != "edit" {
			spec.EntityURI = ""
			spec.CollectionURI = ""
		}
	} else if spec.DefinitionKind == DefinitionMapping && spec.Mode != "edit" {
		// A fresh mapping definition uses the framework's generic mapping entity.
		// Imported mappings may still carry explicit companion classes, which are
		// preserved in edit mode for lossless round-tripping.
		spec.EntityClass = ""
		spec.CollectionClass = ""
		spec.EntityURI = ""
		spec.CollectionURI = ""
	} else if spec.DefinitionKind == DefinitionEntity {
		if spec.EntityClass == "" {
			spec.EntityClass = qualify(namespace, base+"Entity")
		}
		if spec.CollectionClass == "" {
			spec.CollectionClass = qualify(namespace, base+"Collection")
		}
	}
}

func completeBulkExtensions(spec *EntitySpec) {
	if spec.DefinitionKind == DefinitionBulkExtension {
		for targetIndex := range spec.BulkExtensions {
			target := &spec.BulkExtensions[targetIndex]
			if target.ID == "" {
				target.ID = fmt.Sprintf("bulk-target-%d", targetIndex)
			}
			fields := make([]FieldSpec, 0, len(target.Fields))
			for _, field := range target.Fields {
				completeFieldSpec(&field, len(fields))
				fields = append(fields, field)
			}
			target.Fields = fields
		}
		spec.Fields = nil
		spec.Indexes = nil
	}
}

func completeDefinitionBehaviorFields(behavior *DefinitionBehaviorSpec, idPrefix string) {
	if behavior == nil {
		return
	}
	for index := range behavior.DefaultFields {
		if behavior.DefaultFields[index].ID == "" {
			behavior.DefaultFields[index].ID = fmt.Sprintf("%sdefault-field-%d", idPrefix, index+1)
		}
		completeFieldSpec(&behavior.DefaultFields[index], index)
	}
	for index := range behavior.BaseFields {
		if behavior.BaseFields[index].ID == "" {
			behavior.BaseFields[index].ID = fmt.Sprintf("%sbase-field-%d", idPrefix, index+1)
		}
		completeFieldSpec(&behavior.BaseFields[index], index)
	}
}

func completeHierarchyFields(spec *EntitySpec) {
	hasVersion := hasVersionField(*spec)
	complete := func(field *FieldSpec) {
		if field.Kind != FieldHierarchy {
			return
		}
		field.TargetDefinitionClass = spec.DefinitionClass
		field.TargetEntityClass = spec.EntityClass
		field.TargetCollectionClass = spec.CollectionClass
		field.TargetEntityName = spec.EntityName
		field.HierarchyVersionAware = hasVersion
	}
	if spec.DefinitionBehavior != nil {
		for index := range spec.DefinitionBehavior.DefaultFields {
			complete(&spec.DefinitionBehavior.DefaultFields[index])
		}
		for index := range spec.DefinitionBehavior.BaseFields {
			complete(&spec.DefinitionBehavior.BaseFields[index])
		}
	}
	for index := range spec.Fields {
		complete(&spec.Fields[index])
	}
}

func completeOwnedFields(spec *EntitySpec) {
	fields := make([]FieldSpec, 0, len(spec.Fields))
	for _, field := range spec.Fields {
		if isImplicitTimestampField(*spec, field.Kind) {
			continue
		}
		completeFieldSpec(&field, len(fields))
		fields = append(fields, field)
	}
	spec.Fields = fields
}

func completeFieldModifications(spec *EntitySpec) {
	for index := range spec.FieldModifications {
		if spec.FieldModifications[index].ID == "" {
			spec.FieldModifications[index].ID = fmt.Sprintf("modify-%s-%d", spec.FieldModifications[index].PropertyName, index)
		}
	}
}

func isImplicitTimestampField(spec EntitySpec, kind FieldKind) bool {
	overridden := spec.DefinitionBehavior != nil && spec.DefinitionBehavior.OverrideDefaultFields
	return spec.DefinitionKind == DefinitionEntity && !overridden && (kind == FieldCreatedAt || kind == FieldUpdatedAt)
}

func completeTranslationSpec(spec EntitySpec, namespace, base string) EntitySpec {
	hasTranslatedFields := false
	for _, field := range spec.Fields {
		if field.Translated {
			hasTranslatedFields = true
			break
		}
	}
	if hasTranslatedFields && spec.Translation == nil {
		spec.Translation = &TranslationSpec{Enabled: true, AssociationRequired: true}
	}
	if spec.Translation != nil && (spec.Translation.Enabled || hasTranslatedFields) {
		spec.Translation.Enabled = true
		translationBase := base + "Translation"
		translationNamespace := qualify(namespace, `Aggregate\`+translationBase)
		if spec.Translation.EntityName == "" {
			spec.Translation.EntityName = spec.EntityName + "_translation"
		}
		if spec.Translation.DefinitionClass == "" {
			spec.Translation.DefinitionClass = qualify(translationNamespace, translationBase+"Definition")
		}
		if spec.Translation.EntityClass == "" {
			spec.Translation.EntityClass = qualify(translationNamespace, translationBase+"Entity")
		}
		if spec.Translation.CollectionClass == "" {
			spec.Translation.CollectionClass = qualify(translationNamespace, translationBase+"Collection")
		}
		if spec.Translation.ParentDefinitionClass == "" {
			spec.Translation.ParentDefinitionClass = spec.DefinitionClass
		}
		if spec.Translation.ParentStorageName == "" {
			spec.Translation.ParentStorageName = spec.EntityName + "_id"
		}
		if spec.Translation.ParentPropertyName == "" {
			spec.Translation.ParentPropertyName = camelizeStorageName(spec.EntityName)
		}
		if spec.Translation.AssociationProperty == "" {
			spec.Translation.AssociationProperty = "translations"
		}
		if spec.Translation.AssociationLocalField == "" {
			spec.Translation.AssociationLocalField = "id"
		}
	}
	return spec
}

func completeFieldSpec(field *FieldSpec, index int) {
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
	if field.Kind == FieldForeignKey {
		if field.ReferenceField == "" {
			field.ReferenceField = "id"
		}
		if field.ReferenceStorageName == "" {
			field.ReferenceStorageName = "id"
		}
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
	if field.Kind == FieldHierarchy {
		field.PropertyName = defaultString(field.PropertyName, "children")
		field.HierarchyParentProperty = "parent"
		field.ForeignKeyPropertyName = "parentId"
		field.StorageName = "parent_id"
		field.ReferenceField = "id"
		field.ReferenceStorageName = "id"
		field.Required = false
		field.Primary = false
		field.DeleteBehavior = DeleteCascade
		field.Editable = true
	}
}

func RenderDefinition(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	if spec.DefinitionKind == DefinitionBulkExtension {
		return renderBulkEntityExtension(spec)
	}
	imports := newImportTable(definitionImports(spec))
	var fields []string
	for _, field := range spec.Fields {
		if field.TranslationDefinitionOnly || isImplicitTimestampField(spec, field.Kind) {
			continue
		}
		var rendered []string
		var err error
		if field.Kind == FieldLocked && strings.TrimSpace(field.Raw) != "" {
			rendered = []string{strings.TrimSuffix(strings.TrimSpace(field.Raw), ",") + ","}
		} else if field.Translated {
			rendered = []string{renderTranslatedField(field, imports)}
		} else {
			if spec.DefinitionKind == DefinitionExtension {
				field = withEntityExtensionFlag(field)
			}
			rendered, err = renderDefinitionField(field, imports)
		}
		if err != nil {
			return "", err
		}
		fields = append(fields, rendered...)
	}
	if spec.Translation != nil && spec.Translation.Enabled {
		fields = append(fields, renderTranslationsAssociation(*spec.Translation, imports))
	}
	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(strings.Trim(spec.Namespace, `\`))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(spec.DefinitionClass))
	switch spec.DefinitionKind {
	case DefinitionExtension:
		builder.WriteString(" extends EntityExtension\n{\n")
		writeEntityExtensionMethods(&builder, spec, imports, fields)
		builder.WriteString("}\n")
		return builder.String(), nil
	case DefinitionMapping:
		builder.WriteString(" extends MappingEntityDefinition\n{\n")
	default:
		builder.WriteString(" extends EntityDefinition\n{\n")
	}
	builder.WriteString("    final public const ENTITY_NAME = '")
	builder.WriteString(escapePHPSingle(spec.EntityName))
	builder.WriteString("';\n\n")
	builder.WriteString("    public function getEntityName(): string\n    {\n        return self::ENTITY_NAME;\n    }\n\n")
	if spec.DefinitionKind == DefinitionEntity || spec.DefinitionKind == DefinitionMapping && spec.EntityClass != "" && spec.CollectionClass != "" {
		builder.WriteString("    public function getEntityClass(): string\n    {\n        return ")
		builder.WriteString(imports.Ref(spec.EntityClass))
		builder.WriteString("::class;\n    }\n\n")
		builder.WriteString("    public function getCollectionClass(): string\n    {\n        return ")
		builder.WriteString(imports.Ref(spec.CollectionClass))
		builder.WriteString("::class;\n    }\n\n")
		if spec.DefinitionKind == DefinitionEntity && spec.InheritanceAware {
			builder.WriteString("    public function isInheritanceAware(): bool\n    {\n        return true;\n    }\n\n")
		}
		if spec.DefinitionKind == DefinitionEntity {
			if protectionMethod := renderEntityProtectionMethod(spec); protectionMethod != "" {
				for _, line := range strings.Split(strings.TrimSpace(protectionMethod), "\n") {
					builder.WriteString("    ")
					builder.WriteString(line)
					builder.WriteByte('\n')
				}
				builder.WriteByte('\n')
			}
		}
	}
	behavior, err := renderDefinitionBehavior(spec.DefinitionBehavior, imports, false)
	if err != nil {
		return "", err
	}
	if behavior != "" {
		writeIndentedClassMember(&builder, behavior)
		builder.WriteByte('\n')
	}
	if metadata := renderDefinitionMetadata(spec.DefinitionMetadata, imports, false); metadata != "" {
		writeIndentedClassMember(&builder, metadata)
		builder.WriteByte('\n')
	}
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

func renderBulkEntityExtension(spec EntitySpec) (string, error) {
	imports := newImportTable(definitionImports(spec))
	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(strings.Trim(spec.Namespace, `\`))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(spec.DefinitionClass))
	builder.WriteString(" extends BulkEntityExtension\n{\n")
	if strings.TrimSpace(spec.CollectMethodRaw) != "" {
		lines := strings.Split(strings.TrimSpace(spec.CollectMethodRaw), "\n")
		for index, line := range lines {
			if index == 0 {
				builder.WriteString("    ")
			}
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		builder.WriteString("}\n")
		return builder.String(), nil
	}
	builder.WriteString("    public function collect(): \\Generator\n    {\n")
	for _, target := range spec.BulkExtensions {
		builder.WriteString("        yield ")
		if target.ExtendedDefinitionClass != "" {
			builder.WriteString(imports.Ref(target.ExtendedDefinitionClass))
			builder.WriteString("::ENTITY_NAME")
		} else {
			builder.WriteString(quotePHP(target.EntityName))
		}
		builder.WriteString(" => [\n")
		for _, original := range target.Fields {
			field := withEntityExtensionFlag(original)
			var rendered []string
			var err error
			if field.Kind == FieldLocked && strings.TrimSpace(field.Raw) != "" {
				rendered = []string{strings.TrimSuffix(strings.TrimSpace(field.Raw), ",") + ","}
			} else {
				rendered, err = renderDefinitionField(field, imports)
			}
			if err != nil {
				return "", err
			}
			for _, expression := range rendered {
				for _, line := range strings.Split(strings.TrimSpace(expression), "\n") {
					builder.WriteString("            ")
					builder.WriteString(line)
					builder.WriteByte('\n')
				}
			}
		}
		builder.WriteString("        ];\n\n")
	}
	builder.WriteString("    }\n}\n")
	return builder.String(), nil
}

func renderEntityProtectionMethod(spec EntitySpec) string {
	if strings.TrimSpace(spec.ProtectionMethodRaw) != "" {
		return strings.TrimSpace(spec.ProtectionMethodRaw)
	}
	var protections []string
	if spec.ReadProtected {
		protections = append(protections, "new ReadProtection("+renderStringArguments(spec.ReadProtectionScopes)+")")
	}
	if spec.WriteProtected {
		protections = append(protections, "new WriteProtection("+renderStringArguments(spec.WriteProtectionScopes)+")")
	}
	protections = append(protections, spec.PreservedProtections...)
	if len(protections) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("protected function defineProtections(): EntityProtectionCollection\n{\n    return new EntityProtectionCollection([\n")
	for _, protection := range protections {
		builder.WriteString("        ")
		builder.WriteString(strings.TrimSuffix(strings.TrimSpace(protection), ","))
		builder.WriteString(",\n")
	}
	builder.WriteString("    ]);\n}")
	return builder.String()
}

func renderStringArguments(values []string) string {
	arguments := make([]string, 0, len(values))
	for _, value := range values {
		arguments = append(arguments, quotePHP(value))
	}
	return strings.Join(arguments, ", ")
}

func withEntityExtensionFlag(field FieldSpec) FieldSpec {
	mark := func(metadata **FieldMetadata) {
		if *metadata == nil {
			*metadata = &FieldMetadata{}
		}
		(*metadata).Extension = true
	}
	if field.Kind != FieldOneToMany && field.Kind != FieldManyToMany &&
		((field.Kind != FieldOneToOne && field.Kind != FieldManyToOne) || !field.UsesExistingColumn) {
		mark(&field.Metadata)
	}
	if field.Kind == FieldManyToOne || field.Kind == FieldOneToOne ||
		field.Kind == FieldOneToMany || field.Kind == FieldManyToMany {
		mark(&field.AssociationMetadata)
	}
	return field
}

func writeEntityExtensionMethods(
	builder *strings.Builder,
	spec EntitySpec,
	imports *importTable,
	fields []string,
) {
	builder.WriteString("    public function extendFields(FieldCollection $collection): void\n    {\n")
	for _, field := range fields {
		builder.WriteString("        $collection->add(\n")
		for _, line := range strings.Split(strings.TrimSpace(field), "\n") {
			builder.WriteString("            ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		builder.WriteString("        );\n")
	}
	builder.WriteString("    }\n\n")
	if protectionMethod := renderEntityExtensionProtectionMethod(spec); protectionMethod != "" {
		for _, line := range strings.Split(strings.TrimSpace(protectionMethod), "\n") {
			builder.WriteString("    ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
	}
	if modificationMethod := renderFieldModifications(spec, imports); modificationMethod != "" {
		for _, line := range strings.Split(strings.TrimSpace(modificationMethod), "\n") {
			builder.WriteString("    ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
		builder.WriteByte('\n')
	}
	target := imports.Ref(spec.ExtendedDefinitionClass)
	builder.WriteString("    public function getEntityName(): string\n    {\n        return ")
	builder.WriteString(target)
	builder.WriteString("::ENTITY_NAME;\n    }\n")
	if entityExtensionNeedsLegacyMethod(spec.ShopwareVersion) {
		builder.WriteString("\n    public function getDefinitionClass(): string\n    {\n        return ")
		builder.WriteString(target)
		builder.WriteString("::class;\n    }\n")
	}
}

func renderEntityExtensionProtectionMethod(spec EntitySpec) string {
	if strings.TrimSpace(spec.ProtectionMethodRaw) != "" {
		return strings.TrimSpace(spec.ProtectionMethodRaw)
	}
	var protections []string
	if spec.ReadProtected {
		protections = append(protections, "new ReadProtection("+renderStringArguments(spec.ReadProtectionScopes)+")")
	}
	if spec.WriteProtected {
		protections = append(protections, "new WriteProtection("+renderStringArguments(spec.WriteProtectionScopes)+")")
	}
	protections = append(protections, spec.PreservedProtections...)
	if len(protections) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("public function extendProtections(EntityProtectionCollection $protections): void\n{\n")
	for _, protection := range protections {
		builder.WriteString("    $protections->add(")
		builder.WriteString(strings.TrimSuffix(strings.TrimSpace(protection), ","))
		builder.WriteString(");\n")
	}
	builder.WriteString("}")
	return builder.String()
}

func entityExtensionNeedsLegacyMethod(versionConstraint string) bool {
	version, known := project.ParseVersionConstraint(versionConstraint)
	return !known || !version.AtLeastPatch(6, 7, 0)
}

func RenderEntity(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if spec.DefinitionKind != DefinitionEntity {
		return "", fmt.Errorf("%s definitions do not have entity classes", spec.DefinitionKind)
	}
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	imports := newImportTable(entityImports(spec))
	var properties, methods []string
	fields := schemaDefinitionFields(spec)
	for _, field := range fields {
		if field.TranslationDefinitionOnly {
			continue
		}
		fieldProperties, fieldMethods, err := renderEntityField(field, imports)
		if err != nil {
			return "", err
		}
		properties = append(properties, fieldProperties...)
		methods = append(methods, fieldMethods...)
	}
	if spec.Translation != nil && spec.Translation.Enabled {
		collection := imports.Ref(spec.Translation.CollectionClass)
		property := spec.Translation.AssociationProperty
		properties = append(properties, "protected ?"+collection+" $"+property+" = null;")
		methods = append(methods, accessors(property, "?"+collection, false)...)
	}
	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(strings.Trim(spec.Namespace, `\`))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(spec.EntityClass))
	builder.WriteString(" extends Entity\n{\n")
	for _, trait := range entityImplementationTraits(spec, false) {
		builder.WriteString("    use ")
		builder.WriteString(imports.Ref(trait))
		builder.WriteString(";\n")
	}
	builder.WriteString("    use EntityIdTrait;\n")
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
	if spec.DefinitionKind != DefinitionEntity {
		return "", fmt.Errorf("%s definitions do not have collection classes", spec.DefinitionKind)
	}
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

// RenderTranslationDefinition renders the EntityTranslationDefinition owned
// by spec. Translated scalar fields are stored here while their parent
// TranslatedField facades remain on the main definition.
func RenderTranslationDefinition(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	if spec.Translation == nil || !spec.Translation.Enabled {
		return "", fmt.Errorf("entity has no translation bundle")
	}
	translation := *spec.Translation
	imports := newImportTable(translationDefinitionImports(spec))
	var fields []string
	for _, field := range spec.Fields {
		if !field.Translated || field.Kind == FieldLocked && !field.TranslationDefinitionOnly {
			continue
		}
		storageField := field
		storageField.Translated = false
		rendered, err := renderDefinitionField(storageField, imports)
		if err != nil {
			return "", err
		}
		fields = append(fields, rendered...)
	}
	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(namespaceOf(translation.DefinitionClass))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(translation.DefinitionClass))
	builder.WriteString(" extends EntityTranslationDefinition\n{\n")
	builder.WriteString("    final public const ENTITY_NAME = '")
	builder.WriteString(escapePHPSingle(translation.EntityName))
	builder.WriteString("';\n\n")
	builder.WriteString("    public function getEntityName(): string\n    {\n        return self::ENTITY_NAME;\n    }\n\n")
	builder.WriteString("    public function getEntityClass(): string\n    {\n        return ")
	builder.WriteString(imports.Ref(translation.EntityClass))
	builder.WriteString("::class;\n    }\n\n")
	builder.WriteString("    public function getCollectionClass(): string\n    {\n        return ")
	builder.WriteString(imports.Ref(translation.CollectionClass))
	builder.WriteString("::class;\n    }\n\n")
	builder.WriteString("    protected function getParentDefinitionClass(): string\n    {\n        return ")
	builder.WriteString(imports.Ref(translation.ParentDefinitionClass))
	builder.WriteString("::class;\n    }\n\n")
	behavior, err := renderDefinitionBehavior(translation.DefinitionBehavior, imports, true)
	if err != nil {
		return "", err
	}
	if behavior != "" {
		writeIndentedClassMember(&builder, behavior)
		builder.WriteByte('\n')
	}
	if metadata := renderDefinitionMetadata(translation.DefinitionMetadata, imports, true); metadata != "" {
		writeIndentedClassMember(&builder, metadata)
		builder.WriteByte('\n')
	}
	builder.WriteString("    protected function defineFields(): FieldCollection\n    {\n        return new FieldCollection([\n")
	for _, field := range fields {
		for _, line := range strings.Split(strings.TrimSpace(field), "\n") {
			builder.WriteString("            ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("        ]);\n    }\n}\n")
	return builder.String(), nil
}

func writeIndentedClassMember(builder *strings.Builder, member string) {
	for _, line := range strings.Split(member, "\n") {
		if line != "" {
			builder.WriteString("    ")
			builder.WriteString(line)
		}
		builder.WriteByte('\n')
	}
}

func RenderTranslationEntity(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	if spec.Translation == nil || !spec.Translation.Enabled {
		return "", fmt.Errorf("entity has no translation bundle")
	}
	translation := *spec.Translation
	imports := newImportTable(translationEntityImports(spec))
	parentProperty := translation.ParentPropertyName
	parentIDProperty := camelizeStorageName(translation.ParentStorageName)
	var properties, methods []string
	overridesBaseFields := translation.DefinitionBehavior != nil && translation.DefinitionBehavior.OverrideBaseFields
	if !overridesBaseFields {
		properties = append(properties, "protected string $"+parentIDProperty+";")
		methods = append(methods, accessors(parentIDProperty, "string", false)...)
	}
	if !overridesBaseFields && hasVersionField(spec) {
		versionProperty := camelizeStorageName(strings.TrimSuffix(translation.ParentStorageName, "_id") + "_version_id")
		properties = append(properties, "protected string $"+versionProperty+";")
		methods = append(methods, accessors(versionProperty, "string", false)...)
	}
	if translation.DefinitionBehavior != nil && translation.DefinitionBehavior.OverrideDefaultFields {
		for _, field := range translation.DefinitionBehavior.DefaultFields {
			fieldProperties, fieldMethods, fieldErr := renderEntityField(field, imports)
			if fieldErr != nil {
				return "", fieldErr
			}
			properties = append(properties, fieldProperties...)
			methods = append(methods, fieldMethods...)
		}
	}
	if translation.DefinitionBehavior != nil && translation.DefinitionBehavior.OverrideBaseFields {
		for _, field := range translation.DefinitionBehavior.BaseFields {
			fieldProperties, fieldMethods, fieldErr := renderEntityField(field, imports)
			if fieldErr != nil {
				return "", fieldErr
			}
			properties = append(properties, fieldProperties...)
			methods = append(methods, fieldMethods...)
		}
	}
	for _, field := range spec.Fields {
		if !field.Translated || field.Kind == FieldLocked {
			continue
		}
		if field.Implementation != nil && !field.Implementation.ManageEntity && field.Implementation.EntityTrait == "" {
			continue
		}
		if field.Implementation != nil && field.Implementation.EntityTrait != "" {
			continue
		}
		phpType, boolean, err := entityFieldPHPType(field, imports)
		if err != nil {
			return "", err
		}
		if field.Implementation != nil {
			if field.Implementation.EntityType != "" {
				phpType = entityImplementationType(field.Implementation.EntityType, imports)
			}
			boolean = field.Implementation.EntityBooleanGetter
		}
		phpType = nullablePHPType(phpType)
		properties = append(properties, "protected "+phpType+" $"+field.PropertyName+" = null;")
		fieldMethods := behaviorAccessors(field.PropertyName, phpType, boolean, field.TranslationBehavior)
		if field.Implementation != nil && field.Implementation.ImplicitComputed {
			fieldMethods = fieldMethods[:1]
		}
		methods = append(methods, fieldMethods...)
	}
	if !overridesBaseFields {
		parentEntity := imports.Ref(spec.EntityClass)
		properties = append(properties, "protected ?"+parentEntity+" $"+parentProperty+" = null;")
		methods = append(methods, accessors(parentProperty, "?"+parentEntity, false)...)
	}

	var builder strings.Builder
	builder.WriteString("<?php declare(strict_types=1);\n\nnamespace ")
	builder.WriteString(namespaceOf(translation.EntityClass))
	builder.WriteString(";\n\n")
	builder.WriteString(imports.Render())
	builder.WriteString("\nclass ")
	builder.WriteString(ShortClass(translation.EntityClass))
	builder.WriteString(" extends TranslationEntity\n{")
	for _, trait := range entityImplementationTraits(spec, true) {
		builder.WriteString("\n    use ")
		builder.WriteString(imports.Ref(trait))
		builder.WriteString(";\n")
	}
	for _, property := range properties {
		builder.WriteString("\n    ")
		builder.WriteString(property)
		builder.WriteByte('\n')
	}
	for _, method := range methods {
		builder.WriteByte('\n')
		for _, line := range strings.Split(method, "\n") {
			builder.WriteString("    ")
			builder.WriteString(line)
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("}\n")
	return builder.String(), nil
}

func RenderTranslationCollection(spec EntitySpec) (string, error) {
	spec = CompleteSpec(spec)
	if err := ValidSpec(spec); err != nil {
		return "", err
	}
	if spec.Translation == nil || !spec.Translation.Enabled {
		return "", fmt.Errorf("entity has no translation bundle")
	}
	translation := spec.Translation
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
`, namespaceOf(translation.CollectionClass), ShortClass(translation.EntityClass),
		ShortClass(translation.CollectionClass), escapePHPSingle(translation.EntityName),
		ShortClass(translation.EntityClass)), nil
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
		apiAware = renderAPIAware(imports, field.APIAwareSources)
	}
	ranking := ""
	if field.SearchRanking > 0 {
		ranking = renderSearchRanking(field.SearchRanking, field.SearchRankingTokenize)
	}
	storage := quotePHP(field.StorageName)
	property := quotePHP(field.PropertyName)
	fieldExpression := func(base string, values ...string) string {
		return withModifiers(base, field.ModifiersBeforeFlags, flags(definitionFieldFlagValues(field, values...)...), field.ModifiersAfterFlags)
	}
	associationExpression := func(base string, values ...string) string {
		return withModifiers(base, field.AssociationBeforeFlags, flags(definitionAssociationFlagValues(field, values...)...), field.AssociationAfterFlags)
	}
	associationAPI := ""
	if field.AssociationAPIAware {
		associationAPI = renderAPIAware(imports, field.AssociationAPIAwareSources)
	}
	associationRanking := ""
	if field.AssociationSearchRank > 0 {
		associationRanking = renderSearchRanking(field.AssociationSearchRank, field.AssociationSearchTokenize)
	}
	if field.Implementation != nil {
		return renderSpecializedDefinitionField(field, imports, fieldExpression, required, apiAware, ranking)
	}
	switch field.Kind {
	case FieldID:
		return []string{fieldExpression("(new IdField('id', 'id'))", "new Required()", "new PrimaryKey()", apiAware, ranking) + ","}, nil
	case FieldBinaryID:
		return []string{fieldExpression("(new IdField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldString:
		arguments := storage + ", " + property
		if field.MaxLength > 0 && field.MaxLength != 255 {
			arguments += ", " + strconv.Itoa(field.MaxLength)
		}
		return []string{fieldExpression("(new StringField("+arguments+"))", required, apiAware, ranking) + ","}, nil
	case FieldEnum:
		enum := imports.Ref(field.EnumClass)
		return []string{fieldExpression("(new EnumField("+storage+", "+property+", "+enum+"::"+field.EnumCase+"))", required, apiAware, ranking) + ","}, nil
	case FieldLongText:
		return []string{fieldExpression("(new LongTextField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldInt:
		arguments := storage + ", " + property
		if field.Min != nil || field.Max != nil {
			arguments += ", " + nullableInt(field.Min) + ", " + nullableInt(field.Max)
		}
		return []string{fieldExpression("(new IntField("+arguments+"))", required, apiAware, ranking) + ","}, nil
	case FieldFloat:
		return []string{fieldExpression("(new FloatField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldBool:
		return []string{fieldExpression("(new BoolField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldDate:
		return []string{fieldExpression("(new DateField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldDateTime:
		return []string{fieldExpression("(new DateTimeField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldJSON:
		arguments := storage + ", " + property
		propertyMapping := strings.TrimSpace(field.JSONPropertyMappingExpression)
		defaultValue := strings.TrimSpace(field.JSONDefaultExpression)
		if propertyMapping != "" || defaultValue != "" {
			if propertyMapping == "" {
				propertyMapping = "[]"
			}
			arguments += ", " + propertyMapping
		}
		if defaultValue != "" {
			arguments += ", " + defaultValue
		}
		return []string{fieldExpression("(new JsonField("+arguments+"))", required, apiAware, ranking) + ","}, nil
	case FieldList:
		arguments := storage + ", " + property
		if field.ElementTypeClass != "" {
			arguments += ", " + imports.Ref(field.ElementTypeClass) + "::class"
		}
		return []string{fieldExpression("(new ListField("+arguments+"))", required, apiAware, ranking) + ","}, nil
	case FieldObject:
		return []string{fieldExpression("(new ObjectField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldBlob:
		return []string{fieldExpression("(new BlobField("+storage+", "+property+"))", required, apiAware, ranking) + ","}, nil
	case FieldAutoIncrement:
		return []string{fieldExpression("(new AutoIncrementField())", apiAware, ranking) + ","}, nil
	case FieldVersion:
		return []string{fieldExpression("(new VersionField())", apiAware, ranking) + ","}, nil
	case FieldReferenceVersion:
		target := imports.Ref(field.TargetDefinitionClass)
		return []string{fieldExpression("(new ReferenceVersionField("+target+"::class, "+storage+"))", required, apiAware, ranking) + ","}, nil
	case FieldCreatedAt:
		return []string{fieldExpression("(new CreatedAtField())", apiAware, ranking) + ","}, nil
	case FieldUpdatedAt:
		return []string{fieldExpression("(new UpdatedAtField())", apiAware, ranking) + ","}, nil
	case FieldForeignKey:
		target := imports.Ref(field.TargetDefinitionClass)
		return []string{fieldExpression("(new FkField("+storage+", "+property+", "+target+"::class, "+quotePHP(defaultString(field.ReferenceField, "id"))+"))", required, apiAware, ranking) + ","}, nil
	case FieldManyToOne, FieldOneToOne:
		target := imports.Ref(field.TargetDefinitionClass)
		var result []string
		if !field.UsesExistingColumn {
			result = append(result, fieldExpression("(new FkField("+storage+", "+quotePHP(field.ForeignKeyPropertyName)+", "+target+"::class))", required, apiAware, ranking)+",")
		}
		association := renderToOneConstructor(field.Kind, property, storage, target, field.ReferenceField, field.AssociationAutoload)
		if conditional := field.ConditionalAssociation; conditional != nil {
			alternative := renderToOneConstructor(conditional.AlternativeKind, property, storage, target, field.ReferenceField, conditional.AlternativeAutoload)
			association = "(" + conditional.ConditionExpression + " ? " + association + " : " + alternative + ")"
		}
		deleteFlag := renderDeleteFlag(field)
		association = associationExpression("("+association+")", deleteFlag, associationAPI, associationRanking)
		return append(result, association+","), nil
	case FieldOneToMany:
		target := imports.Ref(field.TargetDefinitionClass)
		arguments := property + ", " + target + "::class, " + quotePHP(field.ReferenceStorageName)
		if field.SourceColumn != "" && field.SourceColumn != "id" {
			arguments += ", " + quotePHP(field.SourceColumn)
		}
		expression := "new OneToManyAssociationField(" + arguments + ")"
		deleteFlag := renderDeleteFlag(field)
		expression = associationExpression("("+expression+")", deleteFlag, associationAPI, associationRanking)
		return []string{expression + ","}, nil
	case FieldManyToMany:
		target := imports.Ref(field.TargetDefinitionClass)
		mapping := imports.Ref(field.MappingDefinitionClass)
		expression := "new ManyToManyAssociationField(" + property + ", " + target + "::class, " + mapping + "::class, " + quotePHP(field.MappingLocalColumn) + ", " + quotePHP(field.MappingReferenceColumn) + ", " + quotePHP(defaultString(field.SourceColumn, "id")) + ", " + quotePHP(defaultString(field.ReferenceField, "id")) + ")"
		return []string{associationExpression("("+expression+")", renderDeleteFlag(field), associationAPI, associationRanking) + ","}, nil
	case FieldHierarchy:
		target := imports.Ref(field.TargetDefinitionClass)
		parentFK := fieldExpression("(new ParentFkField("+target+"::class))", apiAware, ranking)
		var result []string
		result = append(result, parentFK+",")
		if field.HierarchyVersionAware {
			versionFlags := append([]string{"new Required()"}, field.HierarchyVersionFlags...)
			if field.HierarchyVersionAPIAware {
				versionFlags = append([]string{renderAPIAware(imports, field.HierarchyVersionAPISources), "new Required()"}, field.HierarchyVersionFlags...)
			}
			if field.HierarchyVersionInherited {
				versionFlags = append(versionFlags, renderInheritedFlag(field.HierarchyVersionInheritedFK))
			}
			versionFlags = append(versionFlags, renderBehaviorFlags(field.HierarchyVersionBehavior)...)
			versionFlags = append(versionFlags, renderMetadataFlags(field.HierarchyVersionMetadata)...)
			versionFlags = appendWriteProtectedFlag(versionFlags, field.HierarchyVersionProtected, field.HierarchyVersionWriteScopes)
			version := withModifiers("(new ReferenceVersionField("+target+"::class, 'parent_version_id'))", field.HierarchyVersionBefore, flags(versionFlags...), field.HierarchyVersionAfter)
			result = append(result, version+",")
		}
		parent := associationExpression("(new ParentAssociationField("+target+"::class, 'id'))", associationAPI, associationRanking)
		childrenFlags := append([]string(nil), field.HierarchyChildrenFlags...)
		if field.HierarchyChildrenAPIAware {
			childrenFlags = append([]string{renderAPIAware(imports, field.HierarchyChildrenAPISources)}, childrenFlags...)
		}
		if field.HierarchyChildrenRank > 0 {
			childrenFlags = append([]string{renderSearchRanking(field.HierarchyChildrenRank, field.HierarchyChildrenTokenize)}, childrenFlags...)
		}
		if field.HierarchyChildrenInherited {
			childrenFlags = append(childrenFlags, renderInheritedFlag(field.HierarchyChildrenInheritedFK))
		}
		if field.HierarchyChildrenReverse != "" {
			childrenFlags = append(childrenFlags, "new ReverseInherited("+quotePHP(field.HierarchyChildrenReverse)+")")
		}
		childrenFlags = append(childrenFlags, renderBehaviorFlags(field.HierarchyChildrenBehavior)...)
		childrenFlags = append(childrenFlags, renderMetadataFlags(field.HierarchyChildrenMetadata)...)
		childrenFlags = appendWriteProtectedFlag(childrenFlags, field.HierarchyChildrenProtected, field.HierarchyChildrenWriteScopes)
		children := "(new ChildrenAssociationField(" + target + "::class"
		if field.PropertyName != "children" {
			children += ", " + property
		}
		children += "))"
		children = withModifiers(children, field.HierarchyChildrenBefore, flags(childrenFlags...), field.HierarchyChildrenAfter)
		return append(result, parent+",", children+","), nil
	default:
		return nil, fmt.Errorf("unsupported field kind %q", field.Kind)
	}
}

func renderToOneConstructor(kind FieldKind, property, storage, target, referenceField string, autoload bool) string {
	if kind == FieldOneToOne {
		return "new OneToOneAssociationField(" + property + ", " + storage + ", " + quotePHP(defaultString(referenceField, "id")) + ", " + target + "::class, " + strconv.FormatBool(autoload) + ")"
	}
	return "new ManyToOneAssociationField(" + property + ", " + storage + ", " + target + "::class, " + quotePHP(defaultString(referenceField, "id")) + ", " + strconv.FormatBool(autoload) + ")"
}

func renderSpecializedDefinitionField(
	field FieldSpec,
	imports *importTable,
	fieldExpression func(string, ...string) string,
	required, apiAware, ranking string,
) ([]string, error) {
	implementation := field.Implementation
	if implementation == nil || implementation.Class == "" {
		return nil, fmt.Errorf("specialized field %q has no implementation class", field.ID)
	}
	var arguments []string
	switch implementation.ConstructorMode {
	case constructorStorageProperty:
		arguments = append(arguments, quotePHP(field.StorageName), quotePHP(field.PropertyName))
		additional := append([]string(nil), implementation.AdditionalArguments...)
		if implementation.MaxLengthArgument {
			if len(additional) == 0 {
				if field.MaxLength != 64 {
					additional = append(additional, strconv.Itoa(field.MaxLength))
				}
			} else {
				additional[0] = strconv.Itoa(field.MaxLength)
			}
		}
		arguments = append(arguments, additional...)
	case constructorFixed:
		arguments = append(arguments, implementation.FixedArguments...)
	default:
		return nil, fmt.Errorf("specialized field %q has unsupported constructor mode %q", field.ID, implementation.ConstructorMode)
	}
	expression := "(new " + imports.Ref(implementation.Class) + "(" + strings.Join(arguments, ", ") + "))"
	return []string{fieldExpression(expression, required, apiAware, ranking) + ","}, nil
}

func definitionFieldFlagValues(field FieldSpec, values ...string) []string {
	result := append([]string(nil), values...)
	if field.Primary && field.Kind != FieldID && field.Kind != FieldVersion {
		result = append(result, "new PrimaryKey()")
	}
	if field.Inherited {
		result = append(result, renderInheritedFlag(field.InheritedForeignKey))
	}
	result = append(result, renderBehaviorFlags(field.Behavior)...)
	result = append(result, renderMetadataFlags(field.Metadata)...)
	result = appendWriteProtectedFlag(result, field.WriteProtected, field.WriteProtectedScopes)
	return append(result, field.PreservedFlags...)
}

func definitionAssociationFlagValues(field FieldSpec, values ...string) []string {
	result := append([]string(nil), values...)
	if field.AssociationInherited {
		result = append(result, renderInheritedFlag(field.AssociationInheritedFK))
	}
	if field.ReverseInheritedProperty != "" {
		result = append(result, "new ReverseInherited("+quotePHP(field.ReverseInheritedProperty)+")")
	}
	result = append(result, renderBehaviorFlags(field.AssociationBehavior)...)
	result = append(result, renderMetadataFlags(field.AssociationMetadata)...)
	result = appendWriteProtectedFlag(result, field.AssociationWriteProtected, field.AssociationWriteScopes)
	return append(result, field.AssociationFlags...)
}

func appendWriteProtectedFlag(flags []string, enabled bool, scopes []string) []string {
	if !enabled {
		return flags
	}
	return append(flags, renderWriteProtectedFlag(scopes))
}

func renderSearchRanking(ranking float64, tokenize *bool) string {
	arguments := strconv.FormatFloat(ranking, 'g', -1, 64)
	if tokenize != nil {
		arguments += ", " + strconv.FormatBool(*tokenize)
	}
	return "new SearchRanking(" + arguments + ")"
}

func renderAPIAware(imports *importTable, sources []string) string {
	arguments := make([]string, 0, len(sources))
	for _, source := range sources {
		arguments = append(arguments, imports.Ref(source)+"::class")
	}
	return "new ApiAware(" + strings.Join(arguments, ", ") + ")"
}

func renderDeleteFlag(field FieldSpec) string {
	switch field.DeleteBehavior {
	case DeleteCascade:
		if field.DeleteCloneRelevant != nil {
			return "new CascadeDelete(" + strconv.FormatBool(*field.DeleteCloneRelevant) + ")"
		}
		return "new CascadeDelete()"
	case DeleteSetNull:
		if field.DeleteEnforcedByConstraint != nil {
			return "new SetNullOnDelete(" + strconv.FormatBool(*field.DeleteEnforcedByConstraint) + ")"
		}
		return "new SetNullOnDelete()"
	case DeleteRestrict:
		return "new RestrictDelete()"
	default:
		return ""
	}
}

func renderBehaviorFlags(behavior *FieldBehavior) []string {
	if behavior == nil {
		return nil
	}
	flags := make([]string, 0, 3)
	if behavior.Runtime {
		arguments := strings.TrimSpace(behavior.RuntimeDependenciesExpression)
		if arguments == "" && len(behavior.RuntimeDependencies) != 0 {
			dependencies := make([]string, 0, len(behavior.RuntimeDependencies))
			for _, dependency := range behavior.RuntimeDependencies {
				dependencies = append(dependencies, quotePHP(dependency))
			}
			arguments = "[" + strings.Join(dependencies, ", ") + "]"
		}
		flags = append(flags, "new Runtime("+arguments+")")
	}
	if behavior.Computed {
		flags = append(flags, "new Computed()")
	}
	if behavior.NoConstraint {
		flags = append(flags, "new NoConstraint()")
	}
	return flags
}

func renderMetadataFlags(metadata *FieldMetadata) []string {
	if metadata == nil {
		return nil
	}
	var flags []string
	if metadata.AllowHTML != nil {
		if *metadata.AllowHTML {
			flags = append(flags, "new AllowHtml()")
		} else {
			flags = append(flags, "new AllowHtml(false)")
		}
	}
	if metadata.AllowEmptyString {
		flags = append(flags, "new AllowEmptyString()")
	}
	if metadata.AsArray {
		flags = append(flags, "new AsArray()")
	}
	if metadata.Immutable {
		flags = append(flags, "new Immutable()")
	}
	if metadata.Since != "" {
		flags = append(flags, "new Since("+quotePHP(metadata.Since)+")")
	}
	if metadata.Deprecated != nil {
		arguments := []string{quotePHP(metadata.Deprecated.DeprecatedSince), quotePHP(metadata.Deprecated.WillBeRemovedIn)}
		if metadata.Deprecated.ReplacedBy != "" {
			arguments = append(arguments, quotePHP(metadata.Deprecated.ReplacedBy))
		}
		flags = append(flags, "new Deprecated("+strings.Join(arguments, ", ")+")")
	}
	if metadata.IgnoreInOpenAPISchema {
		flags = append(flags, "new IgnoreInOpenapiSchema()")
	}
	if metadata.IgnoreInUnusedMediaSearch {
		flags = append(flags, "new IgnoreInUnusedMediaSearch()")
	}
	if metadata.APICriteriaAware {
		flags = append(flags, "new ApiCriteriaAware()")
	}
	if len(metadata.RuleAreas) != 0 {
		flags = append(flags, "new RuleAreas("+renderStringArguments(metadata.RuleAreas)+")")
	}
	if metadata.Choice != nil {
		arguments := "[" + strings.Join(metadata.Choice.Values, ", ") + "]"
		if metadata.Choice.Strict != nil {
			arguments += ", strict: " + strconv.FormatBool(*metadata.Choice.Strict)
		}
		flags = append(flags, "new Choice("+arguments+")")
	}
	if metadata.DoNotUseContext {
		flags = append(flags, "new DoNotUseContext()")
	}
	if metadata.Extension {
		flags = append(flags, "new Extension()")
	}
	return flags
}

func renderTranslatedField(field FieldSpec, imports *importTable) string {
	arguments := quotePHP(field.PropertyName)
	if field.TranslationUseForSort {
		arguments += ", true"
	}
	apiAware := field.APIAware
	apiAwareSources := field.APIAwareSources
	if field.TranslationAPIAware != nil {
		apiAware = *field.TranslationAPIAware
		apiAwareSources = field.TranslationAPIAwareSources
	}
	searchRanking := field.SearchRanking
	if field.TranslationSearchRank != nil {
		searchRanking = *field.TranslationSearchRank
	}
	var flags []string
	if apiAware {
		flags = append(flags, renderAPIAware(imports, apiAwareSources))
	}
	if searchRanking > 0 {
		flags = append(flags, renderSearchRanking(searchRanking, field.TranslationSearchTokenize))
	}
	if field.TranslationInherited {
		flags = append(flags, renderInheritedFlag(field.TranslationInheritedFK))
	}
	if field.TranslationWriteProtected {
		flags = append(flags, renderWriteProtectedFlag(field.TranslationWriteScopes))
	}
	flags = append(flags, renderBehaviorFlags(field.TranslationBehavior)...)
	flags = append(flags, renderMetadataFlags(field.TranslationMetadata)...)
	flags = append(flags, field.TranslationFlags...)
	flagSuffix := ""
	if len(flags) != 0 {
		flagSuffix = "->addFlags(" + strings.Join(flags, ", ") + ")"
	}
	return withModifiers("(new TranslatedField("+arguments+"))", field.TranslationBeforeFlags, flagSuffix, field.TranslationAfterFlags) + ","
}

func renderInheritedFlag(foreignKey string) string {
	if foreignKey == "" {
		return "new Inherited()"
	}
	return "new Inherited(" + quotePHP(foreignKey) + ")"
}

func renderWriteProtectedFlag(scopes []string) string {
	arguments := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		arguments = append(arguments, quotePHP(scope))
	}
	return "new WriteProtected(" + strings.Join(arguments, ", ") + ")"
}

func renderTranslationsAssociation(translation TranslationSpec, imports *importTable) string {
	arguments := imports.Ref(translation.DefinitionClass) + "::class, " + quotePHP(translation.ParentStorageName)
	if translation.AssociationProperty != "translations" || translation.AssociationLocalField != "id" {
		arguments += ", " + quotePHP(translation.AssociationProperty) + ", " + quotePHP(translation.AssociationLocalField)
	}
	var flags []string
	if translation.AssociationRequired {
		flags = append(flags, "new Required()")
	}
	if translation.AssociationAPIAware {
		flags = append(flags, renderAPIAware(imports, translation.AssociationAPIAwareSources))
	}
	if translation.AssociationInherited {
		flags = append(flags, renderInheritedFlag(translation.AssociationInheritedFK))
	}
	if translation.ReverseInheritedProperty != "" {
		flags = append(flags, "new ReverseInherited("+quotePHP(translation.ReverseInheritedProperty)+")")
	}
	if translation.AssociationWriteProtected {
		flags = append(flags, renderWriteProtectedFlag(translation.AssociationWriteScopes))
	}
	flags = append(flags, renderBehaviorFlags(translation.AssociationBehavior)...)
	flags = append(flags, renderMetadataFlags(translation.AssociationMetadata)...)
	flags = append(flags, translation.AssociationFlags...)
	flagSuffix := ""
	if len(flags) != 0 {
		flagSuffix = "->addFlags(" + strings.Join(flags, ", ") + ")"
	}
	return withModifiers("(new TranslationsAssociationField("+arguments+"))", translation.AssociationBeforeFlags, flagSuffix, translation.AssociationAfterFlags) + ","
}

func renderEntityField(field FieldSpec, imports *importTable) ([]string, []string, error) {
	if field.Kind == FieldID || field.Kind == FieldVersion || field.Kind == FieldReferenceVersion ||
		field.Kind == FieldCreatedAt || field.Kind == FieldUpdatedAt || field.Kind == FieldLocked {
		return nil, nil, nil
	}
	if field.Implementation != nil && !field.Implementation.ManageEntity {
		return nil, nil, nil
	}
	if field.Implementation != nil && field.Implementation.EntityTrait != "" {
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
		methods := behaviorAccessors(field.PropertyName, "?"+target, false, field.AssociationBehavior)
		if !field.UsesExistingColumn {
			properties = append([]string{"protected " + fkType + " $" + field.ForeignKeyPropertyName + fkDefault + ";"}, properties...)
			methods = append(behaviorAccessors(field.ForeignKeyPropertyName, fkReturn, false, field.Behavior), methods...)
		}
		return properties, methods, nil
	}
	if field.Kind == FieldOneToMany || field.Kind == FieldManyToMany {
		target := imports.Ref(field.TargetCollectionClass)
		return []string{"protected ?" + target + " $" + field.PropertyName + " = null;"}, behaviorAccessors(field.PropertyName, "?"+target, false, field.AssociationBehavior), nil
	}
	if field.Kind == FieldHierarchy {
		entity := imports.Ref(field.TargetEntityClass)
		collection := imports.Ref(field.TargetCollectionClass)
		properties := []string{
			"protected ?string $parentId = null;",
			"protected ?" + entity + " $" + field.HierarchyParentProperty + " = null;",
			"protected ?" + collection + " $" + field.PropertyName + " = null;",
		}
		methods := append(behaviorAccessors("parentId", "?string", false, field.Behavior), behaviorAccessors(field.HierarchyParentProperty, "?"+entity, false, field.AssociationBehavior)...)
		methods = append(methods, behaviorAccessors(field.PropertyName, "?"+collection, false, field.HierarchyChildrenBehavior)...)
		return properties, methods, nil
	}
	phpType, boolean, err := entityFieldPHPType(field, imports)
	if err != nil {
		return nil, nil, err
	}
	if field.Implementation != nil {
		if field.Implementation.EntityType != "" {
			phpType = entityImplementationType(field.Implementation.EntityType, imports)
		}
		boolean = field.Implementation.EntityBooleanGetter
	}
	defaultValue := ""
	returnType := phpType
	if field.Translated || !field.Required {
		phpType = nullablePHPType(phpType)
		returnType = phpType
		defaultValue = " = null"
	}
	property := "protected " + phpType + " $" + field.PropertyName + defaultValue + ";"
	methods := behaviorAccessors(field.PropertyName, returnType, boolean, field.Behavior)
	if field.Implementation != nil && field.Implementation.ImplicitComputed {
		methods = methods[:1]
	}
	return []string{property}, methods, nil
}

func entityImplementationType(value string, imports *importTable) string {
	parts := strings.Split(value, "|")
	for index, part := range parts {
		part = strings.Trim(part, `\ `)
		if !isBuiltinEntityType(part) {
			part = imports.Ref(part)
		}
		parts[index] = part
	}
	return strings.Join(parts, "|")
}

func nullablePHPType(value string) string {
	if value == "mixed" || strings.Contains(value, "null") {
		return value
	}
	if strings.Contains(value, "|") {
		return value + "|null"
	}
	return "?" + value
}

func behaviorAccessors(property, phpType string, boolean bool, behavior *FieldBehavior) []string {
	result := accessors(property, phpType, boolean)
	if behavior != nil && (behavior.Runtime || behavior.Computed) {
		return result[:1]
	}
	return result
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
	case FieldString, FieldLongText, FieldBinaryID, FieldForeignKey:
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

func entityFieldPHPType(field FieldSpec, imports *importTable) (string, bool, error) {
	if field.Kind == FieldEnum {
		if strings.TrimSpace(field.EnumClass) == "" {
			return "", false, fmt.Errorf("enum field %q has no enum class", field.PropertyName)
		}
		return imports.Ref(field.EnumClass), false, nil
	}
	return entityPHPType(field.Kind)
}

func definitionImports(spec EntitySpec) []string {
	var imports []string
	if spec.DefinitionKind != DefinitionBulkExtension {
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\FieldCollection`)
	}
	switch spec.DefinitionKind {
	case DefinitionBulkExtension:
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\BulkEntityExtension`)
		for _, target := range spec.BulkExtensions {
			imports = append(imports, target.ExtendedDefinitionClass)
		}
	case DefinitionExtension:
		imports = append(imports,
			`Shopware\Core\Framework\DataAbstractionLayer\EntityExtension`,
			`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Extension`,
			spec.ExtendedDefinitionClass,
		)
	case DefinitionMapping:
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\MappingEntityDefinition`)
		if spec.EntityClass != "" && spec.CollectionClass != "" {
			imports = append(imports, spec.EntityClass, spec.CollectionClass)
		}
	default:
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition`, spec.EntityClass, spec.CollectionClass)
	}
	if (spec.DefinitionKind == DefinitionEntity || spec.DefinitionKind == DefinitionExtension) &&
		(spec.ReadProtected || spec.WriteProtected || len(spec.PreservedProtections) != 0 || strings.TrimSpace(spec.ProtectionMethodRaw) != "") {
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\EntityProtectionCollection`)
	}
	if spec.ReadProtected {
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\ReadProtection`)
	}
	if spec.WriteProtected {
		imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\EntityProtection\WriteProtection`)
	}
	if spec.DefinitionMetadata != nil && spec.DefinitionMetadata.HydratorClass != "" {
		imports = append(imports, spec.DefinitionMetadata.HydratorClass)
	}
	if spec.DefinitionBehavior != nil {
		imports = append(imports, spec.DefinitionBehavior.ParentDefinitionClass)
		if len(spec.DefinitionBehavior.RestrictDeleteMetaProperties) != 0 {
			imports = append(imports, `Shopware\Core\Framework\DataAbstractionLayer\Field\Field`)
		}
	}
	fieldClasses := map[FieldKind]string{
		FieldID: `Shopware\Core\Framework\DataAbstractionLayer\Field\IdField`, FieldBinaryID: `Shopware\Core\Framework\DataAbstractionLayer\Field\IdField`, FieldString: `Shopware\Core\Framework\DataAbstractionLayer\Field\StringField`,
		FieldEnum:     `Shopware\Core\Framework\DataAbstractionLayer\Field\EnumField`,
		FieldLongText: `Shopware\Core\Framework\DataAbstractionLayer\Field\LongTextField`, FieldInt: `Shopware\Core\Framework\DataAbstractionLayer\Field\IntField`,
		FieldFloat: `Shopware\Core\Framework\DataAbstractionLayer\Field\FloatField`, FieldBool: `Shopware\Core\Framework\DataAbstractionLayer\Field\BoolField`,
		FieldDate: `Shopware\Core\Framework\DataAbstractionLayer\Field\DateField`, FieldDateTime: `Shopware\Core\Framework\DataAbstractionLayer\Field\DateTimeField`,
		FieldJSON: `Shopware\Core\Framework\DataAbstractionLayer\Field\JsonField`, FieldList: `Shopware\Core\Framework\DataAbstractionLayer\Field\ListField`,
		FieldObject: `Shopware\Core\Framework\DataAbstractionLayer\Field\ObjectField`, FieldBlob: `Shopware\Core\Framework\DataAbstractionLayer\Field\BlobField`,
		FieldAutoIncrement: `Shopware\Core\Framework\DataAbstractionLayer\Field\AutoIncrementField`, FieldCreatedAt: `Shopware\Core\Framework\DataAbstractionLayer\Field\CreatedAtField`,
		FieldUpdatedAt: `Shopware\Core\Framework\DataAbstractionLayer\Field\UpdatedAtField`, FieldVersion: `Shopware\Core\Framework\DataAbstractionLayer\Field\VersionField`,
		FieldReferenceVersion: `Shopware\Core\Framework\DataAbstractionLayer\Field\ReferenceVersionField`, FieldForeignKey: `Shopware\Core\Framework\DataAbstractionLayer\Field\FkField`,
		FieldManyToOne: `Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField`, FieldOneToOne: `Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField`,
		FieldOneToMany: `Shopware\Core\Framework\DataAbstractionLayer\Field\OneToManyAssociationField`, FieldManyToMany: `Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToManyAssociationField`,
		FieldHierarchy: `Shopware\Core\Framework\DataAbstractionLayer\Field\ParentFkField`,
	}
	need := make(map[string]struct{})
	if spec.DefinitionBehavior != nil {
		for _, field := range spec.DefinitionBehavior.DefaultFields {
			collectDefinitionFieldImports(need, fieldClasses, field)
		}
		for _, field := range spec.DefinitionBehavior.BaseFields {
			collectDefinitionFieldImports(need, fieldClasses, field)
		}
	}
	for _, field := range spec.Fields {
		if field.TranslationDefinitionOnly || isImplicitTimestampField(spec, field.Kind) {
			continue
		}
		collectDefinitionFieldImports(need, fieldClasses, field)
	}
	for _, target := range spec.BulkExtensions {
		for _, field := range target.Fields {
			collectDefinitionFieldImports(need, fieldClasses, withEntityExtensionFlag(field))
		}
	}
	collectTranslationAssociationImports(need, spec.Translation)
	collectFieldModificationImports(need, spec.FieldModifications)
	for class := range need {
		imports = append(imports, class)
	}
	return imports
}

func collectDefinitionFieldImports(need map[string]struct{}, fieldClasses map[FieldKind]string, field FieldSpec) {
	if field.Translated {
		collectTranslatedFacadeImports(need, field)
		return
	}
	if field.Implementation != nil && field.Implementation.Class != "" {
		need[field.Implementation.Class] = struct{}{}
	} else if class := fieldClasses[field.Kind]; class != "" {
		need[class] = struct{}{}
	}
	collectHierarchyImports(need, field)
	collectRelationImports(need, field)
	if field.Kind == FieldReferenceVersion {
		need[field.TargetDefinitionClass] = struct{}{}
	}
	if field.Kind == FieldList && field.ElementTypeClass != "" {
		need[field.ElementTypeClass] = struct{}{}
	}
	if field.Kind == FieldEnum && field.EnumClass != "" {
		need[field.EnumClass] = struct{}{}
	}
	collectDefinitionFieldFlagImports(need, field)
}

func collectTranslatedFacadeImports(need map[string]struct{}, field FieldSpec) {
	need[`Shopware\Core\Framework\DataAbstractionLayer\Field\TranslatedField`] = struct{}{}
	if field.APIAware || field.TranslationAPIAware != nil && *field.TranslationAPIAware {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware`] = struct{}{}
	}
	collectClassImports(need, field.APIAwareSources)
	collectClassImports(need, field.TranslationAPIAwareSources)
	if field.SearchRanking > 0 || field.TranslationSearchRank != nil && *field.TranslationSearchRank > 0 {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SearchRanking`] = struct{}{}
	}
	if field.TranslationInherited {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Inherited`] = struct{}{}
	}
	if field.TranslationWriteProtected {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected`] = struct{}{}
	}
	collectBehaviorImports(need, field.TranslationBehavior)
	collectMetadataImports(need, field.TranslationMetadata)
}

func collectHierarchyImports(need map[string]struct{}, field FieldSpec) {
	if field.Kind != FieldHierarchy {
		return
	}
	need[`Shopware\Core\Framework\DataAbstractionLayer\Field\ParentAssociationField`] = struct{}{}
	need[`Shopware\Core\Framework\DataAbstractionLayer\Field\ChildrenAssociationField`] = struct{}{}
	need[field.TargetDefinitionClass] = struct{}{}
	if field.HierarchyVersionAware {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\ReferenceVersionField`] = struct{}{}
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required`] = struct{}{}
	}
	collectBehaviorImports(need, field.HierarchyChildrenBehavior)
	collectBehaviorImports(need, field.HierarchyVersionBehavior)
	collectMetadataImports(need, field.HierarchyChildrenMetadata)
	collectMetadataImports(need, field.HierarchyVersionMetadata)
}

func collectRelationImports(need map[string]struct{}, field FieldSpec) {
	if field.Kind == FieldForeignKey && field.TargetDefinitionClass != "" {
		need[field.TargetDefinitionClass] = struct{}{}
	}
	if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && !field.UsesExistingColumn {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\FkField`] = struct{}{}
		need[field.TargetDefinitionClass] = struct{}{}
	}
	if field.Kind == FieldOneToMany || field.Kind == FieldManyToOne || field.Kind == FieldOneToOne || field.Kind == FieldManyToMany {
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
	if conditional := field.ConditionalAssociation; conditional != nil {
		switch conditional.AlternativeKind {
		case FieldManyToOne:
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\ManyToOneAssociationField`] = struct{}{}
		case FieldOneToOne:
			need[`Shopware\Core\Framework\DataAbstractionLayer\Field\OneToOneAssociationField`] = struct{}{}
		}
	}
}

func collectDefinitionFieldFlagImports(need map[string]struct{}, field FieldSpec) {
	if field.Required || field.Kind == FieldID {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required`] = struct{}{}
	}
	if field.Kind == FieldID || (field.Primary && field.Kind != FieldVersion) {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey`] = struct{}{}
	}
	if field.APIAware || field.AssociationAPIAware || field.HierarchyChildrenAPIAware || field.HierarchyVersionAPIAware {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware`] = struct{}{}
	}
	collectClassImports(need, field.APIAwareSources)
	collectClassImports(need, field.AssociationAPIAwareSources)
	collectClassImports(need, field.HierarchyChildrenAPISources)
	collectClassImports(need, field.HierarchyVersionAPISources)
	if field.SearchRanking > 0 || field.AssociationSearchRank > 0 || field.HierarchyChildrenRank > 0 {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SearchRanking`] = struct{}{}
	}
	if field.Inherited || field.AssociationInherited || field.HierarchyChildrenInherited || field.HierarchyVersionInherited {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Inherited`] = struct{}{}
	}
	if field.ReverseInheritedProperty != "" || field.HierarchyChildrenReverse != "" {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ReverseInherited`] = struct{}{}
	}
	if field.WriteProtected || field.AssociationWriteProtected || field.HierarchyChildrenProtected || field.HierarchyVersionProtected {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected`] = struct{}{}
	}
	collectBehaviorImports(need, field.Behavior)
	collectBehaviorImports(need, field.AssociationBehavior)
	collectMetadataImports(need, field.Metadata)
	collectMetadataImports(need, field.AssociationMetadata)
}

func collectBehaviorImports(need map[string]struct{}, behavior *FieldBehavior) {
	if behavior == nil {
		return
	}
	if behavior.Runtime {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Runtime`] = struct{}{}
	}
	if behavior.Computed {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Computed`] = struct{}{}
	}
	if behavior.NoConstraint {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\NoConstraint`] = struct{}{}
	}
}

func collectMetadataImports(need map[string]struct{}, metadata *FieldMetadata) {
	if metadata == nil {
		return
	}
	const prefix = `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\`
	if metadata.AllowHTML != nil {
		need[prefix+"AllowHtml"] = struct{}{}
	}
	if metadata.AllowEmptyString {
		need[prefix+"AllowEmptyString"] = struct{}{}
	}
	if metadata.AsArray {
		need[prefix+"AsArray"] = struct{}{}
	}
	if metadata.Immutable {
		need[prefix+"Immutable"] = struct{}{}
	}
	if metadata.Since != "" {
		need[prefix+"Since"] = struct{}{}
	}
	if metadata.Deprecated != nil {
		need[prefix+"Deprecated"] = struct{}{}
	}
	if metadata.IgnoreInOpenAPISchema {
		need[prefix+"IgnoreInOpenapiSchema"] = struct{}{}
	}
	if metadata.IgnoreInUnusedMediaSearch {
		need[prefix+"IgnoreInUnusedMediaSearch"] = struct{}{}
	}
	if metadata.APICriteriaAware {
		need[prefix+"ApiCriteriaAware"] = struct{}{}
	}
	if len(metadata.RuleAreas) != 0 {
		need[prefix+"RuleAreas"] = struct{}{}
	}
	if metadata.Choice != nil {
		need[prefix+"Choice"] = struct{}{}
	}
	if metadata.DoNotUseContext {
		need[prefix+"DoNotUseContext"] = struct{}{}
	}
	if metadata.Extension {
		need[prefix+"Extension"] = struct{}{}
	}
}

func collectTranslationAssociationImports(need map[string]struct{}, translation *TranslationSpec) {
	if translation == nil || !translation.Enabled {
		return
	}
	need[`Shopware\Core\Framework\DataAbstractionLayer\Field\TranslationsAssociationField`] = struct{}{}
	need[translation.DefinitionClass] = struct{}{}
	if translation.AssociationRequired {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required`] = struct{}{}
	}
	if translation.AssociationAPIAware {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware`] = struct{}{}
	}
	collectClassImports(need, translation.AssociationAPIAwareSources)
	if translation.AssociationInherited {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Inherited`] = struct{}{}
	}
	if translation.ReverseInheritedProperty != "" {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ReverseInherited`] = struct{}{}
	}
	if translation.AssociationWriteProtected {
		need[`Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected`] = struct{}{}
	}
	collectBehaviorImports(need, translation.AssociationBehavior)
	collectMetadataImports(need, translation.AssociationMetadata)
}

func collectClassImports(need map[string]struct{}, classes []string) {
	for _, class := range classes {
		if class = strings.Trim(class, `\ `); class != "" {
			need[class] = struct{}{}
		}
	}
}

func entityImports(spec EntitySpec) []string {
	imports := []string{`Shopware\Core\Framework\DataAbstractionLayer\Entity`, `Shopware\Core\Framework\DataAbstractionLayer\EntityIdTrait`}
	imports = append(imports, entityImplementationTraits(spec, false)...)
	if spec.Translation != nil && spec.Translation.Enabled {
		imports = append(imports, spec.Translation.CollectionClass)
	}
	fields := schemaDefinitionFields(spec)
	for _, field := range fields {
		if field.Kind == FieldEnum && field.EnumClass != "" {
			imports = append(imports, field.EnumClass)
		}
		if field.Implementation != nil && field.Implementation.ManageEntity {
			for _, entityType := range strings.Split(field.Implementation.EntityType, "|") {
				entityType = strings.Trim(entityType, `\ `)
				if entityType != "" && !isBuiltinEntityType(entityType) {
					imports = append(imports, entityType)
				}
			}
		}
		if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && field.TargetEntityClass != "" {
			imports = append(imports, field.TargetEntityClass)
		}
		if (field.Kind == FieldOneToMany || field.Kind == FieldManyToMany) && field.TargetCollectionClass != "" {
			imports = append(imports, field.TargetCollectionClass)
		}
		if field.Kind == FieldHierarchy {
			imports = append(imports, field.TargetEntityClass, field.TargetCollectionClass)
		}
	}
	return imports
}

func translationEntityImports(spec EntitySpec) []string {
	imports := []string{
		`Shopware\Core\Framework\DataAbstractionLayer\TranslationEntity`,
		spec.EntityClass,
	}
	imports = append(imports, entityImplementationTraits(spec, true)...)
	if spec.Translation != nil && spec.Translation.DefinitionBehavior != nil {
		fields := append([]FieldSpec(nil), spec.Translation.DefinitionBehavior.DefaultFields...)
		fields = append(fields, spec.Translation.DefinitionBehavior.BaseFields...)
		for _, field := range fields {
			if field.Implementation != nil && field.Implementation.ManageEntity {
				for _, entityType := range strings.Split(field.Implementation.EntityType, "|") {
					entityType = strings.Trim(entityType, `\ `)
					if entityType != "" && !isBuiltinEntityType(entityType) {
						imports = append(imports, entityType)
					}
				}
			}
			if (field.Kind == FieldManyToOne || field.Kind == FieldOneToOne) && field.TargetEntityClass != "" {
				imports = append(imports, field.TargetEntityClass)
			}
			if (field.Kind == FieldOneToMany || field.Kind == FieldManyToMany) && field.TargetCollectionClass != "" {
				imports = append(imports, field.TargetCollectionClass)
			}
		}
	}
	for _, field := range spec.Fields {
		if !field.Translated {
			continue
		}
		if field.Kind == FieldEnum && field.EnumClass != "" {
			imports = append(imports, field.EnumClass)
		}
		if field.Implementation == nil {
			continue
		}
		for _, entityType := range strings.Split(field.Implementation.EntityType, "|") {
			entityType = strings.Trim(entityType, `\ `)
			if entityType != "" && !isBuiltinEntityType(entityType) {
				imports = append(imports, entityType)
			}
		}
	}
	return imports
}

func entityImplementationTraits(spec EntitySpec, translatedOnly bool) []string {
	seen := make(map[string]struct{})
	var result []string
	fields := schemaDefinitionFields(spec)
	if translatedOnly {
		fields = nil
		for _, field := range spec.Fields {
			if field.Translated {
				fields = append(fields, field)
			}
		}
		if spec.Translation != nil && spec.Translation.DefinitionBehavior != nil {
			behavior := spec.Translation.DefinitionBehavior
			if behavior.OverrideDefaultFields {
				fields = append(fields, behavior.DefaultFields...)
			}
			if behavior.OverrideBaseFields {
				fields = append(fields, behavior.BaseFields...)
			}
		}
	}
	for _, field := range fields {
		if field.Implementation == nil {
			continue
		}
		trait := strings.Trim(field.Implementation.EntityTrait, `\ `)
		if trait == "" {
			continue
		}
		key := strings.ToLower(trait)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trait)
	}
	sort.Strings(result)
	return result
}

func translationDefinitionImports(spec EntitySpec) []string {
	translation := spec.Translation
	imports := []string{
		`Shopware\Core\Framework\DataAbstractionLayer\EntityTranslationDefinition`,
		`Shopware\Core\Framework\DataAbstractionLayer\FieldCollection`,
		translation.EntityClass,
		translation.CollectionClass,
		translation.ParentDefinitionClass,
	}
	if translation.DefinitionMetadata != nil && translation.DefinitionMetadata.HydratorClass != "" {
		imports = append(imports, translation.DefinitionMetadata.HydratorClass)
	}
	storageSpec := spec
	storageSpec.Translation = nil
	storageSpec.Fields = nil
	storageSpec.DefinitionBehavior = translation.DefinitionBehavior
	for _, field := range spec.Fields {
		if !field.Translated || field.Kind == FieldLocked && !field.TranslationDefinitionOnly {
			continue
		}
		field.Translated = false
		storageSpec.Fields = append(storageSpec.Fields, field)
	}
	for _, class := range definitionImports(storageSpec) {
		if class == `Shopware\Core\Framework\DataAbstractionLayer\EntityDefinition` || class == storageSpec.EntityClass || class == storageSpec.CollectionClass {
			continue
		}
		imports = append(imports, class)
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

func namespaceOf(class string) string {
	class = strings.Trim(class, `\`)
	if index := strings.LastIndex(class, `\`); index >= 0 {
		return class[:index]
	}
	return ""
}

func hasVersionField(spec EntitySpec) bool {
	for _, field := range schemaDefinitionFields(spec) {
		if field.Kind == FieldVersion {
			return true
		}
	}
	return false
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
