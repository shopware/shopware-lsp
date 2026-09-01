package semantic

// WorkspaceStorageStats returns cardinalities for this snapshot's retained
// base documents. Overlay generations report their locally replaced documents;
// callers profiling a complete workspace should invoke it on the base
// generation returned by Store.Snapshot.
func (s *Snapshot) WorkspaceStorageStats() WorkspaceStorageStats {
	if s == nil {
		return WorkspaceStorageStats{}
	}
	collector := newWorkspaceStatsCollector()
	collector.collectSnapshotIndexes(s)
	for path, document := range s.pathRefs {
		collector.collectDocument(path, document)
	}
	return collector.result()
}

type workspaceStatsCollector struct {
	stats WorkspaceStorageStats

	symbolStrings         map[string]struct{}
	coreSymbolStrings     map[string]struct{}
	referenceStrings      map[string]struct{}
	internedStrings       map[string]struct{}
	typeKeys              map[string]struct{}
	referenceStringTables map[*workspaceReferenceStringTable]struct{}
	symbolStringTables    map[*workspaceSymbolStringTable]struct{}
}

func newWorkspaceStatsCollector() *workspaceStatsCollector {
	return &workspaceStatsCollector{
		symbolStrings:         make(map[string]struct{}),
		coreSymbolStrings:     make(map[string]struct{}),
		referenceStrings:      make(map[string]struct{}),
		internedStrings:       make(map[string]struct{}),
		typeKeys:              make(map[string]struct{}),
		referenceStringTables: make(map[*workspaceReferenceStringTable]struct{}),
		symbolStringTables:    make(map[*workspaceSymbolStringTable]struct{}),
	}
}

func (c *workspaceStatsCollector) result() WorkspaceStorageStats {
	c.stats.UniqueSymbolStrings = len(c.symbolStrings)
	c.stats.UniqueCoreSymbolStrings = len(c.coreSymbolStrings)
	c.stats.UniqueReferenceStrings = len(c.referenceStrings)
	c.stats.UniqueInternedStrings = len(c.internedStrings)
	c.stats.UniqueTypeKeys = len(c.typeKeys)
	return c.stats
}

func (c *workspaceStatsCollector) collectSnapshotIndexes(snapshot *Snapshot) {
	c.stats.SymbolIndexEntries = snapshot.symbols.Len() +
		len(snapshot.expanded) +
		len(snapshot.overrides)
	c.stats.SymbolIndexCapacity = len(snapshot.symbols.slots)
	c.stats.PathIndexEntries = len(snapshot.pathRefs)
	c.stats.ClassNames, c.stats.ClassIDs, c.stats.UniqueClassIDs =
		symbolNameIndexStorageStats(snapshot.classes)
	c.stats.FunctionNames, c.stats.FunctionIDs, c.stats.UniqueFunctionIDs =
		symbolNameIndexStorageStats(snapshot.functions)
	c.stats.ConstantNames, c.stats.ConstantIDs, c.stats.UniqueConstantIDs =
		symbolNameIndexStorageStats(snapshot.constants)
	c.collectMemberIndexes(snapshot)
	c.collectGlobalIndex(snapshot)
}

func symbolNameIndexStorageStats(index symbolNameIndex) (names, ids, unique int) {
	names = len(index.primary)
	uniqueIDs := make(map[SymbolID]struct{})
	for _, id := range index.primary {
		ids++
		uniqueIDs[id] = struct{}{}
	}
	for _, alternatives := range index.alternates {
		ids += len(alternatives)
		for _, id := range alternatives {
			uniqueIDs[id] = struct{}{}
		}
	}
	return names, ids, len(uniqueIDs)
}

func (c *workspaceStatsCollector) collectMemberIndexes(snapshot *Snapshot) {
	memberIDs := make(map[SymbolID]struct{})
	c.stats.MemberContainers = len(snapshot.compactMembers.containers) + len(snapshot.members)
	for container, span := range snapshot.compactMembers.containers {
		nameCount := int(span.count)
		addMemberContainerStorageStats(nameCount, &c.stats)
		c.stats.MemberNames += nameCount
		values := snapshot.compactMembers.valuesForContainer(container)
		c.stats.MemberIDs += len(values)
		c.stats.MemberDuplicateIDs += len(values) - nameCount
		entryStart := int(span.start)
		entryEnd := entryStart + nameCount
		for _, valueSpan := range snapshot.compactMembers.valueSpans[entryStart:entryEnd] {
			if valueSpan.count > 1 {
				c.stats.MemberAlternateNames++
			}
		}
		for _, symbol := range values {
			memberIDs[symbol.ID] = struct{}{}
		}
	}
	for _, names := range snapshot.members {
		c.stats.MemberNames += len(names)
		c.stats.MemberIDs += len(names)
		addMemberContainerStorageStats(len(names), &c.stats)
		for _, id := range names {
			memberIDs[id] = struct{}{}
		}
	}
	for _, names := range snapshot.memberAlternates {
		for _, ids := range names {
			c.stats.MemberAlternateNames++
			c.stats.MemberIDs += len(ids)
			c.stats.MemberDuplicateIDs += len(ids)
			for _, id := range ids {
				memberIDs[id] = struct{}{}
			}
		}
	}
	c.stats.UniqueMemberIDs = len(memberIDs)
}

func (c *workspaceStatsCollector) collectGlobalIndex(snapshot *Snapshot) {
	globalIDs := make(map[SymbolID]struct{})
	c.stats.GlobalIDs = len(snapshot.globals)
	for _, id := range snapshot.globals {
		globalIDs[id] = struct{}{}
	}
	c.stats.UniqueGlobalIDs = len(globalIDs)
}

func (c *workspaceStatsCollector) collectDocument(
	path string,
	document *workspaceDocument,
) {
	if document == nil {
		return
	}
	c.stats.Documents++
	addInternedStorageString(c.internedStrings, document.Path, &c.stats)
	addInternedStorageString(c.internedStrings, document.Namespace, &c.stats)
	c.stats.Symbols += len(document.Symbols)
	c.stats.References += len(document.References)
	c.stats.MaxReferenceStringsPerDocument = max(
		c.stats.MaxReferenceStringsPerDocument,
		document.referenceStringCount(),
	)
	c.stats.MaxReferenceTypesPerDocument = max(
		c.stats.MaxReferenceTypesPerDocument,
		len(document.referenceTypes),
	)
	c.stats.MaxReferenceValuesPerDocument = max(
		c.stats.MaxReferenceValuesPerDocument,
		len(document.referenceValues),
	)
	c.collectReferences(document)
	c.collectSignatures(document)
	c.collectDocumentStringTables(document)
	c.collectSymbols(path, document)
	c.collectHierarchies(document)
	c.collectMetadata(document)
	c.collectReferenceValues(document)
}

func (c *workspaceStatsCollector) collectReferences(document *workspaceDocument) {
	for index := range document.References {
		reference := &document.References[index]
		c.stats.MaxQualified = max(c.stats.MaxQualified, int(reference.qualifiedCount()))
		c.stats.MaxCandidates = max(c.stats.MaxCandidates, int(reference.candidateCount()))
		rng := reference.rangeValue(document)
		rangeLength, _ := workspaceSymbolRangeLength(rng)
		c.stats.MaxReferenceRangeLength = max(c.stats.MaxReferenceRangeLength, rangeLength)
		c.stats.MaxReferenceValueStart = max(
			c.stats.MaxReferenceValueStart,
			int(reference.valueStart(document)),
		)
		if reference.hasFullLocation() {
			c.stats.ReferenceFullRanges++
		} else {
			c.stats.ReferenceCompactRanges++
		}
	}
}

func (c *workspaceStatsCollector) collectSignatures(document *workspaceDocument) {
	c.stats.Signatures += len(document.signatures)
	c.stats.Hierarchies += len(document.hierarchies)
	c.stats.Metadata += len(document.metadata)
	for index := range document.signatures {
		c.collectSignature(&document.signatures[index])
	}
}

func (c *workspaceStatsCollector) collectSignature(signature *workspaceSignature) {
	c.stats.Parameters += len(signature.Parameters)
	for parameterIndex := range signature.Parameters {
		c.collectParameter(&signature.Parameters[parameterIndex])
	}
	c.stats.Templates += len(signature.templates())
	for _, template := range signature.templates() {
		addInternedStorageString(c.internedStrings, template.Name, &c.stats)
		addStorageType(c.typeKeys, template.Bound, &c.stats)
		addStorageType(c.typeKeys, template.Default, &c.stats)
	}
	c.stats.Throws += len(signature.throws())
	for _, value := range signature.throws() {
		addStorageType(c.typeKeys, value, &c.stats)
	}
	c.stats.LiteralReturns += len(signature.literalReturns())
	for _, literalReturn := range signature.literalReturns() {
		addInternedStorageString(c.internedStrings, literalReturn.Value, &c.stats)
		addStorageType(c.typeKeys, literalReturn.Type, &c.stats)
	}
	c.stats.ConstantReturns += len(signature.constantReturns())
	for _, constantReturn := range signature.constantReturns() {
		addInternedStorageString(c.internedStrings, constantReturn.Receiver, &c.stats)
		addInternedStorageString(c.internedStrings, constantReturn.Name, &c.stats)
	}
	if signature.Extras != nil {
		c.stats.SignaturesWithExtras++
	}
}

func (c *workspaceStatsCollector) collectParameter(parameter *workspaceParameter) {
	addInternedStorageString(c.internedStrings, string(parameter.id(nil)), &c.stats)
	addInternedStorageString(c.internedStrings, parameter.Name, &c.stats)
	for _, tag := range parameter.assistantTags() {
		addInternedStorageString(c.internedStrings, tag, &c.stats)
	}
	addStorageType(c.typeKeys, parameter.effectiveType(), &c.stats)
	addStorageType(c.typeKeys, parameter.NativeType, &c.stats)
	addStorageType(c.typeKeys, parameter.DocType, &c.stats)
	if !parameter.NativeType.IsUnknown() {
		c.stats.ParameterNativeTypes++
	}
	if !parameter.DocType.IsUnknown() {
		c.stats.ParameterDocTypes++
	}
	effectiveType := parameter.effectiveType()
	if !effectiveType.Equal(parameter.NativeType) && !effectiveType.Equal(parameter.DocType) {
		c.stats.ParameterExplicitTypes++
	}
	if tags := parameter.assistantTags(); len(tags) != 0 {
		c.stats.ParametersWithAssistantTags++
		c.stats.ParameterAssistantTags += len(tags)
	}
	if parameter.Extras != nil && parameter.Extras.Ranges != nil {
		c.stats.ParameterFullRanges++
	}
}

func (c *workspaceStatsCollector) collectDocumentStringTables(document *workspaceDocument) {
	c.stats.ReferenceStrings += document.referenceStringCount()
	c.stats.ReferenceStringCapacity += cap(document.referenceStringIDs)
	if document.referenceStrings != nil {
		if _, exists := c.referenceStringTables[document.referenceStrings]; !exists {
			c.referenceStringTables[document.referenceStrings] = struct{}{}
			c.stats.ReferenceStringCapacity += cap(document.referenceStrings.Values)
		}
	}
	if document.symbolStrings != nil {
		if _, exists := c.symbolStringTables[document.symbolStrings]; !exists {
			c.symbolStringTables[document.symbolStrings] = struct{}{}
			c.stats.SymbolStringTables++
			c.stats.SymbolStringTableValues += len(document.symbolStrings.Values)
			c.stats.SymbolStringTableCapacity += cap(document.symbolStrings.Values)
		}
	}
	c.stats.ReferenceTypes += len(document.referenceTypes)
	c.stats.ReferenceValues += len(document.referenceValues)
	if document.referenceBloom != [2]uint64{} {
		c.stats.ReferenceBloomDocuments++
		c.stats.ReferenceBloomBytes += len(document.referenceBloom) * 8
	}
}

func (c *workspaceStatsCollector) collectSymbols(path string, document *workspaceDocument) {
	documentCoreStrings := make(map[string]struct{})
	for index := range document.Symbols {
		c.collectSymbol(path, &document.Symbols[index], documentCoreStrings)
	}
	c.stats.CoreSymbolUniquePerDocument += len(documentCoreStrings)
}

func (c *workspaceStatsCollector) collectSymbol(
	documentPath string,
	symbol *workspaceSymbol,
	documentCoreStrings map[string]struct{},
) {
	addWorkspaceSymbolRangeStats(symbol, &c.stats)
	if signature := symbol.signature(); signature != nil {
		for parameterIndex := range signature.Parameters {
			parameter := &signature.Parameters[parameterIndex]
			if parameter.Extras == nil || parameter.Extras.ID == "" {
				c.stats.ParameterDerivedIDs++
			} else {
				c.stats.ParameterFullIDs++
			}
		}
	}
	c.collectSymbolSideTables(symbol)
	addStorageType(c.typeKeys, symbol.Type, &c.stats)
	addStorageType(c.typeKeys, symbol.NativeType, &c.stats)
	addStorageType(c.typeKeys, symbol.DocType, &c.stats)
	addStorageType(c.typeKeys, symbol.ReturnType, &c.stats)

	name := symbol.name()
	fullyQualified := symbol.fullyQualified()
	container := symbol.container()
	coreValues := [...]string{string(symbol.ID), name, fullyQualified, string(container)}
	for _, value := range coreValues {
		addInternedStorageString(c.internedStrings, value, &c.stats)
	}
	c.stats.CoreSymbolStringSlots += len(coreValues)
	for _, value := range coreValues {
		if value == "" {
			continue
		}
		c.stats.CoreSymbolNonEmptyStrings++
		documentCoreStrings[value] = struct{}{}
		c.coreSymbolStrings[value] = struct{}{}
	}
	if container == "" {
		c.stats.SymbolEmptyContainers++
	}
	if name == fullyQualified {
		c.stats.SymbolNameEqualsQualified++
	}
	if string(symbol.ID) == fullyQualified {
		c.stats.SymbolIDEqualsQualified++
	}
	if symbol.path() == documentPath {
		c.stats.SymbolsUsingDocumentPath++
	}
	addStorageString(c.symbolStrings, string(symbol.ID), &c.stats)
	addStorageString(c.symbolStrings, name, &c.stats)
	addStorageString(c.symbolStrings, fullyQualified, &c.stats)
	addStorageString(c.symbolStrings, string(container), &c.stats)
	addStorageString(c.symbolStrings, symbol.path(), &c.stats)
}

func (c *workspaceStatsCollector) collectSymbolSideTables(symbol *workspaceSymbol) {
	signature := symbol.signature() != nil
	hierarchy := symbol.hierarchy() != nil
	metadata := symbol.metadata() != nil
	switch {
	case signature && hierarchy && metadata:
		c.stats.AllSideTables++
	case signature && hierarchy:
		c.stats.SignatureHierarchy++
	case signature && metadata:
		c.stats.SignatureMetadata++
	case hierarchy && metadata:
		c.stats.HierarchyMetadata++
	case signature:
		c.stats.SignatureOnly++
	case hierarchy:
		c.stats.HierarchyOnly++
	case metadata:
		c.stats.MetadataOnly++
	default:
		c.stats.NoSideTables++
	}
}

func (c *workspaceStatsCollector) collectHierarchies(document *workspaceDocument) {
	for index := range document.hierarchies {
		c.collectHierarchy(&document.hierarchies[index])
	}
}

func (c *workspaceStatsCollector) collectHierarchy(hierarchy *workspaceHierarchy) {
	extends := hierarchy.extends()
	implements := hierarchy.implements()
	traits := hierarchy.traits()
	extendsTypes := hierarchy.extendsTypes()
	implementsTypes := hierarchy.implementsTypes()
	traitTypes := hierarchy.traitTypes()
	c.stats.HierarchyExtendsNames += len(extends)
	c.stats.HierarchyImplementsNames += len(implements)
	c.stats.HierarchyTraitNames += len(traits)
	c.stats.HierarchyExtendsTypes += len(extendsTypes)
	c.stats.HierarchyImplementsTypes += len(implementsTypes)
	c.stats.HierarchyTraitTypes += len(traitTypes)
	hierarchyGroups := 0
	if len(extends) != 0 || len(extendsTypes) != 0 {
		c.stats.HierarchiesWithExtends++
		hierarchyGroups++
	}
	if len(implements) != 0 || len(implementsTypes) != 0 {
		c.stats.HierarchiesWithImplements++
		hierarchyGroups++
	}
	if len(traits) != 0 || len(traitTypes) != 0 {
		c.stats.HierarchiesWithTraits++
		hierarchyGroups++
	}
	if hierarchyGroups > 1 {
		c.stats.HierarchiesWithMultipleGroups++
	}
	addHierarchyPairStats(extends, extendsTypes, &c.stats)
	addHierarchyPairStats(implements, implementsTypes, &c.stats)
	addHierarchyPairStats(traits, traitTypes, &c.stats)
	for _, value := range extends {
		addInternedStorageString(c.internedStrings, value, &c.stats)
	}
	for _, value := range implements {
		addInternedStorageString(c.internedStrings, value, &c.stats)
	}
	for _, value := range traits {
		addInternedStorageString(c.internedStrings, value, &c.stats)
	}
	for _, value := range extendsTypes {
		addStorageType(c.typeKeys, value, &c.stats)
	}
	for _, value := range implementsTypes {
		addStorageType(c.typeKeys, value, &c.stats)
	}
	for _, value := range traitTypes {
		addStorageType(c.typeKeys, value, &c.stats)
	}
}

func (c *workspaceStatsCollector) collectMetadata(document *workspaceDocument) {
	for index := range document.metadata {
		c.collectMetadataValue(&document.metadata[index])
	}
}

func (c *workspaceStatsCollector) collectMetadataValue(metadata *workspaceMetadata) {
	attributes := metadata.attributes()
	constantArray := metadata.constantArray()
	c.stats.Attributes += len(attributes)
	c.stats.ConstantArrayItems += len(constantArray)
	c.stats.DocSummaryBytes += len(metadata.DocSummary)
	switch {
	case len(attributes) != 0 && len(constantArray) != 0 && metadata.DocSummary != "":
		c.stats.MetadataAllFields++
	case len(attributes) != 0 && len(constantArray) != 0:
		c.stats.MetadataAttributesConstant++
	case len(attributes) != 0 && metadata.DocSummary != "":
		c.stats.MetadataAttributesSummary++
	case len(constantArray) != 0 && metadata.DocSummary != "":
		c.stats.MetadataConstantSummary++
	case len(attributes) != 0:
		c.stats.MetadataAttributesOnly++
	case len(constantArray) != 0:
		c.stats.MetadataConstantArrayOnly++
	case metadata.DocSummary != "":
		c.stats.MetadataDocSummaryOnly++
	}
	for _, attribute := range attributes {
		addInternedStorageString(c.internedStrings, attribute.Name, &c.stats)
	}
	for _, item := range constantArray {
		addInternedStorageString(c.internedStrings, item.Key, &c.stats)
		addInternedStorageString(c.internedStrings, item.Value, &c.stats)
		addStorageType(c.typeKeys, item.Type, &c.stats)
	}
	addInternedStorageString(c.internedStrings, metadata.DocSummary, &c.stats)
}

func (c *workspaceStatsCollector) collectReferenceValues(document *workspaceDocument) {
	for _, value := range document.referenceTypes {
		addStorageType(c.typeKeys, value, &c.stats)
	}
	for stringIndex := 1; stringIndex <= document.referenceStringCount(); stringIndex++ {
		value := document.referenceString(uint32(stringIndex))
		addInternedStorageString(c.internedStrings, value, &c.stats)
		if value == "" {
			continue
		}
		if _, exists := c.referenceStrings[value]; !exists {
			c.stats.UniqueReferenceStringBytes += len(value)
		}
		c.referenceStrings[value] = struct{}{}
	}
}
