package research2

// Daily slots are a read-only projection of actual buys, independent of original
// run roles. Window functions operate on lightweight metadata before filtering
// and pagination; only the selected page is joined to full recommendation content.
// SQLite normalizes source offsets, then +8 hours selects the Shanghai date.
const dailySelectionQuery = `WITH selection_days AS (
	SELECT id, recommendation_id, selection_role, selection_rank, status,
		date(coalesce(buy_at, signal_at), '+8 hours') AS display_day,
		julianday(buy_at) AS buy_time, julianday(signal_at) AS signal_time,
		CASE WHEN buy_at IS NOT NULL THEN 1 ELSE 0 END AS bought,
		CASE WHEN buy_at IS NULL AND selection_role = 'standby'
			AND status IN ('buy_pending', 'standby') THEN 1 ELSE 0 END AS available_standby
	FROM research2_recommendations
), ranked AS (
	SELECT *,
		sum(bought) OVER (PARTITION BY display_day) AS daily_buys,
		sum(bought) OVER (PARTITION BY display_day ORDER BY bought DESC,
			buy_time, signal_time, selection_rank, id ROWS UNBOUNDED PRECEDING) AS buy_rank,
		sum(available_standby) OVER (PARTITION BY display_day ORDER BY
			signal_time, selection_rank, id ROWS UNBOUNDED PRECEDING) AS standby_rank
	FROM selection_days
)
`

const dailySelectionProjection = `
SELECT r.*,
	CASE WHEN v.bought = 1 THEN 'primary'
		WHEN v.daily_buys < ? AND v.available_standby = 1 THEN 'standby'
		WHEN v.daily_buys < ? AND v.status = 'buy_pending' AND v.selection_role = 'primary' THEN 'pending'
		ELSE '' END AS display_selection_role,
	CASE WHEN v.bought = 1 THEN v.buy_rank
		WHEN v.daily_buys < ? AND v.available_standby = 1 THEN v.standby_rank
		ELSE 0 END AS display_selection_rank
FROM displayed v JOIN research2_recommendations r ON r.id = v.id
`

const dailySelectionVisible = `v.status <> 'standby_not_used'
	AND NOT (v.bought = 0 AND v.selection_role = 'standby'
		AND (v.daily_buys >= ? OR v.available_standby = 0))`

const dailySelectionOrder = ` ORDER BY v.display_day DESC, v.bought DESC,
	v.buy_time, v.signal_time, v.selection_rank, v.id`
