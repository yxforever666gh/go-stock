package data

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"go-stock/backend/logger"
)

const diemengSelfCheckTTL = 10 * time.Minute

type DiemengSelfCheckEndpoint struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	StatusCode int    `json:"statusCode"`
	DurationMS int64  `json:"durationMs"`
	Detail     string `json:"detail"`
}

type DiemengSelfCheckProbe struct {
	Mode     string                   `json:"mode"`
	Label    string                   `json:"label"`
	Status   string                   `json:"status"`
	Summary  string                   `json:"summary"`
	Calendar DiemengSelfCheckEndpoint `json:"calendar"`
	History  DiemengSelfCheckEndpoint `json:"history"`
}

type DiemengSelfCheckSnapshot struct {
	Status    string                  `json:"status"`
	Summary   string                  `json:"summary"`
	Reason    string                  `json:"reason"`
	CheckedAt time.Time               `json:"checkedAt"`
	Probes    []DiemengSelfCheckProbe `json:"probes"`
}

type diemengSelfCheckHTTPResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

var (
	diemengSelfCheckMu      sync.RWMutex
	diemengSelfCheckRunning bool
	diemengSelfCheckState   = DiemengSelfCheckSnapshot{
		Status:  "idle",
		Summary: "尚未执行自检",
	}
)

func GetDiemengSelfCheckSnapshot() DiemengSelfCheckSnapshot {
	diemengSelfCheckMu.RLock()
	defer diemengSelfCheckMu.RUnlock()
	snap := diemengSelfCheckState
	if len(snap.Probes) > 0 {
		snap.Probes = append([]DiemengSelfCheckProbe(nil), snap.Probes...)
	}
	return snap
}

func GetDiemengSelfCheckView() (string, string, string) {
	snap := GetDiemengSelfCheckSnapshot()
	status := strings.TrimSpace(snap.Status)
	summary := strings.TrimSpace(snap.Summary)
	checkedAt := ""
	if !snap.CheckedAt.IsZero() {
		checkedAt = snap.CheckedAt.In(cnLocation()).Format("2006-01-02 15:04:05")
	}
	return status, summary, checkedAt
}

func EnsureDiemengSelfCheckAsync(reason string) {
	snap := GetDiemengSelfCheckSnapshot()
	if snap.Status == "checking" {
		return
	}
	if !snap.CheckedAt.IsZero() && time.Since(snap.CheckedAt) < diemengSelfCheckTTL {
		return
	}
	RefreshDiemengSelfCheckAsync(reason)
}

func RefreshDiemengSelfCheckAsync(reason string) {
	diemengSelfCheckMu.Lock()
	if diemengSelfCheckRunning {
		diemengSelfCheckMu.Unlock()
		return
	}
	diemengSelfCheckRunning = true
	diemengSelfCheckState.Status = "checking"
	diemengSelfCheckState.Summary = "检查中"
	diemengSelfCheckState.Reason = strings.TrimSpace(reason)
	diemengSelfCheckMu.Unlock()

	go func() {
		snap := runDiemengSelfCheck(reason)
		diemengSelfCheckMu.Lock()
		diemengSelfCheckState = snap
		diemengSelfCheckRunning = false
		diemengSelfCheckMu.Unlock()
		logDiemengSelfCheckSnapshot(snap)
	}()
}

func runDiemengSelfCheck(reason string) DiemengSelfCheckSnapshot {
	probes := make([]DiemengSelfCheckProbe, 0, 3)
	for _, mode := range []string{"disable", "inherit", "settings"} {
		probes = append(probes, probeDiemengMode(mode))
	}
	now := time.Now().In(cnLocation())
	return DiemengSelfCheckSnapshot{
		Status:    summarizeDiemengSelfCheckStatus(probes),
		Summary:   summarizeDiemengSelfCheckSummary(probes),
		Reason:    strings.TrimSpace(reason),
		CheckedAt: now,
		Probes:    probes,
	}
}

func summarizeDiemengSelfCheckStatus(probes []DiemengSelfCheckProbe) string {
	hasOK := false
	hasErr := false
	for _, probe := range probes {
		switch strings.TrimSpace(probe.Status) {
		case "ok":
			hasOK = true
		case "error":
			hasErr = true
		}
	}
	switch {
	case hasOK && hasErr:
		return "degraded"
	case hasOK:
		return "ok"
	default:
		return "error"
	}
}

func summarizeDiemengSelfCheckSummary(probes []DiemengSelfCheckProbe) string {
	parts := make([]string, 0, len(probes))
	for _, probe := range probes {
		label := strings.TrimSpace(probe.Label)
		if label == "" {
			label = strings.TrimSpace(probe.Mode)
		}
		switch strings.TrimSpace(probe.Status) {
		case "ok":
			parts = append(parts, label+"可用")
		case "skipped":
			if strings.TrimSpace(probe.Summary) != "" {
				parts = append(parts, label+probe.Summary)
			} else {
				parts = append(parts, label+"未配置")
			}
		default:
			detail := strings.TrimSpace(probe.Summary)
			if detail == "" {
				detail = "失败"
			}
			parts = append(parts, label+detail)
		}
	}
	if len(parts) == 0 {
		return "暂无结果"
	}
	return strings.Join(parts, "；")
}

func probeDiemengMode(mode string) DiemengSelfCheckProbe {
	probe := DiemengSelfCheckProbe{
		Mode:   mode,
		Label:  diemengSelfCheckModeLabel(mode),
		Status: "error",
	}

	client, skipReason, err := newDiemengSelfCheckClient(mode)
	if err != nil {
		probe.Summary = err.Error()
		return probe
	}
	if strings.TrimSpace(skipReason) != "" {
		probe.Status = "skipped"
		probe.Summary = skipReason
		return probe
	}

	apiKey := strings.TrimSpace(diemengAPIKey())
	if apiKey == "" {
		probe.Summary = "缺少 apiKey"
		return probe
	}

	probe.Calendar = checkDiemengCalendar(client, apiKey)
	probe.History = checkDiemengHistory(client, apiKey)

	if probe.Calendar.OK && probe.History.OK {
		probe.Status = "ok"
		probe.Summary = fmt.Sprintf("calendar=%dms，history=%dms", probe.Calendar.DurationMS, probe.History.DurationMS)
		return probe
	}

	failures := make([]string, 0, 2)
	if !probe.Calendar.OK {
		failures = append(failures, "calendar "+strings.TrimSpace(probe.Calendar.Detail))
	}
	if !probe.History.OK {
		failures = append(failures, "history "+strings.TrimSpace(probe.History.Detail))
	}
	probe.Summary = strings.Join(failures, "；")
	return probe
}

func diemengSelfCheckModeLabel(mode string) string {
	switch strings.TrimSpace(mode) {
	case "disable":
		return "直连"
	case "inherit":
		return "系统代理"
	case "settings":
		return "应用代理"
	default:
		return mode
	}
}

func newDiemengSelfCheckClient(mode string) (*resty.Client, string, error) {
	client := resty.New().
		SetBaseURL(diemengEffectiveBaseURL()).
		SetTimeout(min(diemengTimeout(), 12*time.Second)).
		SetRetryCount(0).
		SetHeader("Content-Type", "application/json")

	switch strings.TrimSpace(mode) {
	case "disable":
		restyApplyNoProxy(client)
		return client, "", nil
	case "inherit":
		return client, "", nil
	case "settings":
		settingsProxy := diemengProxyFromSettings()
		if settingsProxy == "" {
			return client, "代理未配置", nil
		}
		u, err := url.Parse(settingsProxy)
		if err != nil || u == nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
			return nil, "", fmt.Errorf("代理地址无效: %s", settingsProxy)
		}
		client.SetProxy(settingsProxy)
		return client, "", nil
	default:
		return nil, "", fmt.Errorf("未知模式: %s", mode)
	}
}

func checkDiemengCalendar(client *resty.Client, apiKey string) DiemengSelfCheckEndpoint {
	endpoint := DiemengSelfCheckEndpoint{Name: "calendar"}
	if client == nil {
		endpoint.Detail = "client unavailable"
		return endpoint
	}

	loc := cnLocation()
	endDate := time.Now().In(loc).Format("2006-01-02")
	startDate := time.Now().In(loc).AddDate(0, 0, -3).Format("2006-01-02")

	startAt := time.Now()
	var resp diemengSelfCheckHTTPResp
	httpResp, err := client.R().
		SetHeader("apiKey", apiKey).
		SetResult(&resp).
		SetQueryParam("start_time", startDate).
		SetQueryParam("end_time", endDate).
		Get("/basic/calendar")
	endpoint.DurationMS = time.Since(startAt).Milliseconds()
	return finalizeDiemengEndpointCheck(endpoint, httpResp, err, resp)
}

func checkDiemengHistory(client *resty.Client, apiKey string) DiemengSelfCheckEndpoint {
	endpoint := DiemengSelfCheckEndpoint{Name: "history"}
	if client == nil {
		endpoint.Detail = "client unavailable"
		return endpoint
	}

	loc := cnLocation()
	end := normalizeMinuteTime(time.Now().In(loc).AddDate(0, 0, -1))
	start := normalizeMinuteTime(end.Add(-30 * time.Minute))
	body := diemengHistoryReq{
		StockCode: "000001.SZ",
		Level:     "1min",
		StartTime: start.Format("2006-01-02 15:04:05"),
		EndTime:   end.Format("2006-01-02 15:04:05"),
		Page:      0,
		PageSize:  8,
	}

	startAt := time.Now()
	var resp diemengSelfCheckHTTPResp
	httpResp, err := client.R().
		SetHeader("apiKey", apiKey).
		SetBody(body).
		SetResult(&resp).
		Post("/stock/history")
	endpoint.DurationMS = time.Since(startAt).Milliseconds()
	return finalizeDiemengEndpointCheck(endpoint, httpResp, err, resp)
}

func finalizeDiemengEndpointCheck(endpoint DiemengSelfCheckEndpoint, httpResp *resty.Response, err error, resp diemengSelfCheckHTTPResp) DiemengSelfCheckEndpoint {
	if err != nil {
		endpoint.Detail = strings.TrimSpace(err.Error())
		return endpoint
	}
	if httpResp == nil {
		endpoint.Detail = "empty http response"
		return endpoint
	}
	endpoint.StatusCode = httpResp.StatusCode()
	if httpResp.StatusCode() >= http.StatusBadRequest {
		endpoint.Detail = fmt.Sprintf("http %d", httpResp.StatusCode())
		return endpoint
	}
	if resp.Code != 200 {
		msg := strings.TrimSpace(resp.Msg)
		if msg == "" {
			msg = "api failed"
		}
		endpoint.Detail = fmt.Sprintf("api code=%d: %s", resp.Code, msg)
		return endpoint
	}
	endpoint.OK = true
	endpoint.Detail = fmt.Sprintf("http %d", httpResp.StatusCode())
	return endpoint
}

func logDiemengSelfCheckSnapshot(snap DiemengSelfCheckSnapshot) {
	checkedAt := "--"
	if !snap.CheckedAt.IsZero() {
		checkedAt = snap.CheckedAt.In(cnLocation()).Format("2006-01-02 15:04:05")
	}
	logger.SugaredLogger.Infof("diemeng self-check status=%s checked_at=%s reason=%s summary=%s",
		strings.TrimSpace(snap.Status),
		checkedAt,
		strings.TrimSpace(snap.Reason),
		strings.TrimSpace(snap.Summary),
	)
	for _, probe := range snap.Probes {
		logger.SugaredLogger.Infof("diemeng self-check mode=%s status=%s calendar=%s history=%s",
			strings.TrimSpace(probe.Mode),
			strings.TrimSpace(probe.Status),
			strings.TrimSpace(probe.Calendar.Detail),
			strings.TrimSpace(probe.History.Detail),
		)
	}
}
