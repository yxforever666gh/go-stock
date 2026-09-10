package research2

// Rank the complete day before pagination. Selected primary stocks retain their selection order across execution;
// only the latest completed run supplies candidates, including cutoff ties.
const dailySelectionQuery = `WITH selection_days AS (
 SELECT id, recommendation_id, analysis_run_id, stock_code, final_score, selection_role, selection_rank, status,
  date(coalesce(buy_at, signal_at), '+8 hours') AS display_day,
  julianday(buy_at) AS buy_time, julianday(signal_at) AS signal_time,
  CASE WHEN buy_at IS NOT NULL THEN 1 ELSE 0 END AS bought
 FROM research2_recommendations
), primary_unique AS (
 SELECT *, row_number() OVER (PARTITION BY display_day, stock_code ORDER BY bought DESC, signal_time, selection_rank, id) AS stock_row
 FROM selection_days WHERE bought = 1 OR (status = 'buy_pending' AND coalesce(selection_role, '') <> 'observation' AND final_score > 50)
), primary_ranked AS (
 SELECT id, display_day, stock_code,
  row_number() OVER (PARTITION BY display_day ORDER BY signal_time, selection_rank, id) AS primary_rank
 FROM primary_unique WHERE stock_row = 1
), primary_counts AS (
 SELECT display_day, count(*) AS daily_primaries FROM primary_ranked GROUP BY display_day
), completed_runs AS (
 SELECT run_id, row_number() OVER (PARTITION BY trading_date ORDER BY attempt_no DESC, id DESC) AS run_rank
 FROM research2_analysis_runs WHERE status IN ('success', 'no_recommendation')
), candidate_unique AS (
 SELECT d.*, row_number() OVER (PARTITION BY d.display_day, d.stock_code ORDER BY d.final_score DESC, d.id) AS stock_row
 FROM selection_days d JOIN completed_runs c ON c.run_id = d.analysis_run_id AND c.run_rank = 1
 WHERE d.bought = 0 AND d.status = 'analysis_only' AND d.selection_role = 'observation' AND d.final_score <= 50
  AND NOT EXISTS (SELECT 1 FROM primary_ranked b WHERE b.display_day = d.display_day AND b.stock_code = d.stock_code)
), candidate_ranked AS (
 SELECT id, rank() OVER (PARTITION BY display_day ORDER BY final_score DESC) AS candidate_rank
 FROM candidate_unique WHERE stock_row = 1
), ranked AS (
 SELECT d.*, b.primary_rank, c.candidate_rank, coalesce(n.daily_primaries, 0) AS daily_primaries
 FROM selection_days d LEFT JOIN primary_ranked b ON b.id = d.id
  LEFT JOIN candidate_ranked c ON c.id = d.id LEFT JOIN primary_counts n ON n.display_day = d.display_day
)
`

const dailySelectionProjection = `
SELECT r.*,
 CASE WHEN v.primary_rank IS NOT NULL THEN 'primary'
  WHEN v.daily_primaries + v.candidate_rank <= ? THEN 'candidate'
  ELSE '' END AS display_selection_role,
 CASE WHEN v.primary_rank IS NOT NULL THEN v.primary_rank
  WHEN v.daily_primaries + v.candidate_rank <= ? THEN v.daily_primaries + v.candidate_rank
  ELSE 0 END AS display_selection_rank
FROM displayed v JOIN research2_recommendations r ON r.id = v.id
`

const dailySelectionVisible = `(v.primary_rank IS NOT NULL OR v.daily_primaries + v.candidate_rank <= ?)`

const dailySelectionOrder = ` ORDER BY v.display_day DESC, (v.primary_rank IS NOT NULL) DESC,
 v.primary_rank, v.candidate_rank, v.stock_code, v.id`
