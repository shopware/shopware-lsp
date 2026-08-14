package entityschema

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

var fieldFlagClasses = map[FieldFlagKind]string{
	FlagRequired:                  `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Required`,
	FlagPrimaryKey:                `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\PrimaryKey`,
	FlagAPIAware:                  `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiAware`,
	FlagSearchRanking:             `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SearchRanking`,
	FlagCascadeDelete:             `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\CascadeDelete`,
	FlagSetNullOnDelete:           `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\SetNullOnDelete`,
	FlagRestrictDelete:            `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\RestrictDelete`,
	FlagRuntime:                   `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Runtime`,
	FlagComputed:                  `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Computed`,
	FlagNoConstraint:              `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\NoConstraint`,
	FlagInherited:                 `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Inherited`,
	FlagReverseInherited:          `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ReverseInherited`,
	FlagWriteProtected:            `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\WriteProtected`,
	FlagAllowHTML:                 `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AllowHtml`,
	FlagAllowEmptyString:          `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AllowEmptyString`,
	FlagAsArray:                   `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\AsArray`,
	FlagImmutable:                 `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Immutable`,
	FlagSince:                     `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Since`,
	FlagDeprecated:                `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Deprecated`,
	FlagIgnoreInOpenAPISchema:     `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\IgnoreInOpenapiSchema`,
	FlagIgnoreInUnusedMediaSearch: `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\IgnoreInUnusedMediaSearch`,
	FlagAPICriteriaAware:          `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\ApiCriteriaAware`,
	FlagRuleAreas:                 `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\RuleAreas`,
	FlagChoice:                    `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Choice`,
	FlagDoNotUseContext:           `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\DoNotUseContext`,
	FlagExtension:                 `Shopware\Core\Framework\DataAbstractionLayer\Field\Flag\Extension`,
}

func fieldFlagKinds() []FieldFlagKind {
	result := make([]FieldFlagKind, 0, len(fieldFlagClasses))
	for kind := range fieldFlagClasses {
		result = append(result, kind)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func validateFieldModifications(v *specValidator) {
	spec := v.spec
	configured := len(spec.FieldModifications) != 0 || strings.TrimSpace(spec.ModifyFieldsMethodRaw) != ""
	if configured && spec.DefinitionKind != DefinitionExtension {
		v.add("entity.extension.modifyFields.owner.unsupported", "Only EntityExtension can modify existing target fields", "")
		return
	}
	if strings.TrimSpace(spec.ModifyFieldsMethodRaw) != "" && len(spec.FieldModifications) != 0 {
		v.add("entity.extension.modifyFields.raw.conflict", "A preserved custom modifyFields method cannot be combined with typed field modifications", "")
		return
	}
	seenIDs := make(map[string]struct{}, len(spec.FieldModifications))
	seenProperties := make(map[string]struct{}, len(spec.FieldModifications))
	for index, modification := range spec.FieldModifications {
		id := modification.ID
		if id == "" {
			id = fmt.Sprintf("modification-%d", index)
		}
		if _, duplicate := seenIDs[id]; duplicate {
			v.add("entity.extension.modifyFields.id.duplicate", fmt.Sprintf("Field modification ID %q is duplicated", id), id)
		}
		seenIDs[id] = struct{}{}
		if !propertyPattern.MatchString(modification.PropertyName) {
			v.add("entity.extension.modifyFields.property.invalid", fmt.Sprintf("Invalid target property %q", modification.PropertyName), id)
		} else if _, duplicate := seenProperties[modification.PropertyName]; duplicate {
			v.add("entity.extension.modifyFields.property.duplicate", fmt.Sprintf("Target property %q is modified more than once", modification.PropertyName), id)
		}
		seenProperties[modification.PropertyName] = struct{}{}
		if len(modification.AddFlags) == 0 && len(modification.RemoveFlags) == 0 {
			v.add("entity.extension.modifyFields.empty", "A field modification must add or remove at least one flag", id)
		}
		added := make(map[FieldFlagKind]struct{}, len(modification.AddFlags))
		for _, flag := range modification.AddFlags {
			if _, duplicate := added[flag.Kind]; duplicate {
				v.add("entity.extension.modifyFields.flag.duplicate", fmt.Sprintf("Flag %q is added more than once", flag.Kind), id)
			}
			added[flag.Kind] = struct{}{}
			validateFieldModificationFlag(v, flag, id)
		}
		removed := make(map[FieldFlagKind]struct{}, len(modification.RemoveFlags))
		for _, kind := range modification.RemoveFlags {
			if _, known := fieldFlagClasses[kind]; !known {
				v.add("entity.extension.modifyFields.flag.invalid", fmt.Sprintf("Unsupported DAL flag %q", kind), id)
			}
			if _, duplicate := removed[kind]; duplicate {
				v.add("entity.extension.modifyFields.flag.duplicate", fmt.Sprintf("Flag %q is removed more than once", kind), id)
			}
			removed[kind] = struct{}{}
			if _, conflict := added[kind]; conflict {
				v.add("entity.extension.modifyFields.flag.conflict", fmt.Sprintf("Flag %q cannot be added and removed together", kind), id)
			}
		}
	}
}

func validateFieldModificationFlag(v *specValidator, flag FieldFlagSpec, fieldID string) {
	if _, known := fieldFlagClasses[flag.Kind]; !known {
		v.add("entity.extension.modifyFields.flag.invalid", fmt.Sprintf("Unsupported DAL flag %q", flag.Kind), fieldID)
		return
	}
	if arguments := unexpectedFieldFlagArguments(flag); len(arguments) != 0 {
		v.add("entity.extension.modifyFields.flag.arguments.invalid", fmt.Sprintf("Flag %q does not accept %s", flag.Kind, strings.Join(arguments, ", ")), fieldID)
	}
	switch flag.Kind {
	case FlagAPIAware:
		v.validateAPIAware(true, flag.APISources, "entity.extension.modifyFields.apiAware.invalid", fieldID)
	case FlagSearchRanking:
		if flag.SearchRanking < 0 || math.IsNaN(flag.SearchRanking) || math.IsInf(flag.SearchRanking, 0) {
			v.add("entity.extension.modifyFields.searchRanking.invalid", "Search ranking must be a finite non-negative number", fieldID)
		}
	case FlagRuntime:
		behavior := &FieldBehavior{Runtime: true, RuntimeDependencies: flag.RuntimeDependencies, RuntimeDependenciesExpression: flag.RuntimeDependenciesExpression}
		v.validateBehavior(FieldSpec{ID: fieldID}, "modified field", behavior)
	case FlagInherited:
		if flag.InheritedForeignKey != "" && !storagePattern.MatchString(flag.InheritedForeignKey) {
			v.add("entity.extension.modifyFields.inherited.invalid", "Inherited foreign-key override must be a valid storage name", fieldID)
		}
	case FlagReverseInherited:
		if !propertyPattern.MatchString(flag.ReverseProperty) {
			v.add("entity.extension.modifyFields.reverseInherited.invalid", "ReverseInherited requires a valid target property", fieldID)
		}
	case FlagWriteProtected:
		v.validateWriteProtectedScopes(true, flag.WriteScopes, "entity.extension.modifyFields.writeProtected.invalid", fieldID)
	case FlagSince:
		if strings.TrimSpace(flag.Since) == "" {
			v.add("entity.extension.modifyFields.since.invalid", "Since requires a non-empty version", fieldID)
		}
	case FlagDeprecated:
		if flag.Deprecated == nil || strings.TrimSpace(flag.Deprecated.DeprecatedSince) == "" || strings.TrimSpace(flag.Deprecated.WillBeRemovedIn) == "" {
			v.add("entity.extension.modifyFields.deprecated.invalid", "Deprecated requires deprecation and removal versions", fieldID)
		}
	case FlagRuleAreas:
		if len(flag.RuleAreas) == 0 {
			v.add("entity.extension.modifyFields.ruleAreas.empty", "RuleAreas requires at least one area", fieldID)
		}
		for _, area := range flag.RuleAreas {
			if strings.TrimSpace(area) == "" {
				v.add("entity.extension.modifyFields.ruleAreas.invalid", "RuleAreas cannot contain empty values", fieldID)
				break
			}
		}
	case FlagChoice:
		if flag.Choice == nil || len(flag.Choice.Values) == 0 {
			v.add("entity.extension.modifyFields.choice.empty", "Choice requires at least one value", fieldID)
			return
		}
		for _, value := range flag.Choice.Values {
			if !validInlinePHPExpression(value) {
				v.add("entity.extension.modifyFields.choice.invalid", "Choice values must be safe PHP scalar or constant expressions", fieldID)
				break
			}
		}
	}
}

func unexpectedFieldFlagArguments(flag FieldFlagSpec) []string {
	var result []string
	allow := func(condition bool, name string, present bool) {
		if present && !condition {
			result = append(result, name)
		}
	}
	allow(flag.Kind == FlagAPIAware, "apiSources", len(flag.APISources) != 0)
	allow(flag.Kind == FlagSearchRanking, "searchRanking", flag.SearchRanking != 0)
	allow(flag.Kind == FlagSearchRanking, "searchTokenize", flag.SearchTokenize != nil)
	allow(flag.Kind == FlagRuntime, "runtimeDependencies", len(flag.RuntimeDependencies) != 0)
	allow(flag.Kind == FlagRuntime, "runtimeDependenciesExpression", strings.TrimSpace(flag.RuntimeDependenciesExpression) != "")
	allow(flag.Kind == FlagInherited, "inheritedForeignKey", flag.InheritedForeignKey != "")
	allow(flag.Kind == FlagReverseInherited, "reverseProperty", flag.ReverseProperty != "")
	allow(flag.Kind == FlagWriteProtected, "writeScopes", len(flag.WriteScopes) != 0)
	allow(flag.Kind == FlagAllowHTML, "allowHtmlSanitized", flag.AllowHTMLSanitized != nil)
	allow(flag.Kind == FlagCascadeDelete, "cloneRelevant", flag.CloneRelevant != nil)
	allow(flag.Kind == FlagSetNullOnDelete, "enforcedByConstraint", flag.EnforcedByConstraint != nil)
	allow(flag.Kind == FlagSince, "since", flag.Since != "")
	allow(flag.Kind == FlagDeprecated, "deprecated", flag.Deprecated != nil)
	allow(flag.Kind == FlagRuleAreas, "ruleAreas", len(flag.RuleAreas) != 0)
	allow(flag.Kind == FlagChoice, "choice", flag.Choice != nil)
	return result
}

func renderFieldModificationFlag(flag FieldFlagSpec, imports *importTable) string {
	switch flag.Kind {
	case FlagAPIAware:
		return renderAPIAware(imports, flag.APISources)
	case FlagSearchRanking:
		return renderSearchRanking(flag.SearchRanking, flag.SearchTokenize)
	case FlagCascadeDelete:
		if flag.CloneRelevant != nil {
			return "new CascadeDelete(" + strconv.FormatBool(*flag.CloneRelevant) + ")"
		}
	case FlagSetNullOnDelete:
		if flag.EnforcedByConstraint != nil {
			return "new SetNullOnDelete(" + strconv.FormatBool(*flag.EnforcedByConstraint) + ")"
		}
	case FlagRuntime:
		return renderBehaviorFlags(&FieldBehavior{Runtime: true, RuntimeDependencies: flag.RuntimeDependencies, RuntimeDependenciesExpression: flag.RuntimeDependenciesExpression})[0]
	case FlagInherited:
		return renderInheritedFlag(flag.InheritedForeignKey)
	case FlagReverseInherited:
		return "new ReverseInherited(" + quotePHP(flag.ReverseProperty) + ")"
	case FlagWriteProtected:
		return renderWriteProtectedFlag(flag.WriteScopes)
	case FlagAllowHTML:
		metadata := &FieldMetadata{AllowHTML: flag.AllowHTMLSanitized}
		if metadata.AllowHTML == nil {
			value := true
			metadata.AllowHTML = &value
		}
		return renderMetadataFlags(metadata)[0]
	case FlagSince:
		return renderMetadataFlags(&FieldMetadata{Since: flag.Since})[0]
	case FlagDeprecated:
		return renderMetadataFlags(&FieldMetadata{Deprecated: flag.Deprecated})[0]
	case FlagRuleAreas:
		return renderMetadataFlags(&FieldMetadata{RuleAreas: flag.RuleAreas})[0]
	case FlagChoice:
		return renderMetadataFlags(&FieldMetadata{Choice: flag.Choice})[0]
	}
	return "new " + imports.Ref(fieldFlagClasses[flag.Kind]) + "()"
}

func renderFieldModifications(spec EntitySpec, imports *importTable) string {
	if strings.TrimSpace(spec.ModifyFieldsMethodRaw) != "" {
		return strings.TrimSpace(spec.ModifyFieldsMethodRaw)
	}
	if len(spec.FieldModifications) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("public function modifyFields(FieldCollection $collection): void\n{\n")
	for _, modification := range spec.FieldModifications {
		receiver := "$collection->get(" + quotePHP(modification.PropertyName) + ")?->"
		if len(modification.AddFlags) != 0 {
			flags := make([]string, 0, len(modification.AddFlags))
			for _, flag := range modification.AddFlags {
				flags = append(flags, renderFieldModificationFlag(flag, imports))
			}
			builder.WriteString("    ")
			builder.WriteString(receiver)
			builder.WriteString("addFlags(")
			builder.WriteString(strings.Join(flags, ", "))
			builder.WriteString(");\n")
		}
		for _, kind := range modification.RemoveFlags {
			builder.WriteString("    ")
			builder.WriteString(receiver)
			builder.WriteString("removeFlag(")
			builder.WriteString(imports.Ref(fieldFlagClasses[kind]))
			builder.WriteString("::class);\n")
		}
	}
	builder.WriteString("}")
	return builder.String()
}

func collectFieldModificationImports(need map[string]struct{}, modifications []FieldModificationSpec) {
	for _, modification := range modifications {
		for _, flag := range modification.AddFlags {
			if className := fieldFlagClasses[flag.Kind]; className != "" {
				need[className] = struct{}{}
			}
			collectClassImports(need, flag.APISources)
		}
		for _, kind := range modification.RemoveFlags {
			if className := fieldFlagClasses[kind]; className != "" {
				need[className] = struct{}{}
			}
		}
	}
}
