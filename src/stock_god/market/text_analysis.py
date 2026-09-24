"""Financial lexicon weights and document-level hot words."""

import json
import math
import sqlite3
import unicodedata
from collections import Counter
from datetime import timedelta
from pathlib import Path

import jieba
import jieba.posseg

from .common import MarketDataError, ProviderState, database_rows, envelope, now, timestamp

RESOURCES = Path(__file__).with_name("resources")
WEIGHTS = json.loads((RESOURCES / "sentiment_weights.json").read_text(encoding="utf-8"))
POSITIVE = WEIGHTS["positiveFinanceWords"]
NEGATIVE = WEIGHTS["negativeFinanceWords"]
DEGREE = WEIGHTS["degreeWords"]
NEGATIONS = set("不没无非未别勿")
TRANSITIONS = {"但是", "然而", "不过", "却", "可是"}
ALIASES = {
    "ai": "人工智能",
    "a.i": "人工智能",
    "新能源车": "新能源汽车",
    "新能源车辆": "新能源汽车",
    "a股市场": "A股",
    "沪深股市": "A股",
    "chat gpt": "ChatGPT",
}


def calculate_score(tokens):
    score, positive, negative = 0.0, 0, 0
    for index, word in enumerate(tokens):
        previous = tokens[index - 1] if index else ""
        if word in POSITIVE or word in NEGATIVE:
            value = POSITIVE.get(word, -NEGATIVE.get(word, 0))
            value *= -1 if previous in NEGATIONS else DEGREE.get(previous, 1)
            score += value
            positive += value > 0
            negative += value < 0
            continue
        if index + 1 < len(tokens) and (word in DEGREE or word in NEGATIONS):
            following = tokens[index + 1]
            value = POSITIVE.get(following, -NEGATIVE.get(following, 0))
            value *= -1 if word in NEGATIONS else DEGREE[word]
            score += value
            positive += value > 0
            negative += value < 0
    return score, positive, negative


def analyze_tokens(tokens):
    transition = next((index for index, word in enumerate(tokens) if word in TRANSITIONS), None)
    if transition is None:
        score, positive, negative = calculate_score(tokens)
    else:
        left, lp, ln = calculate_score(tokens[:transition])
        right, rp, rn = calculate_score(tokens[transition + 1 :])
        score, positive, negative = left + right * 1.5, lp + rp, ln + rn
    category = 0 if score > 1 else 1 if score < -1 else 2
    return {
        "Score": score,
        "Category": category,
        "PositiveCount": positive,
        "NegativeCount": negative,
        "Description": ("看涨", "看跌", "中性")[category],
    }


def normalized_text(text):
    return "".join(char for char in unicodedata.normalize("NFKC", text).lower() if char.isalnum())


def simhash(text):
    weights = [0] * 64
    width = min(3, len(text))
    if not width:
        return 0
    for index in range(len(text) - width + 1):
        hashed = 14695981039346656037
        for byte in text[index : index + width].encode():
            hashed = ((hashed ^ byte) * 1099511628211) & ((1 << 64) - 1)
        for bit in range(64):
            weights[bit] += 1 if hashed & (1 << bit) else -1
    return sum(1 << bit for bit in range(64) if weights[bit] >= 0)


def dedupe(rows):
    documents, indexes = [], {}
    for row in rows:
        text = normalized_text(row.get("content") or row.get("title") or "")
        if not text:
            continue
        at = timestamp(row.get("data_time") or row["created_at"])
        hashed = simhash(text)
        found = indexes.get(text)
        if found is None:
            for index, previous in enumerate(documents):
                if (
                    abs((at - previous["at"]).total_seconds()) <= 21600
                    and 0.85 <= len(text) / len(previous["text"]) <= 1.18
                    and (hashed ^ previous["hash"]).bit_count() <= 3
                ):
                    found = index
                    break
        if found is None:
            indexes[text] = len(documents)
            documents.append(
                {"row": row, "text": text, "hash": hashed, "at": at, "sources": {row.get("source", "")}}
            )
        else:
            indexes[text] = found
            document = documents[found]
            document["sources"].add(row.get("source", ""))
            old = document["row"]
            if (bool(row.get("is_red")), bool(row.get("title")), bool(row.get("url")), at) > (
                bool(old.get("is_red")),
                bool(old.get("title")),
                bool(old.get("url")),
                document["at"],
            ):
                document.update(row=row, at=at)
    return documents


class TextAnalysis(ProviderState):
    def analyze_news(self, text="", save=True):
        if not text:
            rows = database_rows(
                self.config.main_db,
                "SELECT content FROM telegraph_list WHERE deleted_at IS NULL AND julianday(created_at)>julianday(?) ORDER BY data_time DESC,is_red DESC LIMIT 10000",
                ((now() - timedelta(days=1)).isoformat(),),
            )
            text = "\n".join(
                dict.fromkeys(
                    (row["content"] or "").strip() for row in rows if (row["content"] or "").strip()
                )
            )
        value = self.sentiment_weighted(text)
        if save:
            if not self.config.main_db.is_file():
                raise MarketDataError("news analysis database unavailable")
            at = now().isoformat()
            with sqlite3.connect(self.config.main_db, timeout=10) as connection:
                result = value["result"]
                connection.execute(
                    "INSERT INTO sentiment_result_analyzes(created_at,updated_at,data_time,score,category,positive_count,negative_count,description) VALUES(?,?,?,?,?,?,?,?)",
                    (
                        at,
                        at,
                        at,
                        result["Score"],
                        result["Category"],
                        result["PositiveCount"],
                        result["NegativeCount"],
                        result["Description"],
                    ),
                )
                frequencies = sorted(value["frequencies"], key=lambda row: -row["Frequency"])[:10]
                connection.executemany(
                    "INSERT INTO word_analyzes(created_at,updated_at,data_time,word,frequency,weight,score) VALUES(?,?,?,?,?,?,?)",
                    [
                        (at, at, at, row["Word"], row["Frequency"], row["Weight"], row["Score"])
                        for row in frequencies
                    ],
                )
        return value

    def _analyzers(self):
        if getattr(self, "_tokenizers", None):
            return self._tokenizers
        cache = self.config.root / "runtime" / "cache"
        cache.mkdir(parents=True, exist_ok=True)
        # Sentiment historically uses only the financial dictionary; hot words also load general Chinese.
        sentiment = jieba.Tokenizer(str(RESOURCES / "sentiment_dict.txt"))
        hot = jieba.Tokenizer()
        for tokenizer in (sentiment, hot):
            # jieba initializes tmp_dir with None; its supported runtime value is also a directory string.
            tokenizer.tmp_dir = str(cache)  # pyright: ignore[reportAttributeAccessIssue]
            tokenizer.initialize()
        explicit = {}
        for file in (
            RESOURCES / "finance.txt",
            RESOURCES / "user.txt",
            self.config.root / "data/dict/user.txt",
            self.config.root / "runtime/dict/user.txt",
        ):
            if not file.is_file():
                continue
            for line in file.read_text(encoding="utf-8-sig").splitlines():
                fields = line.split()
                if not fields or fields[0].startswith("#"):
                    continue
                word = fields[0]
                try:
                    frequency = int(float(fields[1])) if len(fields) > 1 else 100
                except ValueError:
                    continue
                if frequency <= 0:
                    continue
                sentiment.add_word(word, frequency, fields[2] if len(fields) > 2 else "n")
                hot.add_word(word, 200, "nz")
                explicit[word.lower()] = word
        try:
            for stock in self.stock_master():
                for word in (stock.get("name"), stock.get("bk_name")):
                    if word:
                        sentiment.add_word(word, 200, "n")
                        hot.add_word(word, 200, "nz")
                        explicit[word.lower()] = word
        except MarketDataError:
            pass
        self._tokenizers = sentiment, hot, explicit
        return self._tokenizers

    def sentiment_weighted(self, text):
        tokenizer, _, _ = self._analyzers()
        tokens = list(tokenizer.cut(text, HMM=True))
        result = analyze_tokens(tokens)
        frequencies = [
            {
                "Word": word,
                "Frequency": count,
                "Weight": tokenizer.FREQ.get(word, 0),
                "Score": count * tokenizer.FREQ.get(word, 0),
            }
            for word, count in Counter(tokens).items()
            if tokenizer.FREQ.get(word, 0) >= 100 and any(char.isalnum() for char in word)
        ]
        frequencies.sort(key=lambda item: (-item["Score"], -item["Frequency"], item["Word"]))
        return {"result": result, "frequencies": frequencies}

    def hot_words(self, hours=24, baseline_days=7, limit=30):
        def load():
            end = now()
            begin = end - timedelta(hours=hours)
            baseline_start = begin - timedelta(days=baseline_days)
            rows = database_rows(
                self.config.main_db,
                "SELECT * FROM telegraph_list WHERE deleted_at IS NULL AND datetime(COALESCE(NULLIF(data_time,''),created_at))>=datetime(?) AND datetime(COALESCE(NULLIF(data_time,''),created_at))<=datetime(?) ORDER BY data_time DESC,is_red DESC,id DESC LIMIT 50001",
                (baseline_start.isoformat(), end.isoformat()),
            )
            truncated = len(rows) > 50000
            rows = rows[:50000]
            current = dedupe(
                [row for row in rows if timestamp(row.get("data_time") or row["created_at"]) >= begin]
            )
            baseline = dedupe(
                [row for row in rows if timestamp(row.get("data_time") or row["created_at"]) < begin]
            )
            _, tokenizer, explicit = self._analyzers()
            stop = {
                word.strip().lower()
                for word in (RESOURCES / "stop_words.txt").read_text(encoding="utf-8").splitlines()
            }
            tagger = jieba.posseg.POSTokenizer(tokenizer)

            def stats(documents):
                values, positive, negative = {}, 0, 0
                for document in documents:
                    text = (
                        (document["row"].get("title") or "") + "\n" + (document["row"].get("content") or "")
                    )
                    terms = Counter()
                    for pair in tagger.cut(unicodedata.normalize("NFKC", text), HMM=True):
                        token = str(pair.word)
                        word = ALIASES.get(token.lower(), token).strip()
                        if (
                            not 2
                            <= len(word)
                            <= (
                                24
                                if word.lower() in explicit
                                else 12
                                if any("\u4e00" <= char <= "\u9fff" for char in word)
                                else 24
                            )
                        ):
                            continue
                        if (
                            word.lower() in stop
                            or word.lower().startswith(("http", "www"))
                            or not any(char.isalpha() for char in word)
                        ):
                            continue
                        if word.lower() not in explicit and not (
                            pair.flag.startswith("n") or pair.flag == "eng"
                        ):
                            continue
                        terms[explicit.get(word.lower(), word)] += 1
                    _, pos, neg = calculate_score(list(tokenizer.cut(text, HMM=True)))
                    positive += pos
                    negative += neg
                    for word, count in terms.items():
                        value = values.setdefault(
                            word,
                            {"count": 0, "occurrences": 0, "recency": 0.0, "documents": [], "sources": set()},
                        )
                        value["count"] += 1
                        value["occurrences"] += count
                        value["recency"] += math.exp(
                            -math.log(2) * max(0, (end - document["at"]).total_seconds() / 3600) / 12
                        )
                        value["documents"].append(document)
                        value["sources"].update(document["sources"])
                return values, positive, negative

            current_stats, positive, negative = stats(current)
            baseline_stats, _, _ = stats(baseline)
            qualifying = Counter(document["at"].date() for document in baseline)
            effective_days = sum(count >= 50 for count in qualifying.values())
            available = effective_days >= 3 and len(baseline) >= 500 and not truncated
            items = []
            for word, value in current_stats.items():
                count = value["count"]
                if count < 2:
                    continue
                before = baseline_stats.get(word, {}).get("count", 0)
                ratio = (
                    min(20, ((count + 0.5) / (len(current) + 1)) / ((before + 0.5) / (len(baseline) + 1)))
                    if available
                    else None
                )
                score = math.log1p(count) * (0.5 + 0.5 * value["recency"] / count)
                if ratio is not None:
                    score *= 1 + math.log1p(max(0, ratio - 1))
                examples = sorted(
                    value["documents"],
                    key=lambda doc: (bool(doc["row"].get("is_red")), doc["at"]),
                    reverse=True,
                )[:3]
                sources = sorted(value["sources"])
                items.append(
                    {
                        "word": word,
                        "score": round(score, 6),
                        "documentCount": count,
                        "occurrenceCount": value["occurrences"],
                        "documentShare": round(count / len(current), 6),
                        "baselineDocumentCount": before,
                        "burstRatio": ratio,
                        "growthPct": (ratio - 1) * 100 if ratio is not None else None,
                        "sourceCount": len(sources),
                        "sources": sources,
                        "latestAt": max(doc["at"] for doc in value["documents"]).isoformat(),
                        "confidence": "high"
                        if available and count >= 5 and len(sources) >= 2
                        else "medium"
                        if count >= 3
                        else "low",
                        "representativeNews": [
                            {
                                "id": doc["row"]["id"],
                                "title": doc["row"].get("title", ""),
                                "excerpt": (doc["row"].get("content") or "")[:180],
                                "source": doc["row"].get("source", ""),
                                "publishedAt": doc["at"].isoformat(),
                                "url": doc["row"].get("url", ""),
                            }
                            for doc in examples
                        ],
                    }
                )
            items.sort(key=lambda item: (-item["score"], -item["documentCount"], item["word"]))
            items = [item | {"rank": index + 1} for index, item in enumerate(items[:limit])]
            score = 100 * (positive - negative) / (positive + negative) if positive + negative else 0
            category = 0 if score > 10 else 1 if score < -10 else 2
            data = {
                "window": {"from": begin.isoformat(), "to": end.isoformat(), "hours": hours},
                "baseline": {
                    "from": baseline_start.isoformat(),
                    "to": begin.isoformat(),
                    "requestedDays": baseline_days,
                    "effectiveDays": effective_days,
                    "documentCount": len(baseline),
                    "available": available,
                    "mode": "burst" if available else "coverage_fallback",
                },
                "currentDocumentCount": len(current),
                "sentiment": {
                    "score": score,
                    "category": category,
                    "description": ("看涨", "看跌", "中性")[category],
                    "positiveCount": positive,
                    "negativeCount": negative,
                },
                "items": items,
            }
            result = envelope(
                data,
                "market_news",
                as_of=max((doc["at"] for doc in current), default=None),
                status="empty" if not current else "partial" if not available or truncated else "ok",
                warnings=[] if available else ["历史基线不足，当前按最近窗口覆盖量排序"],
            )
            result["evidenceProfile"] = "market_hot_words_v1"
            return result

        return self.http.cached(f"hotwords:{hours}:{baseline_days}:{limit}", 300, load)
