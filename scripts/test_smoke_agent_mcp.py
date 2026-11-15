"""Regression tests for the bearer-safe Karte MCP smoke client."""

import json
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import smoke_agent_mcp


class QuietHandler(BaseHTTPRequestHandler):
    def log_message(self, format, *args):
        return


class TestSmokeAgentMCP(unittest.TestCase):
    def serve(self, handler):
        httpd = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        thread = threading.Thread(target=httpd.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join, 2)
        self.addCleanup(httpd.server_close)
        self.addCleanup(httpd.shutdown)
        return "http://127.0.0.1:{}/mcp".format(httpd.server_port)

    def test_local_mcp_post_includes_bearer_header(self):
        captured = {}

        class MCPHandler(QuietHandler):
            def do_POST(self):
                captured["auth"] = self.headers.get("Authorization")
                raw = self.rfile.read(int(self.headers["Content-Length"]))
                received = json.loads(raw)
                reply = json.dumps({
                    "jsonrpc": "2.0", "id": received["id"],
                    "result": {"serverInfo": {"name": "fixture"}},
                }).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(reply)))
                self.end_headers()
                self.wfile.write(reply)

        url = self.serve(MCPHandler)
        result = smoke_agent_mcp.rpc(url, "synthetic-token", 1, "initialize", {})
        self.assertEqual(result["serverInfo"]["name"], "fixture")
        self.assertEqual(captured["auth"], "Bearer synthetic-token")

    def test_302_redirect_never_leaks_token_to_another_host_or_port(self):
        captured = []

        class Destination(QuietHandler):
            def do_POST(self):
                captured.append(self.headers.get("Authorization"))
                self.send_response(200)
                self.end_headers()

        redirected_to = self.serve(Destination)

        class Redirector(QuietHandler):
            def do_POST(self):
                self.send_response(302)
                self.send_header("Location", redirected_to)
                self.end_headers()

        origin = self.serve(Redirector)
        with self.assertRaisesRegex(RuntimeError, "HTTP 302"):
            smoke_agent_mcp.rpc(origin, "synthetic-secret-that-must-not-leak", 3, "tools/list", {})
        self.assertEqual(captured, [])

    def test_http_nonloopback_rejected_before_credentials(self):
        with self.assertRaises(SystemExit):
            smoke_agent_mcp.main(["--url", "http://example.com/mcp", "--query", "safe"])


if __name__ == "__main__":
    unittest.main()
