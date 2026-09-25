from stock_god.prediction.core import dto
from stock_god.prediction.views import Views


def test_legacy_null_strings_use_go_zero_values_or_optional_omission():
    source = {
        "slot": None,
        "archive_reason": None,
        "chain_id": None,
        "parent_run_id": None,
        "strategy_version": None,
        "evidence_profile_version": None,
        "evidence_set_id": None,
        "replaces_recommendation_id": None,
        "promotion_reason": None,
        "evidence_coverage_pct": None,
        "degraded": None,
        "baseline_value": None,
        "period_pn_l": None,
        "buy_at": None,
        "execution_quote_at": None,
        "completed_at": None,
    }
    assert dto(source) == {
        "slot": "",
        "archiveReason": "",
        "evidenceCoveragePct": None,
        "degraded": None,
        "buyAt": None,
    }
    assert source["archive_reason"] is None


def test_missing_optional_execution_chain_is_omitted():
    class Repository:
        def row(self, *args):
            return {"run_id": "legacy", "chain_id": None, "archive_reason": None}

        def rows(self, *args):
            return []

    result = Views(Repository()).run("legacy")
    assert "executionChain" not in result
    assert result["archiveReason"] == ""


def test_pending_limit_outcome_and_missing_winner_follow_go_omitempty_and_zero_values():
    assert dto({"buy_day_limit_outcome": "", "winner_run_id": None}) == {"winnerRunId": ""}
