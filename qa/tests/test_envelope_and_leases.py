"""E-M envelope regimes, lane ordering, flock leases, reset parsing, and quota migration."""

from datetime import datetime, timedelta, timezone
import os
import subprocess
import tempfile
import unittest
from unittest import mock

from envelope import build_candidate_order, compute_em_envelope, select_provider
from fleet import (
    acquire_parallel_lease,
    atomic_write_json,
    count_active_leases,
    default_quota_data,
    extract_reset_timestamp,
    load_quota,
    migrate_quota_v3,
    parse_iso_utc,
    probe_muse_usage,
    release_parallel_lease,
)

NOW = datetime(2026, 1, 1, 12, 0, tzinfo=timezone.utc)
CAPPED = ["agy-opus", "muse", "grok", "mistral"]


def _iso(minutes, base=None):
    """Return an ISO timestamp `minutes` after base (default: the real current UTC time)."""
    return ((base or datetime.now(timezone.utc)) + timedelta(minutes=minutes)).isoformat()


def _lane(tier, status="active", reset=None, cooldown=None):
    """Build a minimal provider record for envelope tests."""
    return {"tier": tier, "status": status, "next_reset_at": reset, "cooldown_until": cooldown}


class EnvelopeAndLeaseTests(unittest.TestCase):
    """Fleet energy envelope, candidate ordering, leases, and quota state tests."""

    def test_em_regimes(self):
        corner = compute_em_envelope({"providers": {
            "agy-opus": _lane("capped", reset=_iso(60, NOW)),
            "muse": _lane("capped"),
        }}, now_utc=NOW)
        self.assertEqual(corner["corner_lanes"], ["agy-opus"])
        self.assertEqual(corner["regimes"], {"agy-opus": "CORNER", "muse": "CRUISE"})
        conserve = compute_em_envelope({"providers": {
            "grok": _lane("capped"),
            "muse": _lane("capped", "cooldown", cooldown=_iso(60, NOW)),
        }}, now_utc=NOW)
        self.assertTrue(conserve["conserve_capped"])
        self.assertEqual(conserve["regimes"], {"grok": "CONSERVE", "muse": "REGEN"})
        reserve = compute_em_envelope({"providers": {
            "devin-fusion": _lane("credits"),
            "agy-opus": _lane("capped", "cooldown", cooldown=_iso(30, NOW)),
        }}, now_utc=NOW)
        self.assertEqual(reserve["regimes"]["devin-fusion"], "RESERVE")
        self.assertFalse(reserve["conserve_capped"])
        regen = compute_em_envelope({"providers": {
            n: _lane("capped", "cooldown", cooldown=_iso(120, NOW)) for n in CAPPED
        }}, now_utc=NOW)
        self.assertEqual(regen["active_capped"], [])
        self.assertEqual(set(regen["regimes"].values()), {"REGEN"})
        cruise = compute_em_envelope({"providers": {
            "grok": _lane("capped"), "mistral": _lane("capped"),
        }}, now_utc=NOW)
        self.assertFalse(cruise["conserve_capped"])
        self.assertEqual(cruise["regimes"], {"grok": "CRUISE", "mistral": "CRUISE"})

    def test_candidate_order(self):
        quota = default_quota_data()
        for idx in range(4):
            order = build_candidate_order("auto", "normal", quota, stripe_index=idx)
            self.assertEqual(order[0], CAPPED[idx])
        self.assertEqual(build_candidate_order("auto", "deep", quota)[:4], CAPPED)
        cornered = default_quota_data()
        cornered["providers"]["grok"]["next_reset_at"] = _iso(30)
        self.assertEqual(build_candidate_order("auto", "normal", cornered)[0], "grok")
        self.assertEqual(build_candidate_order("auto", "normal", cornered, stripe_index=1)[0], "grok")
        self.assertEqual(
            build_candidate_order("auto", "review", quota),
            ["mcode", "devin-fast", "devin-pro", "grok", "mistral", "muse", "agy-opus"],
        )
        review = build_candidate_order("auto", "review", quota, avoid_families={"swe-2"})
        self.assertNotIn("devin-fusion", review)
        self.assertEqual(review[0], "mcode")
        for lane in ("devin-fast", "devin-pro"):
            for peer in ("grok", "mistral", "muse"):
                self.assertGreater(review.index(lane), review.index(peer))
        with tempfile.TemporaryDirectory() as tmp, \
                mock.patch.dict(os.environ, {"ROUTER_HUD_DIR": tmp, "ROUTER_DEPTH": "0"}), \
                mock.patch("envelope.select_lane.resolve_cli", return_value="/opt/fake/cli"):
            name, _, cli_path, note = select_provider(
                "auto", "review", quota, exclude={"mcode"}, avoid_families={"swe-2"})
        self.assertEqual((name, cli_path), ("grok", "/opt/fake/cli"))
        self.assertIn("mcode: already attempted", note)

    def test_parallel_leases(self):
        with tempfile.TemporaryDirectory() as tmp, \
                mock.patch.dict(os.environ, {"ROUTER_HUD_DIR": tmp, "ROUTER_DEPTH": "0"}), \
                mock.patch("shutil.which", return_value=None):
            for lane in ("devin-fast", "mcode"):
                held = [acquire_parallel_lease(lane, 4, blocking=False) for _ in range(4)]
                self.assertTrue(all(h is not None for h in held))
                self.assertEqual(count_active_leases(lane, 4), 4)
                self.assertIsNone(acquire_parallel_lease(lane, 4, blocking=False))
                release_parallel_lease(held.pop(0))
                fifth = acquire_parallel_lease(lane, 4, blocking=False)
                self.assertIsNotNone(fifth)
                held.append(fifth)
                self.assertEqual(count_active_leases(lane, 4), 4)
                for handle in held:
                    release_parallel_lease(handle)
                self.assertEqual(count_active_leases(lane, 4), 0)

    def test_reset_and_migration(self):
        stamp = extract_reset_timestamp("HTTP 429 Too Many Requests: resets at 2099-01-02T03:04:05Z")
        self.assertEqual(parse_iso_utc(stamp), datetime(2099, 1, 2, 3, 4, 5, tzinfo=timezone.utc))
        self.assertIsNone(extract_reset_timestamp("429 quota exceeded, resets at 2001-01-01T00:00:00Z"))
        self.assertIsNone(extract_reset_timestamp("429 quota exceeded"))
        muse_wake = _iso(90)
        data = {
            "version": 6,
            "default_fallback_chain": ["agy-opus", "muse", "devin-fast"],
            "parallel_stripe_pool": ["agy-opus", "devin-fast"],
            "providers": {
                "mcode": {"cli": "mcode", "tier": "high-cap", "max_parallel": None},
                "muse": {"cli": "muse", "tier": "capped", "status": "cooldown",
                         "cooldown_until": muse_wake, "reason": "quota hit", "cycle_hours": 5.0},
            },
        }
        self.assertTrue(migrate_quota_v3(data))
        self.assertEqual(data["version"], 7)
        self.assertEqual(data["providers"]["mcode"]["max_parallel"], 4)
        for pool in ("default_fallback_chain", "parallel_stripe_pool"):
            self.assertIn("grok", data[pool])
            self.assertIn("mistral", data[pool])
        self.assertIn("grok", data["providers"])
        self.assertIn("mistral", data["providers"])
        muse = data["providers"]["muse"]
        self.assertEqual((muse["status"], muse["cooldown_until"]), ("cooldown", muse_wake))
        self.assertEqual(muse["reason"], "quota hit")

    def test_muse_probe_preserves_cooldown(self):
        with tempfile.TemporaryDirectory() as tmp:
            qpath = os.path.join(tmp, "quota.json")
            quota = default_quota_data()
            wake = _iso(120)
            quota["providers"]["muse"].update(status="cooldown", cooldown_until=wake,
                                              next_reset_at=wake, reason="quota hit")
            atomic_write_json(qpath, quota)
            done = subprocess.CompletedProcess(["muse", "usage"], 0,
                                               stdout="warning: server_sent_no_usage\n")
            with mock.patch.dict(os.environ, {"AGENT_QUOTA_FILE": qpath, "ROUTER_DEPTH": "0"}), \
                    mock.patch("fleet.manage_quota.resolve_cli", return_value="/opt/fake/muse"), \
                    mock.patch("fleet.manage_quota.run", return_value=done):
                result = probe_muse_usage(timeout=1)
                after = load_quota()
        self.assertTrue(result["installed"])
        self.assertFalse(result["fresh"])
        self.assertIn("server_sent_no_usage", result["summary"])
        self.assertEqual(after["providers"]["muse"]["status"], "cooldown")
        self.assertEqual(after["providers"]["muse"]["cooldown_until"], wake)


if __name__ == "__main__":
    unittest.main()
