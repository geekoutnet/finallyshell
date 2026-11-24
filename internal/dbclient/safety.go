package dbclient

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type SQLStatement struct {
	Text, Verb            string
	ReadOnly, ReturnsRows bool
}

type sqlToken struct {
	word  string
	depth int
}

func SQLStatements(engine Engine, script string) ([]SQLStatement, error) {
	var result []SQLStatement
	err := walkSQL(engine, script, false, func(text string, tokens []sqlToken) error {
		if len(tokens) == 0 {
			return nil
		}
		if len(result) >= 100 {
			return ErrInput
		}
		statement := SQLStatement{Text: strings.TrimSpace(text), Verb: tokens[0].word}
		if statement.Verb == "" {
			return ErrInput
		}
		words := make([]string, 0, len(tokens))
		for _, token := range tokens {
			if token.word != "" {
				words = append(words, token.word)
			}
		}
		switch statement.Verb {
		case "GRANT", "REVOKE", "SET", "RESET", "USE", "CONNECT", "COPY", "LOAD", "SOURCE", "DO", "EXECUTE", "PREPARE", "DEALLOCATE", "START", "BEGIN", "COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "LOCK", "UNLOCK":
			return ErrSecret
		case "CREATE", "ALTER", "DROP", "RENAME":
			if slices.Contains(words, "USER") || slices.Contains(words, "ROLE") || slices.Contains(words, "PASSWORD") || slices.Contains(words, "SERVER") || slices.Contains(words, "SUBSCRIPTION") || slices.Contains(words, "MAPPING") || slices.Contains(words, "IDENTIFIED") {
				return ErrSecret
			}
		case "SHOW":
			if slices.Contains(words, "USER") || slices.Contains(words, "PASSWORD") {
				return ErrSecret
			}
		}
		switch statement.Verb {
		case "SELECT", "SHOW", "DESCRIBE", "DESC", "EXPLAIN":
			statement.ReadOnly, statement.ReturnsRows = true, true
		case "VALUES", "TABLE":
			statement.ReturnsRows = true
		case "WITH":
			for _, token := range tokens[1:] {
				if token.depth == 0 && slices.Contains([]string{"SELECT", "VALUES", "TABLE", "RETURNING"}, token.word) {
					statement.ReturnsRows = true
				}
			}
		}
		for index, token := range tokens {
			switch token.word {
			case "OUTFILE", "DUMPFILE", "LOAD_FILE", "GET_LOCK", "RELEASE_LOCK", "RELEASE_ALL_LOCKS", "PG_READ_FILE", "PG_READ_BINARY_FILE", "LO_IMPORT", "LO_EXPORT", "PG_RELOAD_CONF", "PG_TERMINATE_BACKEND", "PG_CANCEL_BACKEND", "PG_ADVISORY_LOCK", "PG_ADVISORY_LOCK_SHARED", "PG_TRY_ADVISORY_LOCK", "PG_TRY_ADVISORY_LOCK_SHARED", "SET_CONFIG":
				return ErrSecret
			case "INTO", "UPDATE", "DELETE", "INSERT", "MERGE", "ANALYZE", "ANALYSE", "LOCK", "SHARE", "CALL":
				statement.ReadOnly = false
			case "RETURNING":
				if index > 0 && token.depth == 0 && slices.Contains([]string{"INSERT", "UPDATE", "DELETE", "MERGE", "REPLACE", "WITH"}, statement.Verb) {
					statement.ReturnsRows = true
				}
			}
		}
		result = append(result, statement)
		return nil
	})
	if err == nil && len(result) == 0 {
		err = ErrInput
	}
	return result, err
}

func walkSQL(engine Engine, script string, expression bool, emit func(string, []sqlToken) error) error {
	if len(script) > 1<<20 || strings.ContainsRune(script, 0) {
		return ErrInput
	}
	var tokens []sqlToken
	start, depth := 0, 0
	for index := 0; index < len(script); {
		rest := script[index:]
		char := rest[0]
		switch {
		case char <= ' ':
			index++
		case strings.HasPrefix(rest, "--") && (engine != MySQL || len(rest) == 2 || rest[2] <= ' '), engine == MySQL && char == '#':
			if expression {
				return ErrInput
			}
			end := strings.IndexAny(rest, "\n\r")
			if end < 0 {
				index = len(script)
			} else {
				index += end
			}
		case strings.HasPrefix(rest, "/*"):
			if expression || strings.HasPrefix(rest, "/*!") || strings.HasPrefix(strings.ToUpper(rest[:min(4, len(rest))]), "/*M!") {
				return ErrInput
			}
			end, err := sqlComment(rest, engine == Postgres)
			if err != nil {
				return err
			}
			index += end
		case char == '\'' || char == '"' || char == '`' && engine == MySQL:
			escape := engine == Postgres && char == '\'' && index > 0 && (script[index-1] == 'e' || script[index-1] == 'E') && (index == 1 || !sqlWord(script[index-2]))
			end, err := sqlQuote(rest, escape, engine == MySQL)
			if err != nil {
				return err
			}
			tokens = append(tokens, sqlToken{depth: depth})
			index += end
		case char == '$' && engine == Postgres && (index == 0 || !sqlWord(script[index-1])) && dollarTag(rest) != "":
			tag := dollarTag(rest)
			end := strings.Index(rest[len(tag):], tag)
			if end < 0 {
				return ErrInput
			}
			tokens = append(tokens, sqlToken{depth: depth})
			index += 2*len(tag) + end
		case char == '(':
			tokens = append(tokens, sqlToken{depth: depth})
			depth++
			index++
		case char == ')':
			depth--
			if depth < 0 {
				return ErrInput
			}
			index++
		case char == ';':
			if expression || depth != 0 {
				return ErrInput
			}
			if err := emit(script[start:index], tokens); err != nil {
				return err
			}
			index++
			start, tokens = index, nil
		case sqlWord(char):
			end := index + 1
			for end < len(script) && (sqlWord(script[end]) || script[end] == '$') {
				end++
			}
			tokens = append(tokens, sqlToken{strings.ToUpper(script[index:end]), depth})
			index = end
		default:
			tokens = append(tokens, sqlToken{depth: depth})
			index++
		}
	}
	if depth != 0 {
		return ErrInput
	}
	return emit(script[start:], tokens)
}

func sqlWord(char byte) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char >= 128
}

func dollarTag(text string) string {
	for index := 1; index < len(text); index++ {
		char := text[index]
		if char == '$' {
			return text[:index+1]
		}
		if !sqlWord(char) || index == 1 && char >= '0' && char <= '9' {
			break
		}
	}
	return ""
}

func sqlComment(text string, nested bool) (int, error) {
	depth := 1
	for index := 2; index+1 < len(text); index++ {
		switch text[index : index+2] {
		case "*/":
			depth--
			if depth == 0 {
				return index + 2, nil
			}
			index++
		case "/*":
			if nested {
				depth++
				index++
			}
		}
	}
	return 0, ErrInput
}

func sqlQuote(text string, escape, rejectBackslash bool) (int, error) {
	for index := 1; index < len(text); index++ {
		if text[index] == '\\' {
			if rejectBackslash {
				return 0, ErrInput
			}
			if escape {
				index++
				continue
			}
		}
		if text[index] == text[0] {
			if index+1 < len(text) && text[index+1] == text[0] {
				index++
				continue
			}
			return index + 1, nil
		}
	}
	return 0, ErrInput
}

func quoteIdentifier(engine Engine, value string) (string, error) {
	if value == "" || strings.ContainsRune(value, 0) {
		return "", ErrInput
	}
	quote := `"`
	if engine == MySQL {
		quote = "`"
	}
	return quote + strings.ReplaceAll(value, quote, quote+quote) + quote, nil
}

func qualified(engine Engine, object Object) (string, error) {
	namespace, err := quoteIdentifier(engine, object.Namespace)
	if err != nil {
		return "", err
	}
	name, err := quoteIdentifier(engine, object.Name)
	return namespace + "." + name, err
}

func TablePageSQL(engine Engine, object Object, page Page) (string, error) {
	if page.Index < 0 || page.Index > 1_000_000 || engine != MySQL && engine != Postgres {
		return "", ErrInput
	}
	table, err := qualified(engine, object)
	if err != nil {
		return "", err
	}
	statement := "SELECT * FROM " + table
	if where := strings.TrimSpace(page.Where); where != "" {
		err := walkSQL(engine, where, true, func(_ string, tokens []sqlToken) error {
			for _, token := range tokens {
				if slices.Contains([]string{"SELECT", "WITH", "UNION", "INTO", "FOR", "LOCK", "UPDATE", "DELETE", "INSERT", "OUTFILE", "RETURNING", "LOAD_FILE", "GET_LOCK", "RELEASE_LOCK", "PG_READ_FILE", "PG_READ_BINARY_FILE", "PG_TERMINATE_BACKEND", "PG_CANCEL_BACKEND", "SET_CONFIG"}, token.word) {
					return ErrInput
				}
			}
			return nil
		})
		if err != nil {
			return "", err
		}
		statement += " WHERE (" + where + ")"
	}
	if page.Sort != "" {
		key, err := quoteIdentifier(engine, page.Sort)
		if err != nil {
			return "", err
		}
		statement += " ORDER BY " + key
		if page.Desc {
			statement += " DESC"
		}
	}
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", statement, PageSize+1, page.Index*PageSize), nil
}

type RedisStatement struct {
	Args     []string
	ReadOnly bool
}

var redisRead = strings.Fields("PING ECHO DBSIZE TYPE EXISTS TTL PTTL EXPIRETIME PEXPIRETIME GET MGET GETRANGE STRLEN HGET HMGET HEXISTS HKEYS HLEN HSTRLEN HSCAN LRANGE LLEN LINDEX LPOS SSCAN SCARD SISMEMBER SMISMEMBER SRANDMEMBER ZSCAN ZRANGE ZREVRANGE ZSCORE ZMSCORE ZRANK ZREVRANK ZCOUNT ZCARD ZLEXCOUNT XRANGE XREVRANGE XLEN XINFO SCAN TIME INFO")

func RedisStatements(script string) ([]RedisStatement, error) {
	if len(script) > 1<<20 {
		return nil, ErrInput
	}
	var statements []RedisStatement
	for line := range strings.SplitSeq(script, "\n") {
		args, err := redisWords(strings.TrimSpace(line))
		if err != nil {
			return nil, err
		}
		if len(args) == 0 {
			continue
		}
		if len(statements) == 100 {
			return nil, ErrInput
		}
		args[0] = strings.ToUpper(args[0])
		if strings.IndexFunc(args[0], func(char rune) bool { return char < 'A' || char > 'Z' }) >= 0 {
			return nil, ErrInput
		}
		switch args[0] {
		case "AUTH", "ACL", "HELLO", "MONITOR", "CLIENT", "MIGRATE", "KEYS", "SYNC", "PSYNC", "REPLCONF", "REPLICAOF", "SLAVEOF", "SUBSCRIBE", "PSUBSCRIBE", "SSUBSCRIBE", "MULTI", "EXEC", "WATCH", "UNWATCH", "DISCARD", "RESET", "QUIT", "SELECT", "BLPOP", "BRPOP", "BRPOPLPUSH", "BLMOVE", "BLMPOP", "BZPOPMIN", "BZPOPMAX", "BZMPOP", "XREAD", "XREADGROUP", "WAIT", "WAITAOF", "EVAL", "EVALSHA", "EVALRO", "EVALSHARO", "SCRIPT", "FUNCTION", "FCALL", "FCALLRO", "MODULE":
			return nil, ErrSecret
		case "CONFIG":
			if len(args) < 2 || !slices.Contains([]string{"RESETSTAT", "HELP"}, strings.ToUpper(args[1])) {
				return nil, ErrSecret
			}
		}
		statements = append(statements, RedisStatement{Args: args, ReadOnly: slices.Contains(redisRead, args[0])})
	}
	if len(statements) == 0 {
		return nil, ErrInput
	}
	return statements, nil
}

func redisWords(line string) ([]string, error) {
	var args []string
	for line = strings.TrimLeft(line, " \t"); line != ""; line = strings.TrimLeft(line, " \t") {
		var value strings.Builder
		var quote byte
		if line[0] == '\'' || line[0] == '"' {
			quote, line = line[0], line[1:]
		}
		index := 0
		for ; index < len(line); index++ {
			char := line[index]
			if quote == 0 && (char == ' ' || char == '\t') || quote != 0 && char == quote {
				break
			}
			if quote == 0 && (char == '\'' || char == '"') {
				return nil, ErrInput
			}
			if quote != 0 && char == '\\' {
				index++
				if index == len(line) {
					return nil, ErrInput
				}
				char = line[index]
				switch char {
				case 'n':
					char = '\n'
				case 'r':
					char = '\r'
				case 't':
					char = '\t'
				case '\\', '\'', '"':
				default:
					return nil, ErrInput
				}
			}
			value.WriteByte(char)
		}
		if quote != 0 {
			if index == len(line) {
				return nil, ErrInput
			}
			index++
			if index < len(line) && line[index] != ' ' && line[index] != '\t' {
				return nil, ErrInput
			}
		}
		args = append(args, value.String())
		line = line[index:]
	}
	return args, nil
}

var ErrConfirmation = errors.New("database write requires confirmation")

func authorize(readOnly, confirmed, read bool) error {
	if read {
		return nil
	}
	if readOnly {
		return ErrReadOnly
	}
	if !confirmed {
		return ErrConfirmation
	}
	return nil
}
