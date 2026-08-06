package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"go-stock/internal/bootstrap"
)

func runAI(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("ai", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		stockCode   string
		stockName   string
		question    string
		aiConfigID  int
		baseURL     string
		apiKey      string
		model       string
		maxTokens   int
		temperature float64
		timeout     int
		thinking    bool
		jsonOut     bool
	)

	fs.StringVar(&stockCode, "stock-code", "", "股票代码，例: sh600519")
	fs.StringVar(&stockName, "stock-name", "", "股票名称，例: 贵州茅台")
	fs.StringVar(&question, "question", "", "自定义问题")
	fs.IntVar(&aiConfigID, "ai-config-id", 0, "AI 配置 ID")
	fs.StringVar(&baseURL, "base-url", "", "模型服务地址")
	fs.StringVar(&apiKey, "api-key", "", "模型服务 API Key")
	fs.StringVar(&model, "model", "", "模型名称")
	fs.IntVar(&maxTokens, "max-tokens", 4096, "最大 tokens")
	fs.Float64Var(&temperature, "temperature", 0.7, "temperature")
	fs.IntVar(&timeout, "timeout", 300, "请求超时秒")
	fs.BoolVar(&thinking, "thinking", true, "开启 thinking 模式")
	fs.BoolVar(&jsonOut, "json", g.JSON, "输出 NDJSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stockCode = strings.TrimSpace(stockCode)
	stockName = strings.TrimSpace(stockName)
	if stockCode == "" || stockName == "" {
		return errors.New("请同时提供 --stock-code 与 --stock-name")
	}

	resolver, err := bootstrap.NewProductionCommandAIResolver()
	if err != nil {
		return err
	}
	openAI, err := ResolveAIForCommand(context.Background(), resolver, AIOptions{
		AIConfigID:  aiConfigID,
		BaseURL:     strings.TrimSpace(baseURL),
		APIKey:      strings.TrimSpace(apiKey),
		Model:       strings.TrimSpace(model),
		MaxTokens:   maxTokens,
		Temperature: temperature,
		Timeout:     timeout,
	})
	if err != nil {
		return err
	}

	stream := openAI.NewChatStreamLite(stockName, stockCode, strings.TrimSpace(question), thinking)
	hasErr := false
	for chunk := range stream {
		if jsonOut {
			body, err := marshalJSONLine(chunk)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(stdout, string(body))
			continue
		}

		code := asInt(chunk["code"], 1)
		content := strings.TrimSpace(fmt.Sprintf("%v", chunk["content"]))
		extraContent := strings.TrimSpace(fmt.Sprintf("%v", chunk["extraContent"]))
		if code == 0 {
			hasErr = true
			if content != "" && content != "<nil>" {
				_, _ = fmt.Fprintln(stderr, content)
			}
			continue
		}
		if extraContent != "" && extraContent != "<nil>" {
			_, _ = fmt.Fprintln(stderr, extraContent)
		}
		if content != "" && content != "<nil>" {
			_, _ = fmt.Fprint(stdout, content)
		}
	}
	if !jsonOut {
		_, _ = fmt.Fprintln(stdout)
	}
	if hasErr {
		return errors.New("AI 流式输出包含错误信息")
	}
	return nil
}
