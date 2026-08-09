package inference

import (
	"slices"
	"strconv"
	"strings"

	phpsyntax "github.com/shopware/shopware-lsp/internal/parser/php/syntax"
	"github.com/shopware/shopware-lsp/internal/php/literal"
	"github.com/shopware/shopware-lsp/internal/php/types"
)

func (s *functionState) inferArray(node *phpsyntax.Node, env environment) types.Type {
	structure := inspectArrayLiteral(node)
	if structure.implicitList {
		if structure.itemCount > 0 &&
			structure.itemCount <= maxInferredTupleFields {
			fields := make([]types.ShapeField, 0, structure.itemCount)
			for cursor := directNodes(node).Cursor(); cursor.Next(); {
				item := cursor.Node()
				if item.Kind() != phpsyntax.PhpArrayItem {
					continue
				}
				values := directNodes(item)
				if values.Len() == 0 {
					continue
				}
				fields = append(fields, types.ShapeField{
					Name: strconvItoa(len(fields)),
					Type: s.infer(values.At(0), env),
				})
			}
			return types.ArrayShapeOwned(fields, false)
		}
		valueTypes := types.NewJoiner(s.relations, types.Never())
		for cursor := directNodes(node).Cursor(); cursor.Next(); {
			item := cursor.Node()
			if item.Kind() != phpsyntax.PhpArrayItem {
				continue
			}
			values := directNodes(item)
			if values.Len() == 0 {
				continue
			}
			valueTypes.Add(s.infer(values.At(0), env))
		}
		valueType := valueTypes.Value()
		if valueType.Kind() == types.NeverKind {
			return types.Array(types.ArrayKey(), types.Never())
		}
		return types.NonEmptyList(valueType)
	}
	return s.inferExplicitArray(node, env, structure)
}

func (s *functionState) inferExplicitArray(
	node *phpsyntax.Node,
	env environment,
	structure arrayLiteralStructure,
) types.Type {
	inference := newArrayLiteralInference(s, env, structure)
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		inference.addItem(s, cursor.Node())
	}
	return inference.result()
}

type arrayLiteralInference struct {
	env               environment
	structure         arrayLiteralStructure
	keyTypes          types.Joiner
	valueTypes        types.Joiner
	shapeFields       []types.ShapeField
	shapeFieldIndices map[string]int
	nextKey           int
	shapeCandidate    bool
	listCandidate     bool
	knownNonEmpty     bool
}

func newArrayLiteralInference(
	state *functionState,
	env environment,
	structure arrayLiteralStructure,
) arrayLiteralInference {
	return arrayLiteralInference{
		env:            env,
		structure:      structure,
		keyTypes:       types.NewJoiner(state.relations, types.Never()),
		valueTypes:     types.NewJoiner(state.relations, types.Never()),
		shapeCandidate: true,
		listCandidate:  true,
	}
}

func (r *arrayLiteralInference) addItem(
	state *functionState,
	item *phpsyntax.Node,
) {
	if item.Kind() != phpsyntax.PhpArrayItem {
		return
	}
	values := directNodes(item)
	if values.Len() == 0 {
		return
	}
	if hasDirectToken(item, phpsyntax.TkEllipsis) {
		r.addSpread(state, state.infer(values.At(values.Len()-1), r.env))
		return
	}
	r.knownNonEmpty = true
	if values.Len() > 1 {
		r.addKeyedItem(state, values.At(0), values.At(values.Len()-1))
		return
	}
	r.addUnkeyedItem(state, values.At(0))
}

func (r *arrayLiteralInference) addSpread(
	state *functionState,
	source types.Type,
) {
	r.knownNonEmpty = r.knownNonEmpty || arrayTypeKnownNonEmpty(source)
	key, value := state.arrayIterableTypes(source)
	if !state.arraySpreadProducesList(source) {
		r.listCandidate = false
	}
	if r.shapeCandidate {
		r.addSpreadShape(source)
	}
	r.keyTypes.Add(key)
	r.valueTypes.Add(value)
}

func (r *arrayLiteralInference) addSpreadShape(source types.Type) {
	switch {
	case source.Kind() == types.ArrayShapeKind && !source.IsOpenShape():
		for fieldIndex := 0; fieldIndex < source.FieldCount(); fieldIndex++ {
			r.addSpreadField(source.Field(fieldIndex))
		}
	case (source.Kind() == types.ArrayKind || source.Kind() == types.NonEmptyArrayKind) &&
		source.ArgumentCount() == 2 && source.Argument(1).Kind() == types.NeverKind:
	default:
		r.disableShape()
	}
}

func (r *arrayLiteralInference) addSpreadField(field types.ShapeField) {
	name := strings.Trim(field.Name, `"'`)
	if _, err := strconv.Atoi(name); err == nil {
		field.Name = strconvItoa(r.nextKey)
		r.nextKey++
	} else {
		field.Name = name
	}
	r.upsertShapeField(field)
}

func (r *arrayLiteralInference) addKeyedItem(
	state *functionState,
	keyNode, valueNode *phpsyntax.Node,
) {
	r.listCandidate = false
	key := state.infer(keyNode, r.env)
	value := state.infer(valueNode, r.env)
	name, literal := arrayLiteralKey(key)
	if literal && r.shapeCandidate {
		r.upsertShapeField(types.ShapeField{Name: name, Type: value})
		r.advanceNextKey(key)
	} else if !literal {
		r.disableShape()
	}
	r.keyTypes.Add(key)
	r.valueTypes.Add(value)
}

func (r *arrayLiteralInference) addUnkeyedItem(
	state *functionState,
	valueNode *phpsyntax.Node,
) {
	name := strconvItoa(r.nextKey)
	key := types.LiteralInt(name)
	value := state.infer(valueNode, r.env)
	if r.shapeCandidate {
		r.upsertShapeField(types.ShapeField{Name: name, Type: value})
	}
	r.nextKey++
	r.keyTypes.Add(key)
	r.valueTypes.Add(value)
}

func (r *arrayLiteralInference) upsertShapeField(field types.ShapeField) {
	if r.shapeFields == nil {
		r.shapeFields = make([]types.ShapeField, 0, r.structure.itemCount)
	}
	r.shapeFields, r.shapeFieldIndices = upsertShapeField(
		r.shapeFields,
		r.shapeFieldIndices,
		field,
	)
}

func (r *arrayLiteralInference) advanceNextKey(key types.Type) {
	if key.Kind() != types.LiteralIntKind {
		return
	}
	numeric, err := strconv.Atoi(key.Name())
	if err == nil && numeric >= r.nextKey {
		r.nextKey = numeric + 1
	}
}

func (r *arrayLiteralInference) disableShape() {
	r.shapeCandidate = false
	r.shapeFields = nil
	r.shapeFieldIndices = nil
}

func (r *arrayLiteralInference) result() types.Type {
	keyType := r.keyTypes.Value()
	valueType := r.valueTypes.Value()
	if valueType.Kind() == types.NeverKind {
		return types.Array(types.ArrayKey(), types.Never())
	}
	if r.shapeCandidate {
		return types.ArrayShapeOwned(slices.Clip(r.shapeFields), false)
	}
	if r.listCandidate {
		if r.knownNonEmpty {
			return types.NonEmptyList(valueType)
		}
		return types.List(valueType)
	}
	if r.knownNonEmpty {
		return types.NonEmptyArray(keyType, valueType)
	}
	return types.Array(keyType, valueType)
}

const maxInferredTupleFields = 16

type arrayLiteralStructure struct {
	itemCount    int
	implicitList bool
	hasSpread    bool
}

func inspectArrayLiteral(node *phpsyntax.Node) arrayLiteralStructure {
	structure := arrayLiteralStructure{implicitList: true}
	for cursor := directNodes(node).Cursor(); cursor.Next(); {
		item := cursor.Node()
		if item.Kind() != phpsyntax.PhpArrayItem {
			continue
		}
		values := directNodes(item)
		if values.Len() == 0 {
			continue
		}
		structure.itemCount++
		if hasDirectToken(item, phpsyntax.TkEllipsis) {
			structure.implicitList = false
			structure.hasSpread = true
			continue
		}
		if values.Len() > 1 {
			structure.implicitList = false
		}
	}
	return structure
}

func isImplicitListArray(node *phpsyntax.Node) bool {
	return inspectArrayLiteral(node).implicitList
}

const shapeFieldLinearLimit = 16

func upsertShapeField(
	fields []types.ShapeField,
	indices map[string]int,
	field types.ShapeField,
) ([]types.ShapeField, map[string]int) {
	if indices != nil {
		if index, exists := indices[field.Name]; exists {
			fields[index] = field
			return fields, indices
		}
		indices[field.Name] = len(fields)
		return append(fields, field), indices
	}

	for index := range fields {
		if fields[index].Name == field.Name {
			fields[index] = field
			return fields, nil
		}
	}
	if len(fields) == shapeFieldLinearLimit {
		indices = make(map[string]int, shapeFieldLinearLimit*2)
		for index := range fields {
			indices[fields[index].Name] = index
		}
		indices[field.Name] = len(fields)
	}
	return append(fields, field), indices
}

func arrayLiteralKey(value types.Type) (string, bool) {
	switch value.Kind() {
	case types.LiteralIntKind, types.LiteralStringKind:
		return value.Name(), true
	default:
		return "", false
	}
}

func (s *functionState) arrayFieldName(
	node *phpsyntax.Node,
	env environment,
) (string, bool) {
	if value, literal := literal.TypeOf(node); literal {
		return arrayLiteralKey(value)
	}
	receiverNode := classConstantReceiver(node)
	if receiverNode == nil {
		return "", false
	}
	receiver := s.inferReceiver(receiverNode, env, true)
	if receiver.Kind() != types.ObjectKind || receiver.Name() == "" {
		return "", false
	}
	return receiver.Name(), true
}

func (s *functionState) arrayIterableTypes(
	value types.Type,
) (types.Type, types.Type) {
	switch value.Kind() {
	case types.ListKind, types.NonEmptyListKind:
		return types.Int(), value.Argument(0)
	case types.ArrayKind, types.NonEmptyArrayKind, types.IterableKind:
		return value.Argument(0), value.Argument(1)
	case types.ArrayShapeKind:
		return arrayShapeIterableTypes(value, s.relations)
	case types.UnionKind:
		keys := types.NewJoiner(s.relations, types.Never())
		values := types.NewJoiner(s.relations, types.Never())
		for index := 0; index < value.ArgumentCount(); index++ {
			member := value.Argument(index)
			key, element := s.arrayIterableTypes(member)
			keys.Add(key)
			values.Add(element)
		}
		return keys.Value(), values.Value()
	default:
		return types.ArrayKey(), types.Unknown()
	}
}

func (s *functionState) arraySpreadProducesList(value types.Type) bool {
	switch value.Kind() {
	case types.UnionKind:
		for _, member := range value.Arguments() {
			if !s.arraySpreadProducesList(member) {
				return false
			}
		}
		return value.ArgumentCount() > 0
	default:
		key, element := s.arrayIterableTypes(value)
		return element.Kind() == types.NeverKind ||
			!key.IsUnknown() && s.relations.IsSubtype(key, types.Int())
	}
}

func (s *functionState) inferArrayAccess(node *phpsyntax.Node, env environment) types.Type {
	nodes := directNodes(node)
	nodeCount := nodes.Len()
	if nodeCount == 0 {
		return types.Unknown()
	}
	receiver := s.infer(nodes.At(0), env)
	key := types.Unknown()
	if nodeCount > 1 {
		key = s.infer(nodes.At(nodeCount-1), env)
	}
	return s.arrayAccessType(receiver, key)
}

func (s *functionState) arrayAccessType(receiver, key types.Type) types.Type {
	if receiver.Kind() == types.NeverKind {
		// Chained access through a definitely absent array element remains
		// impossible. Preserving never lets isset() discard that flow branch.
		return types.Never()
	}
	if receiver.Kind() == types.UnionKind {
		var members []types.Type
		for _, alternative := range receiver.Arguments() {
			value := s.arrayAccessType(alternative, key)
			if !value.IsUnknown() {
				members = append(members, value)
			}
		}
		return joinTypes(s.relations, members)
	}
	switch receiver.Kind() {
	case types.ArrayKind, types.NonEmptyArrayKind, types.IterableKind:
		if receiver.ArgumentCount() == 2 {
			return receiver.Argument(1)
		}
	case types.ListKind, types.NonEmptyListKind:
		if receiver.ArgumentCount() == 1 {
			return receiver.Argument(0)
		}
	case types.StringKind, types.LiteralStringKind:
		return types.String()
	case types.ArrayShapeKind:
		if key.Kind() == types.LiteralStringKind ||
			key.Kind() == types.LiteralIntKind {
			for fieldIndex := 0; fieldIndex < receiver.FieldCount(); fieldIndex++ {
				field := receiver.Field(fieldIndex)
				if strings.Trim(field.Name, `"'`) == key.Name() {
					if field.Optional {
						return types.Nullable(field.Type)
					}
					return field.Type
				}
			}
		}
	}
	return types.Unknown()
}
