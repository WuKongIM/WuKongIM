#!/usr/bin/env python3
"""Bound one credential-free candidate check and reclaim its owned session."""

import argparse
from contextlib import ExitStack
import json
import os
from pathlib import Path
import selectors
import signal
import subprocess
import time


class CommandInterrupted(Exception):
    """Remain visible to selectors, which may swallow InterruptedError itself."""


def session_members(session_id):
    """Find live members across all process groups; enumeration errors are fatal."""
    members = []
    if Path("/proc/self/stat").exists():
        for entry in Path("/proc").iterdir():
            if not entry.name.isdigit():
                continue
            try:
                fields = (entry / "stat").read_text().rsplit(") ", 1)[1].split()
                if int(fields[3]) == session_id and fields[0] != "Z":
                    members.append(int(entry.name))
            except (FileNotFoundError, ProcessLookupError):
                pass
    else:
        # macOS ps's session field is not a numeric SID. Query identity via getsid.
        pids = subprocess.check_output(["ps", "-axo", "pid="], text=True, timeout=5)
        for value in pids.split():
            pid = int(value)
            try:
                if os.getsid(pid) != session_id:
                    continue
                state = subprocess.run(["ps", "-p", str(pid), "-o", "stat="],
                                       capture_output=True, text=True, timeout=5)
                if state.returncode != 0 or not state.stdout.strip():
                    # A disappeared process is harmless; an unreadable live one is not.
                    os.getsid(pid)
                    raise RuntimeError(f"cannot inspect live session member {pid}")
                if not state.stdout.lstrip().startswith("Z"):
                    members.append(pid)
            except ProcessLookupError:
                pass
    return sorted(members)


def cleanup_session(session_id):
    """Reclaim only this command's new SID, including separately grouped nodes."""
    receipt = {"session_id": session_id, "verified": False, "complete": False,
               "initial_processes": None, "remaining_processes": None}
    try:
        if session_id == os.getsid(0):
            raise RuntimeError("refusing to clean the orchestrator session")
        receipt["initial_processes"] = session_members(session_id)
        # Stay below the runner's cancellation grace; repeated signals are masked by caller.
        for sig in (signal.SIGTERM, signal.SIGKILL):
            deadline = time.monotonic() + 2
            while True:
                members = session_members(session_id)
                receipt["remaining_processes"] = members
                if not members:
                    receipt.update(verified=True, complete=True)
                    return receipt
                for pid in members:
                    try:
                        # Recheck SID immediately before signalling, including after PID reuse.
                        if os.getsid(pid) == session_id:
                            os.kill(pid, sig)
                    except ProcessLookupError:
                        pass
                if time.monotonic() >= deadline:
                    break
                time.sleep(0.05)
        receipt["remaining_processes"] = session_members(session_id)
        receipt.update(verified=True, complete=not receipt["remaining_processes"])
    except Exception as error:
        receipt["error"] = str(error)
    return receipt


def write_json(path, value):
    """Replace receipts atomically so a canceled write cannot look complete."""
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(value, indent=2) + "\n")
    temporary.replace(path)


def run_command(arguments, cwd, evidence, timeout_seconds, max_output_bytes,
                stdout_name="stdout-stderr.log", split_output=False, metadata=None):
    """Preserve command outcome and independently prove cleanup on every exit path."""
    evidence = Path(evidence)
    evidence.mkdir(parents=True, exist_ok=True)
    command = {"arguments": arguments, "working_dir": str(cwd),
               "timeout_seconds": timeout_seconds, "max_output_bytes": max_output_bytes}
    command.update(metadata or {})
    write_json(evidence / "command.json", command)
    result = {"status": "running", "exit_code": None, "session_id": None,
              "output_bytes": 0, "output_truncated": False, "passed": False}
    write_json(evidence / "result.json", result)
    started = time.monotonic()
    process = None
    received_signal = None

    def interrupt(signum, _frame):
        nonlocal received_signal
        received_signal = signum
        raise CommandInterrupted(f"received signal {signum}")

    previous_signals = {sig: signal.signal(sig, interrupt)
                        for sig in (signal.SIGTERM, signal.SIGINT)}
    with ExitStack() as stack:
        selector = stack.enter_context(selectors.DefaultSelector())

        def consume(key):
            """Keep combined stdout/stderr bounded, including the terminal pipe tail."""
            try:
                chunk = os.read(key.fd, 65536)
            except BlockingIOError:
                return False
            if not chunk:
                selector.unregister(key.fileobj)
                return False
            previous = result["output_bytes"]
            result["output_bytes"] += len(chunk)
            key.data.write(chunk[:max(0, max_output_bytes - previous)])
            if result["output_bytes"] > max_output_bytes:
                result["output_truncated"] = True
            return True

        try:
            output = stack.enter_context((evidence / stdout_name).open("wb"))
            error_output = stack.enter_context((evidence / "stderr.log").open("wb")) if split_output else output
            process = subprocess.Popen(arguments, cwd=cwd, stdout=subprocess.PIPE,
                                       stderr=subprocess.PIPE if split_output else subprocess.STDOUT,
                                       start_new_session=True)
            result["session_id"] = process.pid
            for stream, destination in ((process.stdout, output), (process.stderr, error_output)):
                if stream is not None:
                    stack.callback(stream.close)
                    os.set_blocking(stream.fileno(), False)
                    selector.register(stream, selectors.EVENT_READ, destination)
            while True:
                for key, _ in selector.select(0.05):
                    consume(key)
                # A failed driver must not turn into a timeout because a node holds its pipe.
                result["exit_code"] = process.poll()
                if result["output_truncated"]:
                    result["status"] = "output_limit"
                    break
                if result["exit_code"] is not None:
                    result["status"] = "passed" if result["exit_code"] == 0 else "failed"
                    break
                if time.monotonic() - started >= timeout_seconds:
                    result["status"] = "timeout"
                    break
        except BaseException as error:
            result.update(status="interrupted" if isinstance(error, (CommandInterrupted, KeyboardInterrupt)) else "runner_error",
                          error=str(error))
        finally:
            # Do not let a second runner cancellation interrupt owned-session cleanup.
            for sig in previous_signals:
                signal.signal(sig, signal.SIG_IGN)
            if process is not None:
                result["cleanup"] = cleanup_session(process.pid)
                try:
                    result["exit_code"] = process.wait(timeout=1)
                except subprocess.TimeoutExpired:
                    result["cleanup"].update(complete=False, error="driver did not exit after cleanup")
                # Cleanup has stopped writers; nonblocking drain preserves any final output.
                for key in list(selector.get_map().values()):
                    while consume(key):
                        if result["output_truncated"]:
                            break
            else:
                result["cleanup"] = {"session_id": None, "verified": True, "complete": True,
                                     "initial_processes": [], "remaining_processes": []}
            result["command_status"] = result["status"]
            if result["status"] == "passed" and result["output_truncated"]:
                result["status"] = "output_limit"
            if result["status"] == "passed" and not result["cleanup"]["complete"]:
                result["status"] = "cleanup_failed"
            result["passed"] = result["status"] == "passed" and result["cleanup"]["complete"]
            result["wrapper_exit_code"] = 0 if result["passed"] else {
                "failed": result["exit_code"] if (result["exit_code"] or 0) > 0 else 1,
                "timeout": 124, "output_limit": 125,
                "interrupted": 128 + (received_signal or signal.SIGINT),
            }.get(result["status"], 1)
            result["elapsed_seconds"] = round(time.monotonic() - started, 3)
            write_json(evidence / "result.json", result)
            for sig, handler in previous_signals.items():
                signal.signal(sig, handler)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence-dir", type=Path, required=True)
    parser.add_argument("--timeout-seconds", type=int, required=True)
    parser.add_argument("--max-output-bytes", type=int, required=True)
    parser.add_argument("--stdout-name", default="stdout-stderr.log")
    parser.add_argument("--split-output", action="store_true")
    parser.add_argument("arguments", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    arguments = args.arguments[1:] if args.arguments[:1] == ["--"] else args.arguments
    if not arguments or args.timeout_seconds <= 0 or args.max_output_bytes <= 0:
        parser.error("a command and positive bounds are required")
    if Path(args.stdout_name).name != args.stdout_name:
        parser.error("stdout name must be a filename")
    result = run_command(arguments, Path.cwd(), args.evidence_dir, args.timeout_seconds,
                         args.max_output_bytes, args.stdout_name, args.split_output)
    print(json.dumps(result))
    raise SystemExit(result["wrapper_exit_code"])


if __name__ == "__main__":
    main()
