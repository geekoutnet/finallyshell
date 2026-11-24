package dbclient

import (
	"context"
	"fmt"
	"strings"
)

func (c *sqlClient) Namespaces(ctx context.Context) ([]Namespace, error) {
	query := `SELECT schema_name FROM information_schema.schemata ORDER BY schema_name`
	if c.profile.Engine == Postgres {
		query = `SELECT datname FROM pg_database WHERE datallowconn AND NOT datistemplate AND has_database_privilege(datname,'CONNECT') ORDER BY datname`
	}
	result, err := c.read(ctx, "", 10000, query)
	if err != nil {
		return nil, err
	}
	var out []Namespace
	for _, row := range result.Rows {
		if strings.Contains(row[0].Text, "•••") {
			continue
		}
		if c.profile.Engine == MySQL {
			out = append(out, Namespace{Schema: row[0].Text, Database: row[0].Text})
		} else {
			out = append(out, Namespace{Database: row[0].Text})
		}
	}
	return out, nil
}

func (c *sqlClient) Objects(ctx context.Context, namespace Namespace) ([]Object, error) {
	if c.profile.Engine == MySQL {
		result, err := c.read(ctx, "", 10000, `SELECT table_name, table_type FROM information_schema.tables WHERE table_schema = ? ORDER BY table_name`, namespace.Schema)
		if err != nil {
			return nil, err
		}
		return objectsFromRows(result, namespace), nil
	}
	result, err := c.read(ctx, namespace.Database, 10000, `SELECT n.nspname, c.relname, CASE WHEN c.relkind IN ('v','m') THEN 'VIEW' ELSE 'TABLE' END
FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE c.relkind IN ('r','p','v','m','f') AND n.nspname NOT IN ('information_schema','pg_catalog') AND n.nspname NOT LIKE 'pg_toast%' AND n.nspname NOT LIKE 'pg_temp_%'
AND has_schema_privilege(n.oid,'USAGE') AND (has_table_privilege(c.oid,'SELECT') OR has_any_column_privilege(c.oid,'SELECT')) ORDER BY n.nspname,c.relname`)
	if err != nil {
		return nil, err
	}
	return objectsFromRows(result, namespace), nil
}

func objectsFromRows(result Result, namespace Namespace) []Object {
	var objects []Object
	for _, row := range result.Rows {
		schema := namespace.Schema
		if len(row) == 3 {
			schema, row = row[0].Text, row[1:]
		}
		if strings.Contains(schema, "•••") || strings.Contains(row[0].Text, "•••") {
			continue
		}
		objects = append(objects, Object{Database: namespace.Database, Namespace: schema, Name: row[0].Text, View: strings.Contains(row[1].Text, "VIEW")})
	}
	return objects
}

func (c *sqlClient) Structure(ctx context.Context, object Object) (Structure, error) {
	if c.profile.Engine == MySQL {
		return c.mysqlStructure(ctx, object)
	}
	return c.postgresStructure(ctx, object)
}

func (c *sqlClient) mysqlStructure(ctx context.Context, object Object) (Structure, error) {
	var structure Structure
	table, err := qualified(MySQL, object)
	if err != nil {
		return structure, err
	}
	command := "SHOW CREATE TABLE "
	if object.View {
		command = "SHOW CREATE VIEW "
	}
	create, err := c.read(ctx, "", 1, command+table)
	if err != nil {
		return structure, err
	}
	if len(create.Rows) > 0 && len(create.Rows[0]) > 1 {
		structure.Create = create.Rows[0][1].Text
	}
	structure.Columns, err = c.read(ctx, "", 2000, `SELECT column_name AS Name, column_type AS Type, is_nullable AS Nullable, column_default AS DefaultValue, column_key AS ColumnKey, extra AS Extra FROM information_schema.columns WHERE table_schema=? AND table_name=? ORDER BY ordinal_position`, object.Namespace, object.Name)
	if err != nil || object.View {
		return structure, err
	}
	structure.Keys, err = c.read(ctx, "", 2000, `SELECT constraint_name AS Name, column_name AS ColumnName, referenced_table_schema AS RefSchema, referenced_table_name AS RefTable, referenced_column_name AS RefColumn FROM information_schema.key_column_usage WHERE table_schema=? AND table_name=? ORDER BY constraint_name,ordinal_position`, object.Namespace, object.Name)
	if err != nil {
		return structure, err
	}
	structure.Indexes, err = c.read(ctx, "", 2000, `SELECT index_name AS Name, column_name AS ColumnName, seq_in_index AS Position, non_unique AS NonUnique, index_type AS Type FROM information_schema.statistics WHERE table_schema=? AND table_name=? ORDER BY index_name,seq_in_index`, object.Namespace, object.Name)
	return structure, err
}

func (c *sqlClient) postgresStructure(ctx context.Context, object Object) (Structure, error) {
	structure := Structure{Reconstructed: true}
	table, err := qualified(Postgres, object)
	if err != nil {
		return structure, err
	}
	const source = ` FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid `
	const scope = ` WHERE n.nspname=$1 AND c.relname=$2 `
	read := func(statement string) (Result, error) {
		return c.read(ctx, object.Database, 2000, statement, object.Namespace, object.Name)
	}
	structure.Columns, err = read(`SELECT a.attname AS "Name", format_type(a.atttypid,a.atttypmod) AS "Type", NOT a.attnotnull AS "Nullable", pg_get_expr(d.adbin,d.adrelid) AS "Default", a.attidentity AS "Identity", a.attgenerated AS "Generated"` + source + ` JOIN pg_attribute a ON a.attrelid=c.oid LEFT JOIN pg_attrdef d ON d.adrelid=c.oid AND d.adnum=a.attnum ` + scope + ` AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum`)
	if err != nil {
		return structure, err
	}
	if object.View {
		view, err := read(`SELECT c.relkind::text,pg_get_viewdef(c.oid,true)` + source + scope)
		if err == nil && len(view.Rows) != 0 {
			kind := "VIEW"
			if view.Rows[0][0].Text == "m" {
				kind = "MATERIALIZED VIEW"
			}
			structure.Create = "CREATE " + kind + " " + table + " AS\n" + view.Rows[0][1].Text
		}
		return structure, err
	}
	structure.Keys, err = read(`SELECT k.conname AS "Name", pg_get_constraintdef(k.oid,true) AS "Definition"` + source + ` JOIN pg_constraint k ON k.conrelid=c.oid ` + scope + ` ORDER BY k.conname`)
	if err != nil {
		return structure, err
	}
	structure.Indexes, err = read(`SELECT idx.relname AS "Name", pg_get_indexdef(idx.oid) AS "Definition", EXISTS (SELECT 1 FROM pg_constraint k WHERE k.conindid=idx.oid AND k.conrelid=c.oid AND k.contype IN ('p','u','x')) AS "Constraint" ` + source + ` JOIN pg_index i ON i.indrelid=c.oid JOIN pg_class idx ON idx.oid=i.indexrelid ` + scope + ` ORDER BY idx.relname`)
	if err != nil {
		return structure, err
	}
	structure.Create = postgresDDL(table, structure)
	return structure, nil
}

func postgresDDL(table string, structure Structure) string {
	definitions := make([]string, 0, len(structure.Columns.Rows)+len(structure.Keys.Rows))
	for _, row := range structure.Columns.Rows {
		name, err := quoteIdentifier(Postgres, row[0].Text)
		if err != nil {
			continue
		}
		text := name + " " + row[1].Text
		switch {
		case row[4].Text == "a":
			text += " GENERATED ALWAYS AS IDENTITY"
		case row[4].Text == "d":
			text += " GENERATED BY DEFAULT AS IDENTITY"
		case row[5].Text == "s":
			text += " GENERATED ALWAYS AS (" + row[3].Text + ") STORED"
		case row[5].Text == "v":
			text += " GENERATED ALWAYS AS (" + row[3].Text + ") VIRTUAL"
		case !row[3].Null:
			text += " DEFAULT " + row[3].Text
		}
		if row[2].Text == "false" {
			text += " NOT NULL"
		}
		definitions = append(definitions, text)
	}
	for _, row := range structure.Keys.Rows {
		name, err := quoteIdentifier(Postgres, row[0].Text)
		if err == nil {
			definitions = append(definitions, "CONSTRAINT "+name+" "+row[1].Text)
		}
	}
	var result strings.Builder
	fmt.Fprintf(&result, "CREATE TABLE %s (\n  %s\n);", table, strings.Join(definitions, ",\n  "))
	for _, row := range structure.Indexes.Rows {
		if row[2].Text == "false" {
			fmt.Fprintf(&result, "\n%s;", row[1].Text)
		}
	}
	return result.String()
}
