import hashlib
import json
import os
import tempfile
import time
import unittest
from pathlib import Path
from unittest import mock

from services.dashboard import server


class PlanningWatchdogTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.run = Path(self.tmp.name) / "book"
        self.nd = self.run / "output/novel"
        self.gid = "pg2_current"
        self.ws = self.run / ".project-all" / self.gid / "output/novel"
        self.gen = self.nd / "meta/planning/v2/.building" / self.gid
        self.now = time.time()
        self.identity = "sha256:" + "c" * 64
        self.pipe = {"run_identity": self.identity, "stages": ["project-all", "seal"], "completed": []}
        self.formal = {"state": "building", "generation_id": self.gid}
        self.write(self.nd / "meta/pipeline.json", self.pipe)
        self.write(self.nd / "meta/progress.json", {"phase": "writing", "current_chapter": 2,
                   "completed_chapters": [], "total_chapters": 4})
        self.write(self.gen / "generation.json", {"generation_id": self.gid, "status": "building",
                   "base_canon_chapter": 1, "first_projected_chapter": 2, "last_projected_chapter": 4,
                   "expected_chapter_count": 3})
        self.write(self.gen / "chapters/0002.bundle.json", {})
        self.write(self.nd / "meta/planning/v2/projection_cursor.json", {"generation_id": self.gid})
        roots = {"foundation_snapshot_root": "sha256:" + "a" * 64, "rag_snapshot_root": "sha256:" + "b" * 64}
        self.write(self.gen / "source_snapshot.json", {"generation_id": self.gid, "base_canon_chapter": 1, **roots})
        self.manifest = {"version": "project-all-workspace.v3", "generation_id": self.gid,
                         "base_chapter": 1, "source_output": str(self.nd), "workspace": str(self.ws),
                         "isolated_writes": True, **roots}
        self.write(self.ws / "meta/project_all_workspace_manifest.json", self.manifest)
        self.lease = {"version": 1, "mode": "project_all", "target_chapter": 2, "process_id": os.getpid(),
                      "owner": "live", "acquired_at": server.iso_time(self.now - 20),
                      "expires_at": server.iso_time(self.now + 3600)}
        self.write(self.nd / "meta/runtime/pipeline_execution.json", self.lease)
        self.write(self.ws / "meta/runtime/pipeline_execution.json", dict(self.lease, owner="shadow", target_chapter=3))
        self.raw = {"schema": "pipeline-watchdog.v1", "run_identity": self.identity,
                    "invocation_id": self.identity + ":project-all", "stage": "project-all", "status": "running",
                    "generation_id": self.gid, "chapter": 3, "cycle": 8, "round": 2,
                    "planning_phase": "arbitration_committed", "progress_artifact_digest": "sha256:" + "d" * 64,
                    "progress_seq": 77, "last_progress_kind": "arbitration_committed",
                    "started_at": server.iso_time(self.now - 600), "last_progress_at": server.iso_time(self.now - 120),
                    "heartbeat_at": server.iso_time(self.now - 1),
                    "planning_event_keys": ["PRIVATE-DEDUP"], "prompt": "PRIVATE-PROMPT", "cost_usd": 400}
        self.path = server.pipeline_watchdog_path(self.nd)
        self.write(self.path, self.raw)

    def write(self, path, value):
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value), encoding="utf-8")

    def snapshot(self):
        ws, projection = server.current_character_planning_workspace(self.nd, self.formal)
        runtime = {"current_stage": "project-all", "execution": server.pipeline_execution_state(self.nd)}
        return server.current_planning_watchdog(self.nd, self.pipe, runtime, ws, projection, self.now)

    def test_one_public_snapshot_preserves_progress_not_heartbeat_or_usage(self):
        reads = []
        original = Path.open

        def observed(path, *args, **kwargs):
            reads.append(path)
            self.assertNotIn("character_agents", str(path))
            return original(path, *args, **kwargs)

        before = self.path.read_bytes()
        with mock.patch.object(Path, "open", observed):
            value = self.snapshot()
        self.assertEqual(reads.count(self.path), 1)
        self.assertEqual(value["chapter"], 3)
        self.assertEqual(value["cycle"], 8)
        self.assertEqual(value["round"], 2)
        self.assertEqual(value["progress_seq"], 77)
        self.assertEqual(value["last_progress_at"], self.raw["last_progress_at"])
        self.assertNotEqual(value["last_progress_at"], value["heartbeat_at"])
        self.assertNotIn("PRIVATE-", json.dumps(value))
        self.assertNotIn("cost_usd", value)
        self.assertNotIn("planning_event_keys", value)
        self.assertEqual(self.path.read_bytes(), before)
        self.write(self.path, dict(self.raw, heartbeat_at=server.iso_time(self.now), cost_usd=500))
        updated = self.snapshot()
        self.assertEqual(updated["progress_seq"], value["progress_seq"])
        self.assertEqual(updated["last_progress_at"], value["last_progress_at"])

    def test_summary_keeps_accepted_and_formal_counts_separate_from_live_target_cycle(self):
        with mock.patch.object(server, "active_rag_processes", return_value=[]):
            data = server.summarize_run(self.run)
        self.assertEqual(data["working"]["chapter"], 0)
        self.assertEqual(data["working"]["target_chapter"], 3)  # Shadow target, not live arc start 2.
        self.assertEqual(data["working"]["cycle"], 8)
        self.assertEqual(data["chapters_completed"], 0)
        self.assertEqual(data["formal_planning"]["planned_chapters"], 1)
        self.assertEqual(data["formal_planning"]["expected_chapters"], 3)

    def test_context_binding_and_stalled_heartbeat_do_not_invent_commits(self):
        self.write(self.path, dict(self.raw, planning_phase="context_bound", cycle=9, round=0,
                                  progress_artifact_digest="", status="stalled"))
        value = self.snapshot()
        self.assertEqual(value["cycle"], 9)
        self.assertEqual(value["status"], "stalled")
        self.assertEqual(value["progress_seq"], 77)
        self.assertEqual(value["last_progress_at"], self.raw["last_progress_at"])
        self.write(self.path, dict(self.raw, planning_phase="context_bound", progress_artifact_digest="",
                                  progress_seq=0, last_progress_kind="stage_started"))
        self.assertEqual(self.snapshot()["last_progress_kind"], "")

    def test_legacy_or_missing_watchdog_keeps_bound_target_without_claiming_a_cycle(self):
        self.write(self.path, {"schema": "pipeline-watchdog.v1", "stage": "project-all"})
        with mock.patch.object(server, "active_rag_processes", return_value=[]):
            data = server.summarize_run(self.run)
        self.assertEqual(data["working"]["chapter"], 0)
        self.assertEqual(data["working"]["target_chapter"], 3)
        self.assertEqual(data["working"]["cycle"], 0)
        self.assertNotIn("planning_progress", data["runtime"])
        self.assertEqual(data["formal_planning"]["planned_chapters"], 1)

    def test_stale_foreign_malformed_or_legacy_monitor_falls_back(self):
        changes = [
            {"schema": "wrong"}, {"generation_id": "pg2_abandoned"}, {"chapter": 2},
            {"run_identity": "foreign"}, {"invocation_id": self.identity + ":render"}, {"stage": "render"},
            {"status": "stopped"}, {"stopped_at": self.raw["heartbeat_at"]},
            {"heartbeat_at": server.iso_time(self.now - 30)},  # Before this invocation's leases.
            {"heartbeat_at": server.iso_time(self.now - 100)}, {"heartbeat_at": server.iso_time(self.now + 100)},
            {"progress_seq": True}, {"cycle": 65}, {"round": -1}, {"chapter": "3"},
            {"planning_phase": "token_received"}, {"progress_artifact_digest": "not-a-digest"},
            {"last_progress_at": "bad"}, {"last_progress_at": server.iso_time(self.now + 10)},
            {"generation_id": "", "planning_phase": ""},
        ]
        for change in changes:
            with self.subTest(change=change):
                self.write(self.path, dict(self.raw, **change))
                before = self.path.read_bytes()
                self.assertIsNone(self.snapshot())
                self.assertEqual(self.path.read_bytes(), before)
        for raw in (b"broken JSON", b"[]", b"\xff", b" " * (server.PLANNING_WATCHDOG_MAX_BYTES + 1)):
            self.path.write_bytes(raw)
            self.assertIsNone(self.snapshot())
        self.path.unlink()
        self.assertIsNone(self.snapshot())

    def test_workspace_and_execution_binding_required_even_for_fresh_watchdog(self):
        lease_path = self.ws / "meta/runtime/pipeline_execution.json"
        for change in ({"process_id": os.getpid() + 100000}, {"mode": "preplan"},
                       {"target_chapter": 4}, {"expires_at": server.iso_time(self.now - 1)},
                       {"acquired_at": server.iso_time(self.now)}):
            with self.subTest(change=change):
                self.write(lease_path, dict(self.lease, target_chapter=3) | change)
                self.assertIsNone(self.snapshot())
        self.write(lease_path, dict(self.lease, target_chapter=3))
        self.write(self.ws / "meta/project_all_workspace_manifest.json", dict(self.manifest, source_output="foreign"))
        self.assertIsNone(self.snapshot())
        self.write(self.ws / "meta/project_all_workspace_manifest.json", self.manifest)
        self.path.unlink()
        target = Path(self.tmp.name) / "foreign-monitor.json"
        self.write(target, self.raw)
        self.path.symlink_to(target)
        self.assertIsNone(self.snapshot())

    def test_external_path_matches_go_opaque_name_and_absolute_root_hash(self):
        nd = Path(self.tmp.name) / "output" / "中文书"
        token = hashlib.sha256("中文书".encode()).hexdigest()
        suffix = hashlib.sha256(str(nd).encode()).hexdigest()[:16]
        self.assertEqual(server.pipeline_watchdog_path(nd),
                         nd.parent / ".pipeline-runtime" / f"{token}-{suffix}" / "meta/runtime/pipeline_watchdog.json")


if __name__ == "__main__":
    unittest.main()
