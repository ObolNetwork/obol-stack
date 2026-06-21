"""Self-wiring x402 payment recovery for a STOCK hermes image (no core seam).

The clean integration is the protocol-agnostic seam in ``tools/mcp_tool.py`` +
``tools/payment_hook.py``. But when those aren't present — e.g. the agent runs an
unmodified upstream ``nousresearch/hermes-agent`` image and this plugin was simply
dropped into the user-plugins dir (the "inject by default, like a skill" path) —
the plugin wires itself in by wrapping the MCP SDK's
``ClientSession.call_tool``.

Every priced-tool 402 is detected on the MCP event loop (where the session
lives), settled, and the call transparently retried with the voucher in
``params._meta["x402/payment"]`` — so the agent only ever sees the paid result.
Settlement (synchronous remote-signer signing) runs in an executor so it never
blocks the loop. The wrap is idempotent and preserves the original; non-priced
calls and non-x402 errors pass straight through.
"""

from __future__ import annotations

import asyncio
import json
import logging

from . import payment, x402

logger = logging.getLogger(__name__)

_installed = False


def _challenge_from_result(result):
    """Return the x402 challenge doc from a CallToolResult, or None.

    Reads the raw (unsanitized) structuredContent + text content, so amount /
    asset / payTo are exact — unlike the post-sanitize error string the host's
    tool handler hands to the model.
    """
    structured = getattr(result, "structuredContent", None)
    text = "".join(
        b.text for b in (getattr(result, "content", None) or [])
        if isinstance(getattr(b, "text", None), str)
    )
    return x402.detect_challenge(structured, text)


def _settle(label: str, doc: dict):
    """Run the (synchronous) settle off the event loop; return the _meta voucher or None."""
    try:
        outcome = payment.handle_payment(label, {"headers": {}, "body": json.dumps(doc)})
    except Exception as exc:  # noqa: BLE001 — a settle bug must not break the tool call
        logger.warning("pay_mcp: self-wired settle raised for %s: %s", label, exc)
        return None
    return outcome.get("voucher_meta") if isinstance(outcome, dict) else None


def make_recovering_call_tool(orig):
    """Return a ``call_tool`` that wraps *orig* with x402 402 recovery.

    Factored out of :func:`install` so the recovery flow is unit-testable
    without monkeypatching the global SDK class.
    """
    async def call_tool(self, name, arguments=None, *args, meta=None, **kwargs):
        result = await orig(self, name, arguments, *args, meta=meta, **kwargs)
        # Only recover an UNPAID call that came back as an x402 challenge.
        if meta is not None or not getattr(result, "isError", False):
            return result
        doc = _challenge_from_result(result)
        if doc is None:
            return result
        voucher = await asyncio.get_running_loop().run_in_executor(None, _settle, name, doc)
        if not voucher:
            return result  # declined / no rail / over-policy — surface the original 402
        logger.info("pay_mcp: self-wired settle for tool %s — retrying with voucher", name)
        return await orig(self, name, arguments, *args, meta=voucher, **kwargs)

    return call_tool


def install() -> bool:
    """Wrap ``ClientSession.call_tool`` to auto-recover x402 402s. Idempotent."""
    global _installed
    if _installed:
        return True
    try:
        from mcp.client.session import ClientSession
    except Exception as exc:  # noqa: BLE001
        logger.warning("pay_mcp: cannot self-wire (mcp SDK unavailable): %s", exc)
        return False
    ClientSession.call_tool = make_recovering_call_tool(ClientSession.call_tool)
    _installed = True
    logger.info("pay_mcp: self-wired via ClientSession.call_tool (no core seam present)")
    return True
