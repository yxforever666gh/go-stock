package bootstrap

import (
	"context"
	"testing"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPersistSyncedTelegraphDeduplicatesAndPersistsTagsAtomically(t *testing.T) {
	database := openNewsCompatibilityTestDB(t)
	if err := database.AutoMigrate(&models.Telegraph{}, &models.Tags{}, &models.TelegraphTags{}); err != nil {
		t.Fatalf("migrate news tables: %v", err)
	}
	adapter := marketAdapter{main: database}
	news := &models.Telegraph{Title: "headline", Content: "body"}
	created, err := adapter.PersistSyncedTelegraph(context.Background(), news, []string{"subject", "rotating_light"})
	if err != nil || !created {
		t.Fatalf("first persist = created:%v err:%v, want created", created, err)
	}
	created, err = adapter.PersistSyncedTelegraph(context.Background(), &models.Telegraph{Title: "headline", Content: "duplicate"}, []string{"other"})
	if err != nil || created {
		t.Fatalf("duplicate persist = created:%v err:%v, want duplicate", created, err)
	}

	var telegraphs []models.Telegraph
	var tags []models.Tags
	var links []models.TelegraphTags
	if err := database.Find(&telegraphs).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Find(&tags).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.Find(&links).Error; err != nil {
		t.Fatal(err)
	}
	if len(telegraphs) != 1 || len(tags) != 1 || len(links) != 1 {
		t.Fatalf("persisted rows = telegraphs:%d tags:%d links:%d, want 1/1/1", len(telegraphs), len(tags), len(links))
	}
}

func TestPersistSyncedTelegraphRollsBackNewsWhenTagAssociationFails(t *testing.T) {
	database := openNewsCompatibilityTestDB(t)
	if err := database.AutoMigrate(&models.Telegraph{}, &models.Tags{}); err != nil {
		t.Fatalf("migrate partial news tables: %v", err)
	}
	adapter := marketAdapter{main: database}
	created, err := adapter.PersistSyncedTelegraph(context.Background(), &models.Telegraph{Title: "headline"}, []string{"subject"})
	if err == nil || created {
		t.Fatalf("partial persist = created:%v err:%v, want rollback error", created, err)
	}
	var count int64
	if err := database.Model(&models.Telegraph{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("telegraph rows after rollback = %d, want 0", count)
	}
}

func openNewsCompatibilityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	return database
}
