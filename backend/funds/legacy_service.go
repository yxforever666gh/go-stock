package funds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go-stock/backend/instruments"
	"go-stock/backend/models"
	appservice "go-stock/internal/service"

	"github.com/PuerkitoBio/goquery"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const legacyFundRequestTimeout = 15 * time.Second

var (
	fundCodePattern      = regexp.MustCompile(`\b\d{6}\b`)
	validFundCodePattern = regexp.MustCompile(`^\d{6}$`)
)

type legacyFundOperations struct {
	database *gorm.DB
	client   HTTPDoer
}

type estimatedFundValue struct {
	Code      string `json:"fundcode"`
	Name      string `json:"name"`
	Value     string `json:"gsz"`
	Timestamp string `json:"gztime"`
}

// NewApplicationService adds the database-backed application operations to
// the read-only provider service used by the HTTP market-data endpoints.
func NewApplicationService(database *gorm.DB) *Service {
	service := NewProductionService()
	service.legacy.database = database
	return service
}

func (s *Service) legacyOperations() *legacyFundOperations {
	if s != nil && s.legacy != nil {
		return s.legacy
	}
	return &legacyFundOperations{client: defaultHTTPDoer(nil)}
}

func (s *Service) GetFundList(key string) []models.FundBasic {
	operations := s.legacyOperations()
	items := make([]models.FundBasic, 0)
	if operations.database == nil {
		return items
	}
	pattern := "%" + strings.TrimSpace(key) + "%"
	_ = operations.database.Where("code LIKE ? OR name LIKE ?", pattern, pattern).Limit(10).Find(&items).Error
	return items
}

func (s *Service) GetFollowedFund() []models.FollowedFund {
	operations := s.legacyOperations()
	items := make([]models.FollowedFund, 0)
	if operations.database == nil {
		return items
	}
	_ = operations.database.Preload("FundBasic").Find(&items).Error
	for index := range items {
		item := &items[index]
		if item.NetUnitValue == nil || item.NetEstimatedUnit == nil || *item.NetUnitValue <= 0 {
			continue
		}
		rate := (*item.NetEstimatedUnit - *item.NetUnitValue) / *item.NetUnitValue * 100
		rate = math.Round(rate*100) / 100
		item.NetEstimatedRate = &rate
	}
	return items
}

func (s *Service) FollowFund(code string) (string, error) {
	operations := s.legacyOperations()
	code = strings.TrimSpace(code)
	if code == "" {
		return "基金代码不能为空", fmt.Errorf("%w: fund code is required", appservice.ErrInvalidInput)
	}
	if operations.database == nil {
		return "关注失败", fmt.Errorf("%w: fund database is unavailable", appservice.ErrOperationFailed)
	}
	var fund models.FundBasic
	if err := operations.database.Where("code = ?", code).First(&fund).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "基金信息不存在", fmt.Errorf("%w: fund %s", appservice.ErrNotFound, code)
		}
		return "关注失败", fmt.Errorf("%w: query fund %s: %v", appservice.ErrOperationFailed, code, err)
	}
	followed := models.FollowedFund{Code: code, Name: fund.Name}
	if err := operations.database.Where("code = ?", code).FirstOrCreate(&followed).Error; err != nil {
		return "关注失败", fmt.Errorf("%w: follow fund %s: %v", appservice.ErrOperationFailed, code, err)
	}
	return "关注成功", nil
}

func (s *Service) UnFollowFund(code string) (string, error) {
	operations := s.legacyOperations()
	code = strings.TrimSpace(code)
	if code == "" {
		return "基金代码不能为空", fmt.Errorf("%w: fund code is required", appservice.ErrInvalidInput)
	}
	if operations.database == nil {
		return "取消关注失败", fmt.Errorf("%w: fund database is unavailable", appservice.ErrOperationFailed)
	}
	result := operations.database.Where("code = ?", code).Delete(&models.FollowedFund{})
	if result.Error != nil {
		return "取消关注失败", fmt.Errorf("%w: unfollow fund %s: %v", appservice.ErrOperationFailed, code, result.Error)
	}
	if result.RowsAffected == 0 {
		return "基金信息不存在", fmt.Errorf("%w: followed fund %s", appservice.ErrNotFound, code)
	}
	return "取消关注成功", nil
}

func (s *Service) GetFollowedETFs() ([]models.ETFWatchlistItem, error) {
	operations := s.legacyOperations()
	items := make([]models.ETFWatchlistItem, 0)
	if operations.database == nil {
		return items, fmt.Errorf("%w: fund database is unavailable", appservice.ErrOperationFailed)
	}
	if err := operations.database.Order("updated_at DESC, code ASC").Find(&items).Error; err != nil {
		return nil, fmt.Errorf("%w: list ETF watchlist: %v", appservice.ErrOperationFailed, err)
	}
	return items, nil
}

func (s *Service) FollowETF(item models.ETFWatchlistItem) (string, error) {
	operations := s.legacyOperations()
	code, ok := instruments.NormalizeETFCode(item.Code)
	if !ok {
		return "关注 ETF 失败", fmt.Errorf("%w: invalid ETF code %q", appservice.ErrInvalidInput, item.Code)
	}
	expectedMarket := "SZ"
	if strings.HasPrefix(code, "sh") {
		expectedMarket = "SH"
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Market = strings.ToUpper(strings.TrimSpace(item.Market))
	if item.Name == "" || item.Market != expectedMarket {
		return "关注 ETF 失败", fmt.Errorf("%w: ETF market %q does not match code %s", appservice.ErrInvalidInput, item.Market, code)
	}
	if operations.database == nil {
		return "关注 ETF 失败", fmt.Errorf("%w: fund database is unavailable", appservice.ErrOperationFailed)
	}
	now := time.Now().UTC()
	item.Code = code
	item.Category = strings.ToLower(strings.TrimSpace(item.Category))
	item.CreatedAt = now
	item.UpdatedAt = now
	err := operations.database.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "code"}},
		DoUpdates: clause.Assignments(map[string]any{
			"name": item.Name, "market": item.Market, "category": item.Category, "updated_at": now,
		}),
	}).Create(&item).Error
	if err != nil {
		return "关注 ETF 失败", fmt.Errorf("%w: follow ETF %s: %v", appservice.ErrOperationFailed, code, err)
	}
	return "关注 ETF 成功", nil
}

func (s *Service) UnFollowETF(code string) (string, error) {
	operations := s.legacyOperations()
	canonical, ok := instruments.NormalizeETFCode(code)
	if !ok {
		return "取消关注 ETF 失败", fmt.Errorf("%w: invalid ETF code %q", appservice.ErrInvalidInput, code)
	}
	if operations.database == nil {
		return "取消关注 ETF 失败", fmt.Errorf("%w: fund database is unavailable", appservice.ErrOperationFailed)
	}
	result := operations.database.Where("code = ?", canonical).Delete(&models.ETFWatchlistItem{})
	if result.Error != nil {
		return "取消关注 ETF 失败", fmt.Errorf("%w: unfollow ETF %s: %v", appservice.ErrOperationFailed, canonical, result.Error)
	}
	if result.RowsAffected == 0 {
		return "ETF 自选不存在", fmt.Errorf("%w: ETF %s", appservice.ErrNotFound, canonical)
	}
	return "取消关注 ETF 成功", nil
}

func (s *Service) CrawlFundBasic(code string) (*models.FundBasic, error) {
	operations := s.legacyOperations()
	code = strings.TrimSpace(code)
	if !validFundCodePattern.MatchString(code) {
		return nil, fmt.Errorf("%w: invalid fund code %q", appservice.ErrInvalidInput, code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyFundRequestTimeout)
	defer cancel()
	body, _, err := fetchBytes(ctx, operations.client, "https://fund.eastmoney.com/"+code+".html", nil, map[string]string{"Referer": "https://fund.eastmoney.com/"})
	if err != nil {
		return nil, err
	}
	fund, err := parseLegacyFundBasic(decodeGBKIfNeeded(body), code)
	if err != nil {
		return nil, err
	}
	if operations.database != nil {
		stored := models.FundBasic{}
		if err := operations.database.Where("code = ?", code).Assign(fund).FirstOrCreate(&stored).Error; err != nil {
			return nil, fmt.Errorf("persist fund %s: %w", code, err)
		}
	}
	return fund, nil
}

func parseLegacyFundBasic(body []byte, code string) (*models.FundBasic, error) {
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	fund := &models.FundBasic{Code: code}
	fund.Name = strings.TrimSpace(strings.ReplaceAll(document.Find(".merchandiseDetail .fundDetail-tit").First().Text(), "查看相关ETF>", ""))
	if fund.Name == "" {
		return nil, fmt.Errorf("fund %s page does not contain basic information", code)
	}
	document.Find(".infoOfFund table td").Each(func(_ int, selection *goquery.Selection) {
		label, value := splitFundLabel(selection.Text())
		switch {
		case strings.Contains(label, "类型"):
			fund.Type = value
		case strings.Contains(label, "成立"):
			fund.Establishment = value
		case strings.Contains(label, "规模"):
			fund.Scale = value
		case strings.Contains(label, "管理人"), strings.Contains(label, "基金公司"):
			fund.Company = value
		case strings.Contains(label, "经理"):
			fund.Manager = value
		case strings.Contains(label, "评级"):
			fund.Rating = value
		case strings.Contains(label, "跟踪标的"):
			fund.TrackingTarget = value
		}
	})
	document.Find(".dataOfFund dl > dd").Each(func(_ int, selection *goquery.Selection) {
		text := strings.TrimSpace(selection.Text())
		value, ok := parseFundPercent(text)
		if !ok {
			return
		}
		switch {
		case strings.Contains(text, "近1月"):
			fund.NetGrowth1 = &value
		case strings.Contains(text, "近3月"):
			fund.NetGrowth3 = &value
		case strings.Contains(text, "近6月"):
			fund.NetGrowth6 = &value
		case strings.Contains(text, "近1年"):
			fund.NetGrowth12 = &value
		case strings.Contains(text, "近3年"):
			fund.NetGrowth36 = &value
		case strings.Contains(text, "近5年"):
			fund.NetGrowth60 = &value
		case strings.Contains(text, "今年来"):
			fund.NetGrowthYTD = &value
		case strings.Contains(text, "成立来"):
			fund.NetGrowthAll = &value
		}
	})
	return fund, nil
}

func splitFundLabel(text string) (string, string) {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), "")
	for _, separator := range []string{"：", ":"} {
		if before, after, ok := strings.Cut(text, separator); ok {
			return before, after
		}
	}
	return text, ""
}

func parseFundPercent(text string) (float64, bool) {
	_, value := splitFundLabel(text)
	value = strings.TrimSpace(strings.TrimSuffix(value, "%"))
	parsed, err := strconv.ParseFloat(value, 64)
	return parsed, err == nil
}

func (s *Service) AllFund() {
	operations := s.legacyOperations()
	if operations.database == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyFundRequestTimeout)
	defer cancel()
	body, _, err := fetchBytes(ctx, operations.client, "https://fund.eastmoney.com/allfund.html", nil, nil)
	if err != nil {
		return
	}
	document, err := goquery.NewDocumentFromReader(strings.NewReader(string(decodeGBKIfNeeded(body))))
	if err != nil {
		return
	}
	document.Find("ul.num_right li a").Each(func(_ int, selection *goquery.Selection) {
		match := fundCodePattern.FindString(selection.Text() + " " + selection.AttrOr("href", ""))
		if match == "" {
			return
		}
		name := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(selection.Text(), "（"+match+"）", ""), "("+match+")", ""))
		if name == "" {
			return
		}
		row := models.FundBasic{Code: match, Name: name}
		_ = operations.database.Where("code = ?", match).FirstOrCreate(&row).Error
	})
}

func (s *Service) CrawlFundNetEstimatedUnit(code string) {
	operations := s.legacyOperations()
	if operations.database == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyFundRequestTimeout)
	defer cancel()
	params := url.Values{"rt": {strconv.FormatInt(time.Now().UnixMilli(), 10)}}
	body, _, err := fetchBytes(ctx, operations.client, "https://fundgz.1234567.com.cn/js/"+strings.TrimSpace(code)+".js", params, map[string]string{"Referer": "https://fund.eastmoney.com/"})
	if err != nil {
		return
	}
	var value estimatedFundValue
	if err := json.Unmarshal(unwrapJSONP(body), &value); err != nil {
		return
	}
	estimated, err := strconv.ParseFloat(strings.TrimSpace(value.Value), 64)
	if err != nil {
		return
	}
	_ = operations.database.Model(&models.FollowedFund{}).Where("code = ?", value.Code).Updates(map[string]any{
		"name": value.Name, "net_estimated_unit": estimated, "net_estimated_time": value.Timestamp,
	}).Error
}

func (s *Service) CrawlFundNetUnitValue(code string) {
	operations := s.legacyOperations()
	if operations.database == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), legacyFundRequestTimeout)
	defer cancel()
	endpoint := fmt.Sprintf("https://hq.sinajs.cn/rn=%d&list=f_%s", time.Now().UnixMilli(), strings.TrimSpace(code))
	body, _, err := fetchBytes(ctx, operations.client, endpoint, nil, map[string]string{"Referer": "https://finance.sina.com.cn"})
	if err != nil {
		return
	}
	assignment := strings.SplitN(string(decodeGBKIfNeeded(body)), "=", 2)
	if len(assignment) != 2 {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimSpace(assignment[1]), "\";"), ",")
	if len(parts) < 5 {
		return
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err != nil {
		return
	}
	_ = operations.database.Model(&models.FollowedFund{}).Where("code = ?", strings.TrimSpace(code)).Updates(map[string]any{
		"name": strings.TrimSpace(parts[0]), "net_unit_value": value, "net_unit_value_date": strings.TrimSpace(parts[4]),
	}).Error
}
