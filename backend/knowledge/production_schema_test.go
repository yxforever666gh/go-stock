package knowledge_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go-stock/backend/knowledge"
	"go-stock/internal/migrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestRepositoryWorksAgainstProductionSchema19(t *testing.T) {
	database, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:knowledge-schema19-%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = migrations.MigrateMain(database); err != nil {
		t.Fatal(err)
	}
	service := knowledge.NewService(knowledge.NewRepository(database), nil)
	document, err := service.CreateDocument(context.Background(), knowledge.CreateDocumentRequest{Title: "schema19 knowledge", Filename: "schema19.md", MimeType: "text/markdown", Data: []byte("银行 风险 线索"), UserID: "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.DecideVersion(context.Background(), document.Versions[0].VersionID, knowledge.VersionDecision{Decision: knowledge.StateApproved, Reason: "verified by user", ActorType: knowledge.ActorUser, ActorID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	hits, err := service.SearchApproved(context.Background(), "银行", 5)
	if err != nil || len(hits) != 1 || hits[0].VersionID != document.Versions[0].VersionID {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	cutoff := time.Now().Add(time.Hour)
	result, err := service.RetrieveForResearch(context.Background(), knowledge.ResearchRetrievalRequest{OwnerType: "research1", OwnerID: "run-schema19", Query: "银行 风险", CutoffAt: cutoff, Limit: 5, ExperimentalEnabled: true})
	if err != nil || len(result.Hits) != 1 || result.RetrievalRunID == "" {
		t.Fatalf("retrieval=%+v err=%v", result, err)
	}
}
