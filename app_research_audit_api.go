package main

import (
	"context"
	"errors"
	"strings"

	"go-stock/backend/data"
	"go-stock/backend/db"
	"go-stock/backend/research"
	"go-stock/backend/researchaudit"
)

func newResearchAuditService() (*researchaudit.Service, error) {
	if db.Dao == nil {
		return nil, errors.New("database is not initialized")
	}
	return researchaudit.NewService(researchaudit.NewRepository(db.Dao)), nil
}

func (a *App) ensureAuditOwnerExists(ctx context.Context, ownerType, ownerID string) error {
	switch ownerType {
	case researchaudit.OwnerResearch1:
		_, err := a.getAIAnalysisReport(ctx, ownerID)
		return err
	case researchaudit.OwnerResearch2:
		_, err := a.getResearch2Run(ctx, ownerID)
		return err
	default:
		return researchaudit.ErrInvalidRequest
	}
}

func (a *App) getResearchAudit(ctx context.Context, ownerType, ownerID string) (researchaudit.AuditView, error) {
	ownerID = strings.TrimSpace(ownerID)
	if err := a.ensureAuditOwnerExists(ctx, ownerType, ownerID); err != nil {
		return researchaudit.AuditView{}, err
	}
	service, err := newResearchAuditService()
	if err != nil {
		return researchaudit.AuditView{}, err
	}
	return service.Audit(ctx, ownerType, ownerID)
}

func (a *App) exportResearchAudit(ctx context.Context, ownerType, ownerID string) ([]byte, error) {
	ownerID = strings.TrimSpace(ownerID)
	if err := a.ensureAuditOwnerExists(ctx, ownerType, ownerID); err != nil {
		return nil, err
	}
	service, err := newResearchAuditService()
	if err != nil {
		return nil, err
	}
	return service.Export(ctx, ownerType, ownerID)
}

type appReplayExecutor struct{ client *data.ResearchAIClient }

func (executor appReplayExecutor) CompleteReplay(ctx context.Context, call researchaudit.ReplayCall) (researchaudit.ReplayCallResult, error) {
	var attempts []research.ModelAttemptRecord
	result, err := executor.client.Complete(ctx, research.CompletionRequest{Phase: "audit_replay_" + call.Phase, Prompt: call.Prompt, OnAttempt: func(record research.ModelAttemptRecord) {
		attempts = append(attempts, record)
	}})
	view := researchaudit.ReplayCallResult{Content: result.Content, ModelName: result.Model, AttemptLog: attempts, ModelParameters: research.AuditModelParameters(attempts)}
	if len(attempts) > 0 {
		last := attempts[len(attempts)-1]
		view.ProviderName = last.ProviderName
		if view.ModelName == "" {
			view.ModelName = last.ModelName
		}
	}
	return view, err
}

func (a *App) createResearchReplay(ctx context.Context, request researchaudit.CreateReplayRequest) (researchaudit.ReplayView, error) {
	if err := a.ensureAuditOwnerExists(ctx, request.SourceOwnerType, strings.TrimSpace(request.SourceOwnerID)); err != nil {
		return researchaudit.ReplayView{}, err
	}
	service, err := newResearchAuditService()
	if err != nil {
		return researchaudit.ReplayView{}, err
	}
	replay, err := service.CreateReplay(ctx, request)
	if err != nil {
		return researchaudit.ReplayView{}, err
	}
	view := researchaudit.ReplayView{Replay: replay}
	a.goTask(func(taskCtx context.Context) {
		_, _ = service.ExecuteReplay(taskCtx, replay.ReplayID, appReplayExecutor{client: data.NewResearchReplayAIClient(replay.ModelConfigID)})
	})
	return view, nil
}

func (a *App) getResearchReplay(ctx context.Context, replayID string) (researchaudit.ReplayView, error) {
	service, err := newResearchAuditService()
	if err != nil {
		return researchaudit.ReplayView{}, err
	}
	return service.GetReplay(ctx, strings.TrimSpace(replayID))
}
