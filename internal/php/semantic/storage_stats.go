package semantic

import (
	"github.com/shopware/shopware-lsp/internal/parser/cst"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

// WorkspaceStorageStats describes the cardinality of the immutable PHP
// declaration/reference graph. It is intended for opt-in profiling and does
// not retain the temporary uniqueness maps used to compute it.
type WorkspaceStorageStats struct {
	Documents  int
	Symbols    int
	References int

	SymbolIndexEntries       int
	SymbolIndexCapacity      int
	PathIndexEntries         int
	SymbolCompactRanges      int
	SymbolFullRanges         int
	SymbolMissingSelections  int
	SymbolMissingBodies      int
	MaxSymbolRangeLength     int
	MaxSymbolSelectionOffset int
	MaxSymbolSelectionLength int
	MaxSymbolBodyOffset      int
	MaxSymbolBodyLength      int
	ClassNames               int
	ClassIDs                 int
	UniqueClassIDs           int
	FunctionNames            int
	FunctionIDs              int
	UniqueFunctionIDs        int
	ConstantNames            int
	ConstantIDs              int
	UniqueConstantIDs        int
	MemberContainers         int
	MemberNames              int
	MemberIDs                int
	UniqueMemberIDs          int
	MemberDuplicateIDs       int
	MemberAlternateNames     int
	MemberContainers1        int
	MemberContainers2To4     int
	MemberContainers5To8     int
	MemberContainers9To16    int
	MemberContainers17To32   int
	MemberContainersOver32   int
	MaxMembersPerContainer   int
	GlobalIDs                int
	UniqueGlobalIDs          int

	Signatures                  int
	Parameters                  int
	ParameterNativeTypes        int
	ParameterDocTypes           int
	ParameterExplicitTypes      int
	ParametersWithAssistantTags int
	ParameterAssistantTags      int
	ParameterFullRanges         int
	ParameterDerivedIDs         int
	ParameterFullIDs            int
	Templates                   int
	Throws                      int
	LiteralReturns              int
	ConstantReturns             int
	SignaturesWithExtras        int
	Hierarchies                 int
	Metadata                    int
	NoSideTables                int
	SignatureOnly               int
	HierarchyOnly               int
	MetadataOnly                int
	SignatureHierarchy          int
	SignatureMetadata           int
	HierarchyMetadata           int
	AllSideTables               int

	HierarchyExtendsNames         int
	HierarchyImplementsNames      int
	HierarchyTraitNames           int
	HierarchyExtendsTypes         int
	HierarchyImplementsTypes      int
	HierarchyTraitTypes           int
	HierarchyPairedNameTypes      int
	HierarchyExactNamedTypePairs  int
	HierarchiesWithExtends        int
	HierarchiesWithImplements     int
	HierarchiesWithTraits         int
	HierarchiesWithMultipleGroups int

	Attributes                 int
	ConstantArrayItems         int
	DocSummaryBytes            int
	MetadataAttributesOnly     int
	MetadataConstantArrayOnly  int
	MetadataDocSummaryOnly     int
	MetadataAttributesConstant int
	MetadataAttributesSummary  int
	MetadataConstantSummary    int
	MetadataAllFields          int

	ReferenceStrings               int
	ReferenceTypes                 int
	ReferenceValues                int
	ReferenceCompactRanges         int
	ReferenceFullRanges            int
	MaxQualified                   int
	MaxCandidates                  int
	MaxReferenceRangeLength        int
	MaxReferenceValueStart         int
	MaxReferenceStringsPerDocument int
	MaxReferenceTypesPerDocument   int
	MaxReferenceValuesPerDocument  int

	ReferenceStringCapacity int

	SymbolsUsingDocumentPath    int
	SymbolStringSlots           int
	UniqueSymbolStrings         int
	CoreSymbolStringSlots       int
	CoreSymbolNonEmptyStrings   int
	CoreSymbolUniquePerDocument int
	UniqueCoreSymbolStrings     int
	SymbolEmptyContainers       int
	SymbolNameEqualsQualified   int
	SymbolIDEqualsQualified     int
	SymbolStringTables          int
	SymbolStringTableValues     int
	SymbolStringTableCapacity   int
	UniqueReferenceStrings      int
	UniqueSymbolStringBytes     int
	UniqueReferenceStringBytes  int

	InternedStringSlots   int
	UniqueInternedStrings int
	UniqueInternedBytes   int

	TypeSlots       int
	UniqueTypeKeys  int
	UniqueTypeBytes int
}

// WorkspaceStorageStats returns cardinalities for this snapshot's retained
// base documents. Overlay generations report their locally replaced documents;
// callers profiling a complete workspace should invoke it on the base
// generation returned by Store.Snapshot.
func (s *Snapshot) WorkspaceStorageStats() WorkspaceStorageStats {
	var stats WorkspaceStorageStats
	if s == nil {
		return stats
	}
	stats.SymbolIndexEntries = s.symbols.Len() +
		len(s.expanded) +
		len(s.overrides)
	stats.SymbolIndexCapacity = len(s.symbols.slots)
	stats.PathIndexEntries = len(s.pathRefs)
	classIDs := make(map[SymbolID]struct{})
	stats.ClassNames = len(s.classes.primary)
	for _, id := range s.classes.primary {
		stats.ClassIDs++
		classIDs[id] = struct{}{}
	}
	for _, ids := range s.classes.alternates {
		stats.ClassIDs += len(ids)
		for _, id := range ids {
			classIDs[id] = struct{}{}
		}
	}
	stats.UniqueClassIDs = len(classIDs)
	functionIDs := make(map[SymbolID]struct{})
	stats.FunctionNames = len(s.functions.primary)
	for _, id := range s.functions.primary {
		stats.FunctionIDs++
		functionIDs[id] = struct{}{}
	}
	for _, ids := range s.functions.alternates {
		stats.FunctionIDs += len(ids)
		for _, id := range ids {
			functionIDs[id] = struct{}{}
		}
	}
	stats.UniqueFunctionIDs = len(functionIDs)
	constantIDs := make(map[SymbolID]struct{})
	stats.ConstantNames = len(s.constants.primary)
	for _, id := range s.constants.primary {
		stats.ConstantIDs++
		constantIDs[id] = struct{}{}
	}
	for _, ids := range s.constants.alternates {
		stats.ConstantIDs += len(ids)
		for _, id := range ids {
			constantIDs[id] = struct{}{}
		}
	}
	stats.UniqueConstantIDs = len(constantIDs)
	memberIDs := make(map[SymbolID]struct{})
	stats.MemberContainers = len(s.compactMembers.containers) + len(s.members)
	for container, span := range s.compactMembers.containers {
		nameCount := int(span.count)
		addMemberContainerStorageStats(nameCount, &stats)
		stats.MemberNames += nameCount
		values := s.compactMembers.valuesForContainer(container)
		stats.MemberIDs += len(values)
		stats.MemberDuplicateIDs += len(values) - nameCount
		entryStart := int(span.start)
		entryEnd := entryStart + nameCount
		for _, valueSpan := range s.compactMembers.valueSpans[entryStart:entryEnd] {
			if valueSpan.count > 1 {
				stats.MemberAlternateNames++
			}
		}
		for _, symbol := range values {
			memberIDs[symbol.ID] = struct{}{}
		}
	}
	for _, names := range s.members {
		stats.MemberNames += len(names)
		stats.MemberIDs += len(names)
		addMemberContainerStorageStats(len(names), &stats)
		for _, id := range names {
			memberIDs[id] = struct{}{}
		}
	}
	for _, names := range s.memberAlternates {
		for _, ids := range names {
			stats.MemberAlternateNames++
			stats.MemberIDs += len(ids)
			stats.MemberDuplicateIDs += len(ids)
			for _, id := range ids {
				memberIDs[id] = struct{}{}
			}
		}
	}
	stats.UniqueMemberIDs = len(memberIDs)
	globalIDs := make(map[SymbolID]struct{})
	stats.GlobalIDs = len(s.globals)
	for _, id := range s.globals {
		globalIDs[id] = struct{}{}
	}
	stats.UniqueGlobalIDs = len(globalIDs)

	symbolStrings := make(map[string]struct{})
	coreSymbolStrings := make(map[string]struct{})
	referenceStrings := make(map[string]struct{})
	referenceStringTables := make(
		map[*workspaceReferenceStringTable]struct{},
	)
	symbolStringTables := make(
		map[*workspaceSymbolStringTable]struct{},
	)
	internedStrings := make(map[string]struct{})
	typeKeys := make(map[string]struct{})
	for path, document := range s.pathRefs {
		if document == nil {
			continue
		}
		stats.Documents++
		addInternedStorageString(internedStrings, document.Path, &stats)
		addInternedStorageString(internedStrings, document.Namespace, &stats)
		stats.Symbols += len(document.Symbols)
		stats.References += len(document.References)
		stats.MaxReferenceStringsPerDocument = max(
			stats.MaxReferenceStringsPerDocument,
			document.referenceStringCount(),
		)
		stats.MaxReferenceTypesPerDocument = max(
			stats.MaxReferenceTypesPerDocument,
			len(document.referenceTypes),
		)
		stats.MaxReferenceValuesPerDocument = max(
			stats.MaxReferenceValuesPerDocument,
			len(document.referenceValues),
		)
		for index := range document.References {
			reference := &document.References[index]
			stats.MaxQualified = max(
				stats.MaxQualified,
				int(reference.qualifiedCount()),
			)
			stats.MaxCandidates = max(
				stats.MaxCandidates,
				int(reference.candidateCount()),
			)
			rng := reference.rangeValue(document)
			rangeLength, _ := workspaceSymbolRangeLength(rng)
			stats.MaxReferenceRangeLength = max(
				stats.MaxReferenceRangeLength,
				rangeLength,
			)
			stats.MaxReferenceValueStart = max(
				stats.MaxReferenceValueStart,
				int(reference.valueStart(document)),
			)
			if reference.hasFullLocation() {
				stats.ReferenceFullRanges++
			} else {
				stats.ReferenceCompactRanges++
			}
		}
		stats.Signatures += len(document.signatures)
		stats.Hierarchies += len(document.hierarchies)
		stats.Metadata += len(document.metadata)
		for index := range document.signatures {
			signature := &document.signatures[index]
			stats.Parameters += len(signature.Parameters)
			for parameterIndex := range signature.Parameters {
				parameter := &signature.Parameters[parameterIndex]
				addInternedStorageString(
					internedStrings,
					string(parameter.id(nil)),
					&stats,
				)
				addInternedStorageString(
					internedStrings,
					parameter.Name,
					&stats,
				)
				for _, tag := range parameter.assistantTags() {
					addInternedStorageString(internedStrings, tag, &stats)
				}
				addStorageType(
					typeKeys,
					parameter.effectiveType(),
					&stats,
				)
				addStorageType(typeKeys, parameter.NativeType, &stats)
				addStorageType(typeKeys, parameter.DocType, &stats)
				if !parameter.NativeType.IsUnknown() {
					stats.ParameterNativeTypes++
				}
				if !parameter.DocType.IsUnknown() {
					stats.ParameterDocTypes++
				}
				effectiveType := parameter.effectiveType()
				if !effectiveType.Equal(parameter.NativeType) &&
					!effectiveType.Equal(parameter.DocType) {
					stats.ParameterExplicitTypes++
				}
				if len(parameter.assistantTags()) != 0 {
					stats.ParametersWithAssistantTags++
					stats.ParameterAssistantTags += len(
						parameter.assistantTags(),
					)
				}
				if parameter.Extras != nil &&
					parameter.Extras.Ranges != nil {
					stats.ParameterFullRanges++
				}
			}
			stats.Templates += len(signature.templates())
			for _, template := range signature.templates() {
				addInternedStorageString(
					internedStrings,
					template.Name,
					&stats,
				)
				addStorageType(typeKeys, template.Bound, &stats)
				addStorageType(typeKeys, template.Default, &stats)
			}
			stats.Throws += len(signature.throws())
			for _, value := range signature.throws() {
				addStorageType(typeKeys, value, &stats)
			}
			stats.LiteralReturns += len(signature.literalReturns())
			for _, literalReturn := range signature.literalReturns() {
				addInternedStorageString(
					internedStrings,
					literalReturn.Value,
					&stats,
				)
				addStorageType(typeKeys, literalReturn.Type, &stats)
			}
			stats.ConstantReturns += len(signature.constantReturns())
			for _, constantReturn := range signature.constantReturns() {
				addInternedStorageString(
					internedStrings,
					constantReturn.Receiver,
					&stats,
				)
				addInternedStorageString(
					internedStrings,
					constantReturn.Name,
					&stats,
				)
			}
			if signature.Extras != nil {
				stats.SignaturesWithExtras++
			}
		}
		stats.ReferenceStrings += document.referenceStringCount()
		stats.ReferenceStringCapacity += cap(document.referenceStringIDs)
		if document.referenceStrings != nil {
			if _, exists :=
				referenceStringTables[document.referenceStrings]; !exists {
				referenceStringTables[document.referenceStrings] = struct{}{}
				stats.ReferenceStringCapacity += cap(
					document.referenceStrings.Values,
				)
			}
		}
		if document.symbolStrings != nil {
			if _, exists :=
				symbolStringTables[document.symbolStrings]; !exists {
				symbolStringTables[document.symbolStrings] = struct{}{}
				stats.SymbolStringTables++
				stats.SymbolStringTableValues += len(
					document.symbolStrings.Values,
				)
				stats.SymbolStringTableCapacity += cap(
					document.symbolStrings.Values,
				)
			}
		}
		stats.ReferenceTypes += len(document.referenceTypes)
		stats.ReferenceValues += len(document.referenceValues)
		documentCoreStrings := make(map[string]struct{})
		for index := range document.Symbols {
			symbol := &document.Symbols[index]
			addWorkspaceSymbolRangeStats(symbol, &stats)
			if signature := symbol.signature(); signature != nil {
				for parameterIndex := range signature.Parameters {
					parameter := &signature.Parameters[parameterIndex]
					if parameter.Extras == nil ||
						parameter.Extras.ID == "" {
						stats.ParameterDerivedIDs++
					} else {
						stats.ParameterFullIDs++
					}
				}
			}
			switch {
			case symbol.signature() != nil &&
				symbol.hierarchy() != nil &&
				symbol.metadata() != nil:
				stats.AllSideTables++
			case symbol.signature() != nil && symbol.hierarchy() != nil:
				stats.SignatureHierarchy++
			case symbol.signature() != nil && symbol.metadata() != nil:
				stats.SignatureMetadata++
			case symbol.hierarchy() != nil && symbol.metadata() != nil:
				stats.HierarchyMetadata++
			case symbol.signature() != nil:
				stats.SignatureOnly++
			case symbol.hierarchy() != nil:
				stats.HierarchyOnly++
			case symbol.metadata() != nil:
				stats.MetadataOnly++
			default:
				stats.NoSideTables++
			}
			addStorageType(typeKeys, symbol.Type, &stats)
			addStorageType(typeKeys, symbol.NativeType, &stats)
			addStorageType(typeKeys, symbol.DocType, &stats)
			addStorageType(typeKeys, symbol.ReturnType, &stats)
			addInternedStorageString(
				internedStrings,
				string(symbol.ID),
				&stats,
			)
			name := symbol.name()
			fullyQualified := symbol.fullyQualified()
			container := symbol.container()
			addInternedStorageString(internedStrings, name, &stats)
			addInternedStorageString(
				internedStrings,
				fullyQualified,
				&stats,
			)
			addInternedStorageString(
				internedStrings,
				string(container),
				&stats,
			)
			coreValues := [...]string{
				string(symbol.ID),
				name,
				fullyQualified,
				string(container),
			}
			stats.CoreSymbolStringSlots += len(coreValues)
			for _, value := range coreValues {
				if value == "" {
					continue
				}
				stats.CoreSymbolNonEmptyStrings++
				documentCoreStrings[value] = struct{}{}
				coreSymbolStrings[value] = struct{}{}
			}
			if container == "" {
				stats.SymbolEmptyContainers++
			}
			if name == fullyQualified {
				stats.SymbolNameEqualsQualified++
			}
			if string(symbol.ID) == fullyQualified {
				stats.SymbolIDEqualsQualified++
			}
			if symbol.path() == path {
				stats.SymbolsUsingDocumentPath++
			}
			addStorageString(symbolStrings, string(symbol.ID), &stats)
			addStorageString(symbolStrings, name, &stats)
			addStorageString(symbolStrings, fullyQualified, &stats)
			addStorageString(symbolStrings, string(container), &stats)
			addStorageString(symbolStrings, symbol.path(), &stats)
		}
		stats.CoreSymbolUniquePerDocument += len(documentCoreStrings)
		for index := range document.hierarchies {
			hierarchy := &document.hierarchies[index]
			extends := hierarchy.extends()
			implements := hierarchy.implements()
			traits := hierarchy.traits()
			extendsTypes := hierarchy.extendsTypes()
			implementsTypes := hierarchy.implementsTypes()
			traitTypes := hierarchy.traitTypes()
			stats.HierarchyExtendsNames += len(extends)
			stats.HierarchyImplementsNames += len(implements)
			stats.HierarchyTraitNames += len(traits)
			stats.HierarchyExtendsTypes += len(extendsTypes)
			stats.HierarchyImplementsTypes += len(implementsTypes)
			stats.HierarchyTraitTypes += len(traitTypes)
			hierarchyGroups := 0
			if len(extends) != 0 || len(extendsTypes) != 0 {
				stats.HierarchiesWithExtends++
				hierarchyGroups++
			}
			if len(implements) != 0 || len(implementsTypes) != 0 {
				stats.HierarchiesWithImplements++
				hierarchyGroups++
			}
			if len(traits) != 0 || len(traitTypes) != 0 {
				stats.HierarchiesWithTraits++
				hierarchyGroups++
			}
			if hierarchyGroups > 1 {
				stats.HierarchiesWithMultipleGroups++
			}
			addHierarchyPairStats(
				extends,
				extendsTypes,
				&stats,
			)
			addHierarchyPairStats(
				implements,
				implementsTypes,
				&stats,
			)
			addHierarchyPairStats(
				traits,
				traitTypes,
				&stats,
			)
			for _, value := range extends {
				addInternedStorageString(internedStrings, value, &stats)
			}
			for _, value := range implements {
				addInternedStorageString(internedStrings, value, &stats)
			}
			for _, value := range traits {
				addInternedStorageString(internedStrings, value, &stats)
			}
			for _, value := range extendsTypes {
				addStorageType(typeKeys, value, &stats)
			}
			for _, value := range implementsTypes {
				addStorageType(typeKeys, value, &stats)
			}
			for _, value := range traitTypes {
				addStorageType(typeKeys, value, &stats)
			}
		}
		for index := range document.metadata {
			metadata := &document.metadata[index]
			attributes := metadata.attributes()
			constantArray := metadata.constantArray()
			stats.Attributes += len(attributes)
			stats.ConstantArrayItems += len(constantArray)
			stats.DocSummaryBytes += len(metadata.DocSummary)
			switch {
			case len(attributes) != 0 &&
				len(constantArray) != 0 &&
				metadata.DocSummary != "":
				stats.MetadataAllFields++
			case len(attributes) != 0 &&
				len(constantArray) != 0:
				stats.MetadataAttributesConstant++
			case len(attributes) != 0 &&
				metadata.DocSummary != "":
				stats.MetadataAttributesSummary++
			case len(constantArray) != 0 &&
				metadata.DocSummary != "":
				stats.MetadataConstantSummary++
			case len(attributes) != 0:
				stats.MetadataAttributesOnly++
			case len(constantArray) != 0:
				stats.MetadataConstantArrayOnly++
			case metadata.DocSummary != "":
				stats.MetadataDocSummaryOnly++
			}
			for _, attribute := range attributes {
				addInternedStorageString(
					internedStrings,
					attribute.Name,
					&stats,
				)
			}
			for _, item := range constantArray {
				addInternedStorageString(
					internedStrings,
					item.Key,
					&stats,
				)
				addInternedStorageString(
					internedStrings,
					item.Value,
					&stats,
				)
				addStorageType(typeKeys, item.Type, &stats)
			}
			addInternedStorageString(
				internedStrings,
				metadata.DocSummary,
				&stats,
			)
		}
		for _, value := range document.referenceTypes {
			addStorageType(typeKeys, value, &stats)
		}
		for stringIndex := 1; stringIndex <= document.referenceStringCount(); stringIndex++ {
			value := document.referenceString(uint32(stringIndex))
			addInternedStorageString(internedStrings, value, &stats)
			if value == "" {
				continue
			}
			if _, exists := referenceStrings[value]; !exists {
				stats.UniqueReferenceStringBytes += len(value)
			}
			referenceStrings[value] = struct{}{}
		}
	}
	stats.UniqueSymbolStrings = len(symbolStrings)
	stats.UniqueCoreSymbolStrings = len(coreSymbolStrings)
	stats.UniqueReferenceStrings = len(referenceStrings)
	stats.UniqueInternedStrings = len(internedStrings)
	stats.UniqueTypeKeys = len(typeKeys)
	return stats
}

func addMemberContainerStorageStats(
	count int,
	stats *WorkspaceStorageStats,
) {
	if stats == nil {
		return
	}
	stats.MaxMembersPerContainer = max(stats.MaxMembersPerContainer, count)
	switch {
	case count == 1:
		stats.MemberContainers1++
	case count <= 4:
		stats.MemberContainers2To4++
	case count <= 8:
		stats.MemberContainers5To8++
	case count <= 16:
		stats.MemberContainers9To16++
	case count <= 32:
		stats.MemberContainers17To32++
	default:
		stats.MemberContainersOver32++
	}
}

func addWorkspaceSymbolRangeStats(
	symbol *workspaceSymbol,
	stats *WorkspaceStorageStats,
) {
	if symbol == nil || stats == nil {
		return
	}
	ranges := symbol.ranges()
	rng := ranges.Range
	selection := ranges.SelectionRange
	body := ranges.BodyRange
	rangeLength, rangeOK := workspaceSymbolRangeLength(rng)
	stats.MaxSymbolRangeLength = max(
		stats.MaxSymbolRangeLength,
		rangeLength,
	)
	selectionOK := true
	if selection == (cst.TextRange{}) {
		stats.SymbolMissingSelections++
	} else {
		offset, length, ok := workspaceSymbolSubrangeStats(rng.Start, selection)
		stats.MaxSymbolSelectionOffset = max(
			stats.MaxSymbolSelectionOffset,
			offset,
		)
		stats.MaxSymbolSelectionLength = max(
			stats.MaxSymbolSelectionLength,
			length,
		)
		selectionOK = ok
	}
	bodyOK := true
	if body == (cst.TextRange{}) {
		stats.SymbolMissingBodies++
	} else {
		offset, length, ok := workspaceSymbolSubrangeStats(rng.Start, body)
		stats.MaxSymbolBodyOffset = max(stats.MaxSymbolBodyOffset, offset)
		stats.MaxSymbolBodyLength = max(stats.MaxSymbolBodyLength, length)
		bodyOK = ok
	}
	if rangeOK && selectionOK && bodyOK {
		stats.SymbolCompactRanges++
	} else {
		stats.SymbolFullRanges++
	}
}

func workspaceSymbolRangeLength(rng cst.TextRange) (int, bool) {
	if rng.End < rng.Start {
		return 0, false
	}
	length := rng.End - rng.Start
	return int(length), length < workspaceCompactRangeMissing
}

func workspaceSymbolSubrangeStats(
	base uint32,
	rng cst.TextRange,
) (offset, length int, compact bool) {
	if rng.Start < base || rng.End < rng.Start {
		return 0, 0, false
	}
	rawOffset := rng.Start - base
	rawLength := rng.End - rng.Start
	return int(rawOffset),
		int(rawLength),
		rawOffset < workspaceCompactRangeMissing &&
			rawLength < workspaceCompactRangeMissing
}

func addHierarchyPairStats(
	names []string,
	typeValues []types.Type,
	stats *WorkspaceStorageStats,
) {
	if stats == nil {
		return
	}
	paired := min(len(names), len(typeValues))
	stats.HierarchyPairedNameTypes += paired
	for index := range paired {
		if names[index] == typeValues[index].Name() {
			stats.HierarchyExactNamedTypePairs++
		}
	}
}

func addStorageString(
	unique map[string]struct{},
	value string,
	stats *WorkspaceStorageStats,
) {
	if value == "" {
		return
	}
	stats.SymbolStringSlots++
	if _, exists := unique[value]; exists {
		return
	}
	unique[value] = struct{}{}
	stats.UniqueSymbolStringBytes += len(value)
}

func addInternedStorageString(
	unique map[string]struct{},
	value string,
	stats *WorkspaceStorageStats,
) {
	if value == "" {
		return
	}
	stats.InternedStringSlots++
	if _, exists := unique[value]; exists {
		return
	}
	unique[value] = struct{}{}
	stats.UniqueInternedBytes += len(value)
}

func addStorageType(
	unique map[string]struct{},
	value types.Type,
	stats *WorkspaceStorageStats,
) {
	stats.TypeSlots++
	key := value.Key()
	if _, exists := unique[key]; exists {
		return
	}
	unique[key] = struct{}{}
	stats.UniqueTypeBytes += len(key)
}
