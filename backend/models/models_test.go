package models

import (
	"encoding/json"
	"github.com/duke-git/lancet/v2/strutil"
	"go-stock/backend/db"
	"go-stock/backend/logger"
	"os"
	"testing"
)

func TestNormalizeAIAPIProtocol(t *testing.T) {
	for input, want := range map[string]string{
		"":                   AIAPIProtocolChatCompletions,
		" CHAT_COMPLETIONS ": AIAPIProtocolChatCompletions,
		"OPENAI_RESPONSES":   AIAPIProtocolOpenAIResponses,
		"anthropic_messages": AIAPIProtocolAnthropicMessage,
		"unsupported":        AIAPIProtocolChatCompletions,
	} {
		if got := NormalizeAIAPIProtocol(input); got != want {
			t.Errorf("NormalizeAIAPIProtocol(%q) = %q, want %q", input, got, want)
		}
	}
}

// @Author spark
// @Date 2025/2/22 16:09
// @Desc
// -----------------------------------------------------------------------------------
type StockInfoHKResp struct {
	Code       int              `json:"code"`
	Status     string           `json:"status"`
	StockInfos *[]StockInfoData `json:"data"`
}

type StockInfoData struct {
	C string `json:"c"`
	N string `json:"n"`
	T string `json:"t"`
	E string `json:"e"`
}

func TestStockInfoHK(t *testing.T) {
	if os.Getenv("GO_STOCK_RUN_INTEGRATION") != "1" {
		t.Skip("skip integration test; set GO_STOCK_RUN_INTEGRATION=1 to enable")
	}
	db.Init("../../data/stock.db")
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Dao.AutoMigrate(&StockInfoHK{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	bs, _ := os.ReadFile("../../build/hk.json")
	v := &StockInfoHKResp{}
	err := json.Unmarshal(bs, v)
	if err != nil {
		return
	}
	hks := &[]StockInfoHK{}
	for i, data := range *v.StockInfos {
		logger.SugaredLogger.Infof("第%d条数据: %+v", i, data)
		hk := &StockInfoHK{
			Code:  strutil.PadStart(data.C, 5, "0") + ".HK",
			EName: data.N,
		}
		*hks = append(*hks, *hk)
	}
	db.Dao.Create(&hks)

}
