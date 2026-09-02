package semantic

import (
	"fmt"
	"math"
	"slices"
	"unsafe"

	"github.com/shopware/shopware-lsp/internal/php/types"
)

type workspaceDocumentPacker struct {
	document              *workspaceDocument
	symbolStrings         workspaceSymbolStringBuilder
	symbolIndex           int
	symbolRangeExtraIndex int
	signatureIndex        int
	signatureExtraIndex   int
	hierarchyIndex        int
	metadataIndex         int
}

const workspaceSymbolStringLinearLimit = 16

type workspaceSymbolStringBuilder struct {
	document       *workspaceDocument
	index          map[string]uint32
	canonicalIndex map[*byte]uint32
	capacity       int
}

func newWorkspaceSymbolStringBuilder(
	document *workspaceDocument,
	symbolCount int,
) workspaceSymbolStringBuilder {
	return workspaceSymbolStringBuilder{
		document: document,
		capacity: min(symbolCount*2, 64),
	}
}

func (decoder *WorkspaceGraphDecoder) newWorkspaceSymbolStringBuilder(
	document *workspaceDocument,
	symbolCount int,
) workspaceSymbolStringBuilder {
	if decoder == nil || decoder.stringCache == nil {
		return newWorkspaceSymbolStringBuilder(document, symbolCount)
	}
	if decoder.sharedStrings == nil {
		decoder.sharedStrings = &workspaceSharedStringTable{Shared: true}
	} else {
		decoder.sharedStrings.Shared = true
	}
	if decoder.sharedStringIndex == nil {
		decoder.sharedStringIndex = make(map[*byte]uint32)
	}
	document.symbolStrings = decoder.sharedStrings
	return workspaceSymbolStringBuilder{
		document:       document,
		canonicalIndex: decoder.sharedStringIndex,
	}
}

func (builder *workspaceSymbolStringBuilder) indexFor(value string) uint32 {
	if builder == nil || builder.document == nil || value == "" {
		return 0
	}
	if builder.canonicalIndex != nil {
		if index, exists :=
			builder.canonicalIndex[unsafe.StringData(value)]; exists {
			return index
		}
	}
	if builder.index != nil {
		if index, exists := builder.index[value]; exists {
			return index
		}
	}
	if builder.document.symbolStrings == nil {
		builder.document.symbolStrings = &workspaceSymbolStringTable{
			Values: make([]string, 0, builder.capacity),
		}
	}
	values := builder.document.symbolStrings.Values
	if builder.index == nil && builder.canonicalIndex == nil {
		for index, existing := range values {
			if existing == value {
				return uint32(index + 1)
			}
		}
	}
	if uint64(len(values)) == math.MaxUint32 {
		panic("semantic: workspace symbol string table exceeds uint32")
	}
	index := uint32(len(values) + 1)
	builder.document.symbolStrings.Values = append(values, value)
	if builder.canonicalIndex != nil {
		builder.canonicalIndex[unsafe.StringData(value)] = index
	} else if builder.index != nil {
		builder.index[value] = index
	} else if len(values)+1 > workspaceSymbolStringLinearLimit {
		builder.index = make(
			map[string]uint32,
			len(builder.document.symbolStrings.Values),
		)
		for valueIndex, existing := range builder.document.symbolStrings.Values {
			builder.index[existing] = uint32(valueIndex + 1)
		}
	}
	return index
}

func countWorkspaceSymbols(
	symbols []Symbol,
	workspaceOnly bool,
) (
	symbolCount,
	signatureCount,
	signatureExtraCount,
	hierarchyCount,
	metadataCount,
	symbolRangeExtraCount int,
) {
	for index := range symbols {
		symbol := &symbols[index]
		if workspaceOnly && !isWorkspaceSymbol(symbol.Kind) {
			continue
		}
		symbolCount++
		if hasWorkspaceSignature(symbol) {
			signatureCount++
			if hasWorkspaceSignatureExtras(symbol) {
				signatureExtraCount++
			}
		}
		if hasWorkspaceHierarchy(symbol) {
			hierarchyCount++
		}
		if hasWorkspaceMetadata(symbol) {
			metadataCount++
		}
		if _, ok := compactWorkspaceSymbolRanges(
			symbol.Range,
			symbol.SelectionRange,
			symbol.BodyRange,
		); !ok {
			symbolRangeExtraCount++
		}
	}
	return
}

func newWorkspaceDocumentPacker(
	path string,
	version int,
	revision uint64,
	namespace string,
	symbolCount,
	signatureCount,
	signatureExtraCount,
	hierarchyCount,
	metadataCount,
	symbolRangeExtraCount int,
) *workspaceDocumentPacker {
	packer := &workspaceDocumentPacker{
		document: &workspaceDocument{
			Path:              path,
			Version:           version,
			WorkspaceRevision: revision,
			Namespace:         namespace,
			Symbols:           make([]workspaceSymbol, symbolCount),
			signatures:        make([]workspaceSignature, signatureCount),
			signatureExtras: make(
				[]workspaceSignatureExtras,
				signatureExtraCount,
			),
			hierarchies: make([]workspaceHierarchy, hierarchyCount),
			metadata:    make([]workspaceMetadata, metadataCount),
		},
	}
	if symbolRangeExtraCount != 0 {
		packer.document.symbolRangeExtras = &workspaceSymbolRangeExtras{
			Values: make(
				[]workspaceSymbolFullRanges,
				symbolRangeExtraCount,
			),
		}
	}
	packer.symbolStrings = newWorkspaceSymbolStringBuilder(
		packer.document,
		symbolCount,
	)
	return packer
}

func (packer *workspaceDocumentPacker) addSymbol(source *Symbol) {
	if packer == nil || packer.document == nil || source == nil {
		return
	}
	target := &packer.document.Symbols[packer.symbolIndex]
	packer.symbolIndex++
	*target = workspaceSymbol{
		ID:                  source.ID,
		Kind:                source.Kind,
		Document:            packer.document,
		nameIndex:           packer.symbolStrings.indexFor(source.Name),
		fullyQualifiedIndex: packer.symbolStrings.indexFor(source.FullyQualified),
		containerIndex: packer.symbolStrings.indexFor(
			string(source.Container),
		),
		Visibility:         source.Visibility,
		WriteVisibility:    source.WriteVisibility,
		HasWriteVisibility: source.HasWriteVisibility,
	}
	target.setTypes(
		packer.document,
		source.Type,
		source.NativeType,
		source.DocType,
		source.ReturnType,
	)
	rangeIndex := -1
	if compact, ok := compactWorkspaceSymbolRanges(
		source.Range,
		source.SelectionRange,
		source.BodyRange,
	); ok {
		target.Ranges = compact
	} else {
		rangeIndex = packer.symbolRangeExtraIndex
		packer.document.symbolRangeExtras.Values[rangeIndex] =
			workspaceSymbolFullRanges{
				Range:          source.Range,
				SelectionRange: source.SelectionRange,
				BodyRange:      source.BodyRange,
			}
		packer.symbolRangeExtraIndex++
	}
	target.setFlagsAndRangeIndex(source.Flags, rangeIndex)
	signatureIndex := -1
	hierarchyIndex := -1
	metadataIndex := -1
	if hasWorkspaceSignature(source) {
		signatureIndex = packer.signatureIndex
		signature := &packer.document.signatures[signatureIndex]
		packer.signatureIndex++
		signature.Parameters = packWorkspaceParametersForSymbol(
			source.Parameters,
			target,
		)
		if hasWorkspaceSignatureExtras(source) {
			signature.Extras =
				&packer.document.signatureExtras[packer.signatureExtraIndex]
			packer.signatureExtraIndex++
			*signature.Extras = newWorkspaceSignatureExtras(
				source.Templates(),
				source.Throws(),
				source.LiteralReturns(),
				source.ConstantReturns(),
				source.Assertions(),
			)
		}
	}
	if hasWorkspaceHierarchy(source) {
		hierarchyIndex = packer.hierarchyIndex
		packer.hierarchyIndex++
		packer.document.hierarchies[hierarchyIndex] = newWorkspaceHierarchy(
			source.Extends(),
			source.Implements(),
			source.Traits(),
			source.ExtendsTypes(),
			source.ImplementsTypes(),
			source.TraitTypes(),
			source.TraitAliases(),
		)
	}
	if hasWorkspaceMetadata(source) {
		metadataIndex = packer.metadataIndex
		packer.metadataIndex++
		packer.document.metadata[metadataIndex] = newWorkspaceMetadata(
			source.Attributes(),
			source.ConstantArray(),
			source.DocSummary(),
		)
	}
	target.setSideIndexes(signatureIndex, hierarchyIndex, metadataIndex)
}

func packWorkspaceDocument(document *Document) *workspaceDocument {
	if document == nil {
		return nil
	}
	symbolCount, signatureCount, signatureExtraCount, hierarchyCount,
		metadataCount, symbolRangeExtraCount :=
		countWorkspaceSymbols(document.Symbols, false)
	packer := newWorkspaceDocumentPacker(
		document.Path,
		document.Version,
		document.WorkspaceRevision,
		document.Namespace,
		symbolCount,
		signatureCount,
		signatureExtraCount,
		hierarchyCount,
		metadataCount,
		symbolRangeExtraCount,
	)
	for index := range document.Symbols {
		packer.addSymbol(&document.Symbols[index])
	}
	packer.document.packReferences(document.References)
	packer.document.CallContracts = cloneCallContracts(document.CallContracts)
	return packer.document
}

func (document *workspaceDocument) packReferences(references []Reference) {
	if document == nil || len(references) == 0 {
		return
	}
	qualifiedCount := 0
	candidateCount := 0
	for index := range references {
		qualifiedCount += boundedWorkspaceReferenceCount(
			references[index].QualifiedNameCount(),
		)
		candidateCount += boundedWorkspaceReferenceCount(
			len(references[index].CandidateIDs()),
		)
	}
	packer := newWorkspaceReferencePacker(
		document,
		len(references),
		qualifiedCount+candidateCount,
	)
	for index := range references {
		source := &references[index]
		target := &document.References[index]
		sourceQualifiedCount := boundedWorkspaceReferenceCount(
			source.QualifiedNameCount(),
		)
		valueStart := len(document.referenceValues)
		for qualifiedIndex := 0; qualifiedIndex < sourceQualifiedCount; qualifiedIndex++ {
			packer.appendStringValue(source.QualifiedNameAt(qualifiedIndex))
		}
		candidateIDs := source.CandidateIDs()
		sourceCandidateCount := boundedWorkspaceReferenceCount(
			len(candidateIDs),
		)
		for _, candidate := range candidateIDs[:sourceCandidateCount] {
			packer.appendStringValue(string(candidate))
		}
		*target = document.newReference(
			source.Range,
			packer.stringIndexFor(source.Name),
			packer.stringIndexFor(string(source.Resolved)),
			uint32(valueStart),
			packer.typeIndexFor(source.Receiver),
			uint8(sourceQualifiedCount),
			uint8(sourceCandidateCount),
			source.Kind,
			source.TargetKind,
			workspaceReferenceFlags(source),
		)
	}
	packer.finishTables()
}

func (document *workspaceDocument) packProjectedReferences(
	references []Reference,
) {
	if document == nil || len(references) == 0 {
		return
	}
	referenceCount := 0
	qualifiedCount := 0
	candidateCount := 0
	for index := range references {
		reference := &references[index]
		if reference.Kind == VariableName {
			continue
		}
		referenceCount++
		qualifiedCount += boundedWorkspaceReferenceCount(
			reference.QualifiedNameCount(),
		)
		candidateCount += boundedWorkspaceReferenceCount(
			len(reference.CandidateIDs()),
		)
	}
	if referenceCount == 0 {
		return
	}
	packer := newWorkspaceReferencePacker(
		document,
		referenceCount,
		qualifiedCount+candidateCount,
	)
	targetIndex := 0
	for index := range references {
		source := &references[index]
		if source.Kind == VariableName {
			continue
		}
		sourceQualifiedCount := boundedWorkspaceReferenceCount(
			source.QualifiedNameCount(),
		)
		valueStart := len(document.referenceValues)
		for qualifiedIndex := 0; qualifiedIndex < sourceQualifiedCount; qualifiedIndex++ {
			packer.appendStringValue(source.QualifiedNameAt(qualifiedIndex))
		}
		candidateIDs := source.CandidateIDs()
		sourceCandidateCount := boundedWorkspaceReferenceCount(
			len(candidateIDs),
		)
		for _, candidate := range candidateIDs[:sourceCandidateCount] {
			packer.appendStringValue(string(candidate))
		}
		target := &document.References[targetIndex]
		*target = document.newReference(
			source.Range,
			packer.stringIndexFor(source.Name),
			packer.stringIndexFor(string(source.Resolved)),
			uint32(valueStart),
			packer.typeIndexFor(source.Receiver),
			uint8(sourceQualifiedCount),
			uint8(sourceCandidateCount),
			source.Kind,
			source.TargetKind,
			workspaceReferenceFlags(source),
		)
		targetIndex++
	}
	packer.finishTables()
}

func boundedWorkspaceReferenceCount(count int) int {
	return min(count, math.MaxUint8)
}

func workspaceReferenceFlags(reference *Reference) uint8 {
	if reference == nil {
		return 0
	}
	flags := uint8(0)
	if reference.Static {
		flags |= workspaceReferenceStatic
	}
	if reference.Write {
		flags |= workspaceReferenceWrite
	}
	return flags
}

func (document *workspaceDocument) validateReferenceSpans() error {
	if document == nil {
		return nil
	}
	for index := range document.References {
		reference := &document.References[index]
		if reference.hasFullLocation() {
			if _, ok := reference.fullLocation(document); !ok {
				return fmt.Errorf(
					"decode workspace graph: reference %d has invalid full location",
					index,
				)
			}
		}
		if !document.validReferenceStringIndex(reference.nameIndex()) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid name index",
				index,
			)
		}
		if !document.validReferenceStringIndex(reference.resolvedIndex()) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid resolved index",
				index,
			)
		}
		if !document.validReferenceTypeIndex(reference.receiverIndex()) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid receiver index",
				index,
			)
		}
		valueStart := reference.valueStart(document)
		if !validWorkspaceSpan(
			valueStart,
			uint32(reference.qualifiedCount())+
				uint32(reference.candidateCount()),
			len(document.referenceValues),
		) {
			return fmt.Errorf(
				"decode workspace graph: reference %d has invalid value span",
				index,
			)
		}
		valueEnd := int(valueStart) +
			int(reference.qualifiedCount()) +
			int(reference.candidateCount())
		for _, stringIndex := range document.referenceValues[int(valueStart):valueEnd] {
			if !document.validReferenceStringIndex(stringIndex) {
				return fmt.Errorf(
					"decode workspace graph: reference %d has invalid value index",
					index,
				)
			}
		}
	}
	return nil
}

func (document *workspaceDocument) validReferenceStringIndex(index uint32) bool {
	return index == 0 ||
		uint64(index) <= uint64(document.referenceStringCount())
}

func (document *workspaceDocument) validReferenceTypeIndex(index uint32) bool {
	return index == 0 || uint64(index) <= uint64(len(document.referenceTypes))
}

func validWorkspaceSpan(start, count uint32, length int) bool {
	return uint64(start)+uint64(count) <= uint64(length)
}

func (document *workspaceDocument) referenceString(index uint32) string {
	if index == 0 {
		return ""
	}
	if document == nil || document.referenceStrings == nil {
		return ""
	}
	if len(document.referenceStringIDs) != 0 {
		sharedIndex := document.referenceStringIDs[index-1]
		if sharedIndex == 0 ||
			int(sharedIndex) > len(document.referenceStrings.Values) {
			return ""
		}
		return document.referenceStrings.Values[sharedIndex-1]
	}
	return document.referenceStrings.Values[index-1]
}

func (document *workspaceDocument) referenceStringCount() int {
	if document == nil || document.referenceStrings == nil {
		return 0
	}
	if document.referenceStringIDs != nil {
		return len(document.referenceStringIDs)
	}
	return len(document.referenceStrings.Values)
}

func (document *workspaceDocument) referenceType(index uint32) types.Type {
	if index == 0 {
		return types.Type{}
	}
	return document.referenceTypes[index-1]
}

func (document *workspaceDocument) referenceValue(index int) string {
	return document.referenceString(document.referenceValues[index])
}

func (document *workspaceDocument) reference(index int) Reference {
	if document == nil || index < 0 || index >= len(document.References) {
		return Reference{}
	}
	source := &document.References[index]
	rng, valueStart := source.location(document)
	qualifiedStart := int(valueStart)
	qualifiedCount := source.qualifiedCount()
	qualifiedEnd := qualifiedStart + int(qualifiedCount)
	candidateStart := qualifiedEnd
	candidateCount := source.candidateCount()
	candidateEnd := candidateStart + int(candidateCount)
	var qualified []string
	if qualifiedCount > 0 {
		qualified = make([]string, qualifiedCount)
		for valueIndex := qualifiedStart; valueIndex < qualifiedEnd; valueIndex++ {
			qualified[valueIndex-qualifiedStart] = document.referenceValue(valueIndex)
		}
	}
	var candidates []SymbolID
	if candidateCount > 0 {
		candidates = make([]SymbolID, candidateCount)
		for valueIndex := candidateStart; valueIndex < candidateEnd; valueIndex++ {
			candidates[valueIndex-candidateStart] = SymbolID(
				document.referenceValue(valueIndex),
			)
		}
	}
	reference := Reference{
		Name:       document.referenceString(source.nameIndex()),
		Kind:       source.kind(),
		Receiver:   document.referenceType(source.receiverIndex()),
		TargetKind: source.targetKind(),
		Static:     source.flags()&workspaceReferenceStatic != 0,
		Write:      source.flags()&workspaceReferenceWrite != 0,
		Range:      rng,
		Resolved: SymbolID(
			document.referenceString(source.resolvedIndex()),
		),
	}
	reference.SetQualifiedNames(qualified)
	reference.SetCandidateIDs(candidates)
	return reference
}

func (document *workspaceDocument) materializeReferences() []Reference {
	if document == nil || len(document.References) == 0 {
		return nil
	}
	result := make([]Reference, len(document.References))
	for index := range document.References {
		result[index] = document.reference(index)
	}
	return result
}

func (document *workspaceDocument) attachDecodedSymbolSides(
	sides []decodedWorkspaceSymbolSides,
	parameterIDs []decodedWorkspaceParameterID,
) {
	if document == nil {
		return
	}
	signatureCount := 0
	signatureExtraCount := 0
	hierarchyCount := 0
	metadataCount := 0
	symbolRangeExtraCount := 0
	for index := range min(len(sides), len(document.Symbols)) {
		side := &sides[index]
		if side.signature != nil {
			signatureCount++
			if side.signature.Extras != nil {
				signatureExtraCount++
			}
		}
		if side.hierarchy != nil {
			hierarchyCount++
		}
		if side.metadata != nil {
			metadataCount++
		}
		if side.ranges != nil {
			symbolRangeExtraCount++
		}
	}
	document.signatures = make([]workspaceSignature, signatureCount)
	document.signatureExtras = make(
		[]workspaceSignatureExtras,
		signatureExtraCount,
	)
	document.hierarchies = make([]workspaceHierarchy, hierarchyCount)
	document.metadata = make([]workspaceMetadata, metadataCount)
	if symbolRangeExtraCount != 0 {
		document.symbolRangeExtras = &workspaceSymbolRangeExtras{
			Values: make(
				[]workspaceSymbolFullRanges,
				symbolRangeExtraCount,
			),
		}
	} else {
		document.symbolRangeExtras = nil
	}
	signatureIndex := 0
	signatureExtraIndex := 0
	hierarchyIndex := 0
	metadataIndex := 0
	symbolRangeExtraIndex := 0
	for index := range document.Symbols {
		symbol := &document.Symbols[index]
		symbol.Document = document
		if index >= len(sides) {
			symbol.setFlagsAndRangeIndex(symbol.flags(), -1)
			symbol.setSideIndexes(-1, -1, -1)
			continue
		}
		side := &sides[index]
		symbolSignatureIndex := -1
		symbolHierarchyIndex := -1
		symbolMetadataIndex := -1
		if side.signature != nil {
			document.signatures[signatureIndex] = *side.signature
			symbolSignatureIndex = signatureIndex
			signature := &document.signatures[signatureIndex]
			if signature.Extras != nil {
				document.signatureExtras[signatureExtraIndex] =
					*signature.Extras
				signature.Extras =
					&document.signatureExtras[signatureExtraIndex]
				signatureExtraIndex++
			}
			parameterIDStart := int(side.parameterIDStart)
			parameterIDEnd := min(
				parameterIDStart+int(side.parameterIDCount),
				len(parameterIDs),
			)
			for parameterIDIndex := parameterIDStart; parameterIDIndex < parameterIDEnd; parameterIDIndex++ {
				parameterID := &parameterIDs[parameterIDIndex]
				parameterIndex := int(parameterID.Index)
				if parameterIndex >= len(signature.Parameters) {
					continue
				}
				signature.Parameters[parameterIndex].setID(
					symbol,
					parameterID.ID,
				)
			}
			signatureIndex++
		}
		if side.hierarchy != nil {
			document.hierarchies[hierarchyIndex] = *side.hierarchy
			symbolHierarchyIndex = hierarchyIndex
			hierarchyIndex++
		}
		if side.metadata != nil {
			document.metadata[metadataIndex] = *side.metadata
			symbolMetadataIndex = metadataIndex
			metadataIndex++
		}
		rangeIndex := -1
		if side.ranges != nil {
			document.symbolRangeExtras.Values[symbolRangeExtraIndex] =
				*side.ranges
			rangeIndex = symbolRangeExtraIndex
			symbolRangeExtraIndex++
		}
		symbol.setFlagsAndRangeIndex(symbol.flags(), rangeIndex)
		symbol.setSideIndexes(
			symbolSignatureIndex,
			symbolHierarchyIndex,
			symbolMetadataIndex,
		)
	}
}

func internPackedWorkspaceGraphOwned(
	document *workspaceDocument,
	internString func(string) string,
	internType func(types.Type) types.Type,
) {
	if document == nil {
		return
	}
	document.Path = internString(document.Path)
	document.Namespace = internString(document.Namespace)
	for index := range document.CallContracts {
		contract := &document.CallContracts[index]
		contract.Target.Name = internString(contract.Target.Name)
		contract.Target.Class = internString(contract.Target.Class)
		for entryIndex := range contract.Return.Map {
			entry := &contract.Return.Map[entryIndex]
			entry.Key.Value = internString(entry.Key.Value)
			entry.Key.Expression = internString(entry.Key.Expression)
			entry.Result.Value = internString(entry.Result.Value)
			entry.Result.Expression = internString(entry.Result.Expression)
		}
		for expectedIndex := range contract.ExpectedArguments {
			expected := &contract.ExpectedArguments[expectedIndex]
			for valueIndex := range expected.Values {
				value := &expected.Values[valueIndex]
				value.Value = internString(value.Value)
				value.Expression = internString(value.Expression)
			}
		}
		for valueIndex := range contract.ExpectedReturnValues {
			value := &contract.ExpectedReturnValues[valueIndex]
			value.Value = internString(value.Value)
			value.Expression = internString(value.Expression)
		}
		for conditionIndex := range contract.ExitArguments {
			condition := &contract.ExitArguments[conditionIndex]
			for valueIndex := range condition.Values {
				value := &condition.Values[valueIndex]
				value.Value = internString(value.Value)
				value.Expression = internString(value.Expression)
			}
		}
	}
	if document.symbolStrings != nil &&
		!document.symbolStrings.Shared {
		internStringsOwned(document.symbolStrings.Values, internString)
	}
	for index := range document.Symbols {
		symbol := &document.Symbols[index]
		symbol.ID = SymbolID(internString(string(symbol.ID)))
		symbol.Document = document
		symbol.primaryType = internType(symbol.primaryType)

		signature := symbol.signature()
		if signature != nil {
			for parameterIndex := range signature.Parameters {
				parameter := &signature.Parameters[parameterIndex]
				parameter.Name = internString(parameter.Name)
				if parameter.Extras != nil {
					parameter.Extras.ID = SymbolID(
						internString(string(parameter.Extras.ID)),
					)
				}
				parameter.NativeType = internType(parameter.NativeType)
				parameter.DocType = internType(parameter.DocType)
				if parameter.EffectiveSource ==
					workspaceParameterExplicitType &&
					parameter.Extras != nil {
					parameter.Extras.Type = internType(
						parameter.Extras.Type,
					)
				}
				internStringsOwned(parameter.assistantTags(), internString)
				internWorkspaceAttributes(
					parameter.attributes(),
					internString,
				)
				internWorkspaceAttributeValue(
					parameter.defaultValue(),
					internString,
				)
			}
			templates := signature.templates()
			for templateIndex := range templates {
				template := &templates[templateIndex]
				template.Name = internString(template.Name)
				template.Bound = internType(template.Bound)
				template.Default = internType(template.Default)
			}
			internTypesOwned(signature.throws(), internType)
			literalReturns := signature.literalReturns()
			for returnIndex := range literalReturns {
				item := &literalReturns[returnIndex]
				item.Value = internString(item.Value)
				item.Type = internType(item.Type)
			}
			constantReturns := signature.constantReturns()
			for returnIndex := range constantReturns {
				item := &constantReturns[returnIndex]
				item.Receiver = internString(item.Receiver)
				item.Name = internString(item.Name)
			}
			assertions := signature.assertions()
			for assertionIndex := range assertions {
				assertion := &assertions[assertionIndex]
				assertion.Target = internString(assertion.Target)
				assertion.Type = internType(assertion.Type)
			}
		}

		hierarchy := symbol.hierarchy()
		if hierarchy != nil {
			internStringsOwned(hierarchy.extends(), internString)
			internStringsOwned(hierarchy.implements(), internString)
			internStringsOwned(hierarchy.traits(), internString)
			internTypesOwned(hierarchy.extendsTypes(), internType)
			internTypesOwned(hierarchy.implementsTypes(), internType)
			internTypesOwned(hierarchy.traitTypes(), internType)
			aliases := hierarchy.aliases()
			for aliasIndex := range aliases {
				alias := &aliases[aliasIndex]
				alias.Trait = internString(alias.Trait)
				alias.Method = internString(alias.Method)
				alias.Alias = internString(alias.Alias)
			}
		}

		metadata := symbol.metadata()
		if metadata != nil {
			attributes := metadata.attributes()
			internWorkspaceAttributes(attributes, internString)
			constantArray := metadata.constantArray()
			for itemIndex := range constantArray {
				item := &constantArray[itemIndex]
				item.Key = internString(item.Key)
				item.Value = internString(item.Value)
				item.Type = internType(item.Type)
			}
			metadata.DocSummary = internString(metadata.DocSummary)
		}
	}
	if document.symbolTypeExtras != nil {
		for index := range document.symbolTypeExtras.Values {
			extra := &document.symbolTypeExtras.Values[index]
			for valueIndex := range extra.Values {
				extra.Values[valueIndex] = internType(extra.Values[valueIndex])
			}
		}
	}
	if document.referenceStrings != nil &&
		document.referenceStringIDs == nil {
		internStringsOwned(document.referenceStrings.Values, internString)
	}
	internTypesOwned(document.referenceTypes, internType)
}

func internWorkspaceAttributes(
	attributes []Attribute,
	intern func(string) string,
) {
	for attributeIndex := range attributes {
		attribute := &attributes[attributeIndex]
		attribute.Name = intern(attribute.Name)
		for argumentIndex := range attribute.Arguments {
			argument := &attribute.Arguments[argumentIndex]
			argument.Name = intern(argument.Name)
			internWorkspaceAttributeValue(&argument.Value, intern)
		}
	}
}

func internWorkspaceAttributeValue(
	value *AttributeValue,
	intern func(string) string,
) {
	if value == nil {
		return
	}
	value.Value = intern(value.Value)
	value.Expression = intern(value.Expression)
	for index := range value.Items {
		internWorkspaceAttributeValue(&value.Items[index].Key, intern)
		internWorkspaceAttributeValue(&value.Items[index].Value, intern)
	}
}

func hasWorkspaceSignature(symbol *Symbol) bool {
	return len(symbol.Parameters) != 0 ||
		hasWorkspaceSignatureExtras(symbol)
}

func hasWorkspaceSignatureExtras(symbol *Symbol) bool {
	return symbol != nil && symbol.signatureExtras.lengths != 0
}

func hasWorkspaceHierarchy(symbol *Symbol) bool {
	return symbol != nil && (symbol.hierarchy.lengths != 0 ||
		symbol.hierarchy.types != nil || symbol.hierarchy.aliases != nil)
}

func hasWorkspaceMetadata(symbol *Symbol) bool {
	return symbol != nil && (symbol.metadata.lengths != 0 ||
		symbol.metadata.docSummary != "")
}

func (document *workspaceDocument) materialize() *Document {
	if document == nil {
		return nil
	}
	symbols := make([]Symbol, len(document.Symbols))
	for index := range document.Symbols {
		symbols[index] = document.Symbols[index].materialize()
	}
	return &Document{
		Path:              document.Path,
		Version:           document.Version,
		WorkspaceRevision: document.WorkspaceRevision,
		Namespace:         document.Namespace,
		Symbols:           symbols,
		References:        document.materializeReferences(),
		CallContracts:     cloneCallContracts(document.CallContracts),
	}
}

func (symbol *workspaceSymbol) materialize() Symbol {
	if symbol == nil {
		return Symbol{}
	}
	ranges := symbol.ranges()
	result := Symbol{
		ID:                 symbol.ID,
		Kind:               symbol.Kind,
		Name:               symbol.name(),
		FullyQualified:     symbol.fullyQualified(),
		Container:          symbol.container(),
		Path:               symbol.path(),
		Range:              ranges.Range,
		SelectionRange:     ranges.SelectionRange,
		BodyRange:          ranges.BodyRange,
		Visibility:         symbol.Visibility,
		WriteVisibility:    symbol.WriteVisibility,
		HasWriteVisibility: symbol.HasWriteVisibility,
		Flags:              symbol.flags(),
		Type:               symbol.valueType(),
		NativeType:         symbol.nativeType(),
		DocType:            symbol.docType(),
		ReturnType:         symbol.returnType(),
	}
	signature := symbol.signature()
	if signature != nil {
		result.Parameters = materializeWorkspaceParameters(
			signature.Parameters,
			symbol,
		)
		result.SetSignatureExtras(
			signature.templates(),
			signature.throws(),
			signature.assertions(),
			signature.literalReturns(),
			signature.constantReturns(),
		)
	}
	hierarchy := symbol.hierarchy()
	if hierarchy != nil {
		result.SetHierarchy(
			hierarchy.extends(),
			hierarchy.implements(),
			hierarchy.traits(),
			hierarchy.extendsTypes(),
			hierarchy.implementsTypes(),
			hierarchy.traitTypes(),
			slices.Clone(hierarchy.aliases()),
		)
	}
	metadata := symbol.metadata()
	if metadata != nil {
		result.SetMetadata(
			metadata.attributes(),
			metadata.constantArray(),
			metadata.DocSummary,
		)
	}
	return result
}

// SymbolView is a lightweight immutable view of a declaration retained by a
// Snapshot. Materialize it only when the complete Symbol value is needed.
