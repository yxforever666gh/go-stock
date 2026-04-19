package data

import (
	"fmt"
	"strings"
	"time"
)

type MinuteProviderAuditResult struct {
	Provider       string `json:"provider"`
	Source         string `json:"source"`
	Bars           int    `json:"bars"`
	FirstTradeTime string `json:"firstTradeTime,omitempty"`
	LastTradeTime  string `json:"lastTradeTime,omitempty"`
}

func AuditAkShareMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error) {
	bars, source, err := fetchMinuteBarsWithAkShare(tsCode, start, end)
	return auditMinuteProvider("akshare", start, end, bars, source, err)
}

func AuditTencentMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error) {
	bars, source, err := fetchMinuteBarsWithTencent(tsCode, start, end)
	return auditMinuteProvider("tencent", start, end, bars, source, err)
}

func AuditSinaMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error) {
	bars, source, err := fetchMinuteBarsWithSina(tsCode, start, end)
	return auditMinuteProvider("sina", start, end, bars, source, err)
}

func AuditDiemengMinuteBars(tsCode string, start, end time.Time) (*MinuteProviderAuditResult, error) {
	bars, source, err := fetchMinuteBarsWithDiemeng(tsCode, start, end)
	return auditMinuteProvider("diemeng", start, end, bars, source, err)
}

func WaitDiemengSelfCheck(reason string, timeout time.Duration) (DiemengSelfCheckSnapshot, error) {
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	RefreshDiemengSelfCheckAsync(reason)
	deadline := time.Now().Add(timeout)
	for {
		snap := GetDiemengSelfCheckSnapshot()
		if strings.TrimSpace(snap.Status) != "checking" {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return snap, fmt.Errorf("wait diemeng self-check timeout after %s", timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func auditMinuteProvider(provider string, start, end time.Time, bars []minuteBar, source string, err error) (*MinuteProviderAuditResult, error) {
	if err != nil {
		return nil, err
	}
	result := &MinuteProviderAuditResult{
		Provider: provider,
		Source:   strings.TrimSpace(source),
		Bars:     len(bars),
	}
	if len(bars) > 0 {
		result.FirstTradeTime = bars[0].TradeTime.In(cnLocation()).Format(time.DateTime)
		result.LastTradeTime = bars[len(bars)-1].TradeTime.In(cnLocation()).Format(time.DateTime)
	}
	return result, nil
}
