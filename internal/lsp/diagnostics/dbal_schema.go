package diagnostics

import (
	"sort"
	"strings"

	"github.com/shopware/shopware-lsp/internal/doctrine"
	shopwaredal "github.com/shopware/shopware-lsp/internal/shopware/dal"
)

// dbalSchemaCatalog combines every indexed source that describes physical
// tables used through Doctrine DBAL. ORM mappings and Shopware DAL definitions
// are separate domain indexes, but a QueryBuilder can address either schema.
type dbalSchemaCatalog struct {
	doctrine *doctrine.Index
	dal      *shopwaredal.Index
	tables   map[string]*dbalSchemaTable
	names    []string

	dalResolved map[string]bool
	dalLoaded   bool
}

type dbalSchemaTable struct {
	doctrineModels []doctrine.Model
	dalDefinitions []shopwaredal.Definition
}

func newDBALSchemaCatalog(
	doctrineIndex *doctrine.Index,
	dalIndex *shopwaredal.Index,
	models []doctrine.Model,
) *dbalSchemaCatalog {
	catalog := &dbalSchemaCatalog{
		doctrine:    doctrineIndex,
		dal:         dalIndex,
		tables:      make(map[string]*dbalSchemaTable),
		dalResolved: make(map[string]bool),
	}
	for _, model := range models {
		if table := catalog.addTable(model.Table); table != nil {
			table.doctrineModels = append(table.doctrineModels, model)
		}
	}
	return catalog
}

func (catalog *dbalSchemaCatalog) addTable(name string) *dbalSchemaTable {
	name = strings.TrimSpace(name)
	if catalog == nil || name == "" {
		return nil
	}
	key := strings.ToLower(name)
	table := catalog.tables[key]
	if table == nil {
		table = &dbalSchemaTable{}
		catalog.tables[key] = table
		catalog.names = append(catalog.names, name)
	}
	return table
}

func (catalog *dbalSchemaCatalog) addDALDefinition(
	definition shopwaredal.Definition,
) {
	table := catalog.addTable(definition.Name)
	if table == nil {
		return
	}
	for _, current := range table.dalDefinitions {
		if current.File == definition.File && current.Class == definition.Class {
			return
		}
	}
	table.dalDefinitions = append(table.dalDefinitions, definition)
}

func (catalog *dbalSchemaCatalog) resolveDALTable(name string) error {
	if catalog == nil || catalog.dal == nil || name == "" {
		return nil
	}
	key := strings.ToLower(name)
	if catalog.dalResolved[key] || catalog.dalLoaded {
		return nil
	}
	catalog.dalResolved[key] = true
	definitions, err := catalog.dal.Definition(name)
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		catalog.addDALDefinition(definition)
	}
	if len(definitions) != 0 {
		return nil
	}
	// The repository key is case-sensitive while DBAL table comparison is not.
	// Load the complete DAL catalog only for an unresolved reference or when
	// typo suggestions are requested, never for every analyzed PHP document.
	return catalog.loadAllDALTables()
}

func (catalog *dbalSchemaCatalog) loadAllDALTables() error {
	if catalog == nil || catalog.dal == nil || catalog.dalLoaded {
		return nil
	}
	definitions, err := catalog.dal.Definitions()
	if err != nil {
		return err
	}
	for _, definition := range definitions {
		catalog.addDALDefinition(definition)
	}
	catalog.dalLoaded = true
	return nil
}

func (catalog *dbalSchemaCatalog) HasTable(name string) (bool, error) {
	if catalog == nil || name == "" {
		return false, nil
	}
	key := strings.ToLower(name)
	if _, found := catalog.tables[key]; found {
		return true, nil
	}
	if err := catalog.resolveDALTable(name); err != nil {
		return false, err
	}
	_, found := catalog.tables[key]
	return found, nil
}

func (catalog *dbalSchemaCatalog) TableNames() ([]string, error) {
	if catalog == nil {
		return nil, nil
	}
	if err := catalog.loadAllDALTables(); err != nil {
		return nil, err
	}
	result := append([]string(nil), catalog.names...)
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result, nil
}

func (catalog *dbalSchemaCatalog) Columns(
	tableName string,
) ([]string, bool, error) {
	if catalog == nil || tableName == "" {
		return nil, false, nil
	}
	if err := catalog.resolveDALTable(tableName); err != nil {
		return nil, false, err
	}
	table, found := catalog.tables[strings.ToLower(tableName)]
	if !found {
		return nil, false, nil
	}
	seen := make(map[string]bool)
	var result []string
	add := func(name string) {
		name = strings.TrimSpace(name)
		key := strings.ToLower(name)
		if name == "" || seen[key] {
			return
		}
		seen[key] = true
		result = append(result, name)
	}
	if catalog.doctrine != nil {
		for _, model := range table.doctrineModels {
			fields, err := catalog.doctrine.Fields(model.Class)
			if err != nil {
				return nil, true, err
			}
			for _, field := range fields {
				name := field.Column
				if name == "" {
					name = field.Name
				}
				add(name)
			}
		}
	}
	for _, definition := range table.dalDefinitions {
		for _, field := range definition.Fields {
			// Association fields describe object navigation. Their foreign-key
			// storage is represented by the corresponding scalar FkField.
			if field.Association {
				continue
			}
			name := field.StorageName
			if name == "" {
				name = field.Name
			}
			add(name)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		return strings.ToLower(result[left]) < strings.ToLower(result[right])
	})
	return result, true, nil
}

func hasDBALSchemaName(names []string, name string) bool {
	for _, candidate := range names {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}
