#!/usr/bin/env python3
"""Unit tests for gh_post.py's no-redirect guard.

Regression suite for the token-exfiltration vector where ``_gh_request``
used urllib's DEFAULT opener: CPython's HTTPRedirectHandler re-sends every
request header — including the bearer-token auth header — to a 3xx
redirect target, with no cross-origin stripping. A redirecting (or
attacker-influenced) GITHUB_API_BASE could therefore receive the literal
token. gh_post.py now routes every call through a module-level opener
whose ``redirect_request`` returns None (mirroring smoke.py's
``_NoRedirect``), so a 3xx is surfaced as the final status and the token
never leaves the intended endpoint.
"""
import http.server
import importlib.util
import sys
import threading
import time
import unittest
from pathlib import Path

MODULE_PATH = (
    Path(__file__).resolve().parents[1]
    / "internal"
    / "embed"
    / "skills"
    / "smoke-test"
    / "scripts"
    / "gh_post.py"
)

TOKEN = "ghp_test_secret_token_do_not_leak"


def load_gh_post_module():
    spec = importlib.util.spec_from_file_location("gh_post_smoke", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class _RecordingHandler(http.server.BaseHTTPRequestHandler):
    """Records every request (method, path, Authorization header)."""

    requests = None  # set per-server below

    def _record_and_respond(self, status, extra_headers=()):
        self.requests.append(
            (self.command, self.path, self.headers.get("Authorization"))
        )
        self.send_response(status)
        for name, value in extra_headers:
            self.send_header(name, value)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"{}")

    def log_message(self, *args):  # keep test output clean
        pass


def _start_server(handler_cls):
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler_cls)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


class GhPostNoRedirectTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.gh_post = load_gh_post_module()

        # "Attacker" server on a different origin — must NEVER be contacted.
        attacker_requests = []

        class AttackerHandler(_RecordingHandler):
            requests = attacker_requests

            def do_GET(self):
                self._record_and_respond(200)

            do_PUT = do_GET

        cls.attacker_requests = attacker_requests
        cls.attacker = _start_server(AttackerHandler)
        attacker_url = "http://127.0.0.1:%d/leak" % cls.attacker.server_address[1]

        # Redirector: answers every request with 302 -> attacker origin.
        redirector_requests = []

        class RedirectorHandler(_RecordingHandler):
            requests = redirector_requests

            def do_GET(self):
                self._record_and_respond(302, [("Location", attacker_url)])

            do_PUT = do_GET

        cls.redirector_requests = redirector_requests
        cls.redirector = _start_server(RedirectorHandler)
        cls.redirector_base = "http://127.0.0.1:%d" % cls.redirector.server_address[1]

    @classmethod
    def tearDownClass(cls):
        for server in (cls.attacker, cls.redirector):
            server.shutdown()
            server.server_close()

    def setUp(self):
        del self.attacker_requests[:]
        del self.redirector_requests[:]
        # _put_file builds its URL from module-level API_BASE; point it at
        # the redirector for the duration of each test.
        self._orig_api_base = self.gh_post.API_BASE
        self.gh_post.API_BASE = self.redirector_base

    def tearDown(self):
        self.gh_post.API_BASE = self._orig_api_base

    # ── the opener refuses redirects outright ─────────────────────────────

    def test_no_redirect_handler_returns_none(self):
        handler = self.gh_post._NoRedirect()
        self.assertIsNone(
            handler.redirect_request(None, None, 302, "Found", {}, "http://evil")
        )

    # ── empirical: a 3xx is final and the token never crosses origins ─────

    def test_get_does_not_follow_redirect_or_leak_token(self):
        status, _, _ = self.gh_post._gh_request(
            "GET", self.redirector_base + "/repos/o/r/contents/x", TOKEN
        )
        self.assertEqual(status, 302)
        self.assertEqual(
            self.attacker_requests, [], "redirect target must never be contacted"
        )
        # Sanity: the intended endpoint did see the Bearer header once.
        self.assertEqual(len(self.redirector_requests), 1)
        self.assertEqual(self.redirector_requests[0][2], "Bearer " + TOKEN)

    def test_put_treats_redirect_as_hard_failure(self):
        deadline = time.monotonic() + 5
        with self.assertRaises(self.gh_post.PostError) as ctx:
            self.gh_post._put_file(
                "o/r", "reports/x/y.md", "msg", b"body", None, TOKEN, deadline
            )
        self.assertIn("status 302", str(ctx.exception))
        self.assertNotIn(TOKEN, str(ctx.exception))
        self.assertEqual(self.attacker_requests, [])


if __name__ == "__main__":
    unittest.main()
