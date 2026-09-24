from datetime import datetime, timedelta

import pytest

from stock_god.market import MarketDataError
from stock_god.market.common import CN
from stock_god.market.prediction_inputs import eligible_coverage, normalize_auxiliary


def test_provider_shaped_index_snapshot_retains_values_and_real_collection_time():
    cutoff = datetime(2026, 9, 25, 9, 50, tzinfo=CN)
    collected = cutoff + timedelta(seconds=2)
    value = {"code": "sh000001", "qtcode": "sh000001", "name": "上证指数", "zxj": 3000, "zdf": 1}
    filtered, available, keep = normalize_auxiliary(value, cutoff, collected)
    assert filtered == value and keep
    assert available == collected and available > cutoff


def test_undated_board_flow_array_retains_provider_fields():
    cutoff = datetime(2026, 9, 25, 9, 50, tzinfo=CN)
    value = [{"category": "gn_ai", "name": "人工智能", "netamount": "1700", "avg_changeratio": ".02"}]
    filtered, available, keep = normalize_auxiliary(value, cutoff, cutoff + timedelta(seconds=3))
    assert filtered == value and keep
    assert available == cutoff + timedelta(seconds=3)


def test_future_dated_news_never_falls_back_to_collection_time():
    cutoff = datetime(2026, 9, 25, 9, 50, tzinfo=CN)
    value = [{"title": "future", "publishedAt": "2026-09-25T10:00:00+08:00"}]
    filtered, _, keep = normalize_auxiliary(value, cutoff, cutoff + timedelta(seconds=2))
    assert filtered == [] and not keep


def test_mixed_news_array_does_not_make_undated_siblings_safe():
    cutoff = datetime(2026, 9, 25, 9, 50, tzinfo=CN)
    safe = {"title": "prior news", "publishedAt": "2026-09-25T09:40:00+08:00"}
    value = [safe, {"title": "future", "publishedAt": "2026-09-25T10:00:00+08:00"}, {"title": "unverified"}]
    filtered, available, keep = normalize_auxiliary(value, cutoff, cutoff + timedelta(seconds=2))
    assert filtered == [safe] and keep
    assert available < cutoff


def test_eligible_coverage_counts_missing_quote_rows_in_denominator():
    collected = datetime(2026, 9, 25, 9, 50, tzinfo=CN)
    rows = [
        {
            "code": f"sh60{index:04d}",
            "name": f"普通主板{index}",
            "listingDate": "19990101",
            "price": 10,
            "amount": 100,
            "asOf": collected.isoformat(),
        }
        for index in range(100)
    ]
    rows.extend(
        [
            rows[0] | {"code": "sz300001"},
            rows[0] | {"code": "sh688001"},
            rows[0] | {"code": "sh601000", "name": "ST风险"},
            rows[0] | {"code": "sh601001", "listingDate": ""},
        ]
    )
    for row in rows[:5]:
        row["price"] = None
    for row in rows[5:10]:
        row["asOf"] = None
    observed, reported = eligible_coverage(rows, collected)
    assert (len(observed), reported) == (90, 100)
    assert len(observed) / reported < 0.95
    for row in rows[:5]:
        row["price"] = 10
    observed, reported = eligible_coverage(rows, collected)
    assert (len(observed), reported) == (95, 100)
    assert len(observed) / reported == 0.95


def test_eligible_coverage_rejects_future_timestamp_and_dedupes_by_newest():
    collected = datetime(2026, 9, 25, 9, 50, tzinfo=CN)
    value = {
        "code": "600000.SH",
        "name": "浦发银行",
        "listingDate": "19991110",
        "price": 10,
        "amount": 10,
        "asOf": collected.isoformat(),
    }
    rows, total = eligible_coverage(
        [value, value | {"amount": 20}, value | {"asOf": (collected + timedelta(seconds=6)).isoformat()}],
        collected,
    )
    assert total == len(rows) == 1
    assert rows[0]["amount"] == 20


def test_collection_fails_with_measured_eligible_coverage(make_market, monkeypatch):
    collected = datetime(2026, 9, 24, 10, tzinfo=CN)
    rows = [
        {
            "code": f"sh60{index:04d}",
            "name": f"主板{index}",
            "listingDate": "19990101",
            "price": 10 if index >= 10 else None,
            "asOf": collected.isoformat(),
        }
        for index in range(100)
    ]
    service = make_market()
    monkeypatch.setattr(
        service,
        "full_market",
        lambda: {"rows": rows, "source": "eastmoney", "reported": 5432, "collectedAt": collected.isoformat()},
    )

    def unavailable(*args):
        raise MarketDataError("fallback unavailable")

    monkeypatch.setattr(service, "fallback_full_market", unavailable)
    with pytest.raises(MarketDataError, match="90.00%") as failure:
        service.collect_prediction_evidence(collected, set(), 12000)
    assert failure.value.evidence["coveragePct"] == 90
    assert failure.value.evidence["eligibleReported"] == 100
    assert failure.value.evidence["sourceReported"] == 5432
