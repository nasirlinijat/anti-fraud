#!/usr/bin/env python3
"""Run exhaustive backend shards and validate their combined coverage."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
BACKEND = ROOT / "backend"
SHARDS = ("core", "cpu")
BLOCK = re.compile(r"^(\S+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$")


def command_output(*args):
    return subprocess.check_output(args, cwd=BACKEND, text=True).strip()


def inventory():
    packages = sorted(command_output("go", "list", "./...").splitlines())
    module = command_output("go", "list", "-m")
    return partition(packages, module)


def partition(packages, module):
    if not packages or len(packages) != len(set(packages)):
        raise ValueError("package inventory is empty or contains duplicates")
    cpu = {module + "/internal/lgbm", module + "/internal/simulator"}
    if not cpu.issubset(packages):
        raise ValueError("configured CPU packages are missing")
    return {"core": sorted(set(packages) - cpu), "cpu": sorted(cpu)}


def checksum(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_profile(path):
    lines = path.read_text().splitlines()
    if not lines or lines[0] != "mode: atomic" or len(lines) == 1:
        raise ValueError("coverage profile must be nonempty and atomic: " + str(path))
    blocks = {}
    for line in lines[1:]:
        match = BLOCK.fullmatch(line)
        if match is None:
            raise ValueError("malformed coverage row: " + line)
        filename, *numbers = match.groups()
        coordinates = tuple(int(n) for n in numbers[:4])
        statements, count = (int(n) for n in numbers[4:])
        if coordinates[0] < 1 or coordinates[2] < 1 or coordinates[:2] > coordinates[2:]:
            raise ValueError("invalid coverage coordinates: " + line)
        key = (filename, *coordinates)
        if key in blocks:
            raise ValueError("duplicate coverage block: " + filename)
        blocks[key] = (statements, count)
    return blocks


def write_profile(path, blocks):
    rows = ["mode: atomic"]
    for (filename, a, b, c, d), (statements, count) in sorted(blocks.items()):
        rows.append(f"{filename}:{a}.{b},{c}.{d} {statements} {count}")
    path.write_text("\n".join(rows) + "\n")


def check_results(results):
    expected = {"backend-tests", "backend-integration"}
    if not isinstance(results, dict) or set(results) != expected or any(
            not isinstance(results[j], dict) or results[j].get("result") != "success" for j in expected):
        raise ValueError("all backend prerequisites must succeed, without skips or cancellations")


def run_shard(args):
    out = args.output_dir.resolve()
    out.mkdir(parents=True, exist_ok=True)
    # A failed rerun must never leave a previously successful manifest usable.
    (out / "manifest.json").unlink(missing_ok=True)
    packages = inventory()[args.shard]
    for name in ("TEST_DATABASE_URL", "TEST_RUNTIME_DATABASE_URL", "TEST_LABELS_DATABASE_URL", "TEST_REDIS_URL"):
        if not os.environ.get(name):
            raise ValueError(name + " must be configured for every regular shard")
    profile = out / "coverage.out"
    command = ["go", "test", "-json", "-race", "-count=1", "-p", "1", "-covermode=atomic", "-coverprofile=" + str(profile), *packages]
    with (out / "tests.jsonl").open("w") as log:
        with subprocess.Popen(command, cwd=BACKEND, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True) as process:
            for line in process.stdout:
                log.write(line)
                print(line, end="", flush=True)
            code = process.wait()
    if code:
        return code
    read_profile(profile)
    manifest = {"schema_version": 1, "revision": command_output("git", "rev-parse", "HEAD"),
                "go_version": command_output("go", "env", "GOVERSION"), "shard": args.shard,
                "packages": packages, "profile_sha256": checksum(profile)}
    (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    return 0


def validated_shard(directory, shard, packages, identity):
    manifest = json.loads((directory / "manifest.json").read_text())
    expected = {"schema_version": 1, **identity,
                "shard": shard, "packages": packages, "profile_sha256": checksum(directory / "coverage.out")}
    if not isinstance(manifest, dict) or type(manifest.get("schema_version")) is not int or manifest != expected:
        raise ValueError("coverage manifest does not match checkout, toolchain or assignment: " + shard)
    blocks = read_profile(directory / "coverage.out")
    for key in blocks:
        if key[0].rsplit("/", 1)[0] not in packages:
            raise ValueError("coverage block belongs to another shard: " + key[0])
    return blocks


def combine(directories, assignments, identity):
    if set(directories) != set(SHARDS) or set(assignments) != set(SHARDS):
        raise ValueError("exactly core and cpu coverage artifacts are required")
    if (not all(assignments.values())
            or any(len(items) != len(set(items)) for items in assignments.values())
            or set(assignments["core"]) & set(assignments["cpu"])):
        raise ValueError("package assignments must be nonempty and disjoint")
    merged = {}
    for shard in SHARDS:
        blocks = validated_shard(directories[shard], shard, assignments[shard], identity)
        if merged.keys() & blocks.keys():
            raise ValueError("coverage blocks overlap across shards")
        merged.update(blocks)
    return merged


def merge_shards(args):
    if args.needs_json is not None:
        check_results(json.loads(args.needs_json))
    blocks = combine({"core": args.core, "cpu": args.cpu}, inventory(),
                     {"revision": command_output("git", "rev-parse", "HEAD"), "go_version": command_output("go", "env", "GOVERSION")})
    args.output_dir.mkdir(parents=True, exist_ok=True)
    write_profile(args.output_dir / "coverage.out", blocks)
    filtered = {key: value for key, value in blocks.items() if "/mocks/" not in key[0]}
    write_profile(args.output_dir / "coverage.txt", filtered)
    print(f"Merged {len(blocks)} blocks; retained zero-hit statements and excluded only /mocks/ from reports")
    return 0


def compare_profiles(args):
    reference, candidate = read_profile(args.reference), read_profile(args.candidate)
    if reference.keys() != candidate.keys():
        raise ValueError("coverage block inventory changed")
    changed = [key for key in reference if reference[key][0] != candidate[key][0]]
    if changed:
        raise ValueError("coverage statement denominator changed: " + repr(changed))
    lost = [key for key in reference if reference[key][1] > 0 and candidate[key][1] == 0]
    if lost:
        raise ValueError("lost covered blocks: " + repr(lost))
    print(f"Coverage preserved: {len(reference)} identical blocks, no lost covered statements")
    return 0


def parser():
    root = argparse.ArgumentParser(description=__doc__)
    commands = root.add_subparsers(dest="command", required=True)
    run = commands.add_parser("run")
    run.add_argument("--shard", choices=SHARDS, required=True)
    run.add_argument("--output-dir", type=Path, required=True)
    run.set_defaults(function=run_shard)
    merge = commands.add_parser("merge")
    merge.add_argument("--core", type=Path, required=True)
    merge.add_argument("--cpu", type=Path, required=True)
    merge.add_argument("--output-dir", type=Path, required=True)
    merge.add_argument("--needs-json")
    merge.set_defaults(function=merge_shards)
    compare = commands.add_parser("compare")
    compare.add_argument("--reference", type=Path, required=True)
    compare.add_argument("--candidate", type=Path, required=True)
    compare.set_defaults(function=compare_profiles)
    return root


def main():
    args = parser().parse_args()
    try:
        return args.function(args)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        print("backend check failed: " + str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
