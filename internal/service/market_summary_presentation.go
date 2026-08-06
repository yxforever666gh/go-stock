package service

// MarketSummaryRecommendationCountPolicy describes the bounded presentation
// output requested for a market summary. It is deliberately separate from the
// strategy decision and is safe to expose to delivery code.
type MarketSummaryRecommendationCountPolicy struct {
	MinimumOutput    int
	MaximumOutput    int
	ProductionTarget int
	RequestedMinimum int
	RequestedMaximum int
	Source           string
	Custom           bool
	Clamped          bool
}

// MarketSummaryReportPrepareStats records deterministic presentation cleanup.
type MarketSummaryReportPrepareStats struct {
	RowsSeen           int
	DuplicateRowsOmit  int
	OutputRowsOmit     int
	AnalysisOnlyRows   int
	RecommendationRows int
}

// MarketSummaryReportMergeStats describes deterministic report-table merging.
type MarketSummaryReportMergeStats struct {
	BaseTableFound               bool
	SupplementTableFound         bool
	MaximumOutput                int
	AcceptedCodeCount            int
	BaseRecommendationRows       int
	SupplementRecommendationRows int
	DuplicateRowsOmitted         int
	UnconfirmedRowsOmitted       int
	OutputRowsOmitted            int
	ReplacedCodes                []string
	AppendedCodes                []string
	VisibleCodes                 []string
	UnconfirmedCodes             []string
	MissingAcceptedCodes         []string
	OmittedByLimitCodes          []string
}
