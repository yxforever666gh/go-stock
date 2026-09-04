package groups

import (
	"testing"
	"time"

	"go-stock/backend/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestServiceMaintainsGroupOrderAndMembership(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Group{}, &models.GroupStock{}); err != nil {
		t.Fatal(err)
	}
	service := NewService(database)

	if !service.AddGroup(models.Group{Name: "first", Sort: 1}) || !service.AddGroup(models.Group{Name: "inserted", Sort: 1}) {
		t.Fatal("add group failed")
	}
	groups := service.GetGroupList()
	if len(groups) != 2 || groups[0].Name != "inserted" || groups[0].Sort != 1 || groups[1].Sort != 2 {
		t.Fatalf("groups after insertion = %+v", groups)
	}
	if !service.UpdateGroupSort(int(groups[0].ID), 2) {
		t.Fatal("update group sort failed")
	}
	groups = service.GetGroupList()
	if groups[0].Name != "first" || groups[0].Sort != 1 || groups[1].Name != "inserted" || groups[1].Sort != 2 {
		t.Fatalf("groups after reorder = %+v", groups)
	}

	groupID := int(groups[0].ID)
	if !service.AddStockGroup(groupID, "sh600000") || !service.AddStockGroup(groupID, "sh600000") {
		t.Fatal("idempotent group membership failed")
	}
	stocks := service.GetGroupStockList(groupID)
	if len(stocks) != 1 || stocks[0].StockCode != "sh600000" || stocks[0].GroupInfo.Name != "first" {
		t.Fatalf("group stocks = %+v", stocks)
	}
	if !service.RemoveStockGroup("sh600000", "", groupID) || len(service.GetGroupStockList(groupID)) != 0 {
		t.Fatal("remove group membership failed")
	}
	if !service.RemoveGroup(groupID) || len(service.GetGroupList()) != 1 {
		t.Fatal("remove group failed")
	}
}

func TestInitializeGroupSortUsesCreationOrder(t *testing.T) {
	database, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrate(&models.Group{}); err != nil {
		t.Fatal(err)
	}
	rows := []models.Group{
		{Name: "later", Sort: 8, Model: gorm.Model{CreatedAt: time.Now().Add(time.Minute)}},
		{Name: "earlier", Sort: 9, Model: gorm.Model{CreatedAt: time.Now()}},
	}
	if err := database.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	service := NewService(database)
	if !service.InitializeGroupSort() {
		t.Fatal("initialize group sort failed")
	}
	groups := service.GetGroupList()
	if len(groups) != 2 || groups[0].Name != "earlier" || groups[0].Sort != 1 || groups[1].Sort != 2 {
		t.Fatalf("initialized groups = %+v", groups)
	}
}
