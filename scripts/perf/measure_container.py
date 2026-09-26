"""Measure one HTTP load run against a Docker container's cgroup.

Example:
    python3 scripts/perf/measure_container.py --container linkding-test -- \
        ./bin/perf-load -base-url http://127.0.0.1:9090 -token-file /tmp/token \
        -case search -rate 50 -requests 1000 -concurrency 32 -expected-count 2000
"""

import argparse
import http.client
import json
import socket
import statistics
import subprocess
import sys
import threading
import time
from urllib.parse import quote


class UnixHTTPConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__("localhost", timeout=5)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


def docker_socket():
    host = subprocess.check_output(
        ["docker", "context", "inspect", "--format", "{{.Endpoints.docker.Host}}"],
        text=True,
    ).strip()
    if not host.startswith("unix://"):
        raise ValueError(f"expected a local Docker Unix socket, got {host!r}")
    return host.removeprefix("unix://")


def read_stats(socket_path, container):
    connection = UnixHTTPConnection(socket_path)
    try:
        path = f"/containers/{quote(container, safe='')}/stats?stream=false&one-shot=true"
        connection.request("GET", path)
        response = connection.getresponse()
        body = response.read()
        if response.status != 200:
            raise RuntimeError(f"Docker stats returned HTTP {response.status}: {body[:200]!r}")
        data = json.loads(body)
    finally:
        connection.close()
    memory = data["memory_stats"]
    memory_fields = memory.get("stats", {})
    usage = memory["usage"]
    return {
        "at_monotonic": time.monotonic(),
        "cpu_ns": data["cpu_stats"]["cpu_usage"]["total_usage"],
        "memory_usage_bytes": usage,
        "memory_working_set_bytes": max(0, usage - memory_fields.get("inactive_file", 0)),
        "memory_anon_bytes": memory_fields.get("anon"),
        "pids": data.get("pids_stats", {}).get("current"),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--container", required=True)
    parser.add_argument("--socket", help="Docker Engine Unix socket; default is current context")
    parser.add_argument("--interval", type=float, default=0.25)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command[1:] if args.command[:1] == ["--"] else args.command
    if not command or args.interval <= 0:
        parser.error("a command after -- and a positive interval are required")

    socket_path = args.socket or docker_socket()
    samples = [read_stats(socket_path, args.container)]
    stop = threading.Event()
    sampler_error = []

    def sample_loop():
        while not stop.wait(args.interval):
            try:
                samples.append(read_stats(socket_path, args.container))
            except Exception as exc:
                sampler_error.append(str(exc))
                break

    thread = threading.Thread(target=sample_loop, daemon=True)
    thread.start()
    try:
        process = subprocess.run(command, capture_output=True, text=True, check=False)
    finally:
        stop.set()
        thread.join()
        samples.append(read_stats(socket_path, args.container))

    if sampler_error:
        raise RuntimeError(f"Docker stats sampler failed: {sampler_error[0]}")
    if process.returncode:
        raise RuntimeError(
            f"load command exited {process.returncode}: {process.stderr[-1000:]}"
        )
    load = json.loads(process.stdout)
    if load.get("error_counts"):
        raise RuntimeError(f"load command reported errors: {load['error_counts']}")
    cpu_seconds = (samples[-1]["cpu_ns"] - samples[0]["cpu_ns"]) / 1e9
    elapsed = samples[-1]["at_monotonic"] - samples[0]["at_monotonic"]
    if cpu_seconds < 0 or elapsed <= 0:
        raise RuntimeError("Docker returned non-monotonic resource counters")
    memory = [item["memory_working_set_bytes"] for item in samples]
    anon = [item["memory_anon_bytes"] for item in samples if item["memory_anon_bytes"] is not None]
    report = {
        "container": args.container,
        "load": load,
        "cpu_core_seconds": cpu_seconds,
        "cpu_core_seconds_per_1000_requests": cpu_seconds * 1000 / load["requests"],
        "cpu_average_cores": cpu_seconds / elapsed,
        "memory_working_set_mib_median": statistics.median(memory) / 1048576,
        "memory_working_set_mib_peak_sampled": max(memory) / 1048576,
        "memory_anon_mib_median": statistics.median(anon) / 1048576 if anon else None,
        "memory_anon_mib_peak_sampled": max(anon) / 1048576 if anon else None,
        "sample_interval_seconds": args.interval,
        "sample_count": len(samples),
        "measurement_elapsed_seconds": elapsed,
        "samples": [
            {**item, "at_seconds": item["at_monotonic"] - samples[0]["at_monotonic"]}
            for item in samples
        ],
    }
    for item in report["samples"]:
        del item["at_monotonic"]
    json.dump(report, sys.stdout, separators=(",", ":"))
    sys.stdout.write("\n")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError, json.JSONDecodeError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
