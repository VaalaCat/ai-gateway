package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const defaultMigrationReadBatch = 2000
const fallbackSQLiteVariableLimit = 999

func safeInsertBatchRows(sqliteVariableLimit, insertColumns int) int {
	if sqliteVariableLimit <= 0 || insertColumns <= 0 {
		return 1
	}
	rows := (sqliteVariableLimit * 9 / 10) / insertColumns
	if rows < 1 {
		return 1
	}
	return rows
}

func CreateInSafeBatches[T any](ctx context.Context, target *gorm.DB, values []T, conflict clause.OnConflict) error {
	_, err := CreateInSafeBatchesCount(ctx, target, values, conflict)
	return err
}

func CreateInSafeBatchesCount[T any](ctx context.Context, target *gorm.DB, values []T, conflict clause.OnConflict) (int64, error) {
	if len(values) == 0 {
		return 0, nil
	}
	columns, err := insertColumnCount[T](target)
	if err != nil {
		return 0, err
	}
	result := target.WithContext(ctx).Clauses(conflict).CreateInBatches(&values, safeInsertBatchRows(sqliteVariableLimit(target), columns))
	return result.RowsAffected, result.Error
}

func insertColumnCount[T any](db *gorm.DB) (int, error) {
	statement := &gorm.Statement{DB: db}
	if err := statement.Parse(new(T)); err != nil {
		return 0, fmt.Errorf("parse migration insert schema: %w", err)
	}
	columns := 0
	for _, field := range statement.Schema.Fields {
		if field.DBName != "" && field.Creatable {
			columns++
		}
	}
	if columns == 0 {
		return 0, fmt.Errorf("migration insert schema has no writable columns")
	}
	return columns, nil
}

func sqliteVariableLimit(db *gorm.DB) int {
	var options []string
	if err := db.Raw("PRAGMA compile_options").Scan(&options).Error; err != nil {
		return fallbackSQLiteVariableLimit
	}
	for _, option := range options {
		value, found := strings.CutPrefix(option, "MAX_VARIABLE_NUMBER=")
		if !found {
			continue
		}
		if limit, err := strconv.Atoi(value); err == nil && limit > 0 {
			return limit
		}
	}
	return fallbackSQLiteVariableLimit
}

type bootstrapTableCopier struct {
	name    string
	copyAll func(context.Context, *gorm.DB, *gorm.DB) (int64, error)
}

type bootstrapColumn struct {
	name          string
	nullable      bool
	nullableKnown bool
	hasDefault    bool
	primaryKey    bool
}

func bootstrapTableCopiers() []bootstrapTableCopier {
	return []bootstrapTableCopier{
		bootstrapUintCopier[models.User]("users"), bootstrapUintCopier[models.Token]("tokens"),
		bootstrapUintCopier[models.Channel]("channels"), bootstrapUintCopier[models.ModelConfig]("model_configs"),
		bootstrapUintCopier[models.Agent]("agents"), bootstrapUintCopier[models.EnrollmentToken]("enrollment_tokens"),
		bootstrapStringCopier[models.Setting]("settings", "key"), bootstrapUintCopier[models.AgentRoute]("agent_routes"),
		bootstrapUintCopier[models.RequestLimiter]("request_limiters"), bootstrapUintCopier[models.LimiterBinding]("limiter_bindings"),
		bootstrapUintCopier[models.TokenTemplate]("token_templates"), bootstrapUintCopier[models.UserGroup]("user_groups"),
		bootstrapUintCopier[models.OAuthProvider]("o_auth_providers"), bootstrapUintCopier[models.OAuthIdentity]("o_auth_identities"),
		bootstrapUintCopier[models.ModelRouting]("model_routings"), bootstrapUintCopier[models.PrivateChannel]("private_channels"),
		bootstrapUintCopier[models.PrivateChannelShare]("private_channel_shares"), bootstrapUintCopier[models.AdminScript]("admin_scripts"),
		bootstrapUintCopier[models.InviteCode]("invite_codes"), bootstrapUintCopier[models.InviteRedemption]("invite_redemptions"),
		bootstrapStringCopier[models.MasterSigningKey]("master_signing_keys", "key_id"),
	}
}

func bootstrapUintCopier[T any](name string) bootstrapTableCopier {
	return bootstrapCopier[T](name, "id")
}
func bootstrapStringCopier[T any](name, key string) bootstrapTableCopier {
	return bootstrapCopier[T](name, key)
}

func bootstrapCopier[T any](name, key string) bootstrapTableCopier {
	return bootstrapTableCopier{name: name, copyAll: func(ctx context.Context, source, target *gorm.DB) (int64, error) {
		exists, err := bootstrapSourceHasTable(ctx, source, name)
		if err != nil || !exists {
			return 0, err
		}
		sourceColumns, err := readBootstrapColumns(ctx, source, name)
		if err != nil {
			return 0, err
		}
		targetColumns, err := readBootstrapColumns(ctx, target, name)
		if err != nil {
			return 0, err
		}
		columns, err := sharedBootstrapColumns(sourceColumns, targetColumns, key)
		if err != nil {
			return 0, err
		}
		var copied int64
		var last any
		for {
			rows, err := readBootstrapRows(ctx, source, name, columns, key, last)
			if err != nil {
				return 0, err
			}
			if len(rows) == 0 {
				return copied, nil
			}
			if err := insertBootstrapRows(ctx, target, name, columns, rows); err != nil {
				return 0, err
			}
			last = rows[len(rows)-1][key]
			if last == nil {
				return 0, fmt.Errorf("bootstrap pagination key %s is null", key)
			}
			copied += int64(len(rows))
		}
	}}
}

func readBootstrapColumns(ctx context.Context, db *gorm.DB, table string) ([]bootstrapColumn, error) {
	types, err := db.WithContext(ctx).Migrator().ColumnTypes(table)
	if err != nil {
		return nil, fmt.Errorf("inspect bootstrap table schema: %w", err)
	}
	columns := make([]bootstrapColumn, 0, len(types))
	for _, columnType := range types {
		nullable, nullableKnown := columnType.Nullable()
		_, hasDefault := columnType.DefaultValue()
		primaryKey, _ := columnType.PrimaryKey()
		columns = append(columns, bootstrapColumn{
			name:          columnType.Name(),
			nullable:      nullable,
			nullableKnown: nullableKnown,
			hasDefault:    hasDefault,
			primaryKey:    primaryKey,
		})
	}
	return columns, nil
}

func sharedBootstrapColumns(source, target []bootstrapColumn, key string) ([]string, error) {
	sourceByName := make(map[string]bootstrapColumn, len(source))
	for _, column := range source {
		sourceByName[column.name] = column
	}
	if _, found := sourceByName[key]; !found {
		return nil, fmt.Errorf("pagination key %s is not shared", key)
	}
	targetByName := make(map[string]bootstrapColumn, len(target))
	for _, column := range target {
		targetByName[column.name] = column
	}
	if _, found := targetByName[key]; !found {
		return nil, fmt.Errorf("pagination key %s is not shared", key)
	}

	columns := make([]string, 0, len(target))
	for _, column := range target {
		if _, found := sourceByName[column.name]; found {
			columns = append(columns, column.name)
			continue
		}
		if column.primaryKey {
			return nil, fmt.Errorf("target column %s is a primary key and is not shared", column.name)
		}
		if !column.hasDefault && (!column.nullableKnown || !column.nullable) {
			return nil, fmt.Errorf("target column %s is not nullable and has no default", column.name)
		}
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("bootstrap tables have no shared columns")
	}
	return columns, nil
}

func readBootstrapRows(ctx context.Context, source *gorm.DB, table string, columns []string, key string, last any) ([]map[string]any, error) {
	query := source.WithContext(ctx).Table(table).Select(columns).
		Clauses(clause.Select{Columns: bootstrapClauseColumns(columns)}).
		Order(clause.OrderByColumn{Column: clause.Column{Name: key}}).
		Limit(defaultMigrationReadBatch)
	if last != nil {
		query = query.Where(clause.Gt{Column: clause.Column{Name: key}, Value: last})
	}
	var rows []map[string]any
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func bootstrapClauseColumns(columns []string) []clause.Column {
	result := make([]clause.Column, len(columns))
	for i, column := range columns {
		result[i] = clause.Column{Name: column}
	}
	return result
}

func insertBootstrapRows(ctx context.Context, target *gorm.DB, table string, columns []string, rows []map[string]any) error {
	result := target.WithContext(ctx).Table(table).Select(columns).
		Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(rows, safeInsertBatchRows(sqliteVariableLimit(target), len(columns)))
	return result.Error
}

func bootstrapSourceHasTable(ctx context.Context, source *gorm.DB, name string) (bool, error) {
	var count int64
	err := source.WithContext(ctx).Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&count).Error
	if err != nil {
		return false, fmt.Errorf("inspect source table: %w", err)
	}
	return count == 1, nil
}
