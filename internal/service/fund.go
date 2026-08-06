package service

import "go-stock/backend/models"

type FundService struct {
	operations FundOperations
}

func NewFundService(operations FundOperations) FundService {
	return FundService{operations: operations}
}

func (s FundService) GetFundList(key string) []models.FundBasic {
	return s.operations.GetFundList(key)
}

func (s FundService) GetFollowedFund() []models.FollowedFund {
	return s.operations.GetFollowedFund()
}

func (s FundService) FollowFund(fundCode string) string {
	return s.operations.FollowFund(fundCode)
}

func (s FundService) UnFollowFund(fundCode string) string {
	return s.operations.UnFollowFund(fundCode)
}

func (s FundService) AllFund() {
	s.operations.AllFund()
}

func (s FundService) CrawlFundBasic(code string) (*models.FundBasic, error) {
	return s.operations.CrawlFundBasic(code)
}

func (s FundService) CrawlFundNetEstimatedUnit(code string) {
	s.operations.CrawlFundNetEstimatedUnit(code)
}

func (s FundService) CrawlFundNetUnitValue(code string) {
	s.operations.CrawlFundNetUnitValue(code)
}
