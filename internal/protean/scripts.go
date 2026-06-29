package protean

// recorderScript is a Python script run inside the Protean venv. It starts a
// dual-track screen recording, waits for a "stop" line on stdin (or SIGINT as a
// fallback), then stops and prints a JSON summary to stdout. stdin is used
// because signals are unreliable when spawned from Go on macOS.
const recorderScript = `
import json
import signal
import sys
import threading
from pathlib import Path

from protean.platform.base import get_platform
from protean.recorder.session import RecordingConfig, RecordingSession

out_dir = Path(sys.argv[1])
out_dir.mkdir(parents=True, exist_ok=True)

config = RecordingConfig(
    output_dir=out_dir,
    capture_audio=False,
    record_audio=False,
    screenshots=True,
)
platform = get_platform()
session = RecordingSession(config, platform=platform)

stopped = threading.Event()

def on_stop():
    stopped.set()

signal.signal(signal.SIGINT, lambda _s, _f: on_stop())

info = session.start()
print(json.dumps({"status": "started", "info": info}), flush=True)

# Wait for either SIGINT or a "stop" line on stdin.
stdin_thread = threading.Thread(target=lambda: (
    sys.stdin.readline() if not stopped.is_set() else None,
    on_stop() if not stopped.is_set() else None
), daemon=True)
stdin_thread.start()
stopped.wait()

result = session.stop()
print(json.dumps({
    "status": "stopped",
    "output_dir": str(result.output_dir),
    "events_file": str(result.events_file),
    "video_file": str(result.video_file) if result.video_file else None,
    "duration": result.duration,
    "event_count": result.event_count,
    "display_index": result.display_index,
    "summary": result.summary,
}), flush=True)
`

// runSkillScript is a Python script run inside the Protean venv to execute a
// named skill using the deterministic step_by_step executor. It streams events
// back as newline-delimited JSON and returns a final result object.
const runSkillScript = `
import asyncio
import json
import os
import sys
from pathlib import Path

from protean.executor import ExecutorEventType
from protean.executor.providers.step_by_step import StepByStepExecutor
from protean.platform.base import get_platform
from protean.skills.registry import load_skill_from_file
from protean.skills.renderer import render_skill_for_llm

async def main():
    skills_dir = Path(sys.argv[1])
    name = sys.argv[2]
    skill_path = skills_dir / name / "SKILL.md"
    if not skill_path.exists():
        print(json.dumps({"success": False, "error": f"Skill {name!r} not found"}))
        return

    platform = get_platform()
    skill = load_skill_from_file(skill_path)
    executor = StepByStepExecutor(platform=platform)
    blocks = render_skill_for_llm(skill)
    await executor.start_task("run skill", content_blocks=blocks)

    lines = []
    async for evt in executor.get_events():
        kind = evt.type.value
        msg = evt.message or evt.tool_name or evt.error or ""
        lines.append(f"[{kind}] {msg}")
        if evt.type in (ExecutorEventType.DONE, ExecutorEventType.ERROR):
            success = evt.type == ExecutorEventType.DONE
            print(json.dumps({
                "success": success,
                "output": "\n".join(lines),
                "error": evt.error or "",
            }))
            break

asyncio.run(main())
`
