package semantic

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
