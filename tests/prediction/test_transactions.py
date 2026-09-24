import asyncio
from concurrent.futures import ThreadPoolExecutor
from datetime import timedelta

import pytest

from stock_god.prediction.core import Conflict, stamp


@pytest.mark.asyncio
async def test_simultaneous_buy_transactions_share_current_cash_and_stop_at_five(env):
    await env.service.analyze()
    env.service.repo.set("execution_chains", {"sell_completed_at": stamp(env.clock())}, "1")
    rows = env.service.repo.rows("recommendations")

    def buy(row):
        try:
            env.service.repo.buy(
                row["recommendation_id"], env.market.quote(row["stock_code"]), env.clock() + timedelta(days=1)
            )
            return True
        except Conflict:
            return False

    with ThreadPoolExecutor(max_workers=7) as pool:
        result = list(pool.map(buy, rows))
    assert sum(result) == 5
    assert env.service.account()["cash"] >= 0
    assert len(env.service.repo.rows("trades")) == 5


@pytest.mark.asyncio
async def test_shutdown_marks_running_analysis_and_audit_failed(env):
    waiting = asyncio.Event()

    async def model():
        waiting.set()
        await asyncio.Event().wait()

    env.ai.hook = model
    await env.service.tick()
    await asyncio.wait_for(waiting.wait(), 5)
    await env.service.close()
    row = env.service.repo.rows("analysis_runs")[0]
    assert row["status"] == "failed"
    assert env.audit.detail(row["run_id"])["state"]["status"] == "failed"


@pytest.mark.asyncio
async def test_recovery_without_resume_does_not_call_providers_or_launch_tasks(env):
    env.service.repo.claim_run(env.clock())
    await env.service.recover(resume=False)
    assert env.service.repo.rows("analysis_runs")[0]["status"] == "failed"
    assert not env.service._tasks
    assert not env.market.network_calls and not env.ai.calls
