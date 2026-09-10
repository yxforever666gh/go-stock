package research2

// Rank the complete day before pagination. Actual buys retain their chronology;
// only the latest completed run supplies candidates, including cutoff ties.
const dailySelectionQuery = `WITH selection_days AS (
 SELECT id, recommendation_id, analysis_run_id, stock_code, final_score, selection_role, selection_rank, status,
  date(coalesce(buy_at, signal_at), '+8 hours') AS display_day,
  julianday(buy_at) AS buy_time, julianday(signal_at) AS signal_time,
  CASE WHEN buy_at IS NOT NULL THEN 1 ELSE 0 END AS bought
 FROM research2_recommendations
), bought_unique AS (
 SELECT *, row_number() OVER (PARTITION BY display_day, stock_code ORDER BY buy_time, signal_time, selection_rank, id) AS stock_row
 FROM selection_days WHERE bought = 1
), bought_ranked AS (
 SELECT id, display_day, stock_code,
  row_number() OVER (PARTITION BY display_day ORDER BY buy_time, signal_time, selection_rank, id) AS buy_rank
 FROM bought_unique WHERE stock_row = 1
), buy_counts AS (
 SELECT display_day, count(*) AS daily_buys FROM bought_ranked GROUP BY display_day
), completed_runs AS (
 SELECT run_id, row_number() OVER (PARTITION BY trading_date ORDER BY attempt_no DESC, id DESC) AS run_rank
 FROM research2_analysis_runs WHERE status IN ('success', 'no_recommendation')
), candidate_unique AS (
 SELECT d.*, row_number() OVER (PARTITION BY d.display_day, d.stock_code ORDER BY d.final_score DESC, d.id) AS stock_row
 FROM selection_days d JOIN completed_runs c ON c.run_id = d.analysis_run_id AND c.run_rank = 1
 WHERE d.bought = 0 AND d.status IN ('buy_pending', 'standby', 'analysis_only')
  AND NOT EXISTS (SELECT 1 FROM bought_ranked b WHERE b.display_day = d.display_day AND b.stock_code = d.stock_code)
), candidate_ranked AS (
 SELECT id, rank() OVER (PARTITION BY display_day ORDER BY final_score DESC) AS candidate_rank
 FROM candidate_unique WHERE stock_row = 1
), ranked AS (
 SELECT d.*, b.buy_rank, c.candidate_rank, coalesce(n.daily_buys, 0) AS daily_buys
 FROM selection_days d LEFT JOIN bought_ranked b ON b.id = d.id
  LEFT JOIN candidate_ranked c ON c.id = d.id LEFT JOIN buy_counts n ON n.display_day = d.display_day
)
`

const dailySelectionProjection = `
SELECT r.*,
 CASE WHEN v.buy_rank IS NOT NULL THEN 'primary'
  WHEN v.daily_buys + v.candidate_rank <= ? THEN 'candidate'
  ELSE '' END AS display_selection_role,
 CASE WHEN v.buy_rank IS NOT NULL THEN v.buy_rank
  WHEN v.daily_buys + v.candidate_rank <= ? THEN v.daily_buys + v.candidate_rank
  ELSE 0 END AS display_selection_rank
FROM displayed v JOIN research2_recommendations r ON r.id = v.id
`

const dailySelectionVisible = `(v.buy_rank IS NOT NULL OR v.daily_buys + v.candidate_rank <= ?)`

const dailySelectionOrder = ` ORDER BY v.display_day DESC, v.bought DESC,
 v.buy_rank, v.candidate_rank, v.stock_code, v.id`
