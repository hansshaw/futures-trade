"""Standard-library-only tests; no SDK installation, credentials or network feed."""

import contextlib
import http.client
import io
import json
import sys
import threading
import unittest
from datetime import datetime, timedelta
from types import SimpleNamespace
from unittest.mock import patch

import tqsdk_bridge as bridge


NOW = datetime(2026, 10, 28, 15, 5, tzinfo=bridge.CHINA)
TOKEN = "test-only-token-" + "a" * 32


def raw(symbol="DCE.c2701"):
    product = bridge.actual_product(symbol)
    return {
        "instrument_id": symbol, "instrument_name": "玉米2701",
        "exchange_id": "DCE", "product_id": product, "ins_class": "FUTURE", "expired": False,
        "datetime": "2026-10-28 15:00:00.000001", "last_price": 2224,
        "pre_settlement": 2208, "open": 2220, "highest": 2229, "lowest": 2211,
        "volume": 609271, "open_interest": 1276926,
    }


def all_rows(now=NOW):
    return [bridge.normalize_quote(s, raw(s), now) for s in ("DCE.c2701", "DCE.c2703", "DCE.cs2701", "DCE.lh2701", "DCE.m2701")]


class NormalizationTests(unittest.TestCase):
    def test_actual_contract_timezone_and_settlement(self):
        q = bridge.normalize_quote("DCE.c2701", raw(), NOW)
        self.assertEqual(q["symbol"], "c2701")
        self.assertEqual(q["observed_at"], "2026-10-28T07:00:00.000001Z")
        self.assertEqual(q["trade_date"], "2026-10-28")
        self.assertEqual(q["change_basis"], "previous_settlement")
        self.assertAlmostEqual(q["change_pct"], (2224 / 2208 - 1) * 100)
        self.assertNotIn("received_at", q)

    def test_invalid_data_are_not_exported(self):
        cases = {"last_price": float("nan"), "pre_settlement": 0, "highest": 2100,
                 "volume": 1.5, "open_interest": float("inf"), "expired": True,
                 "product_id": "m", "ins_class": "CONT", "exchange_id": "SHFE",
                 "instrument_id": "DCE.c2703", "datetime": "2026-10-29 15:00:00"}
        for key, value in cases.items():
            with self.subTest(key=key), self.assertRaises((ValueError, TypeError)):
                bridge.normalize_quote("DCE.c2701", {**raw(), key: value}, NOW)
        for symbol in ("KQ.m@DCE.c", "DCE.c0", "DCE.C2701", "DCE.c2713", "DCE.c2701-C-2000"):
            with self.subTest(symbol=symbol), self.assertRaises(ValueError):
                bridge.normalize_quote(symbol, raw(symbol), NOW)

    def test_night_calendar_date_not_next_day_guess(self):
        q = raw()
        q["datetime"] = "2026-10-28 21:01:00"
        actual = bridge.normalize_quote("DCE.c2701", q, NOW.replace(hour=21, minute=2))
        self.assertEqual(actual["trade_date"], "2026-10-28")

    def test_config_required_and_secrets_not_in_repr(self):
        with self.assertRaisesRegex(ValueError, "缺少环境变量"):
            bridge.load_config({})
        env = {"TQ_USER": "private-user", "TQ_PASSWORD": "private-password", "CORNWATCH_BRIDGE_TOKEN": TOKEN}
        config = bridge.load_config(env)
        for secret in env.values():
            self.assertNotIn(secret, repr(config))
        for invalid in ("short", "a" * 31, "a" * 32 + " ", "中" * 32):
            with self.assertRaises(ValueError):
                bridge.load_config({**env, "CORNWATCH_BRIDGE_TOKEN": invalid})


class StoreTests(unittest.TestCase):
    def test_receipt_preserved_when_unchanged(self):
        store = bridge.SnapshotStore()
        rows = all_rows()
        store.publish(rows, NOW)
        first = store.response(NOW)[1]
        later = NOW + timedelta(seconds=2)
        store.publish(rows, later)
        second = store.response(later)[1]
        self.assertNotEqual(first["generated_at"], second["generated_at"])
        self.assertEqual(first["quotes"], second["quotes"])
        rows[0] = {**rows[0], "last": 2225}
        store.publish(rows, later)
        self.assertNotEqual(first["quotes"][0]["received_at"], store.response(later)[1]["quotes"][0]["received_at"])

    def test_pending_failed_or_stalled_is_503(self):
        store = bridge.SnapshotStore()
        self.assertEqual(store.response(NOW)[0], 503)
        store.publish(all_rows()[:1], NOW)
        self.assertEqual(store.response(NOW)[0], 503)
        store.publish(all_rows(), NOW)
        self.assertEqual(store.response(NOW)[0], 200)
        self.assertEqual(store.response(NOW + timedelta(seconds=16))[0], 503)
        store.unavailable("connection_failed")
        self.assertEqual(store.response(NOW)[0], 503)

    def test_http_snapshot_cannot_mutate_store(self):
        store = bridge.SnapshotStore()
        store.publish(all_rows(), NOW)
        payload = store.response(NOW)[1]
        payload["quotes"][0]["last"] = -1
        self.assertGreater(store.response(NOW)[1]["quotes"][0]["last"], 0)


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.store = bridge.SnapshotStore()
        self.now = NOW
        self.server = bridge.make_server(self.store, TOKEN, 0, clock=lambda: self.now)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def request(self, method="GET", path="/quotes", headers=None):
        conn = http.client.HTTPConnection("127.0.0.1", self.server.server_port, timeout=2)
        try:
            conn.request(method, path, headers=headers or {})
            res = conn.getresponse()
            return res.status, dict(res.getheaders()), json.loads(res.read())
        finally:
            conn.close()

    def test_authentication_and_read_only_methods(self):
        self.assertEqual(self.request()[0], 401)
        self.assertEqual(self.request(headers={"Authorization": "Bearer wrong"})[0], 401)
        self.assertEqual(self.request(headers={"Authorization": "Bearer " + TOKEN})[0], 503)
        self.assertEqual(self.request(method="POST")[0], 405)
        self.assertEqual(self.request(method="OPTIONS")[0], 405)
        self.assertEqual(self.request(path="/quotes?token=" + TOKEN)[0], 401)
        self.assertEqual(self.request(path="/unknown", headers={"Authorization": "Bearer " + TOKEN})[0], 404)

    def test_local_host_origin_and_duplicate_auth(self):
        headers = {"Authorization": "Bearer " + TOKEN}
        self.assertEqual(self.request(headers={**headers, "Host": "attacker.invalid"})[0], 403)
        self.assertEqual(self.request(headers={**headers, "Origin": "https://example.com"})[0], 403)
        conn = http.client.HTTPConnection("127.0.0.1", self.server.server_port, timeout=2)
        conn.putrequest("GET", "/quotes")
        conn.putheader("Authorization", "Bearer " + TOKEN)
        conn.putheader("Authorization", "Bearer " + TOKEN)
        conn.endheaders()
        response = conn.getresponse()
        self.assertEqual(response.status, 401)
        response.read()
        conn.close()

    def test_get_exports_quote_without_retimestamp(self):
        self.store.publish(all_rows(), self.now)
        status, headers, payload = self.request(headers={"Authorization": "Bearer " + TOKEN})
        self.assertEqual(status, 200)
        self.assertEqual(len(payload["quotes"]), 5)
        self.assertEqual(headers["Cache-Control"], "no-store")
        self.now += timedelta(seconds=3)
        second = self.request(headers={"Authorization": "Bearer " + TOKEN})[2]
        self.assertEqual(payload["quotes"], second["quotes"])
        self.assertNotEqual(payload["generated_at"], second["generated_at"])
        self.assertNotIn(TOKEN, json.dumps(payload))


class WorkerTests(unittest.TestCase):
    def test_only_read_functions_and_explicit_local_simulation(self):
        seen = {}
        fake = SimpleNamespace(TqSim=lambda: "local-sim", TqAuth=lambda u, p: (u, p),
                               TqApi=lambda **kwargs: seen.update(kwargs) or "api")
        config = bridge.Config("user", "secret", TOKEN)
        with patch.dict(sys.modules, {"tqsdk": fake}):
            self.assertEqual(bridge.sdk_factory(config), "api")
        self.assertEqual(seen["account"], "local-sim")
        self.assertFalse(seen["debug"])

    def test_universe_filters_continuous_and_refresh_removes_old(self):
        class FakeAPI:
            def query_quotes(self, **kwargs):
                self.options = kwargs
                return ["DCE.c2703", "KQ.m@DCE.c", "SHFE.rb2701", "DCE.c0"]
            def get_quote(self, symbol):
                return raw(symbol)
        api = FakeAPI()
        quotes = bridge.refresh_universe(api, {"DCE.c2701": raw()})
        self.assertEqual(set(quotes), {"DCE.c2703"})
        self.assertEqual(api.options["expired"], False)

    def test_worker_exports_complete_valid_actual_set(self):
        stop = threading.Event()
        store = bridge.SnapshotStore()
        class FakeAPI:
            def query_quotes(self, **kwargs):
                return ["DCE.c2701", "DCE.c2703", "DCE.cs2701", "DCE.lh2701", "DCE.m2701"]
            def get_quote(self, symbol):
                return raw(symbol)
            def wait_update(self, deadline):
                stop.set()
            def close(self):
                self.closed = True
        api = FakeAPI()
        bridge.run_worker(bridge.Config("user", "secret", TOKEN), store, stop, factory=lambda _: api, clock=lambda: NOW)
        self.assertTrue(api.closed)
        self.assertEqual(store.response(NOW)[0], 200)

    def test_error_does_not_expose_credentials(self):
        stop = threading.Event()
        store = bridge.SnapshotStore()
        def broken(_):
            stop.set()
            raise ValueError("SECRET-PASSWORD")
        output = io.StringIO()
        with contextlib.redirect_stderr(output):
            bridge.run_worker(bridge.Config("user", "secret", TOKEN), store, stop, factory=broken, clock=lambda: NOW)
        self.assertNotIn("SECRET-PASSWORD", output.getvalue())
        self.assertEqual(store.response(NOW)[0], 503)


if __name__ == "__main__":
    unittest.main()
