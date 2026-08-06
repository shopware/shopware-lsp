package entityschema

import (
	"fmt"
	"sort"
	"strings"
)

func MigrationStatements(previous, next Schema, decisions []Decision) ([]string, SchemaDiff, error) {
	previous = previous.Normalize()
	next = next.Normalize()
	diff := DiffSchemas(previous, next)
	if len(diff.RenameQuestions) != 0 {
		if _, _, err := ResolveRenameQuestions(diff, decisions); err != nil {
			return nil, diff, err
		}
	}
	decisionByTarget := make(map[string]Decision)
	for _, decision := range decisions {
		decisionByTarget[decision.Entity+"\x00"+decision.To] = decision
	}
	renamedFrom := make(map[string]struct{})
	renamedTo := make(map[string]struct{})
	var statements []string

	for _, change := range diff.RemovedForeignKeys {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s DROP FOREIGN KEY %s;", sqlIdent(change.Entity), sqlIdent(change.ForeignKey.Name)))
	}
	for _, change := range diff.RemovedIndexes {
		statements = append(statements, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", sqlIdent(change.Entity), sqlIdent(change.Index.Name)))
	}
	for _, change := range diff.ChangedPrimaryKeys {
		if len(change.Before) != 0 {
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s DROP PRIMARY KEY;", sqlIdent(change.Entity)))
		}
	}
	for _, entity := range diff.CreatedEntities {
		statements = append(statements, createTableSQL(entity))
	}
	for _, entity := range diff.RemovedEntities {
		statements = append(statements, fmt.Sprintf("DROP TABLE IF EXISTS %s;", sqlIdent(entity.Name)))
	}
	for _, change := range diff.AddedColumns {
		if change.After == nil {
			continue
		}
		decision := decisionByTarget[change.Entity+"\x00"+change.After.Name]
		if decision.Kind != "columnRename" {
			continue
		}
		beforeEntity := previous.Entities[change.Entity]
		oldColumn, found := beforeEntity.Columns[decision.From]
		if !found {
			return nil, diff, fmt.Errorf("rename source %s.%s does not exist", change.Entity, decision.From)
		}
		after := *change.After
		if oldColumn.AutoIncrement {
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", sqlIdent(change.Entity), sqlIdent(oldColumn.Name)))
		}
		if isJSONKind(oldColumn.Kind) {
			statements = append(statements, dropJSONConstraintSQL(change.Entity, oldColumn.Name))
		}
		if !oldColumn.NotNull && after.NotNull && !after.AutoIncrement {
			if !validBackfillExpression(after.BackfillSQL) {
				return nil, diff, fmt.Errorf("rename to NOT NULL column %s.%s requires a valid backfill expression", change.Entity, after.Name)
			}
			nullable := after
			nullable.NotNull = false
			statements = append(statements,
				fmt.Sprintf("ALTER TABLE %s CHANGE COLUMN %s %s;", sqlIdent(change.Entity), sqlIdent(decision.From), columnSQL(nullable)),
				backfillSQL(change.Entity, after),
				fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s;", sqlIdent(change.Entity), columnSQL(after)),
			)
		} else {
			statements = append(statements, fmt.Sprintf(
				"ALTER TABLE %s CHANGE COLUMN %s %s;",
				sqlIdent(change.Entity), sqlIdent(decision.From), columnSQL(after),
			))
		}
		if isJSONKind(after.Kind) {
			statements = append(statements, jsonConstraintSQL(change.Entity, after.Name))
		}
		renamedFrom[change.Entity+"\x00"+decision.From] = struct{}{}
		renamedTo[change.Entity+"\x00"+change.After.Name] = struct{}{}
	}
	for _, change := range diff.ChangedColumns {
		if change.Before == nil || change.After == nil {
			continue
		}
		beforeJSON := isJSONKind(change.Before.Kind)
		afterJSON := isJSONKind(change.After.Kind)
		if change.Before.AutoIncrement && !change.After.AutoIncrement {
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", sqlIdent(change.Entity), sqlIdent(change.Before.Name)))
		}
		if beforeJSON && !afterJSON {
			statements = append(statements, dropJSONConstraintSQL(change.Entity, change.Before.Name))
		}
		if !change.Before.NotNull && change.After.NotNull && !change.After.AutoIncrement {
			if !validBackfillExpression(change.After.BackfillSQL) {
				return nil, diff, fmt.Errorf("NOT NULL change for %s.%s requires a valid backfill expression", change.Entity, change.After.Name)
			}
			statements = append(statements, backfillSQL(change.Entity, *change.After))
		}
		statements = append(statements, fmt.Sprintf(
			"ALTER TABLE %s MODIFY COLUMN %s;",
			sqlIdent(change.Entity), columnSQL(*change.After),
		))
		if !beforeJSON && afterJSON {
			statements = append(statements, jsonConstraintSQL(change.Entity, change.After.Name))
		}
	}
	for _, change := range diff.AddedColumns {
		if change.After == nil {
			continue
		}
		if _, renamed := renamedTo[change.Entity+"\x00"+change.After.Name]; renamed {
			continue
		}
		after := *change.After
		if after.NotNull && !after.AutoIncrement {
			if !validBackfillExpression(after.BackfillSQL) {
				return nil, diff, fmt.Errorf("adding NOT NULL column %s.%s requires a valid backfill expression", change.Entity, after.Name)
			}
			nullable := after
			nullable.NotNull = false
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", sqlIdent(change.Entity), columnSQL(nullable)))
			if isJSONKind(after.Kind) {
				statements = append(statements, jsonConstraintSQL(change.Entity, after.Name))
			}
			statements = append(statements, backfillSQL(change.Entity, after), fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s;", sqlIdent(change.Entity), columnSQL(after)))
			continue
		}
		statements = append(statements, fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s;",
			sqlIdent(change.Entity), columnSQL(after),
		))
		if isJSONKind(change.After.Kind) {
			statements = append(statements, jsonConstraintSQL(change.Entity, change.After.Name))
		}
	}
	for _, change := range diff.RemovedColumns {
		if change.Before == nil {
			continue
		}
		if _, renamed := renamedFrom[change.Entity+"\x00"+change.Before.Name]; renamed {
			continue
		}
		if isJSONKind(change.Before.Kind) {
			statements = append(statements, dropJSONConstraintSQL(change.Entity, change.Before.Name))
		}
		statements = append(statements, fmt.Sprintf(
			"ALTER TABLE %s DROP COLUMN %s;",
			sqlIdent(change.Entity), sqlIdent(change.Before.Name),
		))
	}
	for _, change := range diff.ChangedPrimaryKeys {
		if len(change.After) != 0 {
			statements = append(statements, fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s);", sqlIdent(change.Entity), sqlColumns(change.After)))
		}
	}
	for _, change := range diff.AddedIndexes {
		statements = append(statements, addIndexSQL(change.Entity, change.Index))
	}
	for _, change := range diff.AddedForeignKeys {
		statements = append(statements, addForeignKeySQL(change.Entity, change.ForeignKey))
	}
	return statements, diff, nil
}

func backfillSQL(entity string, column Column) string {
	return fmt.Sprintf("UPDATE %s SET %s = %s WHERE %s IS NULL;", sqlIdent(entity), sqlIdent(column.Name), strings.TrimSpace(column.BackfillSQL), sqlIdent(column.Name))
}

func createTableSQL(entity Entity) string {
	var definitions []string
	columnNames := make([]string, 0, len(entity.Columns))
	for name := range entity.Columns {
		columnNames = append(columnNames, name)
	}
	sort.Strings(columnNames)
	var primary []string
	for _, name := range columnNames {
		column := entity.Columns[name]
		definitions = append(definitions, "    "+columnSQL(column))
		if column.PrimaryKey {
			primary = append(primary, sqlIdent(column.Name))
		}
		if isJSONKind(column.Kind) {
			definitions = append(definitions, "    "+jsonConstraintDefinition(entity.Name, column.Name))
		}
	}
	if len(primary) > 0 {
		definitions = append(definitions, "    PRIMARY KEY ("+strings.Join(primary, ", ")+")")
	}
	indexNames := make([]string, 0, len(entity.Indexes))
	for name := range entity.Indexes {
		indexNames = append(indexNames, name)
	}
	sort.Strings(indexNames)
	for _, name := range indexNames {
		index := entity.Indexes[name]
		prefix := "KEY"
		if index.Unique {
			prefix = "UNIQUE KEY"
		}
		definitions = append(definitions, fmt.Sprintf(
			"    %s %s (%s)", prefix, sqlIdent(index.Name), sqlColumns(index.Columns),
		))
	}
	fkNames := make([]string, 0, len(entity.ForeignKeys))
	for name := range entity.ForeignKeys {
		fkNames = append(fkNames, name)
	}
	sort.Strings(fkNames)
	for _, name := range fkNames {
		definitions = append(definitions, "    "+foreignKeyDefinition(entity.ForeignKeys[name]))
	}
	return fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s (\n%s\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;",
		sqlIdent(entity.Name), strings.Join(definitions, ",\n"),
	)
}

func columnSQL(column Column) string {
	nullability := "NULL"
	if column.NotNull {
		nullability = "NOT NULL"
	}
	suffix := ""
	if column.AutoIncrement {
		suffix = " AUTO_INCREMENT UNIQUE"
	}
	return fmt.Sprintf("%s %s %s%s", sqlIdent(column.Name), column.SQLType, nullability, suffix)
}

func addIndexSQL(entity string, index Index) string {
	kind := "INDEX"
	if index.Unique {
		kind = "UNIQUE INDEX"
	}
	return fmt.Sprintf("ALTER TABLE %s ADD %s %s (%s);", sqlIdent(entity), kind, sqlIdent(index.Name), sqlColumns(index.Columns))
}

func addForeignKeySQL(entity string, foreignKey ForeignKey) string {
	return fmt.Sprintf("ALTER TABLE %s ADD %s;", sqlIdent(entity), foreignKeyDefinition(foreignKey))
}

func foreignKeyDefinition(foreignKey ForeignKey) string {
	onDelete := strings.ToUpper(strings.ReplaceAll(string(foreignKey.OnDelete), "-", " "))
	if onDelete == "" {
		onDelete = "RESTRICT"
	}
	onUpdate := strings.ToUpper(foreignKey.OnUpdate)
	if onUpdate == "" {
		onUpdate = "CASCADE"
	}
	return fmt.Sprintf(
		"CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s) ON DELETE %s ON UPDATE %s",
		sqlIdent(foreignKey.Name), sqlColumns(foreignKeyColumns(foreignKey)),
		sqlIdent(foreignKey.ReferenceEntity), sqlColumns(foreignKeyReferenceColumns(foreignKey)),
		onDelete, onUpdate,
	)
}

func jsonConstraintSQL(entity, column string) string {
	return fmt.Sprintf("ALTER TABLE %s ADD %s;", sqlIdent(entity), jsonConstraintDefinition(entity, column))
}

func dropJSONConstraintSQL(entity, column string) string {
	return fmt.Sprintf("ALTER TABLE %s DROP CHECK %s;", sqlIdent(entity), sqlIdent(jsonConstraintName(entity, column)))
}

func jsonConstraintDefinition(entity, column string) string {
	return fmt.Sprintf("CONSTRAINT %s CHECK (JSON_VALID(%s))", sqlIdent(jsonConstraintName(entity, column)), sqlIdent(column))
}

func jsonConstraintName(entity, column string) string { return "json." + entity + "." + column }

func sqlColumns(columns []string) string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, sqlIdent(column))
	}
	return strings.Join(quoted, ", ")
}

func sqlIdent(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
