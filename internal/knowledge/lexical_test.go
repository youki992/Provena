package knowledge

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/chobits02/provena/internal/database"
	"go.uber.org/zap"
)

func TestLexicalRetrieverDoesNotRequireEmbedder(t *testing.T) {
	db, err := database.NewKnowledgeDB(filepath.Join(t.TempDir(), "knowledge.db"), zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO knowledge_base_items (id,category,title,file_path,content,created_at,updated_at) VALUES ('i1','API','ARL evidence gate','/tmp/i1.md','ARL results require evidence verification before promotion',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRetriever(db.DB, nil, &RetrievalConfig{Mode: "lexical", TopK: 3}, zap.NewNop())
	results, err := r.Search(context.Background(), &SearchRequest{Query: "evidence verification"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Item.ID != "i1" {
		t.Fatalf("unexpected lexical results: %+v", results)
	}
}
