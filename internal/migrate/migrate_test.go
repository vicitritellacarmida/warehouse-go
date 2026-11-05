package migrate

import (
	"testing"
	"testing/fstest"

	"github.com/vicitritellacarmida/warehouse-go/migrations"
)

func TestSplitStatements(t *testing.T) {
	script := `-- header comment; with semicolon
CREATE TABLE a (x VARCHAR(10) DEFAULT 'a;b');
-- another
CREATE TRIGGER t BEFORE DELETE ON a
FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'no; delete';

`
	got := SplitStatements(script)
	if len(got) != 2 {
		t.Fatalf("got %d statements: %q", len(got), got)
	}
	if got[0] != "CREATE TABLE a (x VARCHAR(10) DEFAULT 'a;b')" {
		t.Errorf("first statement = %q", got[0])
	}
}

func TestLoadOrdersAndValidates(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_b.sql": {Data: []byte("SELECT 2;")},
		"0001_a.sql": {Data: []byte("SELECT 1;")},
		"README.txt": {Data: []byte("ignored")},
	}
	ms, err := Load(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Version != 1 || ms[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", ms)
	}

	if _, err := Load(fstest.MapFS{"x_bad.sql": {Data: []byte("")}}); err == nil {
		t.Error("expected error for bad name")
	}
	if _, err := Load(fstest.MapFS{"0001_a.sql": {}, "0001_b.sql": {}}); err == nil {
		t.Error("expected error for duplicate version")
	}
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	ms, err := Load(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) < 7 {
		t.Fatalf("expected at least 7 migrations, got %d", len(ms))
	}
	for _, m := range ms {
		if len(SplitStatements(m.SQL)) == 0 {
			t.Errorf("%s has no statements", m.Name)
		}
	}
}
