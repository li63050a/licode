package cmd

import (
	"encoding/json"
	"io"
	"net/http"

	"licode/internal/backup"
)

// handleExport 导出配置/会话/Skills 为 zip 下载。
func handleExport(w http.ResponseWriter, r *http.Request) {
	data, err := backup.Export()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="licode-backup.zip"`)
	w.Write(data)
}

// handleImport 从上传的 zip 恢复配置/会话/Skills。
func handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
	if err != nil {
		w.Write([]byte(`{"ok":false,"error":` + jsonErr(err.Error()) + `}`))
		return
	}
	if err := backup.Import(data, ""); err != nil {
		w.Write([]byte(`{"ok":false,"error":` + jsonErr(err.Error()) + `}`))
		return
	}
	w.Write([]byte(`{"ok":true}`))
}

func jsonErr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
