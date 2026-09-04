package themes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go-stock/backend/marketdata"

	"gorm.io/gorm"
)

type ThemeLifecycleRuntime struct {
	Sources    *ThemeSourceAggregator
	Service    *Service
	Repository *Repository
	Clock      func() time.Time
}

type ThemeLifecycleRunResult struct {
	Status            string                       `json:"status"`
	TradeDate         string                       `json:"tradeDate"`
	ObservedAt        time.Time                    `json:"observedAt"`
	FrozenAt          time.Time                    `json:"frozenAt"`
	SourceStates      []ThemeSourceState           `json:"sourceStates"`
	SourceErrors      []ThemeSourceError           `json:"sourceErrors"`
	Errors            []ThemeLifecycleRuntimeError `json:"errors"`
	FrozenSnapshotIDs []string                     `json:"frozenSnapshotIds"`
	Themes            []ThemeLifecycleFreezeResult `json:"themes"`
}

type ThemeLifecycleRuntimeError struct {
	ThemeName string `json:"themeName,omitempty"`
	ThemeID   string `json:"themeId,omitempty"`
	Operation string `json:"operation"`
	Message   string `json:"message"`
}

type ThemeLifecycleFreezeResult struct {
	ThemeID        string         `json:"themeId"`
	ThemeName      string         `json:"themeName"`
	SnapshotID     string         `json:"snapshotId"`
	LifecycleStage LifecycleStage `json:"lifecycleStage"`
	CycleNo        int            `json:"cycleNo"`
	Rank           int            `json:"rank"`
	HeatScore      float64        `json:"heatScore"`
	Existing       bool           `json:"existing"`
}

func NewThemeLifecycleRuntime(sources *ThemeSourceAggregator, service *Service, repository *Repository, clock func() time.Time) *ThemeLifecycleRuntime {
	if clock == nil {
		clock = time.Now
	}
	if service == nil && repository != nil {
		service = NewService(repository)
	}
	return &ThemeLifecycleRuntime{Sources: sources, Service: service, Repository: repository, Clock: clock}
}

// CollectAndFreeze performs one daily collection. Source degradation is
// represented in the result and does not become a fatal error while usable
// signals remain. Persistence errors are returned after independent themes
// have had a chance to freeze, so one malformed theme cannot erase another
// theme's valid daily snapshot.
func (runtime *ThemeLifecycleRuntime) CollectAndFreeze(ctx context.Context, observedAt time.Time) (ThemeLifecycleRunResult, error) {
	result := ThemeLifecycleRunResult{
		Status: marketdata.StatusUnavailable, SourceStates: []ThemeSourceState{}, SourceErrors: []ThemeSourceError{},
		Errors: []ThemeLifecycleRuntimeError{}, FrozenSnapshotIDs: []string{}, Themes: []ThemeLifecycleFreezeResult{},
	}
	if runtime == nil || runtime.Sources == nil || runtime.Repository == nil || runtime.Service == nil {
		return result, errors.New("theme lifecycle runtime dependencies are unavailable")
	}
	clock := runtime.Clock
	if clock == nil {
		clock = time.Now
	}
	if observedAt.IsZero() {
		observedAt = clock()
	}
	result.ObservedAt = observedAt
	result.TradeDate = themeLifecycleTradeDate(observedAt)

	sourceBatch := runtime.Sources.Collect(ctx, observedAt)
	// FrozenAt is an evidence-availability boundary, not the time collection
	// started. Calculate it only after collection completes and never place the
	// snapshot before the batch or any adopted signal became available.
	result.FrozenAt = themeLifecycleFreezeAt(clock(), observedAt, sourceBatch)
	result.Status = sourceBatch.Status
	result.SourceStates = append(result.SourceStates, sourceBatch.Sources...)
	result.SourceErrors = append(result.SourceErrors, sourceBatch.Errors...)
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(sourceBatch.Signals) == 0 {
		// Empty or wholly unavailable collection must not create themes or
		// fabricate a daily snapshot.
		return result, nil
	}

	groups, err := groupThemeLifecycleSignals(ctx, runtime.Repository, sourceBatch.Signals)
	if err != nil {
		return result, err
	}
	for _, group := range groups {
		group.HeatScore = computeThemeLifecycleHeat(group.Signals)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].HeatScore != groups[j].HeatScore {
			return groups[i].HeatScore > groups[j].HeatScore
		}
		return groups[i].NormalizedName < groups[j].NormalizedName
	})
	for index := range groups {
		groups[index].Rank = index + 1
	}

	var persistenceErrors []error
	for _, group := range groups {
		freeze, persistErr := runtime.freezeThemeGroup(ctx, group, result.TradeDate, observedAt, result.FrozenAt)
		if persistErr != nil {
			item := ThemeLifecycleRuntimeError{ThemeName: group.CanonicalName, ThemeID: group.ThemeID, Operation: "freeze", Message: persistErr.Error()}
			result.Errors = append(result.Errors, item)
			persistenceErrors = append(persistenceErrors, fmt.Errorf("theme %s: %w", group.CanonicalName, persistErr))
			continue
		}
		result.Themes = append(result.Themes, freeze)
		result.FrozenSnapshotIDs = append(result.FrozenSnapshotIDs, freeze.SnapshotID)
	}

	sort.Strings(result.FrozenSnapshotIDs)
	if len(result.Errors) > 0 || sourceBatch.Status == marketdata.StatusPartial {
		if len(result.FrozenSnapshotIDs) == 0 {
			result.Status = marketdata.StatusUnavailable
		} else {
			result.Status = marketdata.StatusPartial
		}
	} else if len(result.FrozenSnapshotIDs) > 0 {
		result.Status = marketdata.StatusOK
	}
	if len(persistenceErrors) > 0 {
		return result, errors.Join(persistenceErrors...)
	}
	return result, nil
}

func themeLifecycleFreezeAt(now, observedAt time.Time, batch ThemeSourceBatch) time.Time {
	frozenAt := now
	if frozenAt.IsZero() {
		frozenAt = observedAt
	}
	for _, candidate := range []time.Time{observedAt, batch.CollectedAt} {
		if candidate.After(frozenAt) {
			frozenAt = candidate
		}
	}
	for _, signal := range batch.Signals {
		for _, candidate := range []time.Time{signal.AvailableAt, signal.CollectedAt} {
			if candidate.After(frozenAt) {
				frozenAt = candidate
			}
		}
	}
	return frozenAt
}

type themeLifecycleGroup struct {
	NormalizedName string
	CanonicalName  string
	Description    string
	ThemeID        string
	Names          map[string]string
	PrimaryNames   map[string]struct{}
	Signals        []RawThemeSignal
	HeatScore      float64
	Rank           int
}

func groupThemeLifecycleSignals(ctx context.Context, repository *Repository, signals []RawThemeSignal) ([]*themeLifecycleGroup, error) {
	union := newThemeNameUnion()
	for _, signal := range signals {
		primary := NormalizeName(signal.ThemeName)
		if primary == "" {
			continue
		}
		union.add(primary)
		for _, alias := range signal.Aliases {
			normalized := NormalizeName(alias)
			if normalized == "" {
				continue
			}
			union.add(normalized)
			union.join(primary, normalized)
		}
	}

	byRoot := make(map[string]*themeLifecycleGroup)
	for _, signal := range signals {
		primary := NormalizeName(signal.ThemeName)
		if primary == "" {
			continue
		}
		root := union.find(primary)
		group := byRoot[root]
		if group == nil {
			group = &themeLifecycleGroup{NormalizedName: root, Names: map[string]string{}, PrimaryNames: map[string]struct{}{}, Signals: []RawThemeSignal{}}
			byRoot[root] = group
		}
		name := strings.TrimSpace(signal.ThemeName)
		group.Names[name] = signal.SourceName
		group.PrimaryNames[name] = struct{}{}
		for _, alias := range signal.Aliases {
			alias = strings.TrimSpace(alias)
			if alias != "" {
				group.Names[alias] = signal.SourceName
			}
		}
		group.Signals = append(group.Signals, signal)
	}

	initial := make([]*themeLifecycleGroup, 0, len(byRoot))
	for _, group := range byRoot {
		group.CanonicalName = chooseThemeCanonicalName(group.PrimaryNames)
		if group.CanonicalName == "" {
			continue
		}
		initial = append(initial, group)
	}
	sort.Slice(initial, func(i, j int) bool { return initial[i].NormalizedName < initial[j].NormalizedName })

	// Existing aliases may connect groups whose raw spellings do not normalize
	// to the same text. Resolve them before ranking so one stored theme never
	// receives two competing ranks for the same trade date.
	merged := make(map[string]*themeLifecycleGroup)
	for _, group := range initial {
		ids := map[string]struct{}{}
		for _, name := range sortedThemeLifecycleNames(group.Names) {
			id, err := repository.ResolveThemeID(ctx, name)
			if err == nil {
				ids[id] = struct{}{}
				continue
			}
			if !errors.Is(err, ErrNotFound) {
				return nil, err
			}
		}
		if len(ids) > 1 {
			return nil, fmt.Errorf("theme aliases resolve to multiple themes: %s", group.CanonicalName)
		}
		key := "new:" + NormalizeName(group.CanonicalName)
		for id := range ids {
			group.ThemeID = id
			key = "id:" + id
			stored, err := repository.GetTheme(ctx, id, "")
			if err != nil {
				return nil, err
			}
			group.CanonicalName = stored.CanonicalName
			group.Description = stored.Description
		}
		if existing := merged[key]; existing != nil {
			mergeThemeLifecycleGroup(existing, group)
		} else {
			merged[key] = group
		}
	}

	result := make([]*themeLifecycleGroup, 0, len(merged))
	for _, group := range merged {
		if group.ThemeID == "" {
			group.ThemeID = "theme-" + themeLifecycleHash(NormalizeName(group.CanonicalName))[:32]
		}
		group.NormalizedName = NormalizeName(group.CanonicalName)
		result = append(result, group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].NormalizedName < result[j].NormalizedName })
	return result, nil
}

func mergeThemeLifecycleGroup(target, source *themeLifecycleGroup) {
	target.Signals = append(target.Signals, source.Signals...)
	for name, provider := range source.Names {
		target.Names[name] = provider
	}
	for name := range source.PrimaryNames {
		target.PrimaryNames[name] = struct{}{}
	}
	if target.ThemeID == "" {
		target.ThemeID = source.ThemeID
	}
	if target.Description == "" {
		target.Description = source.Description
	}
}

func (runtime *ThemeLifecycleRuntime) freezeThemeGroup(ctx context.Context, group *themeLifecycleGroup, tradeDate string, observedAt, frozenAt time.Time) (ThemeLifecycleFreezeResult, error) {
	aliases := make([]AliasInput, 0, len(group.Names))
	for _, name := range sortedThemeLifecycleNames(group.Names) {
		if NormalizeName(name) == NormalizeName(group.CanonicalName) {
			continue
		}
		aliases = append(aliases, AliasInput{
			ID: "alias-" + themeLifecycleHash(group.ThemeID + "|" + NormalizeName(name))[:32], Alias: name, Source: group.Names[name],
		})
	}
	stored, err := runtime.Repository.UpsertTheme(ctx, UpsertThemeRequest{
		ID: group.ThemeID, CanonicalName: group.CanonicalName, Description: group.Description, Status: "active", Aliases: aliases,
	})
	if err != nil {
		return ThemeLifecycleFreezeResult{}, fmt.Errorf("upsert theme: %w", err)
	}
	group.ThemeID, group.CanonicalName = stored.ID, stored.CanonicalName

	// A trade date is immutable. Returning the already-frozen document is the
	// runtime-level idempotency guard; it also prevents late same-day signals
	// from appending catalysts to a snapshot that can no longer reference them.
	existing, err := runtime.Repository.SnapshotForDate(ctx, group.ThemeID, tradeDate, nil)
	if err == nil {
		return mapThemeLifecycleFreezeResult(group.CanonicalName, existing, true), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return ThemeLifecycleFreezeResult{}, fmt.Errorf("read same-day snapshot: %w", err)
	}

	previous, err := runtime.Repository.PreviousSnapshot(ctx, group.ThemeID, tradeDate)
	if err != nil {
		return ThemeLifecycleFreezeResult{}, fmt.Errorf("read previous snapshot: %w", err)
	}
	catalystIDs, conflict, err := runtime.ingestThemeCatalysts(ctx, group.ThemeID, group.Signals)
	if err != nil {
		return ThemeLifecycleFreezeResult{}, fmt.Errorf("ingest catalysts: %w", err)
	}
	// Concept membership and fund flow may lift heat and add constituents, but
	// they are not independent catalyst confirmation for the observe->ferment
	// gate. Conflict is calculated from the persisted event claims so a support
	// from an earlier batch plus a new contradiction is visible here.
	cycleNo, stage := nextThemeLifecycle(previous, group.HeatScore, group.Rank, independentThemeCatalystSources(group.Signals), conflict)
	request := FreezeSnapshotRequest{
		ThemeID: group.ThemeID, TradeDate: tradeDate, CycleNo: cycleNo, LifecycleStage: stage,
		Rank: group.Rank, HeatScore: group.HeatScore, Summary: summarizeThemeLifecycleSignals(group.Signals),
		ObservedAt: observedAt, FrozenAt: frozenAt, Constituents: themeLifecycleConstituents(group.Signals), CatalystIDs: catalystIDs,
	}
	// Service.FreezeSnapshot delegates to the repository's transaction, which
	// atomically inserts the immutable snapshot, constituent rows and links.
	snapshot, err := runtime.Service.FreezeSnapshot(ctx, request)
	if err != nil {
		return ThemeLifecycleFreezeResult{}, err
	}
	return mapThemeLifecycleFreezeResult(group.CanonicalName, snapshot, false), nil
}

func mapThemeLifecycleFreezeResult(name string, snapshot DailySnapshot, existing bool) ThemeLifecycleFreezeResult {
	return ThemeLifecycleFreezeResult{
		ThemeID: snapshot.ThemeID, ThemeName: name, SnapshotID: snapshot.ID, LifecycleStage: snapshot.LifecycleStage,
		CycleNo: snapshot.CycleNo, Rank: snapshot.Rank, HeatScore: snapshot.HeatScore, Existing: existing,
	}
}

func nextThemeLifecycle(previous *DailySnapshot, heat float64, rank, sourceCount int, conflict bool) (int, LifecycleStage) {
	if previous == nil {
		return 1, StageObserve
	}
	cycleNo, stage := previous.CycleNo, previous.LifecycleStage
	switch previous.LifecycleStage {
	case StageObserve:
		if sourceCount >= 2 && heat >= 50 {
			stage = StageFerment
		}
	case StageFerment:
		if heat >= 75 && rank <= 10 {
			stage = StageAccelerate
		}
	case StageAccelerate:
		if conflict || previous.HeatScore-heat >= 15 {
			stage = StageDiverge
		}
	case StageDiverge:
		if heat < 40 {
			stage = StageFade
		}
	case StageFade:
		if heat >= 50 {
			cycleNo++
			stage = StageObserve
		}
	}
	return cycleNo, stage
}

func computeThemeLifecycleHeat(signals []RawThemeSignal) float64 {
	if len(signals) == 0 {
		return 0
	}
	strongest := 0.0
	for _, signal := range signals {
		if value := themeLifecycleSignalStrength(signal); value > strongest {
			strongest = value
		}
	}
	sourceBonus := math.Min(15, float64(maxThemeLifecycleInt(0, independentThemeSources(signals)-1))*5)
	densityBonus := math.Min(10, float64(maxThemeLifecycleInt(0, len(signals)-1))*2)
	return math.Round(math.Min(100, math.Max(0, strongest+sourceBonus+densityBonus))*100) / 100
}

func themeLifecycleSignalStrength(signal RawThemeSignal) float64 {
	if signal.HeatScore > 0 {
		return math.Min(100, math.Max(0, signal.HeatScore))
	}
	value := 40.0
	switch signal.Kind {
	case ThemeSignalHotTopic:
		value = 45
	case ThemeSignalHotEvent:
		value = 50
	case ThemeSignalNews:
		value = 50
	case ThemeSignalAnnouncement:
		value = 65
	case ThemeSignalConcept:
		value = 40 + themeLifecycleMetadataNumber(signal.Metadata, "boardYield")*3
	case ThemeSignalFundFlow:
		value = 45 + themeLifecycleMetadataNumber(signal.Metadata, "changePct")*3
		if themeLifecycleMetadataNumber(signal.Metadata, "netAmount") > 0 {
			value += 5
		} else if themeLifecycleMetadataNumber(signal.Metadata, "netAmount") < 0 {
			value -= 5
		}
	}
	if signal.Rank > 0 {
		value += math.Max(0, 6-float64(signal.Rank)/5)
	}
	return math.Min(100, math.Max(0, value))
}

func independentThemeSources(signals []RawThemeSignal) int {
	values := map[string]struct{}{}
	for _, signal := range signals {
		name := strings.ToLower(strings.TrimSpace(signal.SourceName))
		if name != "" {
			values[name] = struct{}{}
		}
	}
	return len(values)
}

func independentThemeCatalystSources(signals []RawThemeSignal) int {
	values := map[string]struct{}{}
	for _, signal := range signals {
		if !themeLifecycleCatalystKind(signal.Kind) {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(signal.SourceName))
		if name != "" {
			values[name] = struct{}{}
		}
	}
	return len(values)
}

func hasThemeLifecycleConflict(signals []RawThemeSignal) bool {
	stances := map[string]map[string]struct{}{}
	for _, signal := range signals {
		if !themeLifecycleCatalystKind(signal.Kind) {
			continue
		}
		key := themeLifecycleEventIdentity(signal)
		if stances[key] == nil {
			stances[key] = map[string]struct{}{}
		}
		stances[key][strings.ToLower(strings.TrimSpace(signal.Stance))] = struct{}{}
	}
	for _, values := range stances {
		_, supports := values[ThemeSignalSupports]
		_, contradicts := values[ThemeSignalContradicts]
		if supports && contradicts {
			return true
		}
	}
	return false
}

func (runtime *ThemeLifecycleRuntime) ingestThemeCatalysts(ctx context.Context, themeID string, signals []RawThemeSignal) ([]string, bool, error) {
	requests := makeThemeLifecycleCatalystRequests(themeID, signals)
	ids := make([]string, 0, len(requests))
	conflict := false
	for _, request := range requests {
		event, err := runtime.Service.IngestCatalyst(ctx, request)
		if err != nil {
			return nil, false, err
		}
		ids = append(ids, event.ID)
		conflict = conflict || themeLifecycleStoredClaimsConflict(event.Claims)
	}
	sort.Strings(ids)
	return ids, conflict, nil
}

type themeLifecycleEvent struct {
	EventType   string
	Title       string
	Summary     string
	EventAt     time.Time
	Credibility int
	EntityKeys  map[string]struct{}
	Claims      []ClaimInput
}

func makeThemeLifecycleCatalystRequests(themeID string, signals []RawThemeSignal) []IngestCatalystRequest {
	events := map[string]*themeLifecycleEvent{}
	for _, signal := range signals {
		if !themeLifecycleCatalystKind(signal.Kind) {
			continue
		}
		eventType := themeLifecycleSignalEventType(signal)
		summary := strings.TrimSpace(signal.Summary)
		if summary == "" {
			summary = strings.TrimSpace(signal.Title)
		}
		key := themeLifecycleEventIdentity(signal)
		event := events[key]
		if event == nil {
			event = &themeLifecycleEvent{EventType: eventType, Title: strings.TrimSpace(signal.Title), Summary: summary, EventAt: signal.EventAt, EntityKeys: map[string]struct{}{}, Claims: []ClaimInput{}}
			events[key] = event
		} else {
			// These presentation fields are not part of event identity. Pick a
			// deterministic representative while retaining every source summary
			// on its claim below.
			if title := strings.TrimSpace(signal.Title); title != "" && title < event.Title {
				event.Title = title
			}
			if summary < event.Summary {
				event.Summary = summary
			}
			if signal.EventAt.Before(event.EventAt) {
				event.EventAt = signal.EventAt
			}
		}
		if signal.SourceCredibilityScore > event.Credibility {
			event.Credibility = signal.SourceCredibilityScore
		}
		for _, entityKey := range themeLifecycleEventEntityKeys(signal) {
			event.EntityKeys[entityKey] = struct{}{}
		}
		availableAt := signal.AvailableAt
		if availableAt.IsZero() {
			availableAt = signal.FirstObservedAt
		}
		collectedAt := signal.CollectedAt
		if collectedAt.IsZero() {
			collectedAt = signal.FirstObservedAt
		}
		sourceRef := strings.TrimSpace(signal.SourceRef)
		if sourceRef == "" {
			sourceRef = "urn:go-stock:theme-source:" + themeLifecycleHash(signal.SourceName+"|"+signal.RawPayloadHash)
		}
		claimID := "claim-" + themeLifecycleHash(themeID + "|" + sourceRef + "|" + signal.Stance + "|" + summary)[:32]
		event.Claims = append(event.Claims, ClaimInput{
			ID: claimID, SourceName: signal.SourceName, SourceRef: sourceRef, Stance: signal.Stance,
			SourceCredibilityScore: signal.SourceCredibilityScore, Summary: summary, PublishedAt: signal.PublishedAt,
			AvailableAt: &availableAt, CollectedAt: collectedAt, RawPayloadHash: signal.RawPayloadHash,
		})
	}

	keys := make([]string, 0, len(events))
	for key := range events {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]IngestCatalystRequest, 0, len(keys))
	for _, key := range keys {
		event := events[key]
		entities := make([]string, 0, len(event.EntityKeys))
		for entity := range event.EntityKeys {
			entities = append(entities, entity)
		}
		sort.Strings(entities)
		sort.SliceStable(event.Claims, func(i, j int) bool {
			left := event.Claims[i].SourceName + "|" + event.Claims[i].SourceRef + "|" + event.Claims[i].Stance
			right := event.Claims[j].SourceName + "|" + event.Claims[j].SourceRef + "|" + event.Claims[j].Stance
			return left < right
		})
		status := "active"
		if themeLifecycleClaimsConflict(event.Claims) {
			status = "disputed"
		}
		if event.Credibility == 0 {
			event.Credibility = 60
		}
		eventID := "catalyst-" + themeLifecycleHash(themeID + "|" + key)[:32]
		result = append(result, IngestCatalystRequest{
			ID: eventID, ThemeID: themeID, EventType: event.EventType, Title: event.Title, Summary: event.Summary,
			EventAt: event.EventAt, CredibilityScore: event.Credibility, Status: status, EntityKeys: entities, Claims: event.Claims,
		})
	}
	return result
}

func themeLifecycleClaimsConflict(claims []ClaimInput) bool {
	supports, contradicts := false, false
	for _, claim := range claims {
		supports = supports || claim.Stance == ThemeSignalSupports
		contradicts = contradicts || claim.Stance == ThemeSignalContradicts
	}
	return supports && contradicts
}

func themeLifecycleStoredClaimsConflict(claims []SourceClaim) bool {
	supports, contradicts := false, false
	for _, claim := range claims {
		stance := strings.ToLower(strings.TrimSpace(claim.Stance))
		supports = supports || stance == ThemeSignalSupports
		contradicts = contradicts || stance == ThemeSignalContradicts
	}
	return supports && contradicts
}

func themeLifecycleSignalEventType(signal RawThemeSignal) string {
	eventType := strings.TrimSpace(signal.EventType)
	if eventType == "" {
		eventType = strings.TrimSpace(signal.Kind)
	}
	return eventType
}

func themeLifecycleEventIdentity(signal RawThemeSignal) string {
	return CanonicalEventIdentity(
		themeLifecycleSignalEventType(signal),
		signal.Title,
		signal.EventAt,
		themeLifecycleEventEntityKeys(signal),
	)
}

func themeLifecycleEventEntityKeys(signal RawThemeSignal) []string {
	values := make([]string, 0, len(signal.Securities))
	seen := make(map[string]struct{}, len(signal.Securities))
	for _, security := range signal.Securities {
		market := strings.ToUpper(strings.TrimSpace(security.Market))
		code := strings.ToLower(strings.TrimSpace(security.Code))
		key := market + code
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		values = append(values, key)
	}
	sort.Strings(values)
	return values
}

func themeLifecycleCatalystKind(kind string) bool {
	switch kind {
	case ThemeSignalHotTopic, ThemeSignalHotEvent, ThemeSignalNews, ThemeSignalAnnouncement:
		return true
	default:
		return false
	}
}

func themeLifecycleConstituents(signals []RawThemeSignal) []SnapshotConstituent {
	type candidate struct {
		security RawThemeSecurity
		score    float64
	}
	byKey := map[string]candidate{}
	for _, signal := range signals {
		strength := themeLifecycleSignalStrength(signal)
		for _, security := range signal.Securities {
			assetType := strings.ToLower(strings.TrimSpace(security.AssetType))
			if assetType == "" {
				assetType = "stock"
			}
			if assetType != "stock" && assetType != "index" && assetType != "etf" && assetType != "fund" {
				continue
			}
			market := strings.ToUpper(strings.TrimSpace(security.Market))
			code := strings.ToLower(strings.TrimSpace(security.Code))
			if code == "" {
				continue
			}
			key := assetType + "|" + market + "|" + code
			current, exists := byKey[key]
			if !exists || strength > current.score {
				byKey[key] = candidate{security: RawThemeSecurity{AssetType: assetType, Market: market, Code: code, Name: strings.TrimSpace(security.Name), Role: strings.TrimSpace(security.Role)}, score: strength}
			}
		}
	}
	values := make([]candidate, 0, len(byKey))
	for _, value := range byKey {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].score != values[j].score {
			return values[i].score > values[j].score
		}
		left := values[i].security.AssetType + "|" + values[i].security.Market + "|" + values[i].security.Code
		right := values[j].security.AssetType + "|" + values[j].security.Market + "|" + values[j].security.Code
		return left < right
	})
	result := make([]SnapshotConstituent, 0, len(values))
	for index, value := range values {
		result = append(result, SnapshotConstituent{
			AssetType: value.security.AssetType, Market: value.security.Market, Code: value.security.Code,
			Name: value.security.Name, Role: value.security.Role, Rank: index + 1, ContributionScore: math.Round(value.score*100) / 100,
		})
	}
	return result
}

func summarizeThemeLifecycleSignals(signals []RawThemeSignal) string {
	values := append([]RawThemeSignal(nil), signals...)
	sort.SliceStable(values, func(i, j int) bool {
		left, right := themeLifecycleSignalStrength(values[i]), themeLifecycleSignalStrength(values[j])
		if left != right {
			return left > right
		}
		return values[i].Title < values[j].Title
	})
	seen := map[string]struct{}{}
	parts := make([]string, 0, 3)
	for _, signal := range values {
		value := strings.TrimSpace(signal.Summary)
		if value == "" {
			value = strings.TrimSpace(signal.Title)
		}
		key := NormalizeName(value)
		if value == "" || key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		parts = append(parts, value)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, "；")
}

func themeLifecycleMetadataNumber(values map[string]interface{}, key string) float64 {
	if values == nil {
		return 0
	}
	value := values[key]
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case jsonNumber:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconvParseFloat(strings.TrimSpace(strings.TrimSuffix(typed, "%")))
		return parsed
	default:
		return 0
	}
}

// These tiny interfaces keep the metadata helper independent of a concrete
// decoder while still accepting encoding/json.Number without another public
// surface. json.Number satisfies jsonNumber.
type jsonNumber interface {
	Float64() (float64, error)
}

func strconvParseFloat(value string) (float64, error) {
	var result float64
	_, err := fmt.Sscan(value, &result)
	return result, err
}

func themeLifecycleTradeDate(value time.Time) string {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*60*60)
	}
	return value.In(location).Format("2006-01-02")
}

func themeLifecycleHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func chooseThemeCanonicalName(values map[string]struct{}) string {
	names := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			names = append(names, strings.TrimSpace(value))
		}
	}
	sort.Slice(names, func(i, j int) bool {
		left, right := len([]rune(names[i])), len([]rune(names[j]))
		if left != right {
			return left < right
		}
		return names[i] < names[j]
	})
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func sortedThemeLifecycleNames(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for name := range values {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}

func maxThemeLifecycleInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

type themeNameUnion struct{ parent map[string]string }

func newThemeNameUnion() *themeNameUnion { return &themeNameUnion{parent: map[string]string{}} }

func (union *themeNameUnion) add(value string) {
	if _, exists := union.parent[value]; !exists {
		union.parent[value] = value
	}
}

func (union *themeNameUnion) find(value string) string {
	parent, exists := union.parent[value]
	if !exists {
		union.parent[value] = value
		return value
	}
	if parent == value {
		return value
	}
	union.parent[value] = union.find(parent)
	return union.parent[value]
}

func (union *themeNameUnion) join(left, right string) {
	leftRoot, rightRoot := union.find(left), union.find(right)
	if leftRoot == rightRoot {
		return
	}
	if leftRoot < rightRoot {
		union.parent[rightRoot] = leftRoot
	} else {
		union.parent[leftRoot] = rightRoot
	}
}
