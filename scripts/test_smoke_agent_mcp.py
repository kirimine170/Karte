"""Regression tests for the bearer-safe Karte MCP smoke client."""

import json
import contextlib
import io
import sys
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from unittest.mock import patch

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

    def test_redirect_never_leaks_token_to_another_port(self):
        captured = []

        class Destination(QuietHandler):
            def do_POST(self):
                captured.append(self.headers.get("Authorization"))
                self.send_response(200)
                self.end_headers()

            do_GET = do_POST

        redirected_to = self.serve(Destination)

        class Redirector(QuietHandler):
            def do_POST(self):
                self.send_response(int(self.path.rsplit("/", 1)[1]))
                self.send_header("Location", redirected_to)
                self.end_headers()

        origin = self.serve(Redirector)
        for status in (301, 302, 303, 307, 308):
            with self.subTest(status=status), self.assertRaisesRegex(RuntimeError, f"HTTP {status}"):
                smoke_agent_mcp.rpc(f"{origin}/{status}", "synthetic-secret", 3, "tools/list", {})
        self.assertEqual(captured, [])

    def test_full_handshake_and_matching_source_without_logging_it(self):
        methods = []
        digest = "a" * 64
        document = {
            "doc_id": "fixture-doc-id", "sha256": digest,
            "relative_path": "content/synthetic.md",
            "provenance": [{"type": "canonical", "reference": "content/synthetic.md", "sha256": digest}],
        }

        class MCPHandler(QuietHandler):
            def do_POST(self):
                request = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                method = request["method"]
                methods.append((method, "id" in request, self.headers.get("MCP-Protocol-Version")))
                if method == "notifications/initialized":
                    self.send_response(202)
                    self.end_headers()
                    return
                if method == "initialize":
                    result = {"serverInfo": {"name": "fixture"}, "protocolVersion": "2025-06-18"}
                elif method == "tools/list":
                    result = {"tools": [{"name": "karte_search"}, {"name": "karte_read"}]}
                else:
                    if request["params"]["name"] == "karte_search":
                        payload = {"status": "ok", "results": [document]}
                    elif request["params"]["arguments"]["doc_id"] == document["doc_id"]:
                        payload = {"status": "ok", "document": {**document, "body": "synthetic body"}}
                    else:
                        payload = {"status": "denied"}
                    result = {"content": [{"type": "text", "text": json.dumps(payload)}]}
                raw = json.dumps({"jsonrpc": "2.0", "id": request["id"], "result": result}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)

        url = self.serve(MCPHandler)
        output = io.StringIO()
        args = ["--url", url, "--expect-doc-id", "fixture-doc-id",
                "--expect-sha256", digest, "--expect-denied-doc-id", "hidden", "--no-expect-propose"]
        with patch.dict("os.environ", {"KARTE_DECISION_AGENT_TOKEN": "synthetic-token"}):
            with contextlib.redirect_stdout(output):
                self.assertEqual(smoke_agent_mcp.main(args), 0)
            args[5] = "b" * 64
            with self.assertRaisesRegex(RuntimeError, "SHA-256"):
                smoke_agent_mcp.main(args)
        self.assertEqual(methods[:3], [
            ("initialize", True, None), ("notifications/initialized", False, "2025-06-18"),
            ("tools/list", True, "2025-06-18"),
        ])
        report = output.getvalue()
        self.assertTrue(json.loads(report)["read_verified"])
        for value in ("synthetic-token", digest, "fixture-doc-id", "synthetic body", "content/synthetic.md"):
            self.assertNotIn(value, report)

    def test_http_nonloopback_rejected_before_credentials(self):
        with self.assertRaises(SystemExit):
            smoke_agent_mcp.main(["--url", "http://example.com/mcp", "--query", "safe"])


if __name__ == "__main__":
    unittest.main()
