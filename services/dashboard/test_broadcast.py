import json
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from http.server import ThreadingHTTPServer
from pathlib import Path
from unittest import mock

from services.dashboard import server


class BroadcastTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.runs = Path(self.tmp.name)
        self.run = self.runs / "internal-directory-name"
        self.nd = self.run / "output/novel"
        (self.nd / "meta").mkdir(parents=True)
        (self.nd / "meta/progress.json").write_text(json.dumps({"novel_name": "直播测试书", "completed_chapters": [], "total_word_count": 0}), encoding="utf-8")
        self.patch = mock.patch.object(server, "RUNS_DIR", self.runs)
        self.patch.start()
        with server._broadcast_cache_lock:
            server._broadcast_cache.clear()
        self.summary = {"name": "直播测试书", "chapters_total": 3, "chapters_completed": 0,
            "chapters_formally_planned": 1, "words_total": 0, "updated_at": 1700000000,
            "formal_planning": {"state": "building", "generation_id": "pg2_current"},
            "working": {"target_chapter": 2, "cycle": 9, "round": 2, "last_error": "PRIVATE_ERROR"},
            "runtime": {"status": "running", "current_stage": "project-all", "process_id": 987654,
                "log": "PRIVATE_LOG", "model": "PRIVATE_MODEL", "recent_events": [],
                "planning_progress": {"planning_phase": "proposal_committed", "last_progress_kind": "proposal_committed",
                    "status": "running", "chapter": 2, "cycle": 9, "round": 2, "progress_seq": 17,
                    "last_progress_at": "2023-11-14T22:13:20+00:00", "heartbeat_at": "2023-11-14T22:13:21+00:00"}}}
        self.usage = {"usage_calls": 3, "tokens_in": 400, "tokens_out": 30, "cost_usd": .02,
            "cost_sources": {"reported": 1, "estimated": 1, "unknown": 1}, "unpriced_calls": 1}
        self.agents = {"usage_summary": self.usage, "characters": [{"agent_id": "private-id", "character": "林澄", "tier": "core", "status": "active",
            "activation_cycle": 9, "decision_cycle": 9, "proposal_cycle": 9, "proposal_round": 2,
            "arbitration_cycle": 9, "arbitration_round": 1, "arbitration_outcome": "partial", "memory_chapter": 1,
            "recent_decision": "PRIVATE_PLOT", "recent_action": "PRIVATE_ACTION", "decision_reason": "PRIVATE_REASON",
            "memory": ["PRIVATE_MEMORY"], "arbitration_result": "PRIVATE_RESULT", **self.usage}]}

    def tearDown(self):
        self.patch.stop()
        self.tmp.cleanup()

    def snapshot(self):
        with mock.patch.object(server, "summarize_run", return_value=self.summary), \
                mock.patch.object(server, "current_character_planning_workspace", return_value=(None, None)), \
                mock.patch.object(server, "character_agent_payload", return_value=self.agents):
            return server.broadcast_detail(server._broadcast_id(self.run.name))

    def test_whitelist_cost_uncertainty_and_real_milestone(self):
        data = self.snapshot()
        wire = json.dumps(data, allow_nan=False)
        for secret in ("PRIVATE", "987654", self.tmp.name, "internal-directory-name", "private-id", "pg2_current"):
            self.assertNotIn(secret, wire)
        self.assertEqual(set(data), {"schema", "id", "title", "status", "stage", "updated_at", "server_time", "chapter", "planning", "execution", "characters", "usage", "events"})
        self.assertEqual(data["usage"]["cost_source"], "partial")
        self.assertEqual(data["usage"]["scope"], "character_agents")
        self.assertEqual(data["usage"]["cost_usd"], .02)
        self.assertTrue(data["characters"][0]["submitted"])
        self.assertEqual(data["characters"][0]["round"], 2)
        self.assertEqual(data["events"][0]["kind"], "proposal_committed")
        self.assertNotIn("summary", data["events"][0])
        self.assertIsNone(data["planning"]["story_minutes"])

    def test_missing_and_nonfinite_values_never_become_completed_or_free(self):
        self.summary["chapters_total"] = float("nan")
        self.summary["words_total"] = float("inf")
        self.summary["updated_at"] = float("inf")
        self.summary["runtime"]["status"] = "PRIVATE_STATUS"
        self.summary["runtime"]["current_stage"] = "PRIVATE_STAGE"
        self.agents["usage_summary"] = {"cost_usd": float("nan"), "usage_calls": 1,
            "tokens_in": float("inf"), "cost_sources": {"unknown": 1}}
        data = self.snapshot()
        json.dumps(data, allow_nan=False)
        self.assertEqual(data["status"], "unknown")
        self.assertIsNone(data["stage"])
        self.assertIsNone(data["chapter"]["total"])
        self.assertIsNone(data["chapter"]["words"])
        self.assertIsNone(data["usage"]["cost_usd"])
        self.assertEqual(data["usage"]["cost_source"], "unknown")
        self.assertIsNone(data["usage"]["input_tokens"])
        self.assertIsNone(server._broadcast_usage({})["calls"])
        zero = server._broadcast_usage({"usage_calls": 1, "cost_usd": 0, "cost_sources": {"reported": 1}})
        self.assertEqual(zero["cost_usd"], 0)
        self.assertEqual(zero["cost_source"], "reported")
        self.assertEqual(server._broadcast_status({"runtime": {"status": "complete"}}), "unknown")
        self.assertEqual(server._broadcast_status({"runtime": {"status": "complete"}, "chapters_total": 3, "chapters_completed": 1}), "attention")

    def test_old_cycle_submission_and_outcome_are_not_current(self):
        self.agents["characters"][0].update(activation_cycle=10)
        row = self.snapshot()["characters"][0]
        self.assertFalse(row["submitted"])
        self.assertIsNone(row["outcome"])
        self.assertIsNone(row["round"])

    def test_only_bound_session_supplies_actual_clock_and_completed_cycles(self):
        ws = self.run / ".project-all/pg2_current/output/novel"
        path = ws / "meta/character_agents/activation_sessions/pg2_current/000002/session.json"
        path.parent.mkdir(parents=True)
        session = {"version": "character-activation-session.v1", "generation_id": "pg2_current", "chapter": 2,
            "phase": "assessing", "current_day": 106 / 1440, "initial_day": 0, "max_cycles": 32, "digest": "sha256:" + "c" * 64,
            "cycle_digests": ["sha256:" + "a" * 64] * 8, "readiness_digests": ["sha256:" + "b" * 64] * 7}
        path.write_text(json.dumps(session), encoding="utf-8")
        projection = {"generation_id": "pg2_current", "chapter": 2, "first_chapter": 1, "last_chapter": 3}
        with mock.patch.object(server, "summarize_run", return_value=self.summary), \
                mock.patch.object(server, "current_character_planning_workspace", return_value=(ws, projection)), \
                mock.patch.object(server, "character_agent_payload", return_value=self.agents):
            data = server._build_broadcast_snapshot(self.run)
        self.assertAlmostEqual(data["planning"]["story_minutes"], 106)
        self.assertEqual(data["planning"]["completed_cycles"], 8)
        session["generation_id"] = "pg2_other"
        path.write_text(json.dumps(session), encoding="utf-8")
        self.assertEqual(server._broadcast_session(ws, projection, 2), ({}, None))

    def test_event_whitelist_and_limit(self):
        events = [{"category": "PIPELINE", "stage": "rehearse-arc", "finished_at": f"2023-11-14T22:13:{i:02d}+00:00",
                   "failed": False, "detail": "PRIVATE_ERROR"} for i in range(25)]
        events += [{"category": "TOOL", "stage": "render", "finished_at": "2023-11-14T22:13:40Z", "failed": False,
                    "summary": "fictional milestone PRIVATE"}]
        safe = server._broadcast_events({"recent_events": events})
        self.assertEqual(len(safe), 16)
        self.assertTrue(all(e["kind"] == "stage_completed" and e["stage"] == "rehearse-arc" for e in safe))
        self.assertNotIn("PRIVATE", json.dumps(safe))
        self.assertGreater(server.timestamp(safe[0]["time"]), server.timestamp(safe[-1]["time"]))

    def terminal_fixture(self):
        ws = self.run / ".project-all/pg2_current/output/novel"
        root = ws / "meta/character_agents/activation_sessions/pg2_current/000002"
        def write(path, value):
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(json.dumps(value), encoding="utf-8")
        identity = {"generation_id": "pg2_current", "chapter": 2}
        digest = lambda char: "sha256:" + char * 64
        session = {**identity, "version": "character-activation-session.v1", "digest": digest("c"), "phase": "collecting",
            "initial_day": 0, "current_day": .1, "max_cycles": 32,
            "cycle_digests": [digest("a")] * 25, "readiness_digests": [digest("b")] * 25}
        write(root / "session.json", session)
        for index in (25, 26):
            proof = root / "work" / f"{index:06d}" / "proof"
            write(proof / "activation.json", {**identity, "digest": digest("d"),
                "entries": [{"agent_id": "private-id", "state": "active", "observation_digest": digest("e")}]})
        proof = root / "work/000026/proof"
        continuation = {**identity, "version": "character-work-continuation:explicit.v1", "cycle": 26, "agent_id": "private-id",
            "input_set_digest": digest("f"), "observation_digest": digest("e"), "digest": digest("a")}
        admission = {"version": "character-arbitration-admission.v3", "session_digest": session["digest"],
            "input_digest": digest("f"), "protocol": digest("e"), "continuations": [continuation]}
        write(proof / "round_sources_v3.json", admission)
        write(root / "cycles/000025.json", {**identity, "index": 25, "digest": digest("a")})
        write(root / "readiness/000025.json", {**identity, "digest": digest("b"), "cycle_digest": digest("a")})
        write(root / "work/000025/proof/proposals/round-01/private-id.json", {**identity, "agent_id": "private-id", "round": 1,
            "digest": digest("f"), "generated_at": "2023-11-14T22:15:00Z", "decision": "PRIVATE_PLOT"})
        self.summary["working"] = {"target_chapter": 0}
        self.summary["runtime"] = {"status": "error", "current_stage": "project-all", "recent_events": []}
        self.summary["formal_planning"]["planned_chapters"] = 1
        self.agents["characters"][0].update(evidence_scope="projected", evidence_chapter=2, activation_cycle=26,
            decision_cycle=25, proposal_cycle=25, arbitration_cycle=25)
        projection = {"generation_id": "pg2_current", "first_chapter": 1, "last_chapter": 3, "chapter": 0, "state": "paused"}
        return ws, root, projection, session, admission, write

    def test_terminal_bound_position_and_recent_events_do_not_claim_running(self):
        ws, root, projection, session, admission, write = self.terminal_fixture()
        with mock.patch.object(server, "summarize_run", return_value=self.summary), \
                mock.patch.object(server, "current_character_planning_workspace", return_value=(ws, projection)), \
                mock.patch.object(server, "character_agent_payload", return_value=self.agents), \
                mock.patch.object(server, "read_json", wraps=server.read_json) as reads:
            data = server._build_broadcast_snapshot(self.run)
        self.assertEqual(data["status"], "error")
        self.assertEqual(data["chapter"]["current"], 2)
        self.assertEqual(data["planning"]["cycle"], 26)
        self.assertEqual(data["planning"]["completed_cycles"], 25)
        self.assertAlmostEqual(data["planning"]["story_minutes"], 144)
        self.assertEqual(data["execution"]["phase"], "collecting")
        self.assertIsNotNone(data["execution"]["last_progress_at"])
        self.assertIsNone(data["execution"]["heartbeat_at"])
        self.assertEqual(data["characters"][0]["state"], "continuing")
        self.assertFalse(data["characters"][0]["submitted"])
        self.assertIn("cycle_committed", [x["kind"] for x in data["events"]])
        self.assertIn("readiness_committed", [x["kind"] for x in data["events"]])
        self.assertEqual([server.timestamp(e["time"]) for e in data["events"]], sorted((server.timestamp(e["time"]) for e in data["events"]), reverse=True))
        self.assertFalse(any("observations" in str(call.args[0]) or "inputs.json" in str(call.args[0]) for call in reads.call_args_list))
        self.assertNotIn("PRIVATE", json.dumps(data))
        # A same-range directory with a newer mtime cannot override formal
        # next chapter or supply a foreign generation's session.
        write(root.parent / "000003/session.json", dict(session, chapter=3, generation_id="pg2_foreign"))
        self.assertEqual(server._broadcast_position(ws, projection, self.summary["formal_planning"], {}, self.agents)[0], 2)
        write(root / "session.json", dict(session, generation_id="pg2_foreign"))
        self.assertIsNone(server._broadcast_position(ws, projection, self.summary["formal_planning"], {}, self.agents)[2])

    def test_continuation_requires_current_admission_and_revision_supersedes_it(self):
        ws, root, projection, session, admission, write = self.terminal_fixture()
        self.assertEqual(server._broadcast_session_activity(ws, session, root / "session.json")[1], {"private-id"})
        write(root / "work/000026/proof/round_sources_v3.json", dict(admission, session_digest="sha256:" + "0" * 64))
        self.assertEqual(server._broadcast_session_activity(ws, session, root / "session.json")[1], set())
        write(root / "work/000026/proof/round_sources_v3.json", admission)
        write(root / "work/000026/proof/proposals/round-02/private-id.json", {"generation_id": "pg2_current", "chapter": 2,
            "agent_id": "private-id", "round": 2, "digest": "sha256:" + "e" * 64})
        self.assertEqual(server._broadcast_session_activity(ws, session, root / "session.json")[1], set())

    def test_assessing_continuation_uses_only_exact_last_cycle(self):
        ws, root, projection, session, admission, write = self.terminal_fixture()
        session = dict(session, phase="assessing", readiness_digests=session["readiness_digests"][:-1])
        write(root / "session.json", session)
        continuation = dict(admission["continuations"][0], cycle=25)
        cycle = {"generation_id": "pg2_current", "chapter": 2, "index": 25,
            "digest": session["cycle_digests"][-1], "input_set_digest": admission["input_digest"],
            "work_continuations": [continuation]}
        write(root / "cycles/000025.json", cycle)
        self.assertEqual(server._broadcast_session_activity(ws, session, root / "session.json")[1], {"private-id"})
        write(root / "cycles/000025.json", dict(cycle, digest="sha256:" + "0" * 64))
        self.assertEqual(server._broadcast_session_activity(ws, session, root / "session.json")[1], set())

    def test_cache_single_flight_expiry_and_detached_results(self):
        calls = []
        def build():
            calls.append(1)
            time.sleep(.02)
            return {"safe": [len(calls)]}
        with ThreadPoolExecutor(max_workers=8) as pool:
            result = list(pool.map(lambda _: server._broadcast_cached("same", build), range(8)))
        self.assertEqual(len(calls), 1)
        result[0]["safe"].append("mutation")
        self.assertEqual(server._broadcast_cached("same", build), {"safe": [1]})
        with mock.patch.object(server.time, "monotonic", return_value=time.monotonic() + 3):
            self.assertEqual(server._broadcast_cached("same", build), {"safe": [2]})

    def test_catalog_opaque_id_and_symlink_escape_rejection(self):
        with mock.patch.object(server, "summarize_run", return_value=self.summary):
            catalog = server.broadcast_catalog()
        self.assertEqual(set(catalog), {"novels", "server_time"})
        self.assertEqual(set(catalog["novels"][0]), {"id", "title", "status"})
        self.assertRegex(catalog["novels"][0]["id"], r"^book_[0-9a-f]{16}$")
        for bad in (self.run.name, "../escape", "/etc/passwd", "book_" + "0" * 16):
            self.assertIsNone(server.broadcast_detail(bad))
        outside = Path(tempfile.mkdtemp())
        try:
            (outside / "output/novel/meta").mkdir(parents=True)
            (self.runs / "escape").symlink_to(outside, target_is_directory=True)
            self.assertNotIn(self.runs / "escape", server._broadcast_runs())
        finally:
            (outside / "output/novel/meta").rmdir()
            (outside / "output/novel").rmdir()
            (outside / "output").rmdir()
            outside.rmdir()

    def test_http_broadcast_errors_never_include_exception_or_path(self):
        httpd = ThreadingHTTPServer(("127.0.0.1", 0), server.Handler)
        thread = threading.Thread(target=httpd.serve_forever, daemon=True)
        thread.start()
        try:
            base = f"http://127.0.0.1:{httpd.server_port}"
            with mock.patch.object(server, "broadcast_catalog", side_effect=RuntimeError("PRIVATE /Users/secret/config")):
                with self.assertRaises(urllib.error.HTTPError) as caught:
                    urllib.request.urlopen(base + "/api/broadcast")
                self.assertEqual(caught.exception.code, 503)
                self.assertEqual(json.loads(caught.exception.read()), {"error": "broadcast unavailable"})
                caught.exception.close()
            with mock.patch.object(server, "broadcast_catalog", return_value={"novels": [], "server_time": 1}):
                with urllib.request.urlopen(base + "/api/broadcast") as response:
                    self.assertEqual(json.load(response), {"novels": [], "server_time": 1})
            with mock.patch.object(server, "broadcast_catalog", return_value={"server_time": float("nan")}):
                with self.assertRaises(urllib.error.HTTPError) as caught:
                    urllib.request.urlopen(base + "/api/broadcast")
                self.assertEqual(caught.exception.code, 503)
                self.assertEqual(json.loads(caught.exception.read()), {"error": "broadcast unavailable"})
                caught.exception.close()
            with self.assertRaises(urllib.error.HTTPError) as caught:
                urllib.request.urlopen(base + "/api/novels/not-an-id/broadcast")
            self.assertEqual(caught.exception.code, 404)
            caught.exception.close()
            static = self.runs / "static"
            static.mkdir()
            for name in ("broadcast.html", "broadcast.css", "broadcast.js"):
                (static / name).write_text("safe " + name, encoding="utf-8")
            with mock.patch.object(server, "STATIC_DIR", static):
                for route, name in (("/broadcast", "broadcast.html"), ("/broadcast.html", "broadcast.html"), ("/broadcast.css", "broadcast.css"), ("/broadcast.js", "broadcast.js")):
                    with urllib.request.urlopen(base + route) as response:
                        self.assertEqual(response.read().decode(), "safe " + name)
        finally:
            httpd.shutdown()
            httpd.server_close()
            thread.join()


if __name__ == "__main__":
    unittest.main()
