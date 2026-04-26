package db

import (
	"testing"
)

func TestWithSearchPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		schema  string
		want    string
		wantErr bool
	}{
		{
			name:   "empty schema returns url unchanged",
			url:    "postgres://user@localhost/db",
			schema: "",
			want:   "postgres://user@localhost/db",
		},
		{
			name:   "adds search_path",
			url:    "postgres://user@localhost/db",
			schema: "app",
			want:   "postgres://user@localhost/db?search_path=app",
		},
		{
			name:   "preserves existing query params",
			url:    "postgres://user@localhost/db?sslmode=disable",
			schema: "app",
			want:   "postgres://user@localhost/db?search_path=app&sslmode=disable",
		},
		{
			name:   "trims whitespace",
			url:    "postgres://user@localhost/db",
			schema: "  app  ",
			want:   "postgres://user@localhost/db?search_path=app",
		},
		{
			name:    "invalid url errors",
			url:     "://bad-url",
			schema:  "app",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := WithSearchPath(tt.url, tt.schema)
			if (err != nil) != tt.wantErr {
				t.Fatalf("WithSearchPath error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("WithSearchPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSearchPathFromURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		url   string
		want  string
		found bool
	}{
		{"has search_path", "postgres://localhost/db?search_path=app", "app", true},
		{"no search_path", "postgres://localhost/db", "", false},
		{"empty search_path", "postgres://localhost/db?search_path=", "", false},
		{"whitespace only", "postgres://localhost/db?search_path=  ", "", false},
		{"invalid url", "://bad", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, found := searchPathFromURL(tt.url)
			if found != tt.found {
				t.Fatalf("searchPathFromURL found = %v, want %v", found, tt.found)
			}
			if got != tt.want {
				t.Fatalf("searchPathFromURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuoteIdent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"users", `"users"`},
		{`user"name`, `"user""name"`},
		{"", `""`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := quoteIdent(tt.input); got != tt.want {
				t.Fatalf("quoteIdent(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestQuoteLiteral(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "'hello'"},
		{"it's", "'it''s'"},
		{"", "''"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := quoteLiteral(tt.input); got != tt.want {
				t.Fatalf("quoteLiteral(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSQLSlug(t *testing.T) {
	t.Parallel()
	tests := []struct {
		stmt string
		want string
	}{
		{"SELECT * FROM users", "select.users"},
		{"  select  id  from  public.users  ", "select.users"},
		{"SELECT foo FROM bar, baz", "select.bar"},
		{"INSERT INTO users (name) VALUES ($1)", "insert.users"},
		{"UPDATE users SET name = $1", "update.users"},
		{"DELETE FROM users WHERE id = $1", "delete.users"},
		{"WITH cte AS (SELECT * FROM orders) SELECT * FROM cte", "select.cte"},
		{"WITH a AS (SELECT 1), b AS (SELECT 2) INSERT INTO logs VALUES (1)", "insert.logs"},
		{"CREATE TABLE users (id INT)", "create"},
		{"", "unknown"},
		{"   ", "unknown"},
		{"ALTER TABLE users", "alter"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := sqlSlug(tt.stmt); got != tt.want {
				t.Fatalf("sqlSlug(%q) = %q, want %q", tt.stmt, got, tt.want)
			}
		})
	}
}

func TestTableAfterKeyword(t *testing.T) {
	t.Parallel()
	tests := []struct {
		s       string
		keyword string
		want    string
	}{
		{"* FROM users WHERE id = 1", "FROM", "users"},
		{"* FROM public.users", "FROM", "users"},
		{"users (name) VALUES ($1)", "INTO", "users"},
		{"* FROM   schema.table_name  WHERE", "FROM", "table_name"},
		{"no keyword here", "FROM", "no"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tableAfterKeyword(tt.s, tt.keyword); got != tt.want {
				t.Fatalf("tableAfterKeyword(%q, %q) = %q, want %q", tt.s, tt.keyword, got, tt.want)
			}
		})
	}
}

func TestFirstWord(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"users", "users"},
		{"public.users", "users"},
		{"  users  ", "users"},
		{"users WHERE id = 1", "users"},
		{"schema.table_name, other", "table_name"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := firstWord(tt.input); got != tt.want {
				t.Fatalf("firstWord(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
