package service

import "go-stock/backend/data"

type FundService struct{}

func NewFundService() FundService {
	return FundService{}
}

func (s FundService) GetFundList(key string) []data.FundBasic {
	return data.NewFundApi().GetFundList(key)
}

func (s FundService) GetFollowedFund() []data.FollowedFund {
	return data.NewFundApi().GetFollowedFund()
}

func (s FundService) FollowFund(fundCode string) string {
	return data.NewFundApi().FollowFund(fundCode)
}

func (s FundService) UnFollowFund(fundCode string) string {
	return data.NewFundApi().UnFollowFund(fundCode)
}

func (s FundService) AllFund() {
	data.NewFundApi().AllFund()
}

func (s FundService) CrawlFundBasic(code string) (*data.FundBasic, error) {
	return data.NewFundApi().CrawlFundBasic(code)
}

func (s FundService) CrawlFundNetEstimatedUnit(code string) {
	data.NewFundApi().CrawlFundNetEstimatedUnit(code)
}

func (s FundService) CrawlFundNetUnitValue(code string) {
	data.NewFundApi().CrawlFundNetUnitValue(code)
}
