import json
import os
import tempfile
import unittest
from datetime import datetime, timedelta
from pathlib import Path
from unittest import mock

from services.dashboard import server


class CharacterPlanningProjectionTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.run = Path(self.tmp.name) / "book"
        self.nd = self.run / "output/novel"
        self.gid = "pg2_current"
        self.ws = self.run / ".project-all" / self.gid / "output/novel"
        self.generation = self.nd / "meta/planning/v2/.building" / self.gid
        self.formal = {"state": "building", "generation_id": self.gid}
        self.write(self.nd, "meta/progress.json", {"phase": "writing", "current_chapter": 1,
                                                  "completed_chapters": [1], "total_chapters": 4})
        self.write(self.generation, "generation.json", {"generation_id": self.gid, "status": "building",
                   "base_canon_chapter": 1, "first_projected_chapter": 2, "last_projected_chapter": 4,
                   "expected_chapter_count": 3})
        self.write(self.nd, "meta/planning/v2/projection_cursor.json", {"generation_id": self.gid})
        roots = {"foundation_snapshot_root": "sha256:" + "a" * 64, "rag_snapshot_root": "sha256:" + "b" * 64}
        self.write(self.generation, "source_snapshot.json", {"generation_id": self.gid, "base_canon_chapter": 1, **roots})
        self.manifest = {"version": "project-all-workspace.v3", "generation_id": self.gid,
                         "base_chapter": 1, "source_output": str(self.nd), "workspace": str(self.ws),
                         "isolated_writes": True, **roots}
        self.write(self.ws, "meta/project_all_workspace_manifest.json", self.manifest)
        now = datetime.now().astimezone()
        lease = {"version": 1, "mode": "project_all", "target_chapter": 2, "process_id": os.getpid(),
                 "owner": "live", "acquired_at": now.isoformat(), "expires_at": (now + timedelta(hours=1)).isoformat()}
        self.write(self.nd, "meta/runtime/pipeline_execution.json", lease)
        self.write(self.ws, "meta/runtime/pipeline_execution.json", dict(lease, owner="shadow"))
        self.write(self.nd, "meta/character_agents/registry.json", {"entries": [
            {"agent_id": "shared", "character": "共同角色", "tier": "core", "memory_version": 2}]})
        self.write(self.nd, "meta/character_agents/memory/shared.json", {"last_accepted_chapter": 1, "facts": ["PRIVATE-CANON"]})
        self.write(self.ws, "meta/character_agents/registry.json", {"entries": [
            {"agent_id": "shared", "character": "共同角色", "memory_version": 99},
            {"agent_id": "new", "character": "新角色", "memory_version": 99}]})
        self.write(self.ws, "meta/character_agents/memory/shared.json", {"last_accepted_chapter": 99, "facts": ["PRIVATE-SHADOW"]})
        self.chapter = self.ws / "meta/character_agents/projected" / self.gid / "chapters/000002"
        identity = {"generation_id": self.gid, "chapter": 2}
        self.write(self.chapter, "activation.json", {**identity, "entries": [
            {"agent_id": "shared", "state": "active", "reasons": ["scene_appearance"]},
            {"agent_id": "new", "state": "active", "reasons": ["current_pressure"]}]})
        self.write(self.chapter, "proposals/round-01/shared.json", {**identity, "agent_id": "shared", "round": 1,
            "decision": "公开的暂存选择", "intended_action": "公开行动", "decision_reason": "PRIVATE-REASON"})
        self.write(self.chapter, "proposals/round-01/new.json", {**identity, "agent_id": "new", "round": 1,
            "decision": "新角色提案", "decision_reason": "PRIVATE-REASON"})
        self.write(self.chapter, "observations/round-01/shared.json", {"context": "PRIVATE-OBSERVATION"})
        self.write(self.chapter, "arbitration-round-01.json", {**identity, "round": 1, "finalized": True,
            "resolutions": [{"agent_id": "shared", "outcome": "success", "immediate_result": "公开裁决结果"}]})
        self.usage = {**identity, "role": "character", "agent_id": "shared", "round": 1, "usage_id": "paid-1",
                      "input": 100, "output": 20, "cost_source": "estimated", "cost_usd": 0.01}
        self.write(self.generation, "chapters/000002.bundle.json", {"character_agent_evidence": {"chapter": 2, "usage": [self.usage]}})
        self.ledger(self.nd, [self.usage, dict(self.usage, generation_id="pg2_older", usage_id="old-paid", cost_usd=0.04)])
        self.ledger(self.ws, [self.usage, dict(self.usage, usage_id="paid-retry", cost_usd=0.02, status="failed"),
                              dict(self.usage, generation_id="pg2_unrelated", usage_id="wrong", cost_usd=500)])

    def tearDown(self):
        self.tmp.cleanup()

    def write(self, root, rel, value):
        path = root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")

    def ledger(self, root, records):
        path = root / "meta/character_agents/usage.jsonl"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("\n".join(json.dumps(x) for x in records), encoding="utf-8")

    def payload(self):
        workspace, projection = server.current_character_planning_workspace(self.nd, self.formal)
        return server.character_agent_payload(self.nd, workspace, projection)

    def test_current_projection_overlays_public_choices_but_keeps_canonical_memory_and_dedupes_cost(self):
        with mock.patch.object(server, "read_json", wraps=server.read_json) as reads:
            payload = self.payload()
        rows = {row["agent_id"]: row for row in payload["characters"]}
        self.assertEqual(rows["shared"]["memory_chapter"], 1)
        self.assertEqual(rows["shared"]["memory_version"], 2)
        self.assertEqual(rows["new"]["memory_chapter"], 0)
        self.assertEqual(rows["new"]["memory_version"], 0)
        self.assertEqual(rows["shared"]["recent_decision"], "公开的暂存选择")
        self.assertEqual(rows["new"]["recent_decision"], "新角色提案")
        self.assertEqual(rows["shared"]["evidence_scope"], "projected")
        self.assertEqual(rows["shared"]["arbitration_result"], "公开裁决结果")
        self.assertEqual(rows["shared"]["status"], "active")
        self.assertEqual(payload["usage_summary"]["usage_calls"], 3)
        self.assertAlmostEqual(payload["usage_summary"]["cost_usd"], 0.07)
        projected = payload["planning_projection"]
        self.assertEqual(projected["generation_id"], self.gid)
        self.assertEqual(projected["state"], "running")
        self.assertEqual(projected["usage_summary"]["usage_calls"], 2)
        self.assertAlmostEqual(projected["usage_summary"]["cost_usd"], 0.03)
        self.assertNotIn("PRIVATE-", json.dumps(payload))
        self.assertFalse(any("observations" in str(call.args[0]) for call in reads.call_args_list))
        self.assertFalse(any(str(self.ws / "meta/character_agents/memory") in str(call.args[0]) for call in reads.call_args_list))

    def test_run_detail_binds_projection_to_formal_current_generation(self):
        detail = server.run_detail(self.run)
        self.assertEqual(detail["formal_planning"]["generation_id"], self.gid)
        self.assertEqual(detail["character_agents"]["planning_projection"]["generation_id"], self.gid)
        self.assertEqual(len(detail["character_agents"]["characters"]), 2)

    def test_invalid_or_unbound_workspaces_do_not_fall_back_to_latest_directory(self):
        for field, value in (("generation_id", "pg2_other"), ("generation_id", "../escape"), ("state", "sealed")):
            with self.subTest(field=field, value=value):
                self.assertEqual(server.current_character_planning_workspace(self.nd, dict(self.formal, **{field: value})), (None, None))
        for field, value in (("source_output", str(Path(self.tmp.name) / "other/output/novel")),
                             ("workspace", str(self.nd)), ("foundation_snapshot_root", "sha256:" + "c" * 64),
                             ("generation_id", "pg2_other"), ("base_chapter", 0)):
            with self.subTest(field=field):
                self.write(self.ws, "meta/project_all_workspace_manifest.json", dict(self.manifest, **{field: value}))
                self.assertEqual(server.current_character_planning_workspace(self.nd, self.formal), (None, None))
        self.write(self.ws, "meta/project_all_workspace_manifest.json", self.manifest)
        self.write(self.nd, f"meta/planning/v2/lifecycle/invalidations/{self.gid}/receipt.json", {})
        self.assertEqual(server.current_character_planning_workspace(self.nd, self.formal), (None, None))

    def test_workspace_symlink_and_evidence_symlink_never_escape_run(self):
        outside = Path(self.tmp.name) / "outside.json"
        outside.write_text(json.dumps({"generation_id": self.gid, "chapter": 2, "agent_id": "shared", "decision": "ESCAPED-SECRET"}), encoding="utf-8")
        proposal = self.chapter / "proposals/round-01/shared.json"
        proposal.unlink()
        proposal.symlink_to(outside)
        self.assertNotIn("ESCAPED-SECRET", json.dumps(self.payload()))
        manifest = self.ws / "meta/project_all_workspace_manifest.json"
        manifest.unlink()
        manifest.symlink_to(outside)
        self.assertEqual(server.current_character_planning_workspace(self.nd, self.formal), (None, None))

    def test_paused_building_projection_keeps_scope_and_never_claims_accepted_memory(self):
        (self.nd / "meta/runtime/pipeline_execution.json").unlink()
        payload = self.payload()
        self.assertEqual(payload["planning_projection"]["state"], "paused")
        self.assertEqual(payload["characters"][0]["memory_chapter"], 1)

    def test_generation_directory_symlink_is_rejected_before_evidence_reads(self):
        generation_root = self.ws.parent.parent
        outside = Path(self.tmp.name) / "outside-generation"
        generation_root.rename(outside)
        generation_root.symlink_to(outside, target_is_directory=True)
        self.assertEqual(server.current_character_planning_workspace(self.nd, self.formal), (None, None))

    def test_changed_canonical_memory_is_read_fresh_and_not_replaced_by_projection(self):
        self.write(self.nd, "meta/character_agents/memory/shared.json", {"last_accepted_chapter": 2, "facts": ["PRIVATE-LIVE-UPDATE"]})
        payload = self.payload()
        row = next(row for row in payload["characters"] if row["agent_id"] == "shared")
        self.assertEqual(row["memory_chapter"], 2)
        self.assertEqual(row["memory_version"], 2)
        self.assertEqual(row["evidence_scope"], "projected")
        self.assertNotIn("PRIVATE-", json.dumps(payload))


if __name__ == "__main__":
    unittest.main()
