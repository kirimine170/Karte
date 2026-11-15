#!/usr/bin/env python3
"""Transport-only smoke test for an authenticated, scoped Karte MCP endpoint.

Reads the agent token from an environment variable; never prints it or tool
content. Use synthetic data initially. HTTP is permitted only on loopback.
"""

import argparse
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request


def rpc(url, token, number, method, params):
    body = json.dumps(
        {"jsonrpc": "2.0", "id": number, "method": method, "params": params},
        ensure_ascii=False,
    ).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=body,
        headers={
            "Authorization": "Bearer " + token,
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "MCP-Protocol-Version": "2025-06-18",
        },
        method="POST",
    )
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            if response.status != 200:
                raise RuntimeError("unexpected MCP HTTP status")
            result = json.load(response)
    except urllib.error.HTTPError as exc:
        raise RuntimeError("MCP request rejected (HTTP {})".format(exc.code)) from None
    except urllib.error.URLError:
        raise RuntimeError("MCP endpoint unavailable") from None
    if result.get("jsonrpc") != "2.0" or result.get("id") != number or "error" in result:
        raise RuntimeError("invalid MCP response envelope")
    return result.get("result", {})


def tool_payload(result):
    if result.get("isError"):
        raise RuntimeError("MCP tool returned isError")
    content = result.get("content")
    if not isinstance(content, list) or len(content) != 1:
        raise RuntimeError("unexpected MCP tool response shape")
    if content[0].get("type") != "text":
        raise RuntimeError("MCP tool did not return text")
    return json.loads(content[0]["text"])


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", default="http://127.0.0.1:8791/mcp")
    parser.add_argument("--token-env", default="KARTE_DECISION_AGENT_TOKEN")
    parser.add_argument("--query", default="Karte")
    parser.add_argument("--expect-doc-id", default="")
    parser.add_argument("--expect-denied-doc-id", default="")
    args = parser.parse_args(argv)

    parsed = urllib.parse.urlsplit(args.url)
    if parsed.path != "/mcp" or parsed.query or parsed.fragment or parsed.username or parsed.password:
        parser.error("URL must point to the exact /mcp path without credentials or query")
    if parsed.scheme != "https" and not (
        parsed.scheme == "http" and parsed.hostname in ("127.0.0.1", "::1", "localhost")
    ):
        parser.error("non-loopback MCP must use HTTPS")
    secret = os.environ.get(args.token_env, "")
    if not secret or "\n" in secret or "\r" in secret:
        parser.error("required token environment variable is not set or invalid")

    initialized = rpc(args.url, secret, 1, "initialize", {
        "protocolVersion": "2025-06-18",
        "clientInfo": {"name": "karte-mcp-smoke", "version": "0.1"},
        "capabilities": {},
    })
    if not initialized.get("serverInfo"):
        raise RuntimeError("MCP initialize did not return serverInfo")

    tools = rpc(args.url, secret, 2, "tools/list", {}).get("tools", [])
    names = {tool.get("name") for tool in tools}
    if not {"karte_search", "karte_read"}.issubset(names):
        raise RuntimeError("shared MCP read/search tools not found")

    results = tool_payload(rpc(args.url, secret, 3, "tools/call", {
        "name": "karte_search",
        "arguments": {"query": args.query, "top_k": 10},
    }))
    if results.get("status") not in ("ok", "denied"):
        raise RuntimeError("unexpected search status")
    if results.get("diagnostics"):
        raise RuntimeError("remote MCP leaked document index diagnostics")
    hits = results.get("results", [])
    if args.expect_doc_id and not any(hit.get("doc_id") == args.expect_doc_id for hit in hits):
        raise RuntimeError("expected document not present in scoped search")

    if args.expect_doc_id:
        doc = tool_payload(rpc(args.url, secret, 4, "tools/call", {
            "name": "karte_read", "arguments": {"doc_id": args.expect_doc_id},
        }))
        if doc.get("status") != "ok" or doc.get("document", {}).get("doc_id") != args.expect_doc_id:
            raise RuntimeError("scoped read did not return expected document")
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
        "search_status": results["status"],
        "returned_count": len(hits),
        "read_verified": bool(args.expect_doc_id),
        "denial_verified": bool(args.expect_denied_doc_id),
    }))
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (RuntimeError, ValueError, KeyError, TypeError) as error:
        print("FAIL: {}".format(error), file=sys.stderr)
        raise SystemExit(1)
