package data

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	defaultMarketSummaryRecommendationMinimum = 8
	defaultMarketSummaryRecommendationMaximum = 12
	marketSummaryRecommendationOutputLimit    = 12
)

var (
	marketSummaryRecommendationRangePattern  = regexp.MustCompile(`(?:推荐|筛选|选出|挑选|输出|给出)\s*(\d{1,3})\s*(?:-|~|～|至|到|–|—)\s*(\d{1,3})\s*(?:只(?:\s*(?:A\s*股|股票|标的|候选股))?|个\s*(?:A\s*股|股票|标的|候选股|股))`)
	marketSummaryRecommendationSinglePattern = regexp.MustCompile(`(?:推荐|筛选|选出|挑选|输出|给出)\s*(\d{1,3})\s*(?:只(?:\s*(?:A\s*股|股票|标的|候选股))?|个\s*(?:A\s*股|股票|标的|候选股|股))`)
)

// MarketSummaryRecommendationCountPolicy is the single source of truth for
// candidate-pool output size and the number of candidates that may enter the
// production/activation path.
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

func defaultMarketSummaryRecommendationCountPolicy() MarketSummaryRecommendationCountPolicy {
	return MarketSummaryRecommendationCountPolicy{
		MinimumOutput:    defaultMarketSummaryRecommendationMinimum,
		MaximumOutput:    defaultMarketSummaryRecommendationMaximum,
		ProductionTarget: marketSummaryMaxProductionCandidates,
		RequestedMinimum: defaultMarketSummaryRecommendationMinimum,
		RequestedMaximum: defaultMarketSummaryRecommendationMaximum,
		Source:           "default",
	}
}

// ResolveMarketSummaryRecommendationCountPolicy extracts only an explicitly
// requested stock count. The verb and the stock unit are both mandatory so
// text such as "未来3-5个交易日" is never treated as a recommendation count.
func ResolveMarketSummaryRecommendationCountPolicy(question string) MarketSummaryRecommendationCountPolicy {
	text := NormalizeMarketSummaryQuestion(question)
	if text == "" {
		return defaultMarketSummaryRecommendationCountPolicy()
	}
	if match := marketSummaryRecommendationRangePattern.FindStringSubmatch(text); len(match) == 3 {
		minimum, minimumErr := strconv.Atoi(match[1])
		maximum, maximumErr := strconv.Atoi(match[2])
		if minimumErr == nil && maximumErr == nil && minimum > 0 && maximum > 0 {
			if minimum > maximum {
				minimum, maximum = maximum, minimum
			}
			return buildCustomMarketSummaryRecommendationCountPolicy(minimum, maximum, "explicit_range")
		}
	}
	if match := marketSummaryRecommendationSinglePattern.FindStringSubmatch(text); len(match) == 2 {
		count, err := strconv.Atoi(match[1])
		if err == nil && count > 0 {
			return buildCustomMarketSummaryRecommendationCountPolicy(count, count, "explicit_single")
		}
	}
	return defaultMarketSummaryRecommendationCountPolicy()
}

func buildCustomMarketSummaryRecommendationCountPolicy(minimum, maximum int, source string) MarketSummaryRecommendationCountPolicy {
	requestedMinimum := minimum
	requestedMaximum := maximum
	if maximum > marketSummaryRecommendationOutputLimit {
		maximum = marketSummaryRecommendationOutputLimit
	}
	if minimum > marketSummaryRecommendationOutputLimit {
		minimum = marketSummaryRecommendationOutputLimit
	}
	if minimum > maximum {
		minimum = maximum
	}
	productionTarget := maximum
	if productionTarget > marketSummaryMaxProductionCandidates {
		productionTarget = marketSummaryMaxProductionCandidates
	}
	return MarketSummaryRecommendationCountPolicy{
		MinimumOutput:    minimum,
		MaximumOutput:    maximum,
		ProductionTarget: productionTarget,
		RequestedMinimum: requestedMinimum,
		RequestedMaximum: requestedMaximum,
		Source:           source,
		Custom:           true,
		Clamped:          minimum != requestedMinimum || maximum != requestedMaximum,
	}
}

func hasExplicitMarketSummaryRecommendationCount(question string) bool {
	text := stripMarketSummaryInstruction(strings.TrimSpace(question))
	return marketSummaryRecommendationRangePattern.MatchString(text) || marketSummaryRecommendationSinglePattern.MatchString(text)
}

func resolveMarketSummaryFinalCandidateLimit(question string) int {
	limit := marketSummaryFinalCandidateLimit
	policy := ResolveMarketSummaryRecommendationCountPolicy(question)
	if policy.MaximumOutput > 0 && policy.MaximumOutput < limit {
		limit = policy.MaximumOutput
	}
	return limit
}

func (policy MarketSummaryRecommendationCountPolicy) Instruction() string {
	outputTarget := fmt.Sprintf("%d 到 %d 只股票", policy.MinimumOutput, policy.MaximumOutput)
	if policy.MinimumOutput == policy.MaximumOutput {
		outputTarget = fmt.Sprintf("%d 只股票", policy.MaximumOutput)
	}
	instruction := fmt.Sprintf(
		"【推荐数量策略】\n本次“推荐股票池”目标输出 %s，其中最多 %d 只可作为可交易生产候选。严格核验后若不足最低目标 %d 只，允许按实际通过数量输出，甚至可以为 0 只；不得编造股票、复用已排除股票或降低证据、交易计划和风控门槛凑数。",
		outputTarget,
		policy.ProductionTarget,
		policy.MinimumOutput,
	)
	if policy.Clamped {
		requested := fmt.Sprintf("%d 到 %d 只", policy.RequestedMinimum, policy.RequestedMaximum)
		if policy.RequestedMinimum == policy.RequestedMaximum {
			requested = fmt.Sprintf("%d 只", policy.RequestedMaximum)
		}
		instruction += fmt.Sprintf("\n用户原始请求为%s；系统单次推荐股票池上限为 %d 只，本次已按上限截断。", requested, marketSummaryRecommendationOutputLimit)
	}
	return instruction
}
