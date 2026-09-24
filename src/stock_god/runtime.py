"""One process owns scheduling; providers only perform the requested operation."""

import asyncio
import logging
import time
from datetime import datetime
from zoneinfo import ZoneInfo

from .audit import redact_text

log = logging.getLogger(__name__)
SHANGHAI = ZoneInfo("Asia/Shanghai")


class Runtime:
    def __init__(self, prediction, market, settings):
        self.prediction, self.market, self.settings = prediction, market, settings
        self.tasks = {}
        self.last = {}
        self.last_basic_day = None
        self.last_theme_day = None

    def launch(self, key, operation):
        if key in self.tasks:
            return
        task = asyncio.create_task(operation(), name="stock-god:" + key)
        self.tasks[key] = task

        def done(completed):
            self.tasks.pop(key, None)
            if not completed.cancelled() and completed.exception():
                log.error("background %s failed: %s", key, redact_text(str(completed.exception()))[0])

        task.add_done_callback(done)

    async def market_job(self, method, *args):
        provider = self.market.with_settings(self.settings.load().config)

        # Keep client lifetime inside the worker. Cancelling the waiter must not close
        # a transport that is still serving that worker's bounded network request.
        def work():
            try:
                return getattr(provider, method)(*args)
            finally:
                provider.close()

        return await asyncio.to_thread(work)

    async def _basic(self, day):
        await self.market_job("refresh_stock_master")
        self.last_basic_day = day

    async def _themes(self, now):
        provider = self.market.with_settings(self.settings.load().config)

        def work():
            try:
                if provider.is_trading_day(now):
                    provider.refresh_themes(now)
            finally:
                provider.close()

        await asyncio.to_thread(work)
        self.last_theme_day = now.date()

    def _due(self, key, interval, operation):
        tick = time.monotonic()
        if key not in self.tasks and tick - self.last.get(key, -1e20) >= interval:
            self.last[key] = tick
            self.launch(key, operation)

    async def run(self):
        settings = self.settings.runtime_values()
        if settings["updateBasicInfoOnStart"]:
            self.launch("basic", lambda: self._basic(datetime.now(SHANGHAI).date()))
        while True:
            now = datetime.now(SHANGHAI)
            try:
                await self.prediction.tick(now)
                settings = self.settings.runtime_values()
                interval = max(1, settings["refreshInterval"])
                self._due("news-analysis", interval + 60, lambda: self.market_job("analyze_news", "", True))
                if settings["enableNews"]:
                    for source in ("财联社电报", "新浪财经", "外媒"):
                        self._due(
                            "news:" + source,
                            max(60, interval + 10),
                            lambda source=source: self.market_job("refresh_telegraphs", source),
                        )
                if now.hour == 2 and self.last_basic_day != now.date():
                    self._due("basic", 60, lambda now=now: self._basic(now.date()))
                if (
                    now.weekday() < 5
                    and (now.hour, now.minute) >= (15, 10)
                    and self.last_theme_day != now.date()
                ):
                    self._due("themes", 60, lambda now=now: self._themes(now))
            except Exception as error:
                log.error("scheduler pulse failed: %s", redact_text(str(error))[0])
            await asyncio.sleep(1)

    async def close(self):
        tasks = list(self.tasks.values())
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        await self.prediction.close()
