"""Durable report delivery with finite retries and snapshot SMTP settings."""

from __future__ import annotations

import asyncio
import json
import logging
import re
import smtplib
import ssl
import uuid
from datetime import timedelta
from email.message import EmailMessage
from email.policy import SMTP
from email.utils import format_datetime, getaddresses
from typing import Any

from .core import PredictionError, local, stamp
from .repository import insert, one, update


def email_config(values, draft=False):
    def get(key, default: Any = "") -> Any:
        return values.get(key if draft else "predictionEmail" + key[0].upper() + key[1:], default)

    config = {
        key: get(key, 465 if key == "smtpPort" else "")
        for key in ("to", "from", "smtpHost", "smtpPort", "smtpUsername", "smtpPassword")
    }
    config["from"] = config["from"] or config["smtpUsername"]
    if (
        not config["smtpHost"]
        or not 1 <= int(config["smtpPort"]) <= 65535
        or not config["smtpUsername"]
        or not config["smtpPassword"]
    ):
        raise PredictionError("请填写有效SMTP主机、端口、用户名和授权码")
    raw = str(config["to"]).replace(";", ",").replace("\n", ",")
    recipients = list(dict.fromkeys(address for _, address in getaddresses([raw]) if address))
    if not recipients or any(not re.fullmatch(r"[^\s@,]+@[^\s@,]+", address) for address in recipients):
        raise PredictionError("请填写有效收件人地址")
    if not re.fullmatch(r"[^\s@,]+@[^\s@,]+", str(config["from"])):
        raise PredictionError("发件人地址无效")
    return config, recipients


def smtp_send(config, delivery):
    message = EmailMessage(policy=SMTP)
    for key, value in [
        ("From", delivery["sender"]),
        ("To", delivery["recipients"]),
        ("Subject", delivery["subject"]),
        ("Message-ID", delivery["message_id"]),
        ("Date", format_datetime(local())),
    ]:
        message[key] = value
    message.set_content(delivery["body"], charset="utf-8", cte="quoted-printable")
    context = ssl.create_default_context()
    if int(config["smtpPort"]) == 465:
        client = smtplib.SMTP_SSL(config["smtpHost"], int(config["smtpPort"]), timeout=15, context=context)
    else:
        client = smtplib.SMTP(config["smtpHost"], int(config["smtpPort"]), timeout=15)
    with client:
        if int(config["smtpPort"]) != 465:
            client.ehlo()
            client.starttls(context=context)
            client.ehlo()
        client.login(config["smtpUsername"], config["smtpPassword"])
        client.send_message(message, from_addr=delivery["sender"], to_addrs=delivery["recipients"].split(","))


def compact_body(body):
    lines = []
    for line in body.splitlines():
        if line.strip().startswith(("- 降级原因：", "降级原因：")):
            reasons = list(
                dict.fromkeys(
                    part.strip() for part in re.split("[；;]", line.split("：", 1)[1]) if part.strip()
                )
            )
            lines.append("- 降级原因摘要：")
            lines.extend("  - " + reason[:80] + ("…" if len(reason) > 80 else "") for reason in reasons[:8])
            if len(reasons) > 8:
                lines.append(f"  - 其余{len(reasons) - 8}项完整明细见数据库审计")
        else:
            lines.append(line)
    return "\n".join(lines)


def queue_published(connection, run):
    if not run["published"] or run["status"] not in ("success", "no_recommendation"):
        return
    stored = one(connection, "SELECT config_json FROM research_settings WHERE center='research2'")
    if not stored:
        return
    config = {re.sub(r"^research2", "prediction", k): v for k, v in json.loads(stored["config_json"]).items()}
    if not config.get("predictionEmailEnabled") or run["slot"] not in config.get("predictionEmailSlots", []):
        return
    try:
        normalized, recipients = email_config(config)
    except PredictionError:
        return
    suffix = (
        "无推荐" if run["status"] == "no_recommendation" else f"分析报告（{run['recommendation_count']}只）"
    )
    insert(
        connection,
        "email_deliveries",
        {
            "analysis_run_id": run["run_id"],
            "status": "pending",
            "attempt_count": 0,
            "next_attempt_at": run["persisted_at"],
            "recipients": ",".join(recipients),
            "sender": normalized["from"],
            "subject": f"[Stock God][股票预测][{run['slot']}] {run['trading_date']} 第{run['attempt_no']}次尝试 {suffix}",
            "body": compact_body(run["report_markdown"]),
            "message_id": f"<research2-{run['run_id']}@{normalized['smtpHost']}>",
            "created_at": run["persisted_at"],
            "updated_at": run["persisted_at"],
        },
        ignore=True,
    )


class EmailService:
    def __init__(self, repository, settings, mailer=None):
        self.repository, self.settings = repository, settings
        self.mailer = mailer or smtp_send
        self.lock = asyncio.Lock()

    def record_attempt(self, delivery, status, error=""):
        at = stamp(self.repository.clock())
        try:
            with self.repository.db.transaction() as connection:
                connection.execute(
                    "INSERT INTO email_send_logs (send_type,triggered_at,status,recipients,subject,error_message,report_created_at,extra_summary,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
                    (
                        "research2_report",
                        at,
                        status,
                        delivery["recipients"],
                        delivery["subject"],
                        error,
                        at,
                        "analysisRunId=" + delivery.get("analysis_run_id", "test"),
                        at,
                        at,
                    ),
                )
        except (OSError, ValueError, RuntimeError):
            logging.getLogger(__name__).exception("failed to record prediction email attempt")

    async def process(self, config):
        async with self.lock:
            repo = self.repository
            now = repo.clock()
            if not config.get("predictionEmailEnabled"):
                repo.set(
                    "email_deliveries",
                    {"status": "cancelled", "next_attempt_at": None},
                    "status IN ('pending','retry_wait')",
                )
                return
            normalized, recipients = email_config(config)
            with repo.db.transaction() as connection:
                update(
                    connection,
                    "email_deliveries",
                    {"status": "retry_wait", "next_attempt_at": stamp(now), "updated_at": stamp(now)},
                    "status='sending' AND julianday(updated_at)<julianday(?)",
                    (stamp(now - timedelta(minutes=2)),),
                )
            rows = repo.rows(
                "email_deliveries",
                "status IN ('pending','retry_wait') AND julianday(next_attempt_at)<=julianday(?)",
                (stamp(now),),
            )[:20]
            for row in rows:
                claimed = repo.set(
                    "email_deliveries",
                    {"status": "sending", "updated_at": stamp(repo.clock())},
                    "id=? AND status IN ('pending','retry_wait')",
                    (row["id"],),
                )
                if not claimed:
                    continue
                attempts = (row["attempt_count"] or 0) + 1
                try:
                    await asyncio.to_thread(self.mailer, normalized, row)
                    values = {
                        "status": "sent",
                        "attempt_count": attempts,
                        "sent_at": stamp(repo.clock()),
                        "next_attempt_at": None,
                        "last_error": "",
                    }
                except (OSError, ValueError, RuntimeError) as error:
                    detail = str(error)
                    for secret in [
                        normalized["smtpPassword"],
                        normalized["smtpUsername"],
                        normalized["from"],
                        *recipients,
                    ]:
                        if secret:
                            detail = detail.replace(secret, "[已脱敏]")
                    retry = attempts < 4 and not any(
                        phrase in str(error).lower() for phrase in ("line too long", "line length exceeded")
                    )
                    next_at = repo.clock() + timedelta(minutes=(1, 3, 10)[attempts - 1]) if retry else None
                    values = {
                        "status": "retry_wait" if retry else "failed",
                        "attempt_count": attempts,
                        "next_attempt_at": stamp(next_at),
                        "last_error": detail[:1000],
                    }
                values["updated_at"] = stamp(repo.clock())
                repo.set("email_deliveries", values, "id=?", (row["id"],))
                self.record_attempt(
                    row, "success" if values["status"] == "sent" else "failed", values.get("last_error", "")
                )

    async def send_test(self, values=None):
        config, recipients = email_config(values or self.settings.load().config, draft=values is not None)
        delivery = {
            "sender": config["from"],
            "recipients": ",".join(recipients),
            "subject": "[链路测试][Stock God][股票预测] 邮件配置测试",
            "body": f"股票预测邮件链路测试成功。\n发送时间：{stamp(self.repository.clock())}\n此邮件不包含交易指令。",
            "message_id": f"<prediction-test-{uuid.uuid4()}@{config['smtpHost']}>",
        }
        try:
            await asyncio.to_thread(self.mailer, config, delivery)
        except (OSError, ValueError, RuntimeError) as error:
            text = str(error)
            for secret in [config["smtpPassword"], config["smtpUsername"], config["from"], *recipients]:
                text = text.replace(secret, "[已脱敏]")
            self.record_attempt(delivery, "failed", text[:1000])
            raise PredictionError(text[:1000]) from None
        self.record_attempt(delivery, "success")
        return {"ok": True, "message": "股票预测测试邮件发送成功"}
