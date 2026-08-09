package types

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unsafe"

	"github.com/vmihailenco/msgpack/v5"
)

// Type is an immutable semantic PHP type. The zero value is Unknown.
//
// Slices owned by a type node are never exposed directly. This makes Type safe
// to share between immutable workspace and document snapshots.
type Type struct {
	node *typeNode
}

type typeNode struct {
	// Named and literal kinds use name as their semantic value. Composite
	// kinds have no semantic name and reuse the same string slot for their
	// cached canonical text.
	name string
	args typeList
}

// typeList is the immutable, exact-length representation of generic and
// composite children. Type nodes never append after construction, so retaining
// a machine-word capacity in every node would be wasted space. Callable and
// shape nodes have no generic arguments and reuse data for their optional
// details record; kind occupies otherwise unused pointer-alignment padding.
type typeList struct {
	data   unsafe.Pointer
	length uint32
	kind   Kind
}

func newTypeList(values []Type) typeList {
	if len(values) == 0 {
		return typeList{}
	}
	if uint64(len(values)) > uint64(^uint32(0)) {
		panic("types: type argument count exceeds uint32")
	}
	storage := make([]Type, len(values))
	copy(storage, values)
	return ownedTypeList(storage)
}

func ownedTypeList(storage []Type) typeList {
	if len(storage) == 0 {
		return typeList{}
	}
	return typeList{
		data:   unsafe.Pointer(&storage[0]),
		length: uint32(len(storage)),
	}
}

func (values typeList) count() int {
	return int(values.length)
}

func (values typeList) values() []Type {
	if values.length == 0 {
		return nil
	}
	return unsafe.Slice((*Type)(values.data), values.length)
}

func (values typeList) details() *typeDetails {
	if values.data == nil || values.length != 0 {
		return nil
	}
	return (*typeDetails)(values.data)
}

// typeDetails holds fields used only by callables and shape types so the much
// more common named, literal, union, and container nodes stay compact.
type typeDetails struct {
	params []CallableParameter
	fields []ShapeField
	result Type
	open   bool
}

// Detacher copies source-backed strings out of immutable type graphs while
// preserving sharing within one semantic document. Built-in canonical types
// remain shared globally.
type Detacher struct {
	intern func(string) string
	nodes  map[*typeNode]Type
}

func NewDetacher(intern func(string) string) *Detacher {
	if intern == nil {
		intern = func(value string) string { return strings.Clone(value) }
	}
	return &Detacher{
		intern: intern,
		nodes:  make(map[*typeNode]Type),
	}
}

func (d *Detacher) Type(value Type) Type {
	if d == nil || value.node == nil || canonicalTypeNode(value.node) {
		return value
	}
	if detached, ok := d.nodes[value.node]; ok {
		return detached
	}
	node := &typeNode{
		name: d.intern(value.node.name),
	}
	node.args.kind = value.node.args.kind
	result := Type{node: node}
	d.nodes[value.node] = result
	sourceArguments := value.node.args.values()
	detachedArguments := make([]Type, len(sourceArguments))
	node.args = ownedTypeList(detachedArguments)
	node.args.kind = value.node.args.kind
	for index, argument := range sourceArguments {
		detachedArguments[index] = d.Type(argument)
	}
	if sourceDetails := value.node.args.details(); sourceDetails != nil {
		details := &typeDetails{open: sourceDetails.open}
		node.args.data = unsafe.Pointer(details)
		details.params = make(
			[]CallableParameter,
			len(sourceDetails.params),
		)
		for index, parameter := range sourceDetails.params {
			details.params[index] = parameter
			details.params[index].Name = d.intern(parameter.Name)
			details.params[index].Type = d.Type(parameter.Type)
		}
		details.result = d.Type(sourceDetails.result)
		details.fields = make([]ShapeField, len(sourceDetails.fields))
		for index, field := range sourceDetails.fields {
			details.fields[index] = field
			details.fields[index].Name = d.intern(field.Name)
			details.fields[index].Type = d.Type(field.Type)
		}
	}
	return result
}

func canonicalTypeNode(node *typeNode) bool {
	switch node {
	case unknownType.node,
		errorType.node,
		neverType.node,
		mixedType.node,
		voidType.node,
		nullType.node,
		boolType.node,
		trueType.node,
		falseType.node,
		intType.node,
		floatType.node,
		stringType.node,
		objectType.node,
		resourceType.node,
		arrayKeyType.node,
		arrayType.node,
		nonEmptyArrayType.node,
		listType.node,
		nonEmptyListType.node,
		iterableType.node,
		callableType.node,
		classStringType.node,
		selfType.node,
		staticType.node,
		parentType.node:
		return true
	default:
		return false
	}
}

// CallableParameter describes one parameter in a callable signature.
type CallableParameter struct {
	Name        string
	Type        Type
	Optional    bool
	Variadic    bool
	ByReference bool
}

// ShapeField describes one array/object shape entry.
type ShapeField struct {
	Name     string
	Type     Type
	Optional bool
}

var (
	unknownType       = rawType(UnknownKind, "")
	errorType         = rawType(ErrorKind, "")
	neverType         = rawType(NeverKind, "")
	mixedType         = rawType(MixedKind, "")
	voidType          = rawType(VoidKind, "")
	nullType          = rawType(NullKind, "")
	boolType          = rawType(BoolKind, "")
	trueType          = rawType(TrueKind, "")
	falseType         = rawType(FalseKind, "")
	intType           = rawType(IntKind, "")
	floatType         = rawType(FloatKind, "")
	stringType        = rawType(StringKind, "")
	objectType        = rawType(ObjectKind, "")
	resourceType      = rawType(ResourceKind, "")
	arrayKeyType      = rawType(ArrayKeyKind, "")
	arrayType         = rawType(ArrayKind, "", mixedType, mixedType)
	nonEmptyArrayType = rawType(NonEmptyArrayKind, "", mixedType, mixedType)
	listType          = rawType(ListKind, "", mixedType)
	nonEmptyListType  = rawType(NonEmptyListKind, "", mixedType)
	iterableType      = rawType(IterableKind, "", mixedType, mixedType)
	callableType      = rawDetailedType(
		CallableKind,
		&typeDetails{result: mixedType},
	)
	classStringType = rawType(ClassStringKind, "", objectType)
	selfType        = rawType(SelfKind, "")
	staticType      = rawType(StaticKind, "")
	parentType      = rawType(ParentKind, "")
	emptyStringType = rawType(LiteralStringKind, "")
	zeroType        = rawType(LiteralIntKind, "0")
	oneType         = rawType(LiteralIntKind, "1")
)

func newType(kind Kind, name string, args ...Type) Type {
	value := rawType(kind, name, args...)
	return finishType(value.node)
}

func rawType(kind Kind, name string, args ...Type) Type {
	arguments := newTypeList(args)
	arguments.kind = kind
	return Type{node: &typeNode{name: name, args: arguments}}
}

func rawDetailedType(kind Kind, details *typeDetails) Type {
	return Type{node: &typeNode{args: typeList{
		data: unsafe.Pointer(details),
		kind: kind,
	}}}
}

func finishType(node *typeNode) Type {
	value := Type{node: node}
	if cacheTypeText(node) {
		node.name = value.render()
	}
	return value
}

func kindHasSemanticName(kind Kind) bool {
	switch kind {
	case ObjectKind, TemplateKind, LiteralIntKind, LiteralFloatKind,
		LiteralStringKind:
		return true
	default:
		return false
	}
}

func (node *typeNode) semanticName() string {
	if node == nil || !kindHasSemanticName(node.args.kind) {
		return ""
	}
	return node.name
}

func (node *typeNode) cachedText() string {
	if node == nil || kindHasSemanticName(node.args.kind) {
		return ""
	}
	return node.name
}

func cloneTypes(values []Type) []Type {
	if len(values) == 0 {
		return nil
	}
	result := make([]Type, len(values))
	copy(result, values)
	return result
}

func Unknown() Type  { return unknownType }
func Error() Type    { return errorType }
func Never() Type    { return neverType }
func Mixed() Type    { return mixedType }
func Void() Type     { return voidType }
func Null() Type     { return nullType }
func Bool() Type     { return boolType }
func True() Type     { return trueType }
func False() Type    { return falseType }
func Int() Type      { return intType }
func Float() Type    { return floatType }
func String() Type   { return stringType }
func Object() Type   { return objectType }
func Resource() Type { return resourceType }
func ArrayKey() Type { return arrayKeyType }
func Self(arguments ...Type) Type {
	if len(arguments) == 0 {
		return selfType
	}
	return newType(SelfKind, "", arguments...)
}

func Static(arguments ...Type) Type {
	if len(arguments) == 0 {
		return staticType
	}
	return newType(StaticKind, "", arguments...)
}

func Parent(arguments ...Type) Type {
	if len(arguments) == 0 {
		return parentType
	}
	return newType(ParentKind, "", arguments...)
}

func Named(name string, args ...Type) Type {
	name = strings.TrimSpace(name)
	if !validQualifiedName(strings.TrimPrefix(name, "\\")) {
		return Unknown()
	}
	return newType(ObjectKind, name, args...)
}

func Template(name string) Type {
	name = strings.TrimSpace(name)
	identifier := strings.TrimPrefix(name, "$")
	if !validIdentifier(identifier) {
		return Unknown()
	}
	return newType(TemplateKind, name)
}

func validQualifiedName(name string) bool {
	if name == "" {
		return false
	}
	segmentStart := 0
	for index := 0; index <= len(name); index++ {
		if index < len(name) && name[index] != '\\' {
			continue
		}
		if !validIdentifier(name[segmentStart:index]) {
			return false
		}
		segmentStart = index + 1
	}
	return true
}

func validIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for index, value := range name {
		if index == 0 {
			if value != '_' && !unicode.IsLetter(value) && value < unicode.MaxASCII+1 {
				return false
			}
			continue
		}
		if value != '_' && !unicode.IsLetter(value) && !unicode.IsDigit(value) &&
			value < unicode.MaxASCII+1 {
			return false
		}
	}
	return true
}

func validIntegerLiteral(value string) bool {
	if value == "" {
		return false
	}
	position := 0
	if value[position] == '+' || value[position] == '-' {
		position++
		if position == len(value) {
			return false
		}
	}
	base := byte(10)
	if position+2 <= len(value) && value[position] == '0' {
		switch value[position+1] {
		case 'x', 'X':
			base = 16
			position += 2
		case 'b', 'B':
			base = 2
			position += 2
		case 'o', 'O':
			base = 8
			position += 2
		}
	}
	digits := 0
	for ; position < len(value); position++ {
		current := value[position]
		if current == '_' {
			continue
		}
		valid := current >= '0' && current <= '9'
		switch base {
		case 2:
			valid = current == '0' || current == '1'
		case 8:
			valid = current >= '0' && current <= '7'
		case 16:
			valid = valid ||
				current >= 'a' && current <= 'f' ||
				current >= 'A' && current <= 'F'
		}
		if !valid {
			return false
		}
		digits++
	}
	return digits > 0
}

func Array(key, value Type) Type {
	if key.IsUnknown() {
		key = Mixed()
	}
	if value.IsUnknown() {
		value = Mixed()
	}
	if key.Equal(Mixed()) && value.Equal(Mixed()) {
		return arrayType
	}
	return newType(ArrayKind, "", key, value)
}

func NonEmptyArray(key, value Type) Type {
	if key.IsUnknown() {
		key = Mixed()
	}
	if value.IsUnknown() {
		value = Mixed()
	}
	if key.Equal(Mixed()) && value.Equal(Mixed()) {
		return nonEmptyArrayType
	}
	return newType(NonEmptyArrayKind, "", key, value)
}

func List(value Type) Type {
	if value.IsUnknown() {
		value = Mixed()
	}
	if value.Equal(Mixed()) {
		return listType
	}
	return newType(ListKind, "", value)
}

func NonEmptyList(value Type) Type {
	if value.IsUnknown() {
		value = Mixed()
	}
	if value.Equal(Mixed()) {
		return nonEmptyListType
	}
	return newType(NonEmptyListKind, "", value)
}

func Iterable(key, value Type) Type {
	if key.IsUnknown() {
		key = Mixed()
	}
	if value.IsUnknown() {
		value = Mixed()
	}
	if key.Equal(Mixed()) && value.Equal(Mixed()) {
		return iterableType
	}
	return newType(IterableKind, "", key, value)
}

func Callable(parameters []CallableParameter, result Type) Type {
	if len(parameters) == 0 && result.IsUnknown() {
		return callableType
	}
	if result.IsUnknown() {
		result = Mixed()
	}
	params := make([]CallableParameter, len(parameters))
	copy(params, parameters)
	node := rawDetailedType(
		CallableKind,
		&typeDetails{
			params: params,
			result: result,
		},
	)
	return finishType(node.node)
}

// Conditional represents PHPDoc's subject-is-target conditional type.
func Conditional(subject, target, ifTrue, ifFalse Type) Type {
	return newType(
		ConditionalKind,
		"",
		subject,
		target,
		ifTrue,
		ifFalse,
	)
}

func ClassString(of Type) Type {
	if of.IsUnknown() || of.Equal(Object()) {
		return classStringType
	}
	return newType(ClassStringKind, "", of)
}

func LiteralInt(value string) Type {
	value = strings.TrimSpace(value)
	if !validIntegerLiteral(value) {
		return Int()
	}
	switch value {
	case "0":
		return zeroType
	case "1":
		return oneType
	}
	return newType(LiteralIntKind, value)
}

func LiteralFloat(value string) Type {
	value = strings.TrimSpace(value)
	normalized := strings.ReplaceAll(value, "_", "")
	if _, err := strconv.ParseFloat(normalized, 64); err != nil {
		return Float()
	}
	return newType(LiteralFloatKind, value)
}

func LiteralString(value string) Type {
	if value == "" {
		return emptyStringType
	}
	return newType(LiteralStringKind, value)
}

func ArrayShape(fields []ShapeField, open bool) Type {
	return shape(ArrayShapeKind, fields, open)
}

func ObjectShape(fields []ShapeField, open bool) Type {
	return shape(ObjectShapeKind, fields, open)
}

func shape(kind Kind, fields []ShapeField, open bool) Type {
	copied := make([]ShapeField, len(fields))
	copy(copied, fields)
	return shapeOwned(kind, copied, open)
}

// ArrayShapeOwned constructs an array shape without copying fields. The
// caller transfers ownership of fields and must not read or mutate it after
// this call. This is useful for inference, which builds a one-use field slice
// specifically for the returned immutable type.
func ArrayShapeOwned(fields []ShapeField, open bool) Type {
	return shapeOwned(ArrayShapeKind, fields, open)
}

func shapeOwned(kind Kind, fields []ShapeField, open bool) Type {
	for index := range fields {
		fields[index].Name = renderShapeFieldName(fields[index].Name)
	}
	slices.SortStableFunc(fields, func(left, right ShapeField) int {
		return strings.Compare(left.Name, right.Name)
	})
	return finishType(
		rawDetailedType(
			kind,
			&typeDetails{fields: fields, open: open},
		).node,
	)
}

// Union canonicalizes and deduplicates a union. Nested unions are flattened.
func Union(values ...Type) Type {
	return union(values, true)
}

func union(values []Type, cacheText bool) Type {
	flat := make([]Type, 0, len(values))
	var seen map[string]struct{}
	hasBool := false
	for _, value := range values {
		if value.IsUnknown() {
			return Unknown()
		}
		if value.Kind() == MixedKind {
			return Mixed()
		}
		if value.Kind() == NeverKind {
			continue
		}
		members := []Type{value}
		if value.Kind() == UnionKind {
			members = value.node.args.values()
		}
		for _, member := range members {
			if member.Kind() == BoolKind {
				hasBool = true
			}
			flat, seen = appendUniqueType(flat, seen, member)
		}
	}
	if hasBool {
		filtered := flat[:0]
		for _, value := range flat {
			if value.Kind() != TrueKind && value.Kind() != FalseKind {
				filtered = append(filtered, value)
			}
		}
		flat = filtered
	}
	if len(flat) == 0 {
		return Never()
	}
	if len(flat) == 1 {
		return flat[0]
	}
	slices.SortStableFunc(flat, canonicalTypeCompare)
	value := rawType(UnionKind, "", flat...)
	if cacheText {
		return finishType(value.node)
	}
	return value
}

// Intersection canonicalizes and deduplicates an intersection.
func Intersection(values ...Type) Type {
	flat := make([]Type, 0, len(values))
	var seen map[string]struct{}
	for _, value := range values {
		if value.IsUnknown() {
			return Unknown()
		}
		if value.Kind() == NeverKind {
			return Never()
		}
		if value.Kind() == MixedKind {
			continue
		}
		members := []Type{value}
		if value.Kind() == IntersectionKind {
			members = value.node.args.values()
		}
		for _, member := range members {
			flat, seen = appendUniqueType(flat, seen, member)
		}
	}
	if len(flat) == 0 {
		return Mixed()
	}
	if len(flat) == 1 {
		return flat[0]
	}
	slices.SortStableFunc(flat, canonicalTypeCompare)
	return newType(IntersectionKind, "", flat...)
}

func appendUniqueType(
	values []Type,
	seen map[string]struct{},
	value Type,
) ([]Type, map[string]struct{}) {
	const maxLinearDeduplication = 16
	if seen == nil && len(values) < maxLinearDeduplication {
		for _, existing := range values {
			if existing.Equal(value) {
				return values, nil
			}
		}
		return append(values, value), nil
	}
	key := value.Key()
	if seen == nil {
		seen = make(map[string]struct{}, len(values)+1)
		for _, existing := range values {
			seen[existing.Key()] = struct{}{}
		}
	}
	if _, exists := seen[key]; exists {
		return values, seen
	}
	seen[key] = struct{}{}
	return append(values, value), seen
}

func Nullable(value Type) Type {
	return Union(value, Null())
}

func (t Type) Kind() Kind {
	if t.node == nil {
		return UnknownKind
	}
	return t.node.args.kind
}

func (t Type) Name() string {
	return t.node.semanticName()
}

func (t Type) Arguments() []Type {
	if t.node == nil {
		return nil
	}
	return cloneTypes(t.node.args.values())
}

// ArgumentCount returns the number of generic or composite type arguments
// without exposing the type's immutable backing storage.
func (t Type) ArgumentCount() int {
	if t.node == nil {
		return 0
	}
	return t.node.args.count()
}

// Argument returns one generic or composite type argument. An out-of-range
// index returns Unknown.
func (t Type) Argument(index int) Type {
	if t.node == nil || index < 0 || index >= t.node.args.count() {
		return Unknown()
	}
	return t.node.args.values()[index]
}

// arguments returns the immutable node storage for internal read-only use.
// Exported callers receive a defensive copy through Arguments.
func (t Type) arguments() []Type {
	if t.node == nil {
		return nil
	}
	return t.node.args.values()
}

func (t Type) Parameters() []CallableParameter {
	details := t.details()
	if details == nil || len(details.params) == 0 {
		return nil
	}
	result := make([]CallableParameter, len(details.params))
	copy(result, details.params)
	return result
}

// ParameterCount returns the number of callable parameters without copying
// the immutable backing slice.
func (t Type) ParameterCount() int {
	details := t.details()
	if details == nil {
		return 0
	}
	return len(details.params)
}

// Parameter returns one callable parameter. An out-of-range index returns the
// zero value.
func (t Type) Parameter(index int) CallableParameter {
	details := t.details()
	if details == nil || index < 0 || index >= len(details.params) {
		return CallableParameter{}
	}
	return details.params[index]
}

func (t Type) parameters() []CallableParameter {
	details := t.details()
	if details == nil {
		return nil
	}
	return details.params
}

func (t Type) Result() Type {
	details := t.details()
	if details == nil {
		return Unknown()
	}
	return details.result
}

func (t Type) Fields() []ShapeField {
	details := t.details()
	if details == nil || len(details.fields) == 0 {
		return nil
	}
	result := make([]ShapeField, len(details.fields))
	copy(result, details.fields)
	return result
}

// FieldCount returns the number of shape fields without copying the immutable
// backing slice.
func (t Type) FieldCount() int {
	details := t.details()
	if details == nil {
		return 0
	}
	return len(details.fields)
}

// Field returns one shape field. An out-of-range index returns the zero value.
func (t Type) Field(index int) ShapeField {
	details := t.details()
	if details == nil || index < 0 || index >= len(details.fields) {
		return ShapeField{}
	}
	return details.fields[index]
}

// ContainsUncertain reports whether any part of a type is unknown or mixed.
// Diagnostics use this to distinguish a proven incompatibility from a value
// whose nested element/callback/shape type is not known precisely enough.
func ContainsUncertain(value Type) bool {
	if value.IsUnknown() ||
		value.Kind() == MixedKind ||
		value.Kind() == TemplateKind {
		return true
	}
	if value.Kind() == CallableKind {
		for _, parameter := range value.parameters() {
			if ContainsUncertain(parameter.Type) {
				return true
			}
		}
		return ContainsUncertain(value.Result())
	}
	for _, field := range value.fields() {
		if ContainsUncertain(field.Type) {
			return true
		}
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		if ContainsUncertain(value.Argument(index)) {
			return true
		}
	}
	return false
}

func (t Type) fields() []ShapeField {
	details := t.details()
	if details == nil {
		return nil
	}
	return details.fields
}

func (t Type) IsOpenShape() bool {
	details := t.details()
	return details != nil && details.open
}

func (t Type) details() *typeDetails {
	if t.node == nil {
		return nil
	}
	return t.node.args.details()
}

func (t Type) IsUnknown() bool {
	return t.Kind() == UnknownKind
}

func (t Type) Equal(other Type) bool {
	return equalType(t, other)
}

func equalType(left, right Type) bool {
	if left.node == right.node {
		return true
	}
	if left.Kind() != right.Kind() {
		return false
	}
	if left.node == nil || right.node == nil {
		return left.Kind() == UnknownKind &&
			right.Kind() == UnknownKind
	}
	if left.node.name != right.node.name ||
		left.node.args.count() != right.node.args.count() {
		return false
	}
	leftArguments := left.node.args.values()
	rightArguments := right.node.args.values()
	for index := range leftArguments {
		if !equalType(leftArguments[index], rightArguments[index]) {
			return false
		}
	}
	return equalTypeDetails(left.details(), right.details())
}

func equalTypeDetails(left, right *typeDetails) bool {
	if left == right {
		return true
	}
	if left == nil || right == nil ||
		left.open != right.open ||
		len(left.params) != len(right.params) ||
		len(left.fields) != len(right.fields) ||
		!equalType(left.result, right.result) {
		return false
	}
	for index := range left.params {
		leftParameter := left.params[index]
		rightParameter := right.params[index]
		if leftParameter.Name != rightParameter.Name ||
			leftParameter.Optional != rightParameter.Optional ||
			leftParameter.Variadic != rightParameter.Variadic ||
			leftParameter.ByReference != rightParameter.ByReference ||
			!equalType(leftParameter.Type, rightParameter.Type) {
			return false
		}
	}
	for index := range left.fields {
		leftField := left.fields[index]
		rightField := right.fields[index]
		if leftField.Name != rightField.Name ||
			leftField.Optional != rightField.Optional ||
			!equalType(leftField.Type, rightField.Type) {
			return false
		}
	}
	return true
}

func canonicalTypeCompare(left, right Type) int {
	if left.Kind() == LiteralStringKind {
		if right.Kind() == LiteralStringKind {
			return strings.Compare(left.Name(), right.Name())
		}
		// Quoted literal strings sort before every valid PHP type name and
		// numeric literal in the canonical serialization.
		return -1
	}
	if right.Kind() == LiteralStringKind {
		return 1
	}
	return strings.Compare(left.Key(), right.Key())
}

const persistedTemplatePrefix = "\x00shopware-lsp-templates:"

// PersistedString preserves template identity in addition to the canonical
// display text. Template variables and ordinary class names intentionally
// render alike in PHPDoc, so the compact persistence format carries the
// template names in a private header.
func (t Type) PersistedString() string {
	templates := make(map[string]struct{})
	collectTemplateNames(t, templates)
	if len(templates) == 0 {
		return t.String()
	}
	names := make([]string, 0, len(templates))
	for name := range templates {
		names = append(names, name)
	}
	slices.Sort(names)
	return persistedTemplatePrefix +
		strings.Join(names, ",") +
		"\x00" +
		t.String()
}

// ParsePersisted restores a type written by PersistedString. It also accepts
// the canonical strings emitted by older caches.
func ParsePersisted(text string) (Type, error) {
	if !strings.HasPrefix(text, persistedTemplatePrefix) {
		return Parse(text)
	}
	payload := strings.TrimPrefix(text, persistedTemplatePrefix)
	separator := strings.IndexByte(payload, 0)
	if separator < 0 {
		return Type{}, fmt.Errorf("parse persisted PHP type: missing template separator")
	}
	names := make(map[string]struct{})
	for _, name := range strings.Split(payload[:separator], ",") {
		if name != "" {
			names[name] = struct{}{}
		}
	}
	value, err := Parse(payload[separator+1:])
	if err != nil {
		return Type{}, err
	}
	return RestoreTemplates(value, names), nil
}

// MarshalText persists a type through its compact parseable representation.
func (t Type) MarshalText() ([]byte, error) {
	return []byte(t.PersistedString()), nil
}

// EncodeMsgpack writes the canonical type string directly. This avoids the
// temporary byte slice required by encoding.TextMarshaler when persisting the
// large workspace type graph.
func (t Type) EncodeMsgpack(encoder *msgpack.Encoder) error {
	return encoder.EncodeString(t.PersistedString())
}

// UnmarshalText restores a type persisted by MarshalText.
func (t *Type) UnmarshalText(text []byte) error {
	value, err := ParsePersisted(string(text))
	if err != nil {
		return fmt.Errorf("parse persisted PHP type %q: %w", string(text), err)
	}
	*t = value
	return nil
}

// DecodeMsgpack accepts both the current string encoding and the byte encoding
// emitted by older caches.
func (t *Type) DecodeMsgpack(decoder *msgpack.Decoder) error {
	text, err := decoder.DecodeString()
	if err != nil {
		return err
	}
	value, err := ParsePersisted(text)
	if err != nil {
		return fmt.Errorf("parse persisted PHP type %q: %w", text, err)
	}
	*t = value
	return nil
}

// Key is the stable canonical serialization used for equality, persistence,
// interning, and deterministic tests.
func (t Type) Key() string {
	return t.String()
}

func (t Type) String() string {
	if text := t.node.cachedText(); text != "" {
		return text
	}
	return t.render()
}

func cacheTypeText(node *typeNode) bool {
	if node == nil || kindHasSemanticName(node.args.kind) {
		return false
	}
	details := node.args.details()
	if node.args.count() > 0 ||
		details != nil &&
			(len(details.params) > 0 || len(details.fields) > 0) {
		return true
	}
	switch node.args.kind {
	case UnionKind, IntersectionKind, ConditionalKind:
		return true
	default:
		return false
	}
}

func (t Type) render() string {
	switch t.Kind() {
	case UnknownKind, ErrorKind, NeverKind, MixedKind, VoidKind, NullKind,
		BoolKind, TrueKind, FalseKind, IntKind, FloatKind, StringKind,
		ResourceKind, ArrayKeyKind:
		return t.Kind().String()
	case SelfKind, StaticKind, ParentKind, LiteralIntKind, LiteralFloatKind,
		LiteralStringKind, TemplateKind, ClassStringKind, ObjectKind:
		return renderNamedType(t)
	case ArrayKind, NonEmptyArrayKind, ListKind, NonEmptyListKind, IterableKind:
		return renderContainerType(t)
	case CallableKind:
		return renderCallableType(t)
	case ConditionalKind:
		return renderConditionalType(t)
	case ArrayShapeKind, ObjectShapeKind:
		return renderShapeType(t)
	case UnionKind:
		return renderComposite(t.arguments(), "|", true)
	case IntersectionKind:
		return renderComposite(t.arguments(), "&", false)
	default:
		return "unknown"
	}
}

func renderNamedType(value Type) string {
	switch value.Kind() {
	case SelfKind, StaticKind, ParentKind:
		return renderGeneric(value.Kind().String(), value.arguments())
	case LiteralStringKind:
		return strconv.Quote(value.Name())
	case ClassStringKind:
		arguments := value.arguments()
		if len(arguments) == 0 || arguments[0].Equal(Object()) {
			return "class-string"
		}
		return renderGeneric("class-string", arguments)
	case ObjectKind:
		if value.Name() == "" {
			return "object"
		}
		return renderGeneric(value.Name(), value.arguments())
	default:
		return value.Name()
	}
}

func renderContainerType(value Type) string {
	name := value.Kind().String()
	arguments := value.arguments()
	switch value.Kind() {
	case ListKind, NonEmptyListKind:
		if len(arguments) == 0 || arguments[0].Equal(Mixed()) {
			return name
		}
	default:
		if len(arguments) != 2 ||
			arguments[0].Equal(Mixed()) && arguments[1].Equal(Mixed()) {
			return name
		}
	}
	return renderGeneric(name, arguments)
}

func renderCallableType(value Type) string {
	parameters := value.parameters()
	if len(parameters) == 0 && value.Result().Equal(Mixed()) {
		return "callable"
	}
	var builder strings.Builder
	builder.Grow(callableRenderedLength(parameters, value.Result()))
	builder.WriteString("callable(")
	for index, parameter := range parameters {
		if index > 0 {
			builder.WriteByte(',')
		}
		writeCallableParameter(&builder, parameter)
	}
	builder.WriteString("):")
	writeRenderedType(&builder, value.Result())
	return builder.String()
}

func callableRenderedLength(parameters []CallableParameter, result Type) int {
	length := len("callable():") + renderedLengthHint(result)
	for index, parameter := range parameters {
		if index > 0 {
			length++
		}
		if parameter.ByReference {
			length++
		}
		if parameter.Variadic {
			length += len("...")
		}
		length += renderedLengthHint(parameter.Type)
		if parameter.Name != "" {
			length += 1 + len(parameter.Name)
		}
		if parameter.Optional {
			length++
		}
	}
	return length
}

func writeCallableParameter(builder *strings.Builder, parameter CallableParameter) {
	if parameter.ByReference {
		builder.WriteByte('&')
	}
	if parameter.Variadic {
		builder.WriteString("...")
	}
	writeRenderedType(builder, parameter.Type)
	if parameter.Name != "" {
		builder.WriteByte(' ')
		builder.WriteString(parameter.Name)
	}
	if parameter.Optional {
		builder.WriteByte('=')
	}
}

func renderConditionalType(value Type) string {
	arguments := value.arguments()
	if len(arguments) != 4 {
		return "conditional"
	}
	var builder strings.Builder
	length := len("( is  ?  : )")
	for _, argument := range arguments {
		length += renderedLengthHint(argument)
	}
	builder.Grow(length)
	builder.WriteByte('(')
	writeRenderedType(&builder, arguments[0])
	builder.WriteString(" is ")
	writeRenderedType(&builder, arguments[1])
	builder.WriteString(" ? ")
	writeRenderedType(&builder, arguments[2])
	builder.WriteString(" : ")
	writeRenderedType(&builder, arguments[3])
	builder.WriteByte(')')
	return builder.String()
}

func renderShapeType(value Type) string {
	prefix := "array"
	if value.Kind() == ObjectShapeKind {
		prefix = "object"
	}
	fields := value.fields()
	length := len(prefix) + 2
	for index, field := range fields {
		if index > 0 {
			length++
		}
		length += len(field.Name) + 1 + renderedLengthHint(field.Type)
		if field.Optional {
			length++
		}
	}
	if value.IsOpenShape() {
		if len(fields) > 0 {
			length++
		}
		length += len("...")
	}
	var builder strings.Builder
	builder.Grow(length)
	builder.WriteString(prefix)
	builder.WriteByte('{')
	for index, field := range fields {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(renderShapeFieldName(field.Name))
		if field.Optional {
			builder.WriteByte('?')
		}
		builder.WriteByte(':')
		writeRenderedType(&builder, field.Type)
	}
	if value.IsOpenShape() {
		if len(fields) > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString("...")
	}
	builder.WriteByte('}')
	return builder.String()
}

func renderShapeFieldName(name string) string {
	if len(name) >= 2 &&
		(name[0] == '\'' && name[len(name)-1] == '\'' ||
			name[0] == '"' && name[len(name)-1] == '"') {
		return name
	}
	if name != "" {
		valid := true
		for index := 0; index < len(name); index++ {
			if !isTypeNameByte(name[index]) {
				valid = false
				break
			}
		}
		if valid {
			return name
		}
	}
	return strconv.Quote(name)
}

func renderGeneric(name string, args []Type) string {
	if len(args) == 0 {
		return name
	}
	var builder strings.Builder
	length := len(name) + 2 + len(args) - 1
	for _, argument := range args {
		length += renderedLengthHint(argument)
	}
	builder.Grow(length)
	builder.WriteString(name)
	builder.WriteByte('<')
	for index, argument := range args {
		if index > 0 {
			builder.WriteByte(',')
		}
		writeRenderedType(&builder, argument)
	}
	builder.WriteByte('>')
	return builder.String()
}

func renderComposite(values []Type, separator string, wrapIntersection bool) string {
	var builder strings.Builder
	length := max(0, len(values)-1) * len(separator)
	for _, value := range values {
		length += renderedLengthHint(value)
		if wrapIntersection &&
			(value.Kind() == IntersectionKind ||
				value.Kind() == CallableKind ||
				value.Kind() == ConditionalKind) {
			length += 2
		}
	}
	builder.Grow(length)
	for index, value := range values {
		if index > 0 {
			builder.WriteString(separator)
		}
		wrap := wrapIntersection &&
			(value.Kind() == IntersectionKind ||
				value.Kind() == CallableKind ||
				value.Kind() == ConditionalKind)
		if wrap {
			builder.WriteByte('(')
		}
		writeRenderedType(&builder, value)
		if wrap {
			builder.WriteByte(')')
		}
	}
	return builder.String()
}

// renderedLengthHint returns the exact cached length for composite types and a
// cheap lower bound for semantic-name types. It deliberately avoids String so
// pre-sizing a parent renderer never duplicates a child's rendering allocation.
func renderedLengthHint(value Type) int {
	if value.node == nil {
		return len("unknown")
	}
	if text := value.node.cachedText(); text != "" {
		return len(text)
	}
	switch value.Kind() {
	case ObjectKind, SelfKind, StaticKind, ParentKind:
		name := value.Name()
		if value.Kind() != ObjectKind {
			name = value.Kind().String()
		} else if name == "" {
			return len("object")
		}
		arguments := value.arguments()
		if len(arguments) == 0 {
			return len(name)
		}
		length := len(name) + 2 + len(arguments) - 1
		for _, argument := range arguments {
			length += renderedLengthHint(argument)
		}
		return length
	case LiteralIntKind, LiteralFloatKind, TemplateKind:
		return len(value.Name())
	case LiteralStringKind:
		// Most inferred literals need only the surrounding quotes. Escaped
		// values may grow the builder once more but do not pay for a speculative
		// render merely to obtain an exact length.
		return len(value.Name()) + 2
	default:
		return len(value.Kind().String())
	}
}

func writeRenderedType(builder *strings.Builder, value Type) {
	if builder == nil {
		return
	}
	if value.node == nil {
		builder.WriteString("unknown")
		return
	}
	if text := value.node.cachedText(); text != "" {
		builder.WriteString(text)
		return
	}
	switch value.Kind() {
	case ObjectKind, SelfKind, StaticKind, ParentKind:
		name := value.Name()
		if value.Kind() != ObjectKind {
			name = value.Kind().String()
		} else if name == "" {
			builder.WriteString("object")
			return
		}
		builder.WriteString(name)
		arguments := value.arguments()
		if len(arguments) == 0 {
			return
		}
		builder.WriteByte('<')
		for index, argument := range arguments {
			if index > 0 {
				builder.WriteByte(',')
			}
			writeRenderedType(builder, argument)
		}
		builder.WriteByte('>')
	case LiteralIntKind, LiteralFloatKind, TemplateKind:
		builder.WriteString(value.Name())
	case LiteralStringKind:
		var scratch [64]byte
		quoted := strconv.AppendQuote(scratch[:0], value.Name())
		_, _ = builder.Write(quoted)
	default:
		builder.WriteString(value.Kind().String())
	}
}

// Substitute replaces template variables recursively.
func Substitute(value Type, templates map[string]Type) Type {
	if len(templates) == 0 {
		return value
	}
	if value.Kind() == TemplateKind {
		if replacement, ok := templates[value.Name()]; ok {
			return replacement
		}
		return value
	}
	args := value.Arguments()
	for index := range args {
		args[index] = Substitute(args[index], templates)
	}
	switch value.Kind() {
	case ObjectKind:
		if value.Name() == "" {
			return Object()
		}
		return Named(value.Name(), args...)
	case SelfKind:
		return Self(args...)
	case StaticKind:
		return Static(args...)
	case ParentKind:
		return Parent(args...)
	case ArrayKind:
		return Array(args[0], args[1])
	case NonEmptyArrayKind:
		return NonEmptyArray(args[0], args[1])
	case ListKind:
		return List(args[0])
	case NonEmptyListKind:
		return NonEmptyList(args[0])
	case IterableKind:
		return Iterable(args[0], args[1])
	case ClassStringKind:
		return ClassString(args[0])
	case UnionKind:
		return Union(args...)
	case IntersectionKind:
		return Intersection(args...)
	case ConditionalKind:
		if len(args) != 4 {
			return value
		}
		if !containsTemplateType(args[0]) {
			if (Relations{}).IsSubtype(args[0], args[1]) {
				return args[2]
			}
			return args[3]
		}
		return Conditional(args[0], args[1], args[2], args[3])
	case CallableKind:
		parameters := value.Parameters()
		for index := range parameters {
			parameters[index].Type = Substitute(parameters[index].Type, templates)
		}
		return Callable(parameters, Substitute(value.Result(), templates))
	case ArrayShapeKind, ObjectShapeKind:
		fields := value.Fields()
		for index := range fields {
			fields[index].Type = Substitute(fields[index].Type, templates)
		}
		if value.Kind() == ArrayShapeKind {
			return ArrayShape(fields, value.IsOpenShape())
		}
		return ObjectShape(fields, value.IsOpenShape())
	default:
		return value
	}
}

// RestoreTemplates turns unqualified object names back into template
// variables using declaration context recovered alongside a persisted type.
func RestoreTemplates(
	value Type,
	templates map[string]struct{},
) Type {
	if len(templates) == 0 {
		return value
	}
	if value.Kind() == ObjectKind &&
		!strings.Contains(value.Name(), "\\") {
		if _, exists := templates[value.Name()]; exists {
			return Template(value.Name())
		}
	}
	args := value.Arguments()
	for index := range args {
		args[index] = RestoreTemplates(args[index], templates)
	}
	switch value.Kind() {
	case ObjectKind:
		if value.Name() == "" {
			return Object()
		}
		return Named(value.Name(), args...)
	case SelfKind:
		return Self(args...)
	case StaticKind:
		return Static(args...)
	case ParentKind:
		return Parent(args...)
	case ArrayKind:
		return Array(args[0], args[1])
	case NonEmptyArrayKind:
		return NonEmptyArray(args[0], args[1])
	case ListKind:
		return List(args[0])
	case NonEmptyListKind:
		return NonEmptyList(args[0])
	case IterableKind:
		return Iterable(args[0], args[1])
	case ClassStringKind:
		return ClassString(args[0])
	case UnionKind:
		return Union(args...)
	case IntersectionKind:
		return Intersection(args...)
	case ConditionalKind:
		return Conditional(args[0], args[1], args[2], args[3])
	case CallableKind:
		parameters := value.Parameters()
		for index := range parameters {
			parameters[index].Type = RestoreTemplates(
				parameters[index].Type,
				templates,
			)
		}
		return Callable(
			parameters,
			RestoreTemplates(value.Result(), templates),
		)
	case ArrayShapeKind, ObjectShapeKind:
		fields := value.Fields()
		for index := range fields {
			fields[index].Type = RestoreTemplates(
				fields[index].Type,
				templates,
			)
		}
		if value.Kind() == ArrayShapeKind {
			return ArrayShape(fields, value.IsOpenShape())
		}
		return ObjectShape(fields, value.IsOpenShape())
	default:
		return value
	}
}

func collectTemplateNames(value Type, result map[string]struct{}) {
	if value.Kind() == TemplateKind {
		result[value.Name()] = struct{}{}
		return
	}
	if value.Kind() == CallableKind {
		for _, parameter := range value.parameters() {
			collectTemplateNames(parameter.Type, result)
		}
		collectTemplateNames(value.Result(), result)
		return
	}
	if value.Kind() == ArrayShapeKind ||
		value.Kind() == ObjectShapeKind {
		for _, field := range value.fields() {
			collectTemplateNames(field.Type, result)
		}
		return
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		collectTemplateNames(value.Argument(index), result)
	}
}

func ContainsTemplate(value Type) bool {
	return containsTemplateType(value)
}

func containsTemplateType(value Type) bool {
	if value.Kind() == TemplateKind {
		return true
	}
	if value.Kind() == CallableKind {
		for _, parameter := range value.parameters() {
			if containsTemplateType(parameter.Type) {
				return true
			}
		}
		return containsTemplateType(value.Result())
	}
	for index := 0; index < value.ArgumentCount(); index++ {
		if containsTemplateType(value.Argument(index)) {
			return true
		}
	}
	return false
}
