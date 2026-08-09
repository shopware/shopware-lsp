package entityschema

import (
	"fmt"
	"strings"
)

func ValidateMigration(previous, next Schema, diff SchemaDiff, decisions []Decision) []ValidationIssue {
	decisionByTarget := make(map[string]Decision)
	for _, decision := range decisions {
		decisionByTarget[decision.Entity+"\x00"+decision.To] = decision
	}
	var issues []ValidationIssue
	missing := func(entity string, column Column) {
		issues = append(issues, ValidationIssue{
			Code:     "entity.migration.backfill.required",
			Message:  fmt.Sprintf("Adding NOT NULL column %s.%s to existing rows requires a backfill SQL expression", entity, column.Name),
			Severity: "error",
		})
	}
	for _, change := range diff.AddedColumns {
		if change.After == nil || !change.After.NotNull || change.After.AutoIncrement || strings.TrimSpace(change.After.BackfillSQL) != "" {
			continue
		}
		if decision := decisionByTarget[change.Entity+"\x00"+change.After.Name]; decision.Kind == "columnRename" {
			if old, found := previous.Entities[change.Entity].Columns[decision.From]; found && old.NotNull {
				continue
			}
		}
		missing(change.Entity, *change.After)
	}
	for _, change := range diff.ChangedColumns {
		if change.Before == nil || change.After == nil || change.Before.NotNull || !change.After.NotNull || change.After.AutoIncrement || strings.TrimSpace(change.After.BackfillSQL) != "" {
			continue
		}
		missing(change.Entity, *change.After)
	}
	return issues
}

func supportedFieldKind(kind FieldKind) bool {
	switch kind {
	case FieldID, FieldString, FieldLongText, FieldInt, FieldFloat, FieldBool,
		FieldDate, FieldDateTime, FieldJSON, FieldList, FieldObject, FieldBlob,
		FieldAutoIncrement, FieldCreatedAt, FieldUpdatedAt, FieldVersion, FieldReferenceVersion,
		FieldManyToOne, FieldOneToOne, FieldOneToMany, FieldManyToMany:
		return true
	default:
		return false
	}
}

func validBackfillExpression(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.Contains(value, ";") && !strings.Contains(value, "--") &&
		!strings.Contains(value, "/*") && !strings.Contains(value, "*/") && !strings.ContainsRune(value, '\x00')
}

func ValidSpec(spec EntitySpec) error {
	issues := ValidateSpec(spec)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", issues[0].Code, issues[0].Message)
}
