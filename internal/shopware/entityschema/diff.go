package entityschema

import (
	"fmt"
	"sort"
	"strings"
)

type ColumnChange struct {
	Entity string  `json:"entity"`
	Before *Column `json:"before,omitempty"`
	After  *Column `json:"after,omitempty"`
}

type RenameCandidate struct {
	From  string `json:"from"`
	Score int    `json:"score"`
}

type RenameQuestion struct {
	Entity     string            `json:"entity"`
	Added      string            `json:"added"`
	Candidates []RenameCandidate `json:"candidates"`
}

type SchemaDiff struct {
	CreatedEntities    []Entity           `json:"createdEntities,omitempty"`
	RemovedEntities    []Entity           `json:"removedEntities,omitempty"`
	AddedColumns       []ColumnChange     `json:"addedColumns,omitempty"`
	RemovedColumns     []ColumnChange     `json:"removedColumns,omitempty"`
	ChangedColumns     []ColumnChange     `json:"changedColumns,omitempty"`
	RenameQuestions    []RenameQuestion   `json:"renameQuestions,omitempty"`
	AddedIndexes       []IndexChange      `json:"addedIndexes,omitempty"`
	RemovedIndexes     []IndexChange      `json:"removedIndexes,omitempty"`
	AddedForeignKeys   []ForeignKeyChange `json:"addedForeignKeys,omitempty"`
	RemovedForeignKeys []ForeignKeyChange `json:"removedForeignKeys,omitempty"`
	ChangedPrimaryKeys []PrimaryKeyChange `json:"changedPrimaryKeys,omitempty"`
}

type IndexChange struct {
	Entity string `json:"entity"`
	Index  Index  `json:"index"`
}

type ForeignKeyChange struct {
	Entity     string     `json:"entity"`
	ForeignKey ForeignKey `json:"foreignKey"`
}

type PrimaryKeyChange struct {
	Entity string   `json:"entity"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

func DiffSchemas(previous, next Schema) SchemaDiff {
	previous = previous.Normalize()
	next = next.Normalize()
	var result SchemaDiff
	entityNames := unionKeys(previous.Entities, next.Entities)
	for _, entityName := range entityNames {
		before, hadBefore := previous.Entities[entityName]
		after, hasAfter := next.Entities[entityName]
		switch {
		case !hadBefore && hasAfter:
			result.CreatedEntities = append(result.CreatedEntities, after)
			continue
		case hadBefore && !hasAfter:
			result.RemovedEntities = append(result.RemovedEntities, before)
			continue
		}
		var added, removed []Column
		for _, name := range unionKeys(before.Columns, after.Columns) {
			oldColumn, oldFound := before.Columns[name]
			newColumn, newFound := after.Columns[name]
			switch {
			case !oldFound && newFound:
				column := newColumn
				result.AddedColumns = append(result.AddedColumns, ColumnChange{Entity: entityName, After: &column})
				added = append(added, newColumn)
			case oldFound && !newFound:
				column := oldColumn
				result.RemovedColumns = append(result.RemovedColumns, ColumnChange{Entity: entityName, Before: &column})
				removed = append(removed, oldColumn)
			case !sameDatabaseColumn(oldColumn, newColumn):
				oldCopy, newCopy := oldColumn, newColumn
				result.ChangedColumns = append(result.ChangedColumns, ColumnChange{Entity: entityName, Before: &oldCopy, After: &newCopy})
			}
		}
		if len(removed) > 0 {
			for _, column := range added {
				question := RenameQuestion{Entity: entityName, Added: column.Name}
				for _, candidate := range removed {
					question.Candidates = append(question.Candidates, RenameCandidate{
						From: candidate.Name, Score: columnSimilarity(candidate, column),
					})
				}
				sort.Slice(question.Candidates, func(i, j int) bool {
					if question.Candidates[i].Score != question.Candidates[j].Score {
						return question.Candidates[i].Score > question.Candidates[j].Score
					}
					return question.Candidates[i].From < question.Candidates[j].From
				})
				result.RenameQuestions = append(result.RenameQuestions, question)
			}
		}
		appendIndexDiff(&result, entityName, before.Indexes, after.Indexes)
		appendForeignKeyDiff(&result, entityName, before.ForeignKeys, after.ForeignKeys)
		beforePrimary, afterPrimary := primaryColumns(before), primaryColumns(after)
		if !sameStrings(beforePrimary, afterPrimary) {
			result.ChangedPrimaryKeys = append(result.ChangedPrimaryKeys, PrimaryKeyChange{Entity: entityName, Before: beforePrimary, After: afterPrimary})
		}
	}
	return result
}

func appendIndexDiff(result *SchemaDiff, entity string, before, after map[string]Index) {
	for _, name := range unionKeys(before, after) {
		oldIndex, oldFound := before[name]
		newIndex, newFound := after[name]
		if oldFound && newFound && sameIndex(oldIndex, newIndex) {
			continue
		}
		if oldFound {
			result.RemovedIndexes = append(result.RemovedIndexes, IndexChange{Entity: entity, Index: oldIndex})
		}
		if newFound {
			result.AddedIndexes = append(result.AddedIndexes, IndexChange{Entity: entity, Index: newIndex})
		}
	}
}

func appendForeignKeyDiff(result *SchemaDiff, entity string, before, after map[string]ForeignKey) {
	for _, name := range unionKeys(before, after) {
		oldFK, oldFound := before[name]
		newFK, newFound := after[name]
		if oldFound && newFound && sameForeignKey(oldFK, newFK) {
			continue
		}
		if oldFound {
			result.RemovedForeignKeys = append(result.RemovedForeignKeys, ForeignKeyChange{Entity: entity, ForeignKey: oldFK})
		}
		if newFound {
			result.AddedForeignKeys = append(result.AddedForeignKeys, ForeignKeyChange{Entity: entity, ForeignKey: newFK})
		}
	}
}

func sameForeignKey(left, right ForeignKey) bool {
	return left.Name == right.Name && left.Column == right.Column &&
		left.ReferenceEntity == right.ReferenceEntity && left.ReferenceColumn == right.ReferenceColumn &&
		left.OnDelete == right.OnDelete && left.OnUpdate == right.OnUpdate &&
		sameStrings(foreignKeyColumns(left), foreignKeyColumns(right)) &&
		sameStrings(foreignKeyReferenceColumns(left), foreignKeyReferenceColumns(right))
}

func foreignKeyColumns(foreignKey ForeignKey) []string {
	if len(foreignKey.Columns) != 0 {
		return foreignKey.Columns
	}
	if foreignKey.Column != "" {
		return []string{foreignKey.Column}
	}
	return nil
}

func foreignKeyReferenceColumns(foreignKey ForeignKey) []string {
	if len(foreignKey.ReferenceColumns) != 0 {
		return foreignKey.ReferenceColumns
	}
	if foreignKey.ReferenceColumn != "" {
		return []string{foreignKey.ReferenceColumn}
	}
	return nil
}

func (d SchemaDiff) Destructive() bool {
	return len(d.RemovedEntities) != 0 || len(d.RemovedColumns) != 0 ||
		len(d.RemovedIndexes) != 0 || len(d.RemovedForeignKeys) != 0 ||
		len(d.ChangedColumns) != 0 || len(d.ChangedPrimaryKeys) != 0
}

func (d SchemaDiff) DatabaseChanged() bool {
	return len(d.CreatedEntities) != 0 || len(d.RemovedEntities) != 0 ||
		len(d.AddedColumns) != 0 || len(d.RemovedColumns) != 0 ||
		len(d.ChangedColumns) != 0 || len(d.AddedIndexes) != 0 ||
		len(d.RemovedIndexes) != 0 || len(d.AddedForeignKeys) != 0 ||
		len(d.RemovedForeignKeys) != 0 || len(d.ChangedPrimaryKeys) != 0
}

func primaryColumns(entity Entity) []string {
	var result []string
	for name, column := range entity.Columns {
		if column.PrimaryKey {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func ResolveRenameQuestions(diff SchemaDiff, decisions []Decision) (SchemaDiff, []Decision, error) {
	byTarget := make(map[string]Decision)
	usedSources := make(map[string]struct{})
	for _, decision := range decisions {
		if decision.Kind != "columnRename" && decision.Kind != "columnCreate" {
			continue
		}
		key := decision.Entity + "\x00" + decision.To
		if _, duplicate := byTarget[key]; duplicate {
			return SchemaDiff{}, nil, fmt.Errorf("duplicate decision for %s.%s", decision.Entity, decision.To)
		}
		if decision.Kind == "columnRename" {
			sourceKey := decision.Entity + "\x00" + decision.From
			if _, duplicate := usedSources[sourceKey]; duplicate {
				return SchemaDiff{}, nil, fmt.Errorf("column %s.%s is used by more than one rename", decision.Entity, decision.From)
			}
			usedSources[sourceKey] = struct{}{}
		}
		byTarget[key] = decision
	}
	var normalized []Decision
	for _, question := range diff.RenameQuestions {
		decision, found := byTarget[question.Entity+"\x00"+question.Added]
		if !found {
			return SchemaDiff{}, nil, fmt.Errorf("unresolved column change for %s.%s", question.Entity, question.Added)
		}
		if decision.Kind == "columnRename" {
			valid := false
			for _, candidate := range question.Candidates {
				if candidate.From == decision.From {
					valid = true
					break
				}
			}
			if !valid {
				return SchemaDiff{}, nil, fmt.Errorf("%s.%s is not a rename candidate for %s", question.Entity, decision.From, question.Added)
			}
		}
		normalized = append(normalized, decision)
	}
	return diff, normalized, nil
}

func sameDatabaseColumn(left, right Column) bool {
	return left.Name == right.Name && left.SQLType == right.SQLType &&
		left.NotNull == right.NotNull &&
		left.AutoIncrement == right.AutoIncrement
}

func sameIndex(left, right Index) bool {
	if left.Name != right.Name || left.Unique != right.Unique || len(left.Columns) != len(right.Columns) {
		return false
	}
	for index := range left.Columns {
		if left.Columns[index] != right.Columns[index] {
			return false
		}
	}
	return true
}

func columnSimilarity(left, right Column) int {
	score := 0
	if left.SQLType == right.SQLType {
		score += 60
	} else if sqlTypeFamily(left.SQLType) == sqlTypeFamily(right.SQLType) {
		score += 25
	}
	if left.NotNull == right.NotNull {
		score += 15
	}
	if left.PrimaryKey == right.PrimaryKey {
		score += 15
	}
	if left.AutoIncrement == right.AutoIncrement {
		score += 5
	}
	return score
}

func sqlTypeFamily(value string) string {
	value = strings.ToUpper(value)
	if index := strings.IndexByte(value, '('); index >= 0 {
		value = value[:index]
	}
	switch value {
	case "VARCHAR", "LONGTEXT", "TEXT":
		return "text"
	case "INT", "INTEGER", "BIGINT", "TINYINT", "DOUBLE", "FLOAT", "DECIMAL":
		return "number"
	case "DATE", "DATETIME", "TIMESTAMP":
		return "date"
	default:
		return value
	}
}

func unionKeys[V any](left, right map[string]V) []string {
	seen := make(map[string]struct{}, len(left)+len(right))
	for key := range left {
		seen[key] = struct{}{}
	}
	for key := range right {
		seen[key] = struct{}{}
	}
	result := make([]string, 0, len(seen))
	for key := range seen {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
