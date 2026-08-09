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
	builder := migrationSQLBuilder{
		previous:         previous,
		diff:             diff,
		decisionByTarget: make(map[string]Decision),
		renamedFrom:      make(map[string]struct{}),
		renamedTo:        make(map[string]struct{}),
	}
	for _, decision := range decisions {
		builder.decisionByTarget[decision.Entity+"\x00"+decision.To] = decision
	}
	statements, err := builder.build()
	if err != nil {
		return nil, diff, err
	}
	return statements, diff, nil
}

type migrationSQLBuilder struct {
	previous         Schema
	diff             SchemaDiff
	decisionByTarget map[string]Decision
	renamedFrom      map[string]struct{}
	renamedTo        map[string]struct{}
	statements       []string
}

func (b *migrationSQLBuilder) build() ([]string, error) {
	b.dropForeignKeysAndIndexes()
	b.dropPrimaryKeys()
	b.createAndDropEntities()
	if err := b.renameColumns(); err != nil {
		return nil, err
	}
	if err := b.changeColumns(); err != nil {
		return nil, err
	}
	if err := b.addColumns(); err != nil {
		return nil, err
	}
	b.removeColumns()
	b.addPrimaryKeysAndConstraints()
	return b.statements, nil
}

func (b *migrationSQLBuilder) dropForeignKeysAndIndexes() {
	for _, change := range b.diff.RemovedForeignKeys {
		b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s DROP FOREIGN KEY %s;", sqlIdent(change.Entity), sqlIdent(change.ForeignKey.Name)))
	}
	for _, change := range b.diff.RemovedIndexes {
		b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", sqlIdent(change.Entity), sqlIdent(change.Index.Name)))
	}
}

func (b *migrationSQLBuilder) dropPrimaryKeys() {
	for _, change := range b.diff.ChangedPrimaryKeys {
		if len(change.Before) != 0 {
			b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s DROP PRIMARY KEY;", sqlIdent(change.Entity)))
		}
	}
}

func (b *migrationSQLBuilder) createAndDropEntities() {
	for _, entity := range b.diff.CreatedEntities {
		b.statements = append(b.statements, createTableSQL(entity))
	}
	for _, entity := range b.diff.RemovedEntities {
		b.statements = append(b.statements, fmt.Sprintf("DROP TABLE IF EXISTS %s;", sqlIdent(entity.Name)))
	}
}

func (b *migrationSQLBuilder) renameColumns() error {
	for _, change := range b.diff.AddedColumns {
		if change.After == nil {
			continue
		}
		decision := b.decisionByTarget[change.Entity+"\x00"+change.After.Name]
		if decision.Kind != "columnRename" {
			continue
		}
		beforeEntity := b.previous.Entities[change.Entity]
		oldColumn, found := beforeEntity.Columns[decision.From]
		if !found {
			return fmt.Errorf("rename source %s.%s does not exist", change.Entity, decision.From)
		}
		after := *change.After
		if oldColumn.AutoIncrement {
			b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", sqlIdent(change.Entity), sqlIdent(oldColumn.Name)))
		}
		if isJSONKind(oldColumn.Kind) {
			b.statements = append(b.statements, dropJSONConstraintSQL(change.Entity, oldColumn.Name))
		}
		if !oldColumn.NotNull && after.NotNull && !after.AutoIncrement {
			if !validBackfillExpression(after.BackfillSQL) {
				return fmt.Errorf("rename to NOT NULL column %s.%s requires a valid backfill expression", change.Entity, after.Name)
			}
			nullable := after
			nullable.NotNull = false
			b.statements = append(b.statements,
				fmt.Sprintf("ALTER TABLE %s CHANGE COLUMN %s %s;", sqlIdent(change.Entity), sqlIdent(decision.From), columnSQL(nullable)),
				backfillSQL(change.Entity, after),
				fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s;", sqlIdent(change.Entity), columnSQL(after)),
			)
		} else {
			b.statements = append(b.statements, fmt.Sprintf(
				"ALTER TABLE %s CHANGE COLUMN %s %s;",
				sqlIdent(change.Entity), sqlIdent(decision.From), columnSQL(after),
			))
		}
		if isJSONKind(after.Kind) {
			b.statements = append(b.statements, jsonConstraintSQL(change.Entity, after.Name))
		}
		b.renamedFrom[change.Entity+"\x00"+decision.From] = struct{}{}
		b.renamedTo[change.Entity+"\x00"+change.After.Name] = struct{}{}
	}
	return nil
}

func (b *migrationSQLBuilder) changeColumns() error {
	for _, change := range b.diff.ChangedColumns {
		if change.Before == nil || change.After == nil {
			continue
		}
		beforeJSON := isJSONKind(change.Before.Kind)
		afterJSON := isJSONKind(change.After.Kind)
		if change.Before.AutoIncrement && !change.After.AutoIncrement {
			b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", sqlIdent(change.Entity), sqlIdent(change.Before.Name)))
		}
		if beforeJSON && !afterJSON {
			b.statements = append(b.statements, dropJSONConstraintSQL(change.Entity, change.Before.Name))
		}
		if !change.Before.NotNull && change.After.NotNull && !change.After.AutoIncrement {
			if !validBackfillExpression(change.After.BackfillSQL) {
				return fmt.Errorf("NOT NULL change for %s.%s requires a valid backfill expression", change.Entity, change.After.Name)
			}
			b.statements = append(b.statements, backfillSQL(change.Entity, *change.After))
		}
		b.statements = append(b.statements, fmt.Sprintf(
			"ALTER TABLE %s MODIFY COLUMN %s;",
			sqlIdent(change.Entity), columnSQL(*change.After),
		))
		if !beforeJSON && afterJSON {
			b.statements = append(b.statements, jsonConstraintSQL(change.Entity, change.After.Name))
		}
	}
	return nil
}

func (b *migrationSQLBuilder) addColumns() error {
	for _, change := range b.diff.AddedColumns {
		if change.After == nil {
			continue
		}
		if _, renamed := b.renamedTo[change.Entity+"\x00"+change.After.Name]; renamed {
			continue
		}
		after := *change.After
		if after.NotNull && !after.AutoIncrement {
			if !validBackfillExpression(after.BackfillSQL) {
				return fmt.Errorf("adding NOT NULL column %s.%s requires a valid backfill expression", change.Entity, after.Name)
			}
			nullable := after
			nullable.NotNull = false
			b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", sqlIdent(change.Entity), columnSQL(nullable)))
			if isJSONKind(after.Kind) {
				b.statements = append(b.statements, jsonConstraintSQL(change.Entity, after.Name))
			}
			b.statements = append(b.statements, backfillSQL(change.Entity, after), fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s;", sqlIdent(change.Entity), columnSQL(after)))
			continue
		}
		b.statements = append(b.statements, fmt.Sprintf(
			"ALTER TABLE %s ADD COLUMN %s;",
			sqlIdent(change.Entity), columnSQL(after),
		))
		if isJSONKind(change.After.Kind) {
			b.statements = append(b.statements, jsonConstraintSQL(change.Entity, change.After.Name))
		}
	}
	return nil
}

func (b *migrationSQLBuilder) removeColumns() {
	for _, change := range b.diff.RemovedColumns {
		if change.Before == nil {
			continue
		}
		if _, renamed := b.renamedFrom[change.Entity+"\x00"+change.Before.Name]; renamed {
			continue
		}
		if isJSONKind(change.Before.Kind) {
			b.statements = append(b.statements, dropJSONConstraintSQL(change.Entity, change.Before.Name))
		}
		b.statements = append(b.statements, fmt.Sprintf(
			"ALTER TABLE %s DROP COLUMN %s;",
			sqlIdent(change.Entity), sqlIdent(change.Before.Name),
		))
	}
}

func (b *migrationSQLBuilder) addPrimaryKeysAndConstraints() {
	for _, change := range b.diff.ChangedPrimaryKeys {
		if len(change.After) != 0 {
			b.statements = append(b.statements, fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s);", sqlIdent(change.Entity), sqlColumns(change.After)))
		}
	}
	for _, change := range b.diff.AddedIndexes {
		b.statements = append(b.statements, addIndexSQL(change.Entity, change.Index))
	}
	for _, change := range b.diff.AddedForeignKeys {
		b.statements = append(b.statements, addForeignKeySQL(change.Entity, change.ForeignKey))
	}
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
