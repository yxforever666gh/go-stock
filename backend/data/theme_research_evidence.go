package data

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"go-stock/backend/marketdata"
	"go-stock/backend/themes"
	"go-stock/internal/researchevidence"

	"gorm.io/gorm"
)

const researchThemeEvidenceProfile = "market-evidence-v2"

func newThemeEvidenceReader(database *gorm.DB) themes.EvidenceReader {
	if database == nil {
		return nil
	}
	return themes.NewService(themes.NewRepository(database))
}

func themeResearchEvidenceDocuments(envelope marketdata.DataEnvelope[themes.ResearchEvidence], cutoff time.Time) []researchevidence.SourceDocument {
	collectedAt := envelope.FetchedAt
	if collectedAt.IsZero() {
		collectedAt = cutoff
	}
	if envelope.Status == marketdata.StatusUnavailable || envelope.Status == marketdata.StatusFailed {
		messages := make([]string, 0, len(envelope.Errors))
		for _, item := range envelope.Errors {
			messages = append(messages, strings.TrimSpace(item.Provider+": "+item.Message))
		}
		message := strings.TrimSpace(strings.Join(messages, "; "))
		if message == "" {
			message = "题材仓储证据不可用"
		}
		return []researchevidence.SourceDocument{{SourceID: "theme-snapshot:unavailable", SourceName: "每日题材快照", Category: "theme", CollectedAt: collectedAt, Error: message}}
	}

	documents := make([]researchevidence.SourceDocument, 0, len(envelope.Data.Themes)*2)
	for _, theme := range envelope.Data.Themes {
		snapshot := theme.Snapshot
		snapshotCollectedAt := collectedAt
		if snapshotCollectedAt.IsZero() {
			snapshotCollectedAt = snapshot.CreatedAt
		}
		var snapshotAvailableAt *time.Time
		if !snapshot.FrozenAt.IsZero() {
			value := snapshot.FrozenAt
			snapshotAvailableAt = &value
		}
		stockConstituents := make([]map[string]any, 0, len(theme.Constituents))
		backgroundTypes := make(map[string]struct{}, len(theme.BackgroundOnlyAssetTypes)+2)
		for _, value := range theme.BackgroundOnlyAssetTypes {
			value = strings.ToLower(strings.TrimSpace(value))
			if value != "" {
				backgroundTypes[value] = struct{}{}
			}
		}
		for _, constituent := range theme.Constituents {
			assetType := strings.ToLower(strings.TrimSpace(constituent.AssetType))
			if assetType != "stock" {
				if assetType != "" {
					backgroundTypes[assetType] = struct{}{}
				}
				continue
			}
			stockConstituents = append(stockConstituents, map[string]any{
				"assetType": assetType, "market": constituent.Market, "code": constituent.Code,
				"name": constituent.Name, "role": constituent.Role, "rank": constituent.Rank,
				"contributionScore": constituent.ContributionScore,
			})
		}
		backgroundOnly := make([]string, 0, len(backgroundTypes))
		for value := range backgroundTypes {
			backgroundOnly = append(backgroundOnly, value)
		}
		sort.Strings(backgroundOnly)
		payload, marshalErr := json.Marshal(map[string]any{
			"themeId": theme.ID, "themeName": theme.Name,
			"snapshot": map[string]any{
				"snapshotId": snapshot.ID, "tradeDate": snapshot.TradeDate, "cycleNo": snapshot.CycleNo,
				"lifecycleStage": snapshot.LifecycleStage, "rank": snapshot.Rank, "heatScore": snapshot.HeatScore,
				"summary": snapshot.Summary, "frozenAt": snapshot.FrozenAt, "contentHash": snapshot.ContentHash,
				"constituentCount": snapshot.ConstituentCount, "catalystCount": snapshot.CatalystCount,
				"conflictingCatalystCount": snapshot.ConflictingCatalystCount,
			},
			"stockConstituents": stockConstituents, "backgroundOnlyAssetTypes": backgroundOnly,
		})
		document := researchevidence.SourceDocument{
			SourceID: "theme-snapshot:" + snapshot.ID, SourceName: "每日题材快照 / " + theme.Name,
			SourceRef: "theme:" + theme.ID + "#snapshot:" + snapshot.ID, Category: "theme",
			CollectedAt: snapshotCollectedAt, AvailableAt: snapshotAvailableAt,
		}
		if marshalErr != nil {
			document.Error = marshalErr.Error()
		} else {
			document.Content = string(payload)
		}
		documents = append(documents, document)

		for _, event := range theme.Catalysts {
			for _, claim := range event.Claims {
				availableAt := themeCatalystAvailableAt(event.FirstAvailableAt, claim.AvailableAt)
				claimPayload, claimMarshalErr := json.Marshal(map[string]any{
					"themeId": theme.ID, "themeName": theme.Name,
					"event": map[string]any{
						"catalystEventId": event.ID, "eventType": event.EventType, "title": event.Title,
						"summary": event.Summary, "eventAt": event.EventAt, "firstAvailableAt": event.FirstAvailableAt,
						"credibilityScore": event.CredibilityScore, "status": event.Status,
					},
					"claim": map[string]any{
						"sourceClaimId": claim.ID, "sourceName": claim.SourceName, "sourceRef": claim.SourceRef,
						"stance": claim.Stance, "sourceCredibilityScore": claim.SourceCredibilityScore,
						"summary": claim.Summary, "publishedAt": claim.PublishedAt, "availableAt": claim.AvailableAt,
						"collectedAt": claim.CollectedAt, "rawPayloadHash": claim.RawPayloadHash,
					},
				})
				claimDocument := researchevidence.SourceDocument{
					SourceID: "theme-catalyst:" + claim.ID, SourceName: "题材催化 / " + theme.Name + " / " + event.Title + " / " + claim.SourceName,
					SourceRef: claim.SourceRef, Category: "catalyst", CollectedAt: claim.CollectedAt, AvailableAt: availableAt,
				}
				if claimMarshalErr != nil {
					claimDocument.Error = claimMarshalErr.Error()
				} else {
					claimDocument.Content = string(claimPayload)
				}
				if availableAt == nil {
					claimDocument.Error = strings.TrimSpace(strings.Join([]string{claimDocument.Error, "事件或来源缺少可验证的 availableAt"}, "; "))
				}
				documents = append(documents, claimDocument)
			}
		}
	}
	return documents
}

func themeCatalystAvailableAt(eventAt, claimAt *time.Time) *time.Time {
	if eventAt == nil || claimAt == nil || eventAt.IsZero() || claimAt.IsZero() {
		return nil
	}
	value := *eventAt
	if claimAt.After(value) {
		value = *claimAt
	}
	return &value
}
