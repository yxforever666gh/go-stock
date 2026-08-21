package main

import (
	"net/http"
	"strconv"
	"strings"

	"go-stock/backend/models"
)

type groupSortRequest struct {
	Sort int `json:"sort"`
}

type groupStockRequest struct {
	StockCode string `json:"stockCode"`
}

func registerGroupRoutes(mux *http.ServeMux, app *App) {
	mux.HandleFunc("GET /api/v1/groups", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, app.services.Group.GetGroupList())
	})
	mux.HandleFunc("POST /api/v1/groups", func(w http.ResponseWriter, r *http.Request) {
		var req models.Group
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeBooleanCommand(w, app.services.Group.AddGroup(req), "添加成功", "添加失败", http.StatusConflict)
	})
	mux.HandleFunc("DELETE /api/v1/groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		writeBooleanCommand(w, app.services.Group.RemoveGroup(id), "移除成功", "分组不存在或移除失败", http.StatusNotFound)
	})
	mux.HandleFunc("PUT /api/v1/groups/{id}/sort", func(w http.ResponseWriter, r *http.Request) {
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		var req groupSortRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		writeBooleanCommand(w, app.services.Group.UpdateGroupSort(id, req.Sort), "排序已更新", "分组不存在或排序失败", http.StatusNotFound)
	})
	mux.HandleFunc("POST /api/v1/groups/initialize-sort", func(w http.ResponseWriter, _ *http.Request) {
		writeBooleanCommand(w, app.services.Group.InitializeGroupSort(), "排序已初始化", "初始化排序失败", http.StatusInternalServerError)
	})
	mux.HandleFunc("POST /api/v1/groups/{id}/stocks", func(w http.ResponseWriter, r *http.Request) {
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		var req groupStockRequest
		if !decodeAPIRequest(w, r, &req) {
			return
		}
		if strings.TrimSpace(req.StockCode) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "stockCode is required"})
			return
		}
		writeBooleanCommand(w, app.services.Group.AddStockGroup(id, strings.TrimSpace(req.StockCode)), "添加成功", "股票或分组不存在，或已在分组中", http.StatusConflict)
	})
	mux.HandleFunc("DELETE /api/v1/groups/{id}/stocks/{code}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		writeBooleanCommand(w, app.services.Group.RemoveStockGroup(r.PathValue("code"), r.URL.Query().Get("name"), id), "移除成功", "分组股票不存在或移除失败", http.StatusNotFound)
	})
}

func groupID(w http.ResponseWriter, r *http.Request) (int, bool) {
	id, err := strconv.Atoi(strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid group id"})
		return 0, false
	}
	return id, true
}
