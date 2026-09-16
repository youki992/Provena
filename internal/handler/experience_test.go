package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/chobits02/provena/internal/database"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestExperienceHTTPRejectsManualVerifiedStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db, err := database.NewDB(filepath.Join(t.TempDir(), "experience-http.db"), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	entry, err := db.CreateExperience(&database.ExperienceEntry{Title: "candidate", Category: "test", Content: "evidence", EvidenceRefs: []string{"ref"}})
	if err != nil {
		t.Fatal(err)
	}
	h := NewExperienceHandler(db, t.TempDir(), zap.NewNop())
	r := gin.New()
	r.POST("/experience/:id/verify", h.Verify)
	req := httptest.NewRequest(http.MethodPost, "/experience/"+entry.ID+"/verify", bytes.NewBufferString(`{"status":"verified"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("manual verified status = %d, want 400: %s", w.Code, w.Body.String())
	}
}
