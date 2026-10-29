#!/usr/bin/env python3
"""Explicit public-source integration probe. Uses a disposable database only.

This script makes real network requests; it is not part of offline CI.
Run after npm run build:backend. It never uses paid accounts or user data.
"""
import json
import tempfile
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from smoke import ROOT, TOKEN, start, stop


def request(base, route, body=None):
    req = urllib.request.Request(base + route, data=None if body is None else json.dumps(body).encode(), headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=115) as response:
        return json.load(response)


def main():
    report = {"checked_at": datetime.now(timezone.utc).isoformat(), "isolated_database": True, "sources": {}}
    with tempfile.TemporaryDirectory(prefix="cornwatch-live-probe-") as folder:
        process, base = start(folder)
        try:
            for name in ("sina", "liaoning", "moa", "weather", "boyar_prices", "boyar_arrivals", "import_auction"):
                began = time.monotonic()
                try:
                    result = request(base, f"/api/data-sources/{name}/sync", {})
                    report["sources"][name] = {"ok": True, "elapsed_seconds": round(time.monotonic() - began, 2), **result}
                except Exception as error:
                    report["sources"][name] = {"ok": False, "error": str(error), "elapsed_seconds": round(time.monotonic() - began, 2)}
                print(name, json.dumps(report["sources"][name], ensure_ascii=False), flush=True)
            sources = request(base, "/api/data-sources")
            report["status"] = sources["sources"]
            live = request(base, "/api/market/live")
            report["market"] = live
            report["observations"] = [row for row in request(base, "/api/observations") if not row["is_demo"]]
            report["current_analysis"] = request(base, "/api/research/current", {})
            assert request(base, "/api/bars?symbol=c2701&demo=false") == [], "Snapshots must never become historical bars"
        finally:
            stop(process)
    output = ROOT / "artifacts" / "live-data-probe-v4.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    print("Report:", output, "(temporary database deleted)")
    if not all(row["ok"] for row in report["sources"].values()):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
