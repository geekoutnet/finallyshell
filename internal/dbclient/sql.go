package dbclient

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	mysql "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

type sqlClient struct {
	unsupported
	profile  Profile
	password redactor
	dial     Dialer
	base     *sql.DB
}

func openSQL(ctx context.Context, profile Profile, password string, dial Dialer) (Client, error) {
	client := &sqlClient{profile: profile, password: redactor(password), dial: dial}
	db, err := client.open(profile.defaultDatabase())
	if err != nil {
		return nil, ErrConnect
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %s", ErrConnect, client.password.text(err.Error()))
	}
	client.base = db
	return client, nil
}

func (c *sqlClient) Close() error { return c.base.Close() }

func (c *sqlClient) open(database string) (*sql.DB, error) {
	p := c.profile
	var db *sql.DB
	if p.Engine == MySQL {
		cfg := mysql.NewConfig()
		cfg.Net, cfg.Addr, cfg.DBName = "tcp", p.address(), database
		cfg.User, cfg.Passwd, cfg.TLS, cfg.DialFunc = p.User, string(c.password), p.tlsConfig(), c.dial
		cfg.MultiStatements = false
		cfg.Timeout = 8 * time.Second
		cfg.ParseTime = true
		cfg.CheckConnLiveness = false
		cfg.Logger = quietMySQL{}
		connector, err := mysql.NewConnector(cfg)
		if err != nil {
			return nil, err
		}
		db = sql.OpenDB(connector)
	} else {
		cfg, err := pgx.ParseConfig("host=127.0.0.1 port=5432 user=finallyshell dbname=postgres sslmode=disable sslcertmode=disable gssencmode=disable passfile='' servicefile='' connect_timeout=8 target_session_attrs=any")
		if err != nil {
			return nil, err
		}
		cfg.Host, cfg.Port, cfg.Database = p.Host, uint16(p.Port), database
		cfg.User, cfg.Password, cfg.TLSConfig = p.User, string(c.password), p.tlsConfig()
		cfg.Fallbacks, cfg.ValidateConnect, cfg.AfterConnect = nil, nil, nil
		cfg.RuntimeParams = map[string]string{"standard_conforming_strings": "on", "client_encoding": "UTF8"}
		cfg.DialFunc = pgconn.DialFunc(c.dial)
		cfg.LookupFunc = func(_ context.Context, _ string) ([]string, error) { return []string{p.Host}, nil }
		db = stdlib.OpenDB(*cfg)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(time.Minute)
	return db, nil
}

func (c *sqlClient) database(database string) (*sql.DB, func(), error) {
	if database == "" || database == c.profile.defaultDatabase() {
		return c.base, func() {}, nil
	}
	if c.password.contains(database) {
		return nil, nil, ErrSecret
	}
	db, err := c.open(database)
	if err != nil {
		return nil, nil, ErrConnect
	}
	return db, func() { db.Close() }, nil
}

type sqlQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type quietMySQL struct{}

func (quietMySQL) Print(...any) {}

func sqlRows(ctx context.Context, db sqlQueryer, limit int, statement string, args ...any) (Result, error) {
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return Result{}, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return Result{}, err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return Result{}, err
	}
	result := Result{Columns: columns}
	values, pointers := make([]any, len(columns)), make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	remaining := 16 << 20
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return result, err
		}
		row := make([]Cell, len(columns))
		for index, value := range values {
			kind := strings.ToUpper(types[index].DatabaseTypeName())
			binary := kind == "BYTEA" || kind == "BIT" || strings.Contains(kind, "BINARY") || strings.HasSuffix(kind, "BLOB") || kind == "GEOMETRY"
			row[index] = cell(value, binary)
			remaining -= len(row[index].Text) + 24
		}
		if len(result.Rows) >= limit || remaining < 0 {
			result.More = true
			break
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, rows.Close()
}

func (c *sqlClient) read(ctx context.Context, database string, limit int, statement string, args ...any) (Result, error) {
	db, closeDB, err := c.database(database)
	if err != nil {
		return Result{}, err
	}
	defer closeDB()
	result, err := sqlRows(ctx, db, limit, statement, args...)
	return c.password.result(result), c.password.err(err)
}

func (c *sqlClient) Browse(ctx context.Context, object Object, page Page) (Result, error) {
	query, err := TablePageSQL(c.profile.Engine, object, page)
	if err != nil {
		return Result{}, err
	}
	if c.password.contains(page.Where) {
		return Result{}, ErrSecret
	}
	db, closeDB, err := c.database(object.Database)
	if err != nil {
		return Result{}, err
	}
	defer closeDB()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Result{}, c.password.err(err)
	}
	defer tx.Rollback()
	result, err := sqlRows(ctx, tx, PageSize, query)
	if result.More && len(result.Rows) < PageSize && err == nil {
		result.More = false
		err = ErrResultSize
	}
	return c.password.result(result), c.password.err(err)
}

func (c *sqlClient) Run(ctx context.Context, batch Batch) ([]Result, error) {
	if c.password.contains(batch.Text) {
		return nil, ErrSecret
	}
	statements, err := SQLStatements(c.profile.Engine, batch.Text)
	if err != nil {
		return nil, err
	}
	for _, statement := range statements {
		if err := authorize(batch.ReadOnly, batch.Confirmed, statement.ReadOnly); err != nil {
			return nil, err
		}
	}
	db, closeDB, err := c.database(batch.Database)
	if err != nil {
		return nil, err
	}
	defer closeDB()
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, c.password.err(err)
	}
	defer conn.Close()
	results := make([]Result, 0, len(statements))
	remaining := 32 << 20
	for _, statement := range statements {
		start := time.Now()
		result, err := sqlStatement(ctx, conn, statement)
		result.Verb, result.Duration = statement.Verb, time.Since(start)
		results = append(results, c.password.result(result))
		if err != nil {
			return results, c.password.err(err)
		}
		for _, row := range result.Rows {
			for _, value := range row {
				remaining -= len(value.Text) + 24
			}
		}
		if remaining < 0 {
			return results, ErrResultSize
		}
	}
	return results, nil
}

func sqlStatement(ctx context.Context, conn *sql.Conn, statement SQLStatement) (Result, error) {
	if statement.ReadOnly {
		tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return Result{}, err
		}
		defer tx.Rollback()
		return sqlRows(ctx, tx, PageSize, statement.Text)
	}
	if statement.ReturnsRows {
		result, err := sqlRows(ctx, conn, PageSize, statement.Text)
		switch statement.Verb {
		case "INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE":
			if err == nil && !result.More {
				result.Affected, result.AffectedKnown = int64(len(result.Rows)), true
			}
		}
		return result, err
	}
	result, err := conn.ExecContext(ctx, statement.Text)
	if err != nil {
		return Result{}, err
	}
	affected, err := result.RowsAffected()
	return Result{Affected: affected, AffectedKnown: err == nil}, err
}
