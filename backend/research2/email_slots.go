package research2

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// NormalizeEmailSlots validates, de-duplicates, and orders selected account
// slots by the canonical Research 2 morning schedule.
func NormalizeEmailSlots(values []string) ([]string, error) {
	selected := make(map[string]struct{}, len(values))
	for _, value := range values {
		slot := strings.TrimSpace(value)
		if !ValidSlot(slot) {
			return nil, fmt.Errorf("无效的研究中心2邮件时间段：%s", value)
		}
		selected[slot] = struct{}{}
	}
	result := make([]string, 0, len(selected))
	for _, slot := range Slots() {
		if _, ok := selected[slot]; ok {
			result = append(result, slot)
		}
	}
	return result, nil
}

func ParseEmailSlots(value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}, nil
	}
	var slots []string
	if err := json.Unmarshal([]byte(value), &slots); err != nil {
		return nil, fmt.Errorf("研究中心2邮件时间段格式无效: %w", err)
	}
	return NormalizeEmailSlots(slots)
}

func MarshalEmailSlots(values []string) (string, []string, error) {
	slots, err := NormalizeEmailSlots(values)
	if err != nil {
		return "", nil, err
	}
	encoded, err := json.Marshal(slots)
	if err != nil {
		return "", nil, err
	}
	return string(encoded), slots, nil
}

func slotLabel(slot string) string {
	if !ValidSlot(slot) {
		return strings.TrimSpace(slot)
	}
	base := SlotTime(time.Date(2000, 1, 1, 0, 0, 0, 0, shanghai()), slot)
	return slot + "–" + base.Add(5*time.Minute).Format("15:04")
}
