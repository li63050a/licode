package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExportImportRoundtrip(t *testing.T) {
	t.Setenv("LICODE_HOME", filepath.Join(t.TempDir(), ".licode"))
	os.MkdirAll(filepath.Join(os.Getenv("LICODE_HOME"), "sessions"), 0o755)
	os.WriteFile(filepath.Join(os.Getenv("LICODE_HOME"), "config.json"), []byte(`{"provider":"openai"}`), 0o600)
	data, err := Export()
	if err != nil { t.Fatal(err) }
	if len(data) == 0 { t.Fatal("empty export") }
	dest := t.TempDir()
	if err := Import(data, dest); err != nil { t.Fatal(err) }
	got, err := os.ReadFile(filepath.Join(dest, "config.json"))
	if err != nil { t.Fatal(err) }
	if string(got) != `{"provider":"openai"}` { t.Fatalf("mismatch: %s", got) }
}
