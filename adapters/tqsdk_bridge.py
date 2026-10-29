#!/usr/bin/env python3
"""Read-only TqSdk market-data bridge, bound exclusively to 127.0.0.1.

Configuration: TQ_USER, TQ_PASSWORD, CORNWATCH_BRIDGE_TOKEN (32+ ASCII chars).
Run: python adapters/tqsdk_bridge.py [--port 8787]
GET /quotes requires Authorization: Bearer <CORNWATCH_BRIDGE_TOKEN>.

The optional SDK is imported only when starting the real worker. No broker
account, order, position, or transaction API is used. TqSim is explicitly a
local SDK bookkeeping object; the bridge does not invoke its trading methods.
"""

from __future__ import annotations

import argparse
import hashlib
import hmac
import json
import math
import os
import re
import sys
import threading
import time
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any, Callable, Mapping, Optional


CHINA = timezone(timedelta(hours=8), "Asia/Shanghai")
UTC = timezone.utc
PRODUCTS = ("c", "cs", "lh", "m")
SOURCE = "TqSdk 授权行情（只读）"
SOURCE_URL = "https://doc.shinnytech.com/tqsdk/latest/usage/mddatas.html"
SYMBOL = re.compile(r"^DCE\.(cs|lh|c|m)([0-9]{2})([0-9]{2})$")
MAX_CONTRACTS = 128


def utc_now() -> datetime:
    return datetime.now(UTC)


def rfc3339(value: datetime) -> str:
    if value.tzinfo is None:
        raise ValueError("timezone required")
    return value.astimezone(UTC).isoformat(timespec="microseconds").replace("+00:00", "Z")


@dataclass(frozen=True)
class Config:
    user: str = field(repr=False)
    password: str = field(repr=False)
    token: str = field(repr=False)
    port: int = 8787


def load_config(environ: Mapping[str, str], port: int = 8787) -> Config:
    missing = [key for key in ("TQ_USER", "TQ_PASSWORD", "CORNWATCH_BRIDGE_TOKEN") if not environ.get(key)]
    if missing:
        raise ValueError("缺少环境变量：" + ", ".join(missing))
    token = environ["CORNWATCH_BRIDGE_TOKEN"]
    if len(token) < 32 or len(token) > 256 or not token.isascii() or any(c.isspace() for c in token):
        raise ValueError("CORNWATCH_BRIDGE_TOKEN 需为 32～256 位无空白 ASCII 随机令牌")
    if not 1 <= port <= 65535:
        raise ValueError("port 必须在 1～65535")
    if not environ["TQ_USER"].strip():
        raise ValueError("TQ_USER 不能为空白")
    return Config(environ["TQ_USER"].strip(), environ["TQ_PASSWORD"], token, port)


def actual_product(symbol: str) -> Optional[str]:
    match = SYMBOL.fullmatch(symbol)
    if not match:
        return None
    month = int(match[3])
    if not 1 <= month <= 12:
        return None
    return match[1]


def read_field(quote: Any, name: str, default: Any = None) -> Any:
    if isinstance(quote, Mapping):
        return quote.get(name, default)
    return getattr(quote, name, default)


def price(value: Any) -> float:
    if isinstance(value, bool):
        raise ValueError("boolean price")
    result = float(value)
    if not math.isfinite(result) or not 0 < result <= 1e9:
        raise ValueError("invalid price")
    return result


def count(value: Any) -> int:
    if isinstance(value, bool):
        raise ValueError("boolean volume")
    result = float(value)
    if not math.isfinite(result) or not 0 <= result <= 1e12 or result != math.trunc(result):
        raise ValueError("invalid count")
    return int(result)


def normalize_quote(symbol: str, quote: Any, now: datetime) -> dict[str, Any]:
    """Export only validated actual DCE futures; never serialize SDK entities."""
    product = actual_product(symbol)
    if product is None or read_field(quote, "instrument_id") != symbol:
        raise ValueError("actual instrument mismatch")
    if read_field(quote, "expired") is not False or read_field(quote, "ins_class") != "FUTURE":
        raise ValueError("inactive or non-future instrument")
    if read_field(quote, "exchange_id") != "DCE" or read_field(quote, "product_id") != product:
        raise ValueError("exchange/product mismatch")
    raw_time = read_field(quote, "datetime")
    if not isinstance(raw_time, str) or not re.fullmatch(r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:\.\d{1,9})?", raw_time):
        raise ValueError("unexpected SDK Beijing datetime")
    observed = datetime.fromisoformat(raw_time).replace(tzinfo=CHINA)
    if now.tzinfo is None or observed > now + timedelta(minutes=2):
        raise ValueError("future quote/timezone missing")
    last, previous = price(read_field(quote, "last_price")), price(read_field(quote, "pre_settlement"))
    opening, high, low = (price(read_field(quote, name)) for name in ("open", "highest", "lowest"))
    if high < max(opening, last) or low > min(opening, last) or high < low:
        raise ValueError("inconsistent OHLC")
    name = read_field(quote, "instrument_name", symbol)
    if not isinstance(name, str) or len(name) > 128:
        raise ValueError("invalid instrument name")
    return {
        "symbol": symbol.removeprefix("DCE."), "product": product,
        "name": name or symbol, "exchange": "DCE",
        # Source calendar date, deliberately not inferred night-session trade date.
        "trade_date": observed.astimezone(CHINA).date().isoformat(),
        "observed_at": rfc3339(observed), "source": SOURCE, "source_url": SOURCE_URL,
        "last": last, "open": opening, "high": high, "low": low,
        "previous_settlement": previous,
        "change_pct": (last / previous - 1) * 100,
        "change_basis": "previous_settlement",
        "volume": count(read_field(quote, "volume")),
        "open_interest": count(read_field(quote, "open_interest")),
    }


class SnapshotStore:
    """Immutable HTTP snapshots; unchanged data keep their original receipt time."""
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._quotes: dict[str, dict[str, Any]] = {}
        self._status = "initializing"
        self._worker_checked_at: Optional[datetime] = None

    def unavailable(self, status: str) -> None:
        # Status is a fixed code supplied by this module, never SDK exception text.
        with self._lock:
            self._status = status

    def publish(self, normalized: list[dict[str, Any]], now: datetime) -> None:
        if len(normalized) > MAX_CONTRACTS:
            raise ValueError("too many contracts")
        new: dict[str, dict[str, Any]] = {}
        with self._lock:
            for row in normalized:
                symbol = row["symbol"]
                if symbol in new:
                    raise ValueError("duplicate actual contract")
                previous = self._quotes.get(symbol)
                unchanged = previous is not None and {k: v for k, v in previous.items() if k != "received_at"} == row
                new[symbol] = {**row, "received_at": previous["received_at"] if unchanged else rfc3339(now)}
            self._quotes = new
            self._worker_checked_at = now
            products = {q["product"] for q in new.values()}
            self._status = "ready" if products == set(PRODUCTS) else "waiting_for_quotes"

    def response(self, now: datetime) -> tuple[int, dict[str, Any]]:
        with self._lock:
            # HTTP requests must not make a stalled SDK worker appear healthy.
            alive = self._worker_checked_at is not None and timedelta(0) <= now - self._worker_checked_at <= timedelta(seconds=15)
            if self._status != "ready" or not alive:
                return 503, {"error": "not_ready", "status": self._status if not alive and self._status != "ready" else ("worker_stalled" if not alive else self._status)}
            return 200, {"generated_at": rfc3339(now), "quotes": [dict(self._quotes[s]) for s in sorted(self._quotes)]}


def sdk_factory(config: Config) -> Any:
    # Optional import permits all stdlib-only unit tests without installing TqSdk.
    try:
        from tqsdk import TqApi, TqAuth, TqSim
    except ImportError as exc:
        raise RuntimeError("未安装可选 TqSdk；请在独立虚拟环境安装 adapters/requirements-tqsdk.txt") from exc
    # Explicit local simulation avoids an implicit environment-selected account.
    # No real broker login or trading method is ever constructed/called.
    return TqApi(account=TqSim(), auth=TqAuth(config.user, config.password), debug=False, disable_print=True)


def refresh_universe(api: Any, quotes: dict[str, Any]) -> dict[str, Any]:
    symbols = api.query_quotes(ins_class="FUTURE", exchange_id="DCE", product_id=list(PRODUCTS), expired=False)
    allowed = sorted({s for s in symbols if isinstance(s, str) and actual_product(s) is not None})
    if not allowed or len(allowed) > MAX_CONTRACTS:
        raise ValueError("invalid authorized contract universe")
    return {s: quotes[s] if s in quotes else api.get_quote(s) for s in allowed}


def run_worker(config: Config, store: SnapshotStore, stop: threading.Event,
               factory: Callable[[Config], Any] = sdk_factory,
               clock: Callable[[], datetime] = utc_now) -> None:
    """All SDK access stays in this one thread, separate from HTTP handlers."""
    retry_seconds = 5
    while not stop.is_set():
        api = None
        try:
            store.unavailable("connecting")
            api = factory(config)
            quotes: dict[str, Any] = {}
            universe_date = None
            while not stop.is_set():
                now = clock()
                today = now.astimezone(CHINA).date()
                if today != universe_date:
                    quotes = refresh_universe(api, quotes)
                    universe_date = today
                # Deadline keeps stop and worker health bounded even while closed.
                api.wait_update(deadline=time.time() + 1.0)
                now = clock()
                rows = []
                for symbol, quote in quotes.items():
                    try:
                        rows.append(normalize_quote(symbol, quote, now))
                    except (TypeError, ValueError, OverflowError):
                        continue  # Pending, expired or invalid quotes are never invented.
                store.publish(rows, now)
                retry_seconds = 5
        except Exception:
            # Auth/network exceptions can include credentials; do not print them.
            store.unavailable("connection_failed")
            if not stop.is_set():
                print("授权行情连接失败；请检查账号、权益或网络。将退避重连。", file=sys.stderr)
        finally:
            if api is not None:
                try:
                    api.close()
                except Exception:
                    pass
        if stop.wait(retry_seconds):
            break
        retry_seconds = min(retry_seconds * 2, 60)


def make_server(store: SnapshotStore, token: str, port: int = 8787,
                clock: Callable[[], datetime] = utc_now) -> ThreadingHTTPServer:
    expected = hashlib.sha256(token.encode("utf-8")).digest()

    class Handler(BaseHTTPRequestHandler):
        server_version = "CornWatchReadOnlyBridge/0.3"

        def setup(self) -> None:
            super().setup()
            self.connection.settimeout(5)

        def log_message(self, _format: str, *args: Any) -> None:
            pass  # Never log paths, authorization headers, or SDK credentials.

        def send_json(self, status: int, payload: dict[str, Any]) -> None:
            body = json.dumps(payload, ensure_ascii=False, allow_nan=False, separators=(",", ":")).encode("utf-8")
            self.send_response(status)
            self.send_header("Content-Type", "application/json; charset=utf-8")
            self.send_header("Cache-Control", "no-store")
            self.send_header("X-Content-Type-Options", "nosniff")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self) -> None:
            expected_host = "127.0.0.1:" + str(self.server.server_port)
            if self.headers.get("Host") != expected_host or self.headers.get("Origin") is not None:
                self.send_json(403, {"error": "local_client_required"})
                return
            auth = self.headers.get_all("Authorization") or []
            supplied = auth[0][7:] if len(auth) == 1 and auth[0].startswith("Bearer ") else ""
            if not hmac.compare_digest(hashlib.sha256(supplied.encode("utf-8")).digest(), expected):
                self.send_json(401, {"error": "unauthorized"})
                return
            if self.path != "/quotes":
                self.send_json(404, {"error": "not_found"})
                return
            status, payload = store.response(clock())
            self.send_json(status, payload)

        def do_POST(self) -> None:
            self.send_json(405, {"error": "read_only"})

        do_PUT = do_DELETE = do_PATCH = do_OPTIONS = do_POST

    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    server.daemon_threads = True
    return server


def main(argv: Optional[list[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="只读 TqSdk 本机行情桥接；不提供交易接口")
    parser.add_argument("--port", type=int, default=8787)
    args = parser.parse_args(argv)
    try:
        config = load_config(os.environ, args.port)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2
    try:
        import importlib.util
        if importlib.util.find_spec("tqsdk") is None:
            print("未安装可选 TqSdk；请在独立虚拟环境安装 adapters/requirements-tqsdk.txt", file=sys.stderr)
            return 2
    except (ImportError, ValueError):
        print("TqSdk 模块不可用；请检查独立虚拟环境", file=sys.stderr)
        return 2
    store, stop = SnapshotStore(), threading.Event()
    try:
        server = make_server(store, config.token, config.port)
    except OSError:
        print("无法监听 127.0.0.1 端口；请检查端口占用", file=sys.stderr)
        return 2
    worker = threading.Thread(target=run_worker, args=(config, store, stop), daemon=True, name="tqsdk-readonly")
    worker.start()
    print(f"只读行情桥接已启动：http://127.0.0.1:{config.port}/quotes（Bearer 验证）")
    try:
        server.serve_forever(poll_interval=0.25)
    except KeyboardInterrupt:
        pass
    finally:
        stop.set()
        server.server_close()
        worker.join(timeout=3)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
