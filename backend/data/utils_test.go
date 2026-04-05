package data

import (
	"github.com/duke-git/lancet/v2/slice"
	"go-stock/backend/logger"
	"os"
	"testing"
)

// TestRemoveNonPrintable tests the RemoveAllBlankChar function.
func TestRemoveNonPrintable(t *testing.T) {
	//tests := []struct {
	//	input    string
	//	expected string
	//}{
	//	{"新 希 望", "新希望"},
	//	{"", ""},
	//	{"Hello, World!", "Hello, World!"},
	//	{"\x00\x01\x02", ""},
	//	{"Hello\x00World", "HelloWorld"},
	//	{"\x1F\x20\x7E\x7F", " \x7E"},
	//}

	//for _, test := range tests {
	//	actual := RemoveAllBlankChar(test.input)
	//	if actual != test.expected {
	//		t.Errorf("RemoveAllBlankChar(%q) = %q; expected %q", test.input, actual, test.expected)
	//	}
	//}
	txt := "新 希 望"
	txt2 := RemoveAllBlankChar(txt)
	logger.SugaredLogger.Infof("RemoveAllBlankChar(%s)", txt2)
	logger.SugaredLogger.Infof("RemoveAllBlankChar(%s)", txt)

}

func TestConvertStockCodeToTushareCode(t *testing.T) {
	logger.SugaredLogger.Infof("ConvertStockCodeToTushareCode(%s)", ConvertStockCodeToTushareCode("sz000802"))
	logger.SugaredLogger.Infof("ConvertTushareCodeToStockCode(%s)", ConvertTushareCodeToStockCode("000802.SZ"))
}
func TestReplaceSensitiveWords(t *testing.T) {
	txt := "新 希 望习近平"
	txt2 := ReplaceSensitiveWords(txt)
	logger.SugaredLogger.Infof("ReplaceSensitiveWords(%s)", txt2)

	os.WriteFile("words.txt", []byte(slice.Join(SensitiveWords, "\n")), 0644)
}
