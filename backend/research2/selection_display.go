package research2

// The daily view retains actual buys, then executable scores, then other results.
// The full report and recommendation detail retain every original score and reason.
const dailySelectionQuery = `WITH selection_days AS (
 SELECT r.id, r.recommendation_id, r.analysis_run_id, r.stock_code, r.final_score, r.status,
  CASE WHEN runs.status IN ('success','no_recommendation') THEN 1 ELSE 0 END AS report_valid,
  date(coalesce(buy_at, signal_at), '+8 hours') AS display_day,
  julianday(signal_at) AS signal_time,
  CASE WHEN buy_at IS NOT NULL THEN 0 WHEN r.status IN ('buy_pending','standby') THEN 1 ELSE 2 END AS execution_priority
 FROM research2_recommendations r LEFT JOIN research2_analysis_runs runs ON runs.run_id = r.analysis_run_id
), unique_stocks AS (
 SELECT *, row_number() OVER (PARTITION BY display_day, stock_code
  ORDER BY execution_priority, final_score DESC, signal_time DESC, id) AS stock_row
 FROM selection_days WHERE report_valid = 1 OR execution_priority = 0
), daily_ranked AS (
 SELECT id, row_number() OVER (PARTITION BY display_day
  ORDER BY execution_priority, final_score DESC, stock_code, id) AS daily_rank
 FROM unique_stocks WHERE stock_row = 1
), ranked AS (
 SELECT d.*, n.daily_rank FROM selection_days d LEFT JOIN daily_ranked n ON n.id = d.id
)
`

const dailySelectionProjection = `
SELECT r.*, '' AS display_selection_role,
 CASE WHEN v.daily_rank <= ? THEN v.daily_rank ELSE 0 END AS display_selection_rank
FROM displayed v JOIN research2_recommendations r ON r.id = v.id
`

const dailySelectionVisible = `v.daily_rank <= ?`
const dailySelectionOrder = ` ORDER BY v.display_day DESC, v.daily_rank, v.stock_code, v.id`
