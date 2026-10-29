#!/usr/bin/env python3
"""Isolated integration test: all generated prices are synthetic test fixtures.

Uses only Python's standard library. No external APIs and no user database.
Run after `npm run build:backend`: python3 scripts/smoke.py
"""
import base64
import csv
import datetime as dt
import io
import json
import os
from pathlib import Path
import secrets
import struct
import subprocess
import tempfile
import urllib.request
import urllib.error
import zlib

ROOT = Path(__file__).resolve().parent.parent
TOKEN = secrets.token_hex(32)


def fixture(count=50, symbol="c2701", initial_price=2200):
    output = io.StringIO()
    writer = csv.writer(output)
    writer.writerow("symbol,timestamp,trade_date,open,high,low,close,settlement,volume,open_interest,limit_up,limit_down".split(","))
    day = dt.date(2026, 6, 1)
    price = initial_price
    for i in range(count):
        while day.weekday() >= 5:
            day += dt.timedelta(days=1)
        close = price + (4 if (i // 13) % 2 == 0 else -5)
        writer.writerow([symbol, str(day) + "T15:00:00+08:00", str(day), price, max(price, close) + 5, min(price, close) - 5, close, close, 10000, 20000, 2400, 2000])
        price = close
        day += dt.timedelta(days=1)
    return output.getvalue()


def screenshot_fixture():
    """A complete 2x2 PNG, constructed without imaging libraries."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    header = struct.pack(">IIBBBBB", 2, 2, 8, 6, 0, 0, 0)
    row = b"\x00" + b"\x30\x70\xa0\xff" * 2
    png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", header) + chunk(b"IDAT", zlib.compress(row * 2)) + chunk(b"IEND", b"")
    return "data:image/png;base64," + base64.b64encode(png).decode()


def journal_checks(base):
    request(base, "/api/journals", expected=401, token=None)
    image_url = screenshot_fixture()
    asset = request(base, "/api/journal-assets", {"name": "../复盘截图.png", "data_url": image_url}, expected=201)
    assert asset["name"] == "复盘截图.png" and asset["width"] == 2 and asset["height"] == 2
    assert asset["mime"] == "image/png" and "data_url" not in asset
    request(base, f'/api/journal-assets/{asset["id"]}', expected=401, token=None)
    assert request(base, f'/api/journal-assets/{asset["id"]}')["data_url"] == image_url
    request(base, "/api/journal-assets", {"name": "spoof.png", "data_url": "data:image/png;base64,PHN2Zz48L3N2Zz4="}, expected=400)
    request(base, "/api/journal-assets", {"name": "spoof.jpeg", "data_url": image_url.replace("image/png", "image/jpeg")}, expected=400)

    note = {"date": "2026-10-28", "title": "合成测试复盘", "symbol": "c2701", "tags": ["纪律", "趋势"], "plan": "先确定风险预算再观察信号。", "thesis": "仅为测试假设。", "execution": "未进行实际交易。", "review": "待核对截图。", "lessons": "", "emotion": "平静", "discipline": 2, "pnl": None, "attachment_ids": [asset["id"]]}
    journal = request(base, "/api/journals", note, expected=201)
    assert journal["revision"] == 1 and journal["pnl"] is None and not journal["archived"]
    original = dict(journal)
    journal["review"] = "检查原始记录，避免事后归因。"
    journal = request(base, f'/api/journals/{journal["id"]}', journal)
    assert journal["revision"] == 2
    request(base, f'/api/journals/{journal["id"]}', original, expected=409)
    assert request(base, "/api/journals?month=2026-10&q=c2701")[0]["id"] == journal["id"]
    assert request(base, "/api/journals?month=2026-09") == []
    request(base, "/api/journals?month=invalid", expected=400)

    review = request(base, "/api/journal-reviews", {"journal_ids": [journal["id"]], "use_llm": False, "include_images": False}, expected=201)
    assert review["model"] == "rules-journal-v1" and review["image_count"] == 0
    assert review["input_revisions"][journal["id"]] == 2 and len(review["input_sha256"]) == 64
    assert review["lessons"] and all(x["status"] == "candidate" for x in review["lessons"])
    assert all(set(p["journal_ids"]) <= {journal["id"]} for p in review["patterns"])
    assert request(base, "/api/journal-lessons") == []
    snapshot = request(base, f'/api/journal-reviews/{review["id"]}/snapshot')
    assert snapshot["journals"][0]["revision"] == 2 and not snapshot["include_images"]
    request(base, "/api/journal-reviews", {"journal_ids": [journal["id"]], "use_llm": True}, expected=400)
    request(base, "/api/journal-reviews", {"journal_ids": [journal["id"]], "use_llm": False, "include_images": True}, expected=400)
    assert len(request(base, "/api/journal-reviews")) == 1
    journal["review"] = "原日记随后编辑；历史整理仍对应旧版本。"
    journal = request(base, f'/api/journals/{journal["id"]}', journal)
    assert journal["revision"] == 3
    assert request(base, f'/api/journal-reviews/{review["id"]}/snapshot')["journals"][0]["review"] == snapshot["journals"][0]["review"]

    lesson = request(base, f'/api/journal-reviews/{review["id"]}/lessons/{review["lessons"][0]["id"]}/accept', {})
    assert lesson["status"] == "accepted" and lesson["review_id"] == review["id"]
    repeated = request(base, f'/api/journal-reviews/{review["id"]}/lessons/{lesson["id"]}/accept', {})
    assert repeated["created_at"] == lesson["created_at"]
    assert len(request(base, "/api/journal-lessons")) == 1
    next_review = request(base, "/api/journal-reviews", {"journal_ids": [journal["id"]], "use_llm": False}, expected=201)
    assert next_review["context_lesson_ids"] == [lesson["id"]]
    next_snapshot = request(base, f'/api/journal-reviews/{next_review["id"]}/snapshot')
    assert next_snapshot["accepted_lessons"][0]["review_id"] == review["id"]
    assert request(base, f'/api/journals/{journal["id"]}')["revision"] == 3
    revoked = request(base, f'/api/journal-lessons/{lesson["id"]}/revoke', {})
    assert revoked["status"] == "candidate" and revoked["review_id"] == review["id"]
    assert request(base, f'/api/journal-lessons/{lesson["id"]}/revoke', {}) == revoked
    assert request(base, "/api/journal-lessons") == []
    memory_free_review = request(base, "/api/journal-reviews", {"journal_ids": [journal["id"]], "use_llm": False}, expected=201)
    assert memory_free_review["context_lesson_ids"] == []
    assert request(base, f'/api/journal-reviews/{next_review["id"]}/snapshot') == next_snapshot
    lesson = request(base, f'/api/journal-reviews/{review["id"]}/lessons/{lesson["id"]}/accept', {})
    assert lesson["status"] == "accepted" and len(request(base, "/api/journal-lessons")) == 1

    archived = request(base, f'/api/journals/{journal["id"]}/archive', {"revision": journal["revision"], "archived": True})
    assert archived["archived"] and archived["revision"] == 4
    assert request(base, "/api/journals") == []
    assert request(base, "/api/journals?archived=true")[0]["id"] == journal["id"]
    request(base, f'/api/journals/{journal["id"]}/archive', {"revision": 3, "archived": False}, expected=409)
    journal = request(base, f'/api/journals/{journal["id"]}/archive', {"revision": 4, "archived": False})
    assert not journal["archived"] and journal["revision"] == 5
    assert len(request(base, "/api/journals")) == 1 and request(base, "/api/journals?archived=true") == []
    return {"journal": journal, "asset": asset, "image_url": image_url, "review": review, "lesson": lesson}


def research_checks(base, config):
    # Keep the original 50-bar contract fixture intact. Two longer, entirely
    # synthetic contracts exercise warm-up, validation, and same-date spreads.
    for symbol, price in [("c2705", 2200), ("c2709", 2250)]:
        imported = request(base, "/api/import/bars", {"csv": fixture(100, symbol, price), "source": "synthetic research integration only"})
        assert imported == {"imported": 100, "duplicates": 0}
    market = request(base, "/api/research/market?symbol=c2705&demo=false")
    assert market["symbol"] == "c2705" and market["is_demo"] is False and market["bar_count"] == 100
    assert len(market["points"]) == 100 and market["points"][0]["ma60"] is None
    assert market["latest"]["ma60"] is not None and market["latest"]["atr14"] > 0
    assert market["points"][19]["prior_high20"] is None and market["points"][20]["prior_high20"] is not None
    assert {x["symbol"] for x in market["term_structure"]} == {"c2705", "c2709"}
    assert market["spread"]["near_symbol"] == "c2705" and market["spread"]["near_minus_far"] == -50
    request(base, "/api/research/market?symbol=invalid&demo=false", expected=400)
    request(base, "/api/research/market?symbol=c2705&demo=false", expected=401, token=None)

    risk_input = {"equity": 100000, "entry": 2200, "stop": 2180, "direction": "long", "risk_pct": 1, "margin_rate": .12, "max_margin_pct": 40, "fee_per_lot": 1.2, "slippage_ticks": 1}
    risk = request(base, "/api/research/risk", risk_input)
    assert risk["risk_budget"] == 1000 and abs(risk["loss_per_lot"] - 222.4) < 1e-6
    assert risk["max_lots"] == min(risk["risk_lots"], risk["margin_lots"]) == 4
    assert risk["planned_loss"] <= risk["risk_budget"] and risk["margin_used"] <= 40000
    request(base, "/api/research/risk", {**risk_input, "stop": 2220}, expected=400)

    validation = request(base, "/api/research/validate", {**config, "symbol": "c2705"})
    assert validation["train_bars"] == 70 and validation["test_bars"] == 30
    assert len(validation["segments"]) == 2 and len(validation["cost_scenarios"]) == 2
    assert len(validation["input_sha256"]) == 64 and validation["is_demo"] is False
    assert all(segment["metrics"]["open_position"] == 0 for segment in validation["segments"])
    assert validation["warnings"] and len(request(base, "/api/backtests")) == 1


def start(folder):
    env = {k: v for k, v in os.environ.items() if not k.startswith("LLM_") and not k.startswith("CORNWATCH_")}
    env["CORNWATCH_TOKEN"] = TOKEN
    env["CORNWATCH_AUTO_SYNC"] = "false"
    binary = ROOT / "bin" / ("cornwatch.exe" if os.name == "nt" else "cornwatch")
    process = subprocess.Popen([str(binary), "--addr", "127.0.0.1:0", "--data", str(Path(folder) / "test.db"), "--ui", str(ROOT / "desktop/dist")], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    line = process.stdout.readline()
    if not line:
        raise RuntimeError(process.stderr.read())
    return process, json.loads(line)["address"]


def request(base, path, body=None, expected=200, token=TOKEN):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(base + path, headers=headers, data=None if body is None else json.dumps(body).encode())
    try:
        with urllib.request.urlopen(req, timeout=15) as response:
            status, payload = response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        status, payload = error.code, json.loads(error.read())
    assert status == expected, (path, status, expected, payload)
    return payload


def stop(process):
    process.terminate()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


def main():
    with tempfile.TemporaryDirectory(prefix="cornwatch-smoke-") as folder:
        process, base = start(folder)
        try:
            health = request(base, "/api/health")
            assert health["live_trading"] is False
            request(base, "/api/dashboard", expected=401, token=None)
            dash = request(base, "/api/dashboard")
            assert dash["mode"] == "demo" and dash["main_contract"]["symbol"] == "c2701"
            assert all(q["is_demo"] for q in dash["quotes"])
            data = fixture()
            result = request(base, "/api/import/bars", {"csv": data, "source": "synthetic integration test only"})
            assert result == {"imported": 50, "duplicates": 0}
            assert request(base, "/api/import/bars", {"csv": data, "source": "repeat"})["duplicates"] == 50
            request(base, "/api/import/bars", {"csv": data.replace("2204", "99999", 1), "source": "invalid"}, expected=400)
            assert len(request(base, "/api/bars?symbol=c2701&demo=false")) == 50
            event = {"title": "集成测试报价", "body": "测试记录：玉米报价2200元/吨，水分14%。", "source": "synthetic integration test", "published_at": "2026-06-01T08:00:00+08:00", "category": "purchase_price", "region": "山东", "severity": "medium"}
            saved = request(base, "/api/events", event, expected=201)
            repeated = request(base, "/api/events", event)
            assert saved["available_at"] == repeated["available_at"]
            analyzed = request(base, f'/api/events/{saved["id"]}/analyze', {"use_llm": False})
            assert analyzed["analysis"]["model"] == "rules-v1"
            request(base, f'/api/events/{saved["id"]}/analyze', {"use_llm": True}, expected=400)
            observation = {"kind": "truck_arrivals", "region": "山东", "entity": "测试样本", "value": 600, "unit": "辆", "observed_at": "2026-06-01T08:00:00+08:00", "source": "synthetic integration test"}
            first = request(base, "/api/observations", observation, expected=201)
            assert request(base, "/api/observations", observation)["available_at"] == first["available_at"]
            config = {"symbol": "c2701", "demo": False, "fast_window": 3, "slow_window": 10, "initial_capital": 100000, "lots": 1, "fee_per_lot": 1.2, "slippage_ticks": 1, "margin_rate": .12, "max_drawdown_pct": 20}
            run = request(base, "/api/backtests", config)
            assert len(run["input_sha256"]) == 64 and run["bar_count"] == 50 and run["engine_version"] == "sma-daily-v2"
            assert len(run["equity_curve"]) == 50 and run["metrics"]["total_fees"] > 0
            assert run["metrics"]["open_position"] == 0 and len(run["warnings"]) >= 3
            assert request(base, "/api/dashboard")["mode"] == "imported"
            settings = request(base, "/api/settings")
            assert TOKEN not in json.dumps(settings) and not settings["llm"]["configured"]
            assert settings["journal"] == {"available": True, "vision_enabled": False}
            request(base, "/api/collect/feeds", {}, expected=400)
            run_id = run["id"]
            journal_state = journal_checks(base)
            research_checks(base, config)
        finally:
            stop(process)
        process, base = start(folder)
        try:
            assert len(request(base, "/api/bars?symbol=c2701&demo=false")) == 50
            assert request(base, "/api/backtests")[0]["id"] == run_id
            assert any(e["id"] == saved["id"] and e["analysis"]["model"] == "rules-v1" for e in request(base, "/api/events"))
            journal = journal_state["journal"]
            assert request(base, f'/api/journals/{journal["id"]}') == journal
            assert request(base, f'/api/journal-assets/{journal_state["asset"]["id"]}')["data_url"] == journal_state["image_url"]
            assert any(r["id"] == journal_state["review"]["id"] for r in request(base, "/api/journal-reviews"))
            assert request(base, "/api/journal-lessons")[0] == journal_state["lesson"]
            assert request(base, f'/api/journal-reviews/{journal_state["review"]["id"]}/snapshot')["journals"][0]["revision"] == 2
            assert request(base, "/api/research/market?symbol=c2705&demo=false")["bar_count"] == 100
            if os.name != "nt":
                assert (Path(folder) / "test.db.journal.db").stat().st_mode & 0o777 == 0o600
        finally:
            stop(process)
    print("PASS: auth, demo isolation, atomic CSV import, duplicates, event analysis, observations, backtest costs, market diagnostics/spreads, risk sizing, train/test validation, journal CRUD/revision conflicts, authenticated PNG assets, review snapshots/lessons/revocation, archive/restore, restart persistence. All test data removed.")


if __name__ == "__main__":
    main()
