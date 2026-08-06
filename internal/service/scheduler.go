package service

import (
	"context"
	"time"

	"go-stock/backend/models"
)

type SchedulerService struct {
	operations SchedulerOperations
}

func NewSchedulerService(operations SchedulerOperations) SchedulerService {
	return SchedulerService{operations: operations}
}

func (s SchedulerService) CreateTaskRun(ctx context.Context, run *models.CronTaskRun) error {
	return s.operations.CreateTaskRun(normalizeServiceContext(ctx), run)
}

func (s SchedulerService) UpdateTaskRun(ctx context.Context, run *models.CronTaskRun) error {
	return s.operations.UpdateTaskRun(normalizeServiceContext(ctx), run)
}

func (s SchedulerService) LatestAIResponseSince(ctx context.Context, stockName, question string, since time.Time) (models.AIResponseResult, error) {
	return s.operations.LatestAIResponseSince(normalizeServiceContext(ctx), stockName, question, since)
}

func (s SchedulerService) EarliestTaskRun(ctx context.Context, taskName string, from, to time.Time, statuses []string) (models.CronTaskRun, error) {
	return s.operations.EarliestTaskRun(normalizeServiceContext(ctx), taskName, from, to, statuses)
}

func normalizeServiceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
