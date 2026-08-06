package service

import "go-stock/backend/models"

type GroupService struct {
	operations GroupOperations
}

func NewGroupService(operations GroupOperations) GroupService {
	return GroupService{operations: operations}
}

func (s GroupService) AddGroup(group models.Group) bool {
	return s.operations.AddGroup(group)
}

func (s GroupService) GetGroupList() []models.Group {
	return s.operations.GetGroupList()
}

func (s GroupService) UpdateGroupSort(id int, newSort int) bool {
	return s.operations.UpdateGroupSort(id, newSort)
}

func (s GroupService) InitializeGroupSort() bool {
	return s.operations.InitializeGroupSort()
}

func (s GroupService) GetGroupStockList(groupID int) []models.GroupStock {
	return s.operations.GetGroupStockList(groupID)
}

func (s GroupService) AddStockGroup(groupID int, stockCode string) bool {
	return s.operations.AddStockGroup(groupID, stockCode)
}

func (s GroupService) RemoveStockGroup(code, name string, groupID int) bool {
	return s.operations.RemoveStockGroup(code, name, groupID)
}

func (s GroupService) RemoveGroup(groupID int) bool {
	return s.operations.RemoveGroup(groupID)
}
