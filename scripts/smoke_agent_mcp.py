#!/usr/bin/env python3
"""Transport-only smoke test for an authenticated, scoped Karte MCP endpoint.

Reads the agent token from an environment variable; never prints it or tool
content. Use synthetic data initially. HTTP is permitted only on loopback.
"""

import argparse
import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request


class _RejectRedirects(urllib.request.HTTPRedirectHandler):
    """A redirect must never inherit an MCP Bearer token."""

    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


# The default urllib opener can forward Authorization across redirects.
# Use a dedicated opener that never follows even same-origin redirects.
_MCP_OPENER = urllib.request.build_opener(_RejectRedirects(), urllib.request.ProxyHandler({}))
_PROTOCOL_VERSION = "2025-06-18"
_SHA256 = re.compile(r"[a-f0-9]{64}")


def rpc(url, token, number, method, params):
    message = {"jsonrpc": "2.0", "method": method}
    if number is not None:
        message["id"] = number
    if params is not None:
        message["params"] = params
    headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
        "Accept": "application/json, text/event-stream",
    }
    if method != "initialize":
        headers["MCP-Protocol-Version"] = _PROTOCOL_VERSION
    request = urllib.request.Request(
        url,
        data=json.dumps(message, ensure_ascii=False).encode("utf-8"),
        headers=headers,
        method="POST",
    )
    try:
        with _MCP_OPENER.open(request, timeout=15) as response:
            if number is None:
                if response.status != 202 or response.read(1):
                    raise RuntimeError("expected an empty HTTP 202 for MCP notification")
                return
            if response.status != 200 or response.headers.get_content_type() != "application/json":
                raise RuntimeError("expected an HTTP 200 JSON response")
            result = json.load(response)
    except urllib.error.HTTPError as exc:
        raise RuntimeError("MCP request rejected (HTTP {})".format(exc.code)) from None
    except urllib.error.URLError:
        raise RuntimeError("MCP endpoint unavailable") from None
    if (not isinstance(result, dict) or result.get("jsonrpc") != "2.0"
            or result.get("id") != number or "error" in result or "result" not in result):
        raise RuntimeError("invalid MCP response envelope")
    return result["result"]


def tool_payload(result):
    if result.get("isError"):
        raise RuntimeError("MCP tool returned isError")
    content = result.get("content")
    if not isinstance(content, list) or len(content) != 1:
        raise RuntimeError("unexpected MCP tool response shape")
    if content[0].get("type") != "text":
        raise RuntimeError("MCP tool did not return text")
    return json.loads(content[0]["text"])


def source_signature(document, expected_sha256):
    """Return the version and sources for an authorized result without logging them."""
    digest = document.get("sha256", "")
    sources = document.get("provenance")
    if (not _SHA256.fullmatch(digest) or not isinstance(sources, list) or not sources
            or not document.get("relative_path")):
        raise RuntimeError("document lacks a source version or provenance")
    if expected_sha256 and digest != expected_sha256:
        raise RuntimeError("document SHA-256 does not match the expected version")
    return digest, document["relative_path"], sources


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8791/mcp")
    parser.add_argument("--token-env", default="KARTE_DECISION_AGENT_TOKEN")
    parser.add_argument("--query", default="Karte")
    parser.add_argument("--expect-doc-id", default="")
    parser.add_argument("--expect-sha256", default="", help="Expected source SHA-256 for a checked document.")
    parser.add_argument("--expect-denied-doc-id", default="")
    parser.add_argument("--expect-propose", action=argparse.BooleanOptionalAction, default=None,
                        help="Assert proposal tool availability without calling it.")
    args = parser.parse_args(argv)

    parsed = urllib.parse.urlsplit(args.url)
    if (not parsed.hostname or parsed.path != "/mcp" or parsed.query or parsed.fragment
            or parsed.username is not None or parsed.password is not None):
        parser.error("URL must point to the exact /mcp path without credentials or query")
    if parsed.scheme != "https" and not (
        parsed.scheme == "http" and parsed.hostname in ("127.0.0.1", "::1", "localhost")
    ):
        parser.error("non-loopback MCP must use HTTPS")
    if args.expect_sha256 and (not args.expect_doc_id or not _SHA256.fullmatch(args.expect_sha256)):
        parser.error("--expect-sha256 requires --expect-doc-id and a lowercase SHA-256")
    secret = os.environ.get(args.token_env, "")
    if not secret or "\n" in secret or "\r" in secret:
        parser.error("required token environment variable is not set or invalid")

    initialized = rpc(args.url, secret, 1, "initialize", {
        "protocolVersion": _PROTOCOL_VERSION,
        "clientInfo": {"name": "karte-mcp-smoke", "version": "0.1"},
        "capabilities": {},
    })
    if not initialized.get("serverInfo") or initialized.get("protocolVersion") != _PROTOCOL_VERSION:
        raise RuntimeError("MCP initialize did not return serverInfo and the expected protocol version")
    rpc(args.url, secret, None, "notifications/initialized", None)

    tools = rpc(args.url, secret, 2, "tools/list", {}).get("tools", [])
    names = {tool.get("name") for tool in tools}
    if not {"karte_search", "karte_read"}.issubset(names):
        raise RuntimeError("shared MCP read/search tools not found")
    can_propose = "karte_propose_decision" in names
    if args.expect_propose is not None and can_propose != args.expect_propose:
        raise RuntimeError("proposal tool availability did not match the expected grant")

    results = tool_payload(rpc(args.url, secret, 3, "tools/call", {
        "name": "karte_search",
        "arguments": {"query": args.query, "top_k": 10},
    }))
    if results.get("status") != "ok":
        raise RuntimeError("search was not authorized or failed")
    if results.get("diagnostics"):
        raise RuntimeError("remote MCP leaked document index diagnostics")
    hits = results.get("results", [])
    if args.expect_doc_id:
        matching = [hit for hit in hits if hit.get("doc_id") == args.expect_doc_id]
        if len(matching) != 1:
            raise RuntimeError("expected a single document in scoped search")
        doc = tool_payload(rpc(args.url, secret, 4, "tools/call", {
            "name": "karte_read", "arguments": {"doc_id": args.expect_doc_id},
        }))
        if doc.get("status") != "ok" or doc.get("document", {}).get("doc_id") != args.expect_doc_id:
            raise RuntimeError("scoped read did not return expected document")
        if doc.get("diagnostics"):
            raise RuntimeError("read returned document index diagnostics")
        if source_signature(doc["document"], args.expect_sha256) != source_signature(matching[0], args.expect_sha256):
            raise RuntimeError("search and read returned different source versions or provenance")
    if args.expect_denied_doc_id:
        denied = tool_payload(rpc(args.url, secret, 5, "tools/call", {
            "name": "karte_read", "arguments": {"doc_id": args.expect_denied_doc_id},
        }))
        if denied.get("status") != "denied" or denied.get("document") is not None or denied.get("diagnostics"):
            raise RuntimeError("restricted read disclosed data")

    # Print only non-sensitive transport statistics, not token, query, IDs or excerpts.
    print(json.dumps({
        "status": "PASS",
        "transport": "streamable-http",
        "initialize": "ok",
        "tools_present": True,
        "proposal_available": can_propose,
        "search_status": results["status"],
        "returned_count": len(hits),
        "read_verified": bool(args.expect_doc_id),
        "denial_verified": bool(args.expect_denied_doc_id),
    }))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except RuntimeError as error:
        print("FAIL: {}".format(error), file=sys.stderr)
        raise SystemExit(1)
    except (ValueError, KeyError, TypeError, AttributeError):
        print("FAIL: invalid endpoint or malformed MCP response", file=sys.stderr)
        raise SystemExit(1)
