package semantic

import "github.com/shopware/shopware-lsp/internal/php/types"

// symbolHierarchy retains source-owned immutable collections without carrying
// seven slice headers in every Symbol. Typed groups and aliases are uncommon
// enough to live behind optional compact records.
type symbolHierarchy struct {
	extendsData    *string
	implementsData *string
	traitsData     *string
	types          *symbolHierarchyTypes
	aliases        *symbolHierarchyAliases
	lengths        uint64
}

type symbolHierarchyTypes struct {
	extendsData    *types.Type
	implementsData *types.Type
	traitsData     *types.Type
	lengths        uint64
}

type symbolHierarchyAliases struct {
	data   *TraitAlias
	length uint32
}

func newSymbolHierarchy(
	extends []string,
	implements []string,
	traits []string,
	extendsTypes []types.Type,
	implementsTypes []types.Type,
	traitTypes []types.Type,
	aliases []TraitAlias,
) symbolHierarchy {
	hierarchy := symbolHierarchy{
		extendsData:    workspaceSliceData(extends),
		implementsData: workspaceSliceData(implements),
		traitsData:     workspaceSliceData(traits),
		lengths: packWorkspaceHierarchyLengths(
			len(extends), len(implements), len(traits),
		),
	}
	if len(extendsTypes) != 0 ||
		len(implementsTypes) != 0 ||
		len(traitTypes) != 0 {
		hierarchy.types = &symbolHierarchyTypes{
			extendsData:    workspaceSliceData(extendsTypes),
			implementsData: workspaceSliceData(implementsTypes),
			traitsData:     workspaceSliceData(traitTypes),
			lengths: packWorkspaceHierarchyLengths(
				len(extendsTypes), len(implementsTypes), len(traitTypes),
			),
		}
	}
	if len(aliases) != 0 {
		if uint64(len(aliases)) > uint64(^uint32(0)) {
			panic("semantic: symbol trait alias collection exceeds packed range")
		}
		hierarchy.aliases = &symbolHierarchyAliases{
			data: workspaceSliceData(aliases), length: uint32(len(aliases)),
		}
	}
	return hierarchy
}

func (s Symbol) Extends() []string {
	return workspaceSlice(
		s.hierarchy.extendsData,
		workspaceHierarchyLength(s.hierarchy.lengths, 0),
	)
}

func (s Symbol) Implements() []string {
	return workspaceSlice(
		s.hierarchy.implementsData,
		workspaceHierarchyLength(s.hierarchy.lengths, 1),
	)
}

func (s Symbol) Traits() []string {
	return workspaceSlice(
		s.hierarchy.traitsData,
		workspaceHierarchyLength(s.hierarchy.lengths, 2),
	)
}

func (s Symbol) ExtendsTypes() []types.Type {
	if s.hierarchy.types == nil {
		return nil
	}
	return workspaceSlice(
		s.hierarchy.types.extendsData,
		workspaceHierarchyLength(s.hierarchy.types.lengths, 0),
	)
}

func (s Symbol) ImplementsTypes() []types.Type {
	if s.hierarchy.types == nil {
		return nil
	}
	return workspaceSlice(
		s.hierarchy.types.implementsData,
		workspaceHierarchyLength(s.hierarchy.types.lengths, 1),
	)
}

func (s Symbol) TraitTypes() []types.Type {
	if s.hierarchy.types == nil {
		return nil
	}
	return workspaceSlice(
		s.hierarchy.types.traitsData,
		workspaceHierarchyLength(s.hierarchy.types.lengths, 2),
	)
}

func (s Symbol) TraitAliases() []TraitAlias {
	if s.hierarchy.aliases == nil {
		return nil
	}
	return workspaceSlice(
		s.hierarchy.aliases.data,
		s.hierarchy.aliases.length,
	)
}

func (s *Symbol) SetHierarchy(
	extends []string,
	implements []string,
	traits []string,
	extendsTypes []types.Type,
	implementsTypes []types.Type,
	traitTypes []types.Type,
	aliases []TraitAlias,
) {
	if s == nil {
		return
	}
	s.hierarchy = newSymbolHierarchy(
		extends, implements, traits,
		extendsTypes, implementsTypes, traitTypes, aliases,
	)
}

func (s *Symbol) SetExtends(values []string) {
	if s != nil {
		s.SetHierarchy(
			values, s.Implements(), s.Traits(),
			s.ExtendsTypes(), s.ImplementsTypes(), s.TraitTypes(),
			s.TraitAliases(),
		)
	}
}

func (s *Symbol) SetImplements(values []string) {
	if s != nil {
		s.SetHierarchy(
			s.Extends(), values, s.Traits(),
			s.ExtendsTypes(), s.ImplementsTypes(), s.TraitTypes(),
			s.TraitAliases(),
		)
	}
}

func (s *Symbol) SetTraits(values []string) {
	if s != nil {
		s.SetHierarchy(
			s.Extends(), s.Implements(), values,
			s.ExtendsTypes(), s.ImplementsTypes(), s.TraitTypes(),
			s.TraitAliases(),
		)
	}
}

func (s *Symbol) SetExtendsTypes(values []types.Type) {
	if s != nil {
		s.SetHierarchy(
			s.Extends(), s.Implements(), s.Traits(),
			values, s.ImplementsTypes(), s.TraitTypes(), s.TraitAliases(),
		)
	}
}

func (s *Symbol) SetImplementsTypes(values []types.Type) {
	if s != nil {
		s.SetHierarchy(
			s.Extends(), s.Implements(), s.Traits(),
			s.ExtendsTypes(), values, s.TraitTypes(), s.TraitAliases(),
		)
	}
}

func (s *Symbol) SetTraitTypes(values []types.Type) {
	if s != nil {
		s.SetHierarchy(
			s.Extends(), s.Implements(), s.Traits(),
			s.ExtendsTypes(), s.ImplementsTypes(), values, s.TraitAliases(),
		)
	}
}

func (s *Symbol) SetTraitAliases(values []TraitAlias) {
	if s != nil {
		s.SetHierarchy(
			s.Extends(), s.Implements(), s.Traits(),
			s.ExtendsTypes(), s.ImplementsTypes(), s.TraitTypes(), values,
		)
	}
}
