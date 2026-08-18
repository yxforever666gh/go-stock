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
		if app.services.Group.AddGroup(req) {
			writeCommand(w, "添加成功")
			return
		}
		writeCommand(w, "添加失败")
	})
	mux.HandleFunc("DELETE /api/v1/groups/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		if app.services.Group.RemoveGroup(id) {
			writeCommand(w, "移除成功")
			return
		}
		writeCommand(w, "移除失败")
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
		writeJSON(w, http.StatusOK, commandResponse{OK: app.services.Group.UpdateGroupSort(id, req.Sort)})
	})
	mux.HandleFunc("POST /api/v1/groups/initialize-sort", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, commandResponse{OK: app.services.Group.InitializeGroupSort()})
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
		if app.services.Group.AddStockGroup(id, strings.TrimSpace(req.StockCode)) {
			writeCommand(w, "添加成功")
			return
		}
		writeCommand(w, "添加失败")
	})
	mux.HandleFunc("DELETE /api/v1/groups/{id}/stocks/{code}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := groupID(w, r)
		if !ok {
			return
		}
		if app.services.Group.RemoveStockGroup(r.PathValue("code"), r.URL.Query().Get("name"), id) {
			writeCommand(w, "移除成功")
			return
		}
		writeCommand(w, "移除失败")
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
