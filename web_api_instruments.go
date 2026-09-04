package main

import (
	"net/http"
	"strings"

	"go-stock/backend/data"
	"go-stock/backend/instruments"
	"go-stock/backend/marketdata"
)

func registerInstrumentEvidenceRoutes(mux *http.ServeMux, _ *App) {
	service := marketEvidenceServiceFactory()
	mux.HandleFunc("GET /api/v1/instruments/{code}/auction", func(w http.ResponseWriter, r *http.Request) {
		code, assetType, ok := evidenceInstrumentParams(w, r, data.AuctionData{Snapshots: []data.AuctionSnapshot{}})
		if !ok {
			return
		}
		date, ok := optionalEvidenceDate(w, r, data.AuctionData{Code: code, AssetType: assetType, Snapshots: []data.AuctionSnapshot{}})
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, service.Auction(r.Context(), marketdata.ProviderRequest{Code: code, AssetType: assetType, Date: date}))
	})
	mux.HandleFunc("GET /api/v1/instruments/{code}/trades", func(w http.ResponseWriter, r *http.Request) {
		code, assetType, ok := evidenceInstrumentParams(w, r, data.TradesData{Items: []data.TradeTick{}})
		if !ok {
			return
		}
		date, ok := optionalEvidenceDate(w, r, data.TradesData{Code: code, AssetType: assetType, Items: []data.TradeTick{}})
		if !ok {
			return
		}
		limit, err := queryBoundedInt(r, "limit", 100, 1, 500)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.TradesData{Code: code, AssetType: assetType, Date: date, Items: []data.TradeTick{}}, "validation", "invalid_limit", err.Error()))
			return
		}
		cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
		if cursor != "" {
			if _, err := queryBoundedInt(r, "cursor", 0, 0, 1000000); err != nil {
				writeJSON(w, http.StatusBadRequest, badMarketEvidence(data.TradesData{Code: code, AssetType: assetType, Date: date, Items: []data.TradeTick{}}, "validation", "invalid_cursor", err.Error()))
				return
			}
		}
		writeJSON(w, http.StatusOK, service.Trades(r.Context(), marketdata.ProviderRequest{Code: code, AssetType: assetType, Date: date, Cursor: cursor, Limit: limit}))
	})
}

func evidenceInstrumentParams[T any](w http.ResponseWriter, r *http.Request, empty T) (string, string, bool) {
	assetType := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("assetType")))
	if assetType == "" {
		assetType = "stock"
	}
	if assetType != "stock" && assetType != "index" && assetType != "etf" {
		writeJSON(w, http.StatusBadRequest, badMarketEvidence(empty, "validation", "invalid_asset_type", "assetType 必须是 stock、index 或 etf"))
		return "", "", false
	}
	code, ok := instruments.NormalizeInstrumentID(r.PathValue("code"), assetType)
	if !ok {
		writeJSON(w, http.StatusBadRequest, badMarketEvidence(empty, "validation", "invalid_code", "code 与 assetType 不匹配；ETF 支持沪市 51/56/58、深市 15，指数支持 sh000xxx/sz399xxx"))
		return "", "", false
	}
	return code, assetType, true
}
