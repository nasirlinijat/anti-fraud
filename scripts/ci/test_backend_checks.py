"""Exercise shard completeness and reject incomplete coverage results."""

import argparse
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import backend_checks as checks

MODULE = "example.test/backend"
IDENTITY = {"revision": "revision-1", "go_version": "go1.26.11"}
PACKAGES = [MODULE + path for path in ("/cmd/empty", "/internal/lgbm", "/internal/simulator")]
ASSIGNMENTS = {"core": PACKAGES[:1], "cpu": PACKAGES[1:]}


class CoverageChecks(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.directories = {shard: self.root / shard for shard in checks.SHARDS}
        self.write_shard("core", MODULE + "/cmd/empty/main.go:1.1,2.1 3 0\n")
        self.write_shard("cpu", MODULE + "/internal/lgbm/model.go:1.1,2.1 4 9\n")

    def write_shard(self, shard, rows):
        directory = self.directories[shard]
        directory.mkdir(exist_ok=True)
        profile = directory / "coverage.out"
        profile.write_text("mode: atomic\n" + rows)
        manifest = {"schema_version": 1, **IDENTITY, "shard": shard,
                    "packages": ASSIGNMENTS[shard], "profile_sha256": checks.checksum(profile)}
        (directory / "manifest.json").write_text(json.dumps(manifest))

    def combine(self):
        return checks.combine(self.directories, ASSIGNMENTS, IDENTITY)

    def test_partition_includes_no_test_and_future_packages(self):
        future = MODULE + "/internal/future"
        result = checks.partition(PACKAGES + [future], MODULE)
        self.assertEqual(result["core"], [PACKAGES[0], future])
        self.assertEqual(result["cpu"], PACKAGES[1:])

    def test_partition_rejects_missing_targets_and_duplicate_inventory(self):
        for packages in ([], PACKAGES[:-1], PACKAGES + [PACKAGES[0]]):
            with self.subTest(packages=packages), self.assertRaises(ValueError):
                checks.partition(packages, MODULE)

    def test_combine_preserves_zero_hit_denominator_and_round_trips(self):
        blocks = self.combine()
        self.assertEqual(sum(row[0] for row in blocks.values()), 7)
        self.assertEqual(sum(row[0] for row in blocks.values() if row[1]), 4)
        path = self.root / "combined.out"
        checks.write_profile(path, blocks)
        self.assertEqual(checks.read_profile(path), blocks)
        self.assertEqual(path.read_text().count("mode: atomic"), 1)

    def test_gate_requires_every_prerequisite_success(self):
        good = {name: {"result": "success"} for name in ("backend-tests", "backend-integration")}
        checks.check_results(good)
        for name in good:
            for result in ("failure", "cancelled", "skipped", ""):
                bad = copy.deepcopy(good)
                bad[name]["result"] = result
                with self.subTest(name=name, result=result), self.assertRaises(ValueError):
                    checks.check_results(bad)
        with self.assertRaises(ValueError):
            checks.check_results({"backend-tests": {"result": "success"}})

    def test_manifest_mismatches_fail(self):
        path = self.directories["core"] / "manifest.json"
        original = json.loads(path.read_text())
        for key, value in {"schema_version": 2, "revision": "other", "go_version": "go1.25",
                           "shard": "cpu", "packages": [], "profile_sha256": "bad"}.items():
            manifest = {**original, key: value}
            path.write_text(json.dumps(manifest))
            with self.subTest(key=key), self.assertRaises(ValueError):
                self.combine()
        path.write_text(json.dumps({**original, "schema_version": True}))
        with self.assertRaises(ValueError):
            self.combine()
        path.write_text("not json")
        with self.assertRaises(ValueError):
            self.combine()

    def test_corrupt_or_missing_files_fail(self):
        profile = self.directories["core"] / "coverage.out"
        profile.write_text(profile.read_text() + "corruption")
        with self.assertRaises(ValueError):
            self.combine()
        profile.unlink()
        with self.assertRaises(OSError):
            self.combine()
        self.write_shard("core", MODULE + "/cmd/empty/main.go:1.1,2.1 3 0\n")
        (self.directories["core"] / "manifest.json").unlink()
        with self.assertRaises(OSError):
            self.combine()

    def test_malformed_profiles_fail_even_with_matching_checksum(self):
        rows = ["", "mode: atomic\n", "mode: set\na.go:1.1,2.1 1 0\n",
                "mode: atomic\ninvalid\n", "mode: atomic\na.go:0.1,2.1 1 0\n",
                "mode: atomic\na.go:3.1,2.1 1 0\n", "mode: atomic\na.go:1.1,2.1 1 -1\n",
                "mode: atomic\na.go:1.1,2.1 1 1\na.go:1.1,2.1 1 2\n"]
        path = self.root / "bad.out"
        for text in rows:
            path.write_text(text)
            with self.subTest(text=text), self.assertRaises(ValueError):
                checks.read_profile(path)

    def test_wrong_package_and_duplicate_or_missing_assignment_fail(self):
        self.write_shard("core", MODULE + "/internal/lgbm/model.go:1.1,2.1 3 0\n")
        with self.assertRaises(ValueError):
            self.combine()
        for key in ("core", "cpu"):
            directories = dict(self.directories)
            directories.pop(key)
            with self.assertRaises(ValueError):
                checks.combine(directories, ASSIGNMENTS, IDENTITY)
        with self.assertRaises(ValueError):
            checks.combine(self.directories, {"core": PACKAGES, "cpu": PACKAGES[1:]}, IDENTITY)
        with self.assertRaises(ValueError):
            checks.combine(self.directories, {"core": PACKAGES[:1] * 2, "cpu": PACKAGES[1:]}, IDENTITY)

    def test_compare_ignores_positive_counts_but_rejects_lost_hits_and_blocks(self):
        reference, candidate = self.root / "reference", self.root / "candidate"
        reference.write_text("mode: atomic\na.go:1.1,2.1 3 9\na.go:3.1,4.1 2 0\n")
        candidate.write_text("mode: atomic\na.go:1.1,2.1 3 1\na.go:3.1,4.1 2 4\n")
        args = argparse.Namespace(reference=reference, candidate=candidate)
        self.assertEqual(checks.compare_profiles(args), 0)
        for text in ["a.go:1.1,2.1 3 0\na.go:3.1,4.1 2 0\n", "a.go:1.1,2.1 3 9\n",
                     "a.go:1.1,2.1 4 9\na.go:3.1,4.1 2 0\n"]:
            candidate.write_text("mode: atomic\n" + text)
            with self.subTest(text=text), self.assertRaises(ValueError):
                checks.compare_profiles(args)

    def test_merge_applies_only_existing_mock_exclusion(self):
        rows = MODULE + "/cmd/empty/main.go:1.1,2.1 3 0\n"
        mock = MODULE + "/cmd/empty/mocks/generated.go:1.1,2.1 2 0\n"
        # A mock subpackage must also be assigned, just like go list discovers it.
        assignments = {"core": [*ASSIGNMENTS["core"], MODULE + "/cmd/empty/mocks"], "cpu": ASSIGNMENTS["cpu"]}
        self.write_shard("core", rows + mock)
        path = self.directories["core"] / "manifest.json"
        manifest = json.loads(path.read_text())
        manifest["packages"] = assignments["core"]
        path.write_text(json.dumps(manifest))
        args = argparse.Namespace(core=self.directories["core"], cpu=self.directories["cpu"], output_dir=self.root / "merged", needs_json=None)
        with patch.object(checks, "inventory", return_value=assignments), patch.object(checks, "command_output", side_effect=IDENTITY.values()):
            checks.merge_shards(args)
        self.assertIn("/mocks/", (args.output_dir / "coverage.out").read_text())
        self.assertNotIn("/mocks/", (args.output_dir / "coverage.txt").read_text())
        self.assertIn(" 3 0", (args.output_dir / "coverage.txt").read_text())

    def test_run_requires_database_roles_and_discards_stale_manifest(self):
        out = self.root / "run"
        out.mkdir()
        (out / "manifest.json").write_text("stale")
        args = argparse.Namespace(shard="core", output_dir=out)
        with patch.object(checks, "inventory", return_value=ASSIGNMENTS), patch.dict(checks.os.environ, {}, clear=True):
            with self.assertRaisesRegex(ValueError, "TEST_DATABASE_URL"):
                checks.run_shard(args)
        self.assertFalse((out / "manifest.json").exists())

    def test_failed_discovery_invalidates_stale_success(self):
        out = self.root / "stale"
        out.mkdir()
        (out / "manifest.json").write_text("stale")
        args = argparse.Namespace(shard="core", output_dir=out)
        with patch.object(checks, "inventory", side_effect=ValueError("discovery failed")):
            with self.assertRaises(ValueError):
                checks.run_shard(args)
        self.assertFalse((out / "manifest.json").exists())


if __name__ == "__main__":
    unittest.main()
