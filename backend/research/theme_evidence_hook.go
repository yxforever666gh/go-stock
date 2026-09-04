package research

import "go-stock/internal/researchevidence"

func sourceBelongsToStage(source researchevidence.SourceDocument, category string) bool {
	if source.Category == "theme" || source.Category == "catalyst" {
		return category == "sector"
	}
	if source.Category == category || source.Error != "" {
		return true
	}
	return false
}
