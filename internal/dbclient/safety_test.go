package dbclient

import (
	"bytes"
	"strings"
	"testing"
)

func TestSQLStatements(t *testing.T) {
	for _, test := range []struct {
		name, query string
		engine      Engine
		verbs       string
		writes      bool
	}{
		{"literals", "-- heading\n SELECT 'a;b', 'it''s'; SHOW TABLES;", MySQL, "SELECT SHOW", false},
		{"postgres dollar quote", "SELECT $body$a;b$body$; EXPLAIN SELECT 1", Postgres, "SELECT EXPLAIN", false},
		{"postgres escape string", `SELECT E'it\';s'; SELECT 2`, Postgres, "SELECT SELECT", false},
		{"delete", "/* comment */ DELETE FROM sample WHERE id=1;", Postgres, "DELETE", true},
		{"explain analyze", "EXPLAIN (ANALYZE TRUE) DELETE FROM sample", Postgres, "EXPLAIN", true},
		{"cte", "WITH v AS (DELETE FROM sample RETURNING id) SELECT id FROM v", Postgres, "WITH", true},
		{"row lock", "SELECT id FROM sample FOR UPDATE", MySQL, "SELECT", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			statements, err := SQLStatements(test.engine, test.query)
			if err != nil {
				t.Fatal(err)
			}
			var verbs []string
			var writes bool
			for _, statement := range statements {
				verbs = append(verbs, statement.Verb)
				writes = writes || !statement.ReadOnly
			}
			if strings.Join(verbs, " ") != test.verbs || writes != test.writes {
				t.Fatalf("%v; writes=%v", verbs, writes)
			}
		})
	}
	for _, input := range []string{"SELECT 'unfinished", "/*!50000 DROP TABLE sample */", "/*M! DROP TABLE sample */", "ALTER USER sample IDENTIFIED BY 'example'", "SELECT id INTO OUTFILE '/tmp/sample' FROM sample", "SHOW CREATE USER sample"} {
		if _, err := SQLStatements(MySQL, input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestRedisStatements(t *testing.T) {
	statements, err := RedisStatements("GET 'a b'\nHSET example field \"a\\nb\"\nFLUSHDB")
	if err != nil || len(statements) != 3 || !statements[0].ReadOnly || statements[0].Args[1] != "a b" || statements[1].Args[3] != "a\nb" || statements[1].ReadOnly || statements[2].ReadOnly {
		t.Fatalf("%#v, %v", statements, err)
	}
	for _, input := range []string{"GET 'unfinished", "AUTH secret", "ACL SETUSER test >secret", "CONFIG SET requirepass secret", "HELLO 3 AUTH user secret", "KEYS *", "CLIENT REPLY OFF", "BLPOP queue 0"} {
		if _, err := RedisStatements(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func TestTablePageSQL(t *testing.T) {
	statement, err := TablePageSQL(Postgres, Object{Namespace: "sample", Name: `daily"report`}, Page{Index: 2, Sort: "id", Desc: true, Where: "id > 12"})
	if err != nil || statement != `SELECT * FROM "sample"."daily""report" WHERE (id > 12) ORDER BY "id" DESC LIMIT 201 OFFSET 400` {
		t.Fatalf("%q, %v", statement, err)
	}
	for _, where := range []string{"id = 2; DELETE FROM sample", "true) UNION SELECT (1", "1 = 1 --", "(SELECT 1) = 1 /*ignored*/"} {
		if _, err := TablePageSQL(MySQL, Object{Namespace: "test", Name: "sample"}, Page{Where: where}); err == nil {
			t.Errorf("accepted %q", where)
		}
	}
}

func TestExportResults(t *testing.T) {
	result := Result{Columns: []string{"title", "empty", "binary"}, Rows: [][]Cell{{{Text: "a,b"}, {Null: true}, {Text: "0x000a", Binary: true}}}}
	var output bytes.Buffer
	if err := Export(&output, result, "csv"); err != nil || output.String() != "title,empty,binary\n\"a,b\",,0x000a\n" {
		t.Fatalf("%q, %v", output.String(), err)
	}
	output.Reset()
	if err := Export(&output, result, "json"); err != nil || !strings.Contains(output.String(), `[["a,b",null,"0x000a"]]`) {
		t.Fatalf("%q, %v", output.String(), err)
	}
}
