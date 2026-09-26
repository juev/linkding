"""Deterministic local website for background metadata queue measurements."""

import argparse
import json
import re
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


parser = argparse.ArgumentParser()
parser.add_argument("--port", type=int, default=32900)
parser.add_argument("--delay-ms", type=int, default=50)
parser.add_argument("--log", type=Path, required=True)
args = parser.parse_args()

lock = threading.Lock()


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        match = re.fullmatch(r"/(python|go)/page/(\d+)", self.path)
        if not match:
            self.send_error(404)
            return
        started_ns = time.time_ns()
        time.sleep(args.delay_ms / 1000)
        body = (
            f"<html><head><title>benchmark metadata {match.group(2)}</title>"
            f'<meta name="description" content="benchmark page {match.group(2)}">'
            "</head><body>Benchmark</body></html>"
        ).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
        ended_ns = time.time_ns()
        with lock, args.log.open("a", encoding="utf-8") as output:
            output.write(
                json.dumps(
                    {
                        "server": match.group(1),
                        "bookmark_id": int(match.group(2)),
                        "started_ns": started_ns,
                        "ended_ns": ended_ns,
                    }
                )
                + "\n"
            )

    def log_message(self, format, *values):
        pass


server = ThreadingHTTPServer(("0.0.0.0", args.port), Handler)
server.daemon_threads = True
print(f"mock site listening on {args.port}", flush=True)
server.serve_forever()
