package main

import "go-stock/backend/data"

func (a *App) AddGroup(group data.Group) string {
	ok := a.services.Group.AddGroup(group)
	if ok {
		return "添加成功"
	}
	return "添加失败"
}

func (a *App) GetGroupList() []data.Group {
	return a.services.Group.GetGroupList()
}

func (a *App) UpdateGroupSort(id int, newSort int) bool {
	return a.services.Group.UpdateGroupSort(id, newSort)
}

func (a *App) InitializeGroupSort() bool {
	return a.services.Group.InitializeGroupSort()
}

func (a *App) GetGroupStockList(groupId int) []data.GroupStock {
	return a.services.Group.GetGroupStockList(groupId)
}

func (a *App) AddStockGroup(groupId int, stockCode string) string {
	ok := a.services.Group.AddStockGroup(groupId, stockCode)
	if ok {
		return "添加成功"
	}
	return "添加失败"
}

func (a *App) RemoveStockGroup(code, name string, groupId int) string {
	ok := a.services.Group.RemoveStockGroup(code, name, groupId)
	if ok {
		return "移除成功"
	}
	return "移除失败"
}

func (a *App) RemoveGroup(groupId int) string {
	ok := a.services.Group.RemoveGroup(groupId)
	if ok {
		return "移除成功"
	}
	return "移除失败"
}
