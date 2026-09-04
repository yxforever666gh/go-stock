package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/research"
	"go-stock/backend/researchapp"
)

type researchRunRepository interface {
	HasRunningAnalysis(context.Context) (bool, error)
}

type researchRunRunner interface {
	Run(context.Context, research.AnalysisRequest) (research.AnalysisRun, error)
}

func executeResearchOnce(ctx context.Context, repository researchRunRepository, runner researchRunRunner, request research.AnalysisRequest) (research.AnalysisRun, error) {
	running, err := repository.HasRunningAnalysis(ctx)
	if err != nil {
		return research.AnalysisRun{}, fmt.Errorf("检查运行中分析失败: %w", err)
	}
	if running {
		return research.AnalysisRun{}, errors.New("已有 running 状态的 AI 分析，本次运行被拒绝")
	}
	return runner.Run(ctx, request)
}

func runResearch(args []string, g GlobalOptions, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.EqualFold(args[0], "help") || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, "用法:")
		fmt.Fprintln(stdout, "  go-stock-cli [--db-path PATH] research run-once [--json] [--timeout 0]")
		return nil
	}
	if !strings.EqualFold(args[0], "run-once") {
		return fmt.Errorf("未知 research 子命令: %s", args[0])
	}

	fs := flag.NewFlagSet("research run-once", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := g.JSON
	timeout := time.Duration(0)
	fs.BoolVar(&jsonOut, "json", jsonOut, "JSON 输出")
	fs.DurationVar(&timeout, "timeout", timeout, "可选整轮分析超时，0 表示无限等待")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if timeout < 0 {
		return errors.New("timeout 不能小于 0")
	}

	setting := data.GetSettingConfig()
	if setting == nil || setting.Settings == nil {
		return errors.New("AI 分析设置不存在")
	}
	selected, err := data.ResolveAIAnalysisConfig(setting)
	if err != nil {
		return err
	}
	dependencies, options, err := data.NewResearchDependencies(int(selected.ID), db.Dao, db.MinuteDao)
	if err != nil {
		return err
	}
	runtime, err := researchapp.NewRuntime(db.Dao, dependencies, options)
	if err != nil {
		return err
	}

	ctx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	run, runErr := executeResearchOnce(ctx, runtime.Repository, runtime.Runner, research.AnalysisRequest{
		ScheduledFor: time.Now(), AIConfigID: selected.ID,
		ProviderName: data.DisplayAIProviderName(selected), ModelName: selected.ModelName, Mode: research.AnalysisModeManual,
	})
	result := struct {
		RunID               string     `json:"runId"`
		Status              string     `json:"status"`
		ScheduledFor        time.Time  `json:"scheduledFor"`
		StartedAt           time.Time  `json:"startedAt"`
		CompletedAt         *time.Time `json:"completedAt,omitempty"`
		ProviderName        string     `json:"providerName"`
		ModelName           string     `json:"modelName"`
		RecommendationCount int        `json:"recommendationCount"`
		FailureReason       string     `json:"failureReason,omitempty"`
	}{
		RunID: run.RunID, Status: run.Status, ScheduledFor: run.ScheduledFor, StartedAt: run.StartedAt,
		CompletedAt: run.CompletedAt, ProviderName: run.ProviderName, ModelName: run.ModelName,
		RecommendationCount: run.RecommendationCount, FailureReason: run.FailureReason,
	}
	if jsonOut {
		body, marshalErr := marshalPrettyJSON(result)
		if marshalErr != nil {
			return marshalErr
		}
		_, _ = fmt.Fprintln(stdout, string(body))
	} else if run.RunID != "" {
		_, _ = fmt.Fprintf(stdout, "run=%s status=%s recommendations=%d\n", run.RunID, run.Status, run.RecommendationCount)
	}
	return runErr
}
