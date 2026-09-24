from datetime import timedelta

import httpx
import pytest
from fastapi import FastAPI

from stock_god.prediction.core import stamp
from stock_god.prediction.router import create_router


@pytest.mark.asyncio
async def test_email_queue_is_publication_atomic_and_retries_exactly_four_times(env):
    env.settings.config.update(predictionEmailEnabled=True, predictionEmailSlots=["09:50"])
    env.settings.save()
    run = await env.service.analyze()
    deliveries = env.service.repo.rows("email_deliveries")
    assert len(deliveries) == 1 and deliveries[0]["analysis_run_id"] == run["runId"]

    def fail(config, delivery):
        raise RuntimeError("SMTP smtp-secret sender@example.com rejected")

    env.service.email.mailer = fail
    for attempt, advance in enumerate([0, 1, 3, 10], 1):
        env.clock.at += timedelta(minutes=advance)
        await env.service.deliver_emails()
        delivery = env.service.repo.rows("email_deliveries")[0]
        assert delivery["attempt_count"] == attempt and "smtp-secret" not in delivery["last_error"]
        assert delivery["status"] == ("failed" if attempt == 4 else "retry_wait")
    with env.db.connection() as con:
        assert con.execute("SELECT count(*) FROM email_send_logs").fetchone()[0] == 4


@pytest.mark.asyncio
async def test_disabled_email_cancels_pending_and_stale_sending_recovers(env):
    env.settings.config.update(predictionEmailEnabled=True, predictionEmailSlots=["09:50"])
    env.settings.save()
    await env.service.analyze()
    env.service.repo.set(
        "email_deliveries",
        {"status": "sending", "updated_at": stamp(env.clock() - timedelta(minutes=3))},
        "1",
    )
    await env.service.deliver_emails()
    assert env.service.repo.rows("email_deliveries")[0]["status"] == "sent"
    env.service.repo.set("email_deliveries", {"status": "retry_wait"}, "1")
    env.settings.config["predictionEmailEnabled"] = False
    env.settings.save()
    await env.service.deliver_emails()
    assert env.service.repo.rows("email_deliveries")[0]["status"] == "cancelled"


@pytest.mark.asyncio
async def test_http_prediction_routes_only_and_invalid_slot_boundary(env):
    await env.service.analyze()
    app = FastAPI()
    app.include_router(create_router(env.service))
    async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app), base_url="http://test") as client:
        assert len((await client.get("/api/v1/prediction/slots")).json()) == 24
        assert len((await client.get("/api/v1/prediction/recommendations?slot=09:50")).json()) == 5
        assert (await client.get("/api/v1/prediction/account?slot=oops")).status_code == 400
        assert (await client.get("/api/v1/prediction/analysis-runs/missing")).status_code == 404
        assert (await client.get("/api/v1/research2/slots")).status_code == 404
        assert (
            await client.get("/api/v1/prediction/portfolio/performance?from=2026-09-25&to=2026-09-24")
        ).status_code == 400
