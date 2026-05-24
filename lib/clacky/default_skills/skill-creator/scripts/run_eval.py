#!/usr/bin/env python3
"""Run trigger evaluation for a skill description.

Tests whether Clacky's agent triggers (invokes) a skill for a set of queries.
Runs clacky agent --json in persistent mode, sends queries via stdin NDJSON,
detects {"type":"tool_call","name":"invoke_skill","args":{"skill_name":"<name>"}}
events, and returns pass/fail results as JSON.

Executes queries serially (Clacky is single-agent, no parallel workers).
"""

import argparse
import json
import os
import select
import shutil
import subprocess
import sys
import time
import uuid
from pathlib import Path

from scripts.utils import parse_skill_md


CLACKY_BIN = shutil.which("clacky") or "/Users/sizzy/.local/share/mise/shims/clacky"
SKILLS_DIR = Path.home() / ".clacky" / "skills"


def find_project_root() -> Path:
    """Find the project root by walking up from cwd, used for --path arg."""
    current = Path.cwd()
    for parent in [current, *current.parents]:
        if (parent / ".clacky").is_dir():
            return parent
    return current


def _read_ndjson_lines(proc, timeout: float) -> list[dict]:
    """Read NDJSON lines from proc.stdout until timeout or process exits."""
    lines = []
    buffer = b""
    start = time.time()
    while time.time() - start < timeout:
        ready = select.select([proc.stdout], [], [], 0.5)[0]
        if ready:
            chunk = os.read(proc.stdout.fileno(), 8192)
            if chunk:
                buffer += chunk
                while b"\n" in buffer:
                    line_b, buffer = buffer.split(b"\n", 1)
                    line = line_b.decode("utf-8", errors="replace").strip()
                    if not line:
                        continue
                    try:
                        lines.append(json.loads(line))
                    except json.JSONDecodeError:
                        pass
        if proc.poll() is not None:
            # drain remaining
            remaining = proc.stdout.read()
            if remaining:
                for line in remaining.decode("utf-8", errors="replace").splitlines():
                    line = line.strip()
                    if line:
                        try:
                            lines.append(json.loads(line))
                        except json.JSONDecodeError:
                            pass
            break
    return lines


def run_single_query(
    query: str,
    skill_name: str,
    skill_description: str,
    timeout: int,
    project_root: str,
) -> bool:
    """Run a single query via clacky agent --json and detect skill trigger.

    Creates a temp skill in ~/.clacky/skills/, starts clacky agent in JSON mode,
    sends the query, watches for invoke_skill tool_call event targeting our temp skill.
    """
    unique_id = uuid.uuid4().hex[:8]
    temp_skill_name = f"{skill_name}-eval-{unique_id}"
    temp_skill_dir = SKILLS_DIR / temp_skill_name

    try:
        # Write temporary skill
        temp_skill_dir.mkdir(parents=True, exist_ok=True)
        skill_md = (
            f"---\n"
            f"name: {temp_skill_name}\n"
            f"description: {skill_description}\n"
            f"---\n\n"
            f"# {skill_name}\n\n"
            f"This skill handles: {skill_description}\n"
        )
        (temp_skill_dir / "SKILL.md").write_text(skill_md)

        # Launch clacky agent in persistent JSON mode
        proc = subprocess.Popen(
            [CLACKY_BIN, "agent", "--json", "--mode", "auto_approve",
             "--path", project_root],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            bufsize=0,
        )

        try:
            # Wait for "system" ready event before sending query
            start = time.time()
            buffer = b""
            ready_received = False
            while time.time() - start < 10:
                r = select.select([proc.stdout], [], [], 0.5)[0]
                if r:
                    chunk = os.read(proc.stdout.fileno(), 4096)
                    if chunk:
                        buffer += chunk
                        while b"\n" in buffer:
                            line_b, buffer = buffer.split(b"\n", 1)
                            line = line_b.strip()
                            if line:
                                try:
                                    evt = json.loads(line)
                                    if evt.get("type") == "system":
                                        ready_received = True
                                except json.JSONDecodeError:
                                    pass
                if ready_received:
                    break

            # Send query
            msg = (json.dumps({"type": "message", "content": query}) + "\n").encode()
            proc.stdin.write(msg)
            proc.stdin.flush()

            # Read events until "complete" or timeout
            triggered = False
            start = time.time()
            buffer = b""
            while time.time() - start < timeout:
                r = select.select([proc.stdout], [], [], 0.5)[0]
                if r:
                    chunk = os.read(proc.stdout.fileno(), 8192)
                    if chunk:
                        buffer += chunk
                        while b"\n" in buffer:
                            line_b, buffer = buffer.split(b"\n", 1)
                            line = line_b.decode("utf-8", errors="replace").strip()
                            if not line:
                                continue
                            try:
                                event = json.loads(line)
                            except json.JSONDecodeError:
                                continue

                            # Detect skill trigger
                            if event.get("type") == "tool_call" and event.get("name") == "invoke_skill":
                                args = event.get("args", {})
                                invoked = args.get("skill_name", "")
                                if invoked == temp_skill_name:
                                    return True  # triggered — exit early

                            # Task complete
                            if event.get("type") == "complete":
                                return triggered

                if proc.poll() is not None:
                    break

            return triggered

        finally:
            # Gracefully exit the agent
            try:
                proc.stdin.write((json.dumps({"type": "exit"}) + "\n").encode())
                proc.stdin.flush()
            except Exception:
                pass
            if proc.poll() is None:
                proc.kill()
                proc.wait()

    finally:
        # Always remove temp skill directory
        if temp_skill_dir.exists():
            shutil.rmtree(temp_skill_dir, ignore_errors=True)


def run_eval(
    eval_set: list[dict],
    skill_name: str,
    description: str,
    timeout: int,
    project_root: Path,
    runs_per_query: int = 1,
    trigger_threshold: float = 0.5,
) -> dict:
    """Run the full eval set serially and return results.

    Note: Clacky is single-agent — queries are executed serially, not in parallel.
    Each query spawns a fresh clacky agent process to avoid session contamination.
    """
    results = []
    query_triggers: dict[str, list[bool]] = {}
    query_items: dict[str, dict] = {}

    for item in eval_set:
        query = item["query"]
        query_items[query] = item
        if query not in query_triggers:
            query_triggers[query] = []

        for run_idx in range(runs_per_query):
            try:
                triggered = run_single_query(
                    query=query,
                    skill_name=skill_name,
                    skill_description=description,
                    timeout=timeout,
                    project_root=str(project_root),
                )
                query_triggers[query].append(triggered)
            except Exception as e:
                print(f"Warning: query failed (run {run_idx}): {e}", file=sys.stderr)
                query_triggers[query].append(False)

    for query, triggers in query_triggers.items():
        item = query_items[query]
        trigger_rate = sum(triggers) / len(triggers)
        should_trigger = item["should_trigger"]
        if should_trigger:
            did_pass = trigger_rate >= trigger_threshold
        else:
            did_pass = trigger_rate < trigger_threshold
        results.append({
            "query": query,
            "should_trigger": should_trigger,
            "trigger_rate": trigger_rate,
            "triggers": sum(triggers),
            "runs": len(triggers),
            "pass": did_pass,
        })

    passed = sum(1 for r in results if r["pass"])
    total = len(results)

    return {
        "skill_name": skill_name,
        "description": description,
        "results": results,
        "summary": {
            "total": total,
            "passed": passed,
            "failed": total - passed,
        },
    }


def main():
    parser = argparse.ArgumentParser(description="Run trigger evaluation for a skill description (Clacky)")
    parser.add_argument("--eval-set", required=True, help="Path to eval set JSON file")
    parser.add_argument("--skill-path", required=True, help="Path to skill directory")
    parser.add_argument("--description", default=None, help="Override description to test")
    parser.add_argument("--timeout", type=int, default=45, help="Timeout per query in seconds")
    parser.add_argument("--runs-per-query", type=int, default=1, help="Number of runs per query (serially)")
    parser.add_argument("--trigger-threshold", type=float, default=0.5, help="Trigger rate threshold")
    parser.add_argument("--verbose", action="store_true", help="Print progress to stderr")
    # --num-workers kept for CLI compat but ignored (Clacky is serial)
    parser.add_argument("--num-workers", type=int, default=1, help="Ignored — Clacky runs serially")
    parser.add_argument("--model", default=None, help="Ignored — model comes from ~/.clacky/config.yml")
    args = parser.parse_args()

    eval_set = json.loads(Path(args.eval_set).read_text())
    skill_path = Path(args.skill_path)

    if not (skill_path / "SKILL.md").exists():
        print(f"Error: No SKILL.md found at {skill_path}", file=sys.stderr)
        sys.exit(1)

    name, original_description, content = parse_skill_md(skill_path)
    description = args.description or original_description
    project_root = find_project_root()

    if args.verbose:
        print(f"Evaluating skill: {name}", file=sys.stderr)
        print(f"Description: {description}", file=sys.stderr)
        print(f"Queries: {len(eval_set)}, runs-per-query: {args.runs_per_query}", file=sys.stderr)

    output = run_eval(
        eval_set=eval_set,
        skill_name=name,
        description=description,
        timeout=args.timeout,
        project_root=project_root,
        runs_per_query=args.runs_per_query,
        trigger_threshold=args.trigger_threshold,
    )

    if args.verbose:
        summary = output["summary"]
        print(f"Results: {summary['passed']}/{summary['total']} passed", file=sys.stderr)
        for r in output["results"]:
            status = "PASS" if r["pass"] else "FAIL"
            rate_str = f"{r['triggers']}/{r['runs']}"
            print(f"  [{status}] rate={rate_str} expected={r['should_trigger']}: {r['query'][:70]}", file=sys.stderr)

    print(json.dumps(output, indent=2))


if __name__ == "__main__":
    main()
