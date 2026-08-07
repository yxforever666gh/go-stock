package service

// MarketSummaryRouteLog is the delivery-facing portion of a phased summary
// route log. The full compatibility payload remains owned by its producer.
type MarketSummaryRouteLog struct {
	RunSlot              string
	IndicatorCandidateCt int
	IndicatorAIInputCt   int
	DiscoveryCandidateCt int
	VerifiedCandidateCt  int
	Notes                []string
}
