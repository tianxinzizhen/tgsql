package load

import (
	"testing"
)

func TestExtractPkgPath(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// free func
		{"github.com/pkg.Func", "github.com/pkg"},
		{"github.com/pkg/subpkg.Func", "github.com/pkg/subpkg"},
		// pointer receiver method
		{"github.com/pkg.(*Type).Method", "github.com/pkg"},
		{"github.com/pkg/subpkg.(*Type).Method", "github.com/pkg/subpkg"},
		// value receiver method
		{"github.com/pkg.Type.Method", "github.com/pkg"},
		{"github.com/pkg/subpkg.Type.Method", "github.com/pkg/subpkg"},
		// edge: short func (no module path)
		{"main.main", "main"},
		// edge: single segment (没有任何 ".")
		{"myFunc", "myFunc"},
	}
	for _, c := range cases {
		got := extractPkgPath(c.input)
		if got != c.want {
			t.Errorf("extractPkgPath(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestExtractSQLBody(t *testing.T) {
	cases := []struct {
		input   string
		wantSQL string
		wantOK  bool
	}{
		{"//sql SELECT * FROM t", " SELECT * FROM t", true},
		{"/*sql SELECT * FROM t */", " SELECT * FROM t ", true},
		{"// just a comment", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := extractSQLBody(c.input)
		if ok != c.wantOK || got != c.wantSQL {
			t.Errorf("extractSQLBody(%q) = (%q, %v), want (%q, %v)", c.input, got, ok, c.wantSQL, c.wantOK)
		}
	}
}

func TestApplyOption(t *testing.T) {
	cases := []struct {
		name     string
		inputSQL string
		want     *SqlDataInfo
	}{
		{
			name:     "no option",
			inputSQL: "SELECT * FROM t WHERE id = {.id}",
			want:     &SqlDataInfo{Sql: "SELECT * FROM t WHERE id = {.id}"},
		},
		{
			name:     "not_prepare only",
			inputSQL: "?option{not_prepare:true} SELECT * FROM t WHERE id = {.id}",
			want:     &SqlDataInfo{Sql: "SELECT * FROM t WHERE id = {.id}", NotPrepare: true},
		},
		{
			name:     "multi option + whitespace",
			inputSQL: "?option{ not_prepare : true , batch_insert : true , name : MyName } INSERT INTO t VALUES(?)",
			want:     &SqlDataInfo{Sql: "INSERT INTO t VALUES(?)", NotPrepare: true, BatchInsert: true, Name: "MyName"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			si := &SqlDataInfo{Sql: c.inputSQL}
			if err := applyOption(si); err != nil {
				t.Fatal(err)
			}
			if si.Sql != c.want.Sql {
				t.Errorf("Sql = %q, want %q", si.Sql, c.want.Sql)
			}
			if si.NotPrepare != c.want.NotPrepare {
				t.Errorf("NotPrepare = %v, want %v", si.NotPrepare, c.want.NotPrepare)
			}
			if si.BatchInsert != c.want.BatchInsert {
				t.Errorf("BatchInsert = %v, want %v", si.BatchInsert, c.want.BatchInsert)
			}
			if si.Name != c.want.Name {
				t.Errorf("Name = %q, want %q", si.Name, c.want.Name)
			}
		})
	}
}

func TestLoadCommentBytes_Integration(t *testing.T) {
	src := []byte(`
package mypkg

import "context"
import "database/sql"

type Demo struct {
	//sql SELECT * FROM demo WHERE id = {.id}
	Get func(ctx context.Context, id int64) (*Demo, error)

	/*sql
	INSERT INTO demo (name) VALUES({.Name})
	*/
	Insert func(ctx context.Context, name string) (sql.Result, error)

	//sql?option{not_prepare:true,name:Custom} SELECT * FROM demo WHERE id = ?
	Alt func(ctx context.Context, id int64) (*Demo, error)
}
`)
	infos, err := loadCommentBytes("github.com/user/mypkg", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Fatalf("want 3 SqlDataInfo, got %d", len(infos))
	}

	// 基本字段
	if infos[0].TypeName != "github.com/user/mypkg.Demo" {
		t.Errorf("TypeName = %q", infos[0].TypeName)
	}
	if infos[0].Name != "Get" {
		t.Errorf("Name = %q", infos[0].Name)
	}
	if len(infos[0].Param) != 2 || infos[0].Param[0] != "ctx" || infos[0].Param[1] != "id" {
		t.Errorf("Param = %v", infos[0].Param)
	}

	// option 解析
	alt := infos[2]
	if !alt.NotPrepare {
		t.Errorf("Alt.NotPrepare should be true")
	}
	if alt.Name != "Custom" {
		t.Errorf("Alt.Name = %q, want Custom", alt.Name)
	}
	if alt.Sql != "SELECT * FROM demo WHERE id = ?" {
		t.Errorf("Alt.Sql = %q", alt.Sql)
	}

	// 测试同一 struct 内重复 name 错误
	badSrc := []byte(`
package mypkg
type Bad struct {
	//sql SELECT 1
	Foo func(ctx context.Context) error
	//sql SELECT 2
	Foo func(ctx context.Context) error
}
`)
	_, err = loadCommentBytes("pkg", badSrc)
	if err == nil {
		t.Fatal("want duplicate name error, got nil")
	}
}
