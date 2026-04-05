package service

import (
	"go-stock/backend/data"
	"go-stock/backend/db"
)

type GroupService struct{}

func NewGroupService() GroupService {
	return GroupService{}
}

func (s GroupService) AddGroup(group data.Group) bool {
	return data.NewStockGroupApi(db.Dao).AddGroup(group)
}

func (s GroupService) GetGroupList() []data.Group {
	return data.NewStockGroupApi(db.Dao).GetGroupList()
}

func (s GroupService) UpdateGroupSort(id int, newSort int) bool {
	return data.NewStockGroupApi(db.Dao).UpdateGroupSort(id, newSort)
}

func (s GroupService) InitializeGroupSort() bool {
	return data.NewStockGroupApi(db.Dao).InitializeGroupSort()
}

func (s GroupService) GetGroupStockList(groupID int) []data.GroupStock {
	return data.NewStockGroupApi(db.Dao).GetGroupStockByGroupId(groupID)
}

func (s GroupService) AddStockGroup(groupID int, stockCode string) bool {
	return data.NewStockGroupApi(db.Dao).AddStockGroup(groupID, stockCode)
}

func (s GroupService) RemoveStockGroup(code, name string, groupID int) bool {
	return data.NewStockGroupApi(db.Dao).RemoveStockGroup(code, name, groupID)
}

func (s GroupService) RemoveGroup(groupID int) bool {
	return data.NewStockGroupApi(db.Dao).RemoveGroup(groupID)
}
