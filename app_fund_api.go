package main

import "go-stock/backend/models"

func (a *App) GetfundList(key string) []models.FundBasic {
	return a.services.Fund.GetFundList(key)
}

func (a *App) GetFollowedFund() []models.FollowedFund {
	return a.services.Fund.GetFollowedFund()
}

func (a *App) FollowFund(fundCode string) string {
	return a.services.Fund.FollowFund(fundCode)
}

func (a *App) UnFollowFund(fundCode string) string {
	return a.services.Fund.UnFollowFund(fundCode)
}
