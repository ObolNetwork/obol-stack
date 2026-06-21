"""Payment orchestration: parse a 402 -> confirm in chat -> settle -> voucher.

``handle_payment`` is the callable the core MCP payment interceptor invokes
(via ``tools/payment_hook.py``). It runs on the agent thread, so the in-chat
confirmation blocks on a heartbeat-safe Event — never on an event loop.
"""

from __future__ import annotations

import logging
import os
import threading
import uuid

from . import rails as rails_mod
from . import x402

logger = logging.getLogger(__name__)

# obol's in-pod remote-signer default (obol/buy.py uses the same address).
_DEFAULT_SIGNER_URL = "http://remote-signer:9000"

# obol/x402-rs read the voucher from X-PAYMENT (carrying a v2 body). A strictly
# standards-conformant v2 server reads PAYMENT-SIGNATURE; override via
# ``pay_mcp.payment_headers`` for those. We never emit a v1-shaped body.
_DEFAULT_PAYMENT_HEADERS = ["X-PAYMENT"]

# Set once in register().
_RAILS: list = []
_PAYMENT_HEADERS: list = list(_DEFAULT_PAYMENT_HEADERS)

# Unattended-settlement policy (autonomous agent turns). Disabled by default —
# every payment then requires an in-chat/CLI confirm. It engages ONLY when a
# COMPLETE bounded policy is configured (see _auto_enabled): a positive per-call
# cap AND a finite session cap AND a non-empty asset allowlist AND a non-empty
# recipient allowlist. With those, a charge auto-settles only when it is within
# both caps and pays an allowlisted asset to an allowlisted recipient; anything
# else is declined — never silently paid, never left blocking on a prompt no
# human will answer. The caps bound atomic units of the (pinned) asset, and the
# asset/recipient pins keep a hostile seller from redirecting funds or inflating
# value via an unexpected token.
_AUTO_MAX_ATOMIC: int = 0
_AUTO_SESSION_ATOMIC: int = 0
_AUTO_ASSETS: set = set()       # allowed "<chainId>:<contract>" and bare "<contract>", lowercased
_AUTO_RECIPIENTS: set = set()   # allowed payTo addresses, lowercased
_auto_spent_atomic: int = 0     # cumulative unattended spend this process (guarded by _auto_lock)
_auto_lock = threading.Lock()   # serializes the session-budget check-and-reserve


def _norm_set(values) -> set:
    if isinstance(values, str):
        values = [values]
    return {str(v).strip().lower() for v in (values or []) if str(v).strip()}


def configure(rails: list, payment_headers: list,
              auto_max_atomic: int = 0, auto_session_atomic: int = 0,
              auto_assets=None, auto_recipients=None) -> None:
    global _RAILS, _PAYMENT_HEADERS, _AUTO_MAX_ATOMIC, _AUTO_SESSION_ATOMIC
    global _AUTO_ASSETS, _AUTO_RECIPIENTS, _auto_spent_atomic
    _RAILS = rails
    _PAYMENT_HEADERS = payment_headers or list(_DEFAULT_PAYMENT_HEADERS)
    _AUTO_MAX_ATOMIC = max(0, _safe_int(auto_max_atomic, 0, "auto_approve_max_atomic"))
    _AUTO_SESSION_ATOMIC = max(0, _safe_int(auto_session_atomic, 0, "auto_approve_session_atomic"))
    _AUTO_ASSETS = _norm_set(auto_assets)
    _AUTO_RECIPIENTS = _norm_set(auto_recipients)
    with _auto_lock:
        _auto_spent_atomic = 0  # reconfiguring resets the unattended spend tally


def _safe_int(value, default: int, name: str = "") -> int:
    """int() that degrades to ``default`` (logged) instead of crashing the plugin.

    A malformed cap must disable autonomy (fail-closed), never take the whole
    pay_mcp plugin — including the interactive confirm path — down with it.
    """
    try:
        return int(value)
    except (TypeError, ValueError):
        if value not in (None, ""):
            logger.warning("pay_mcp: ignoring malformed %s=%r; using %s", name, value, default)
        return default


def _auto_enabled() -> bool:
    """Unattended mode engages only with a complete, bounded policy."""
    return bool(
        _AUTO_MAX_ATOMIC > 0
        and _AUTO_SESSION_ATOMIC > 0
        and _AUTO_ASSETS
        and _AUTO_RECIPIENTS
    )


def load_config() -> dict:
    """Resolve pay_mcp settings from env then config.yaml (env wins).

    ``signer_url`` resolves to None unless explicitly opted in, keeping the
    plugin inert outside an obol environment. It activates when any of these is
    set: ``HERMES_X402_SIGNER_URL`` / ``REMOTE_SIGNER_URL`` env, a
    ``pay_mcp.signer_url`` in config.yaml, or ``pay_mcp.enabled: true`` (which
    then uses the obol in-pod default).
    """
    signer_url = None
    auth_ttl = None
    enabled = False
    payment_headers = None
    auto_max_atomic = 0
    auto_session_atomic = 0
    auto_assets = []
    auto_recipients = []
    try:
        from hermes_cli.config import load_config as _load, cfg_get
        raw = _load()
        signer_url = cfg_get(raw, "pay_mcp", "signer_url", default=None)
        auth_ttl = cfg_get(raw, "pay_mcp", "auth_ttl", default=None)
        enabled = bool(cfg_get(raw, "pay_mcp", "enabled", default=False))
        payment_headers = cfg_get(raw, "pay_mcp", "payment_headers", default=None)
        auto_max_atomic = cfg_get(raw, "pay_mcp", "auto_approve_max_atomic", default=0)
        auto_session_atomic = cfg_get(raw, "pay_mcp", "auto_approve_session_atomic", default=0)
        auto_assets = cfg_get(raw, "pay_mcp", "auto_approve_assets", default=[]) or []
        auto_recipients = cfg_get(raw, "pay_mcp", "auto_approve_recipients", default=[]) or []
    except Exception as exc:
        logger.debug("pay_mcp: config.yaml unavailable: %s", exc)

    signer_url = (
        os.environ.get("HERMES_X402_SIGNER_URL")
        or os.environ.get("REMOTE_SIGNER_URL")
        or signer_url
    )
    if not signer_url and enabled:
        signer_url = _DEFAULT_SIGNER_URL

    auth_ttl = os.environ.get("HERMES_X402_AUTH_TTL") or auth_ttl or rails_mod._DEFAULT_AUTH_TTL
    if isinstance(payment_headers, str):
        payment_headers = [payment_headers]
    if not payment_headers:
        payment_headers = list(_DEFAULT_PAYMENT_HEADERS)
    auto_max_atomic = os.environ.get("HERMES_X402_AUTO_MAX_ATOMIC") or auto_max_atomic or 0
    auto_session_atomic = (
        os.environ.get("HERMES_X402_AUTO_SESSION_ATOMIC") or auto_session_atomic or 0
    )
    # Comma-separated env override for the allowlists (CSV → list).
    for env, target in (("HERMES_X402_AUTO_ASSETS", "assets"),
                        ("HERMES_X402_AUTO_RECIPIENTS", "recipients")):
        raw_env = os.environ.get(env)
        if raw_env:
            items = [s for s in (p.strip() for p in raw_env.split(",")) if s]
            if target == "assets":
                auto_assets = items
            else:
                auto_recipients = items
    return {
        "signer_url": signer_url,
        "auth_ttl": _safe_int(auth_ttl, rails_mod._DEFAULT_AUTH_TTL, "auth_ttl"),
        "payment_headers": list(payment_headers),
        "auto_approve_max_atomic": _safe_int(auto_max_atomic, 0, "auto_approve_max_atomic"),
        "auto_approve_session_atomic": _safe_int(
            auto_session_atomic, 0, "auto_approve_session_atomic"),
        "auto_approve_assets": list(auto_assets),
        "auto_approve_recipients": list(auto_recipients),
    }


def handle_payment(server_name: str, challenge: dict) -> dict:
    """Parse a 402 challenge, confirm with the user, settle, return a voucher.

    Returns one of:
      * ``{"voucher_meta": {...}, "voucher_headers": {...}}`` — settled; core
        injects the channel-appropriate form (MCP _meta or HTTP header) + retries.
      * ``{"error": str, "declined": bool}`` — declined/failed; core surfaces it.
        ``declined`` marks user/policy outcomes that are NOT server faults.
      * ``{}`` — not a payment we handle; core falls through.
    """
    quote = x402.parse_challenge(
        challenge.get("headers") or {}, challenge.get("body") or "",
    )
    if quote is None:
        return {}  # not an x402 'exact' 402 we understand

    rail = rails_mod.select_rail(quote, _RAILS)
    if rail is None:
        return {
            "error": (
                f"No settlement rail can pay this charge "
                f"(scheme={quote.scheme}, network={quote.network})."
            ),
            "declined": True,
        }

    price = quote.human_amount()
    choice = _approve(quote, price, server_name, rail)
    if choice != "Pay":
        reason = ("exceeds the unattended payment policy"
                  if _auto_enabled() else "was declined")
        return {
            "error": f"Payment of {price} to {server_name} {reason}.",
            "declined": True,
        }

    try:
        payload = rail.settle(quote)   # raw x402 PaymentPayload dict
    except rails_mod.X402Error as exc:
        if _auto_enabled():
            _release_reservation(quote)  # settle raised → no charge → free the budget
        return {"error": f"Settlement failed: {exc}"}
    except Exception as exc:  # defensive — a rail bug must not crash the call
        logger.warning("pay_mcp: unexpected settle error: %s", exc)
        if _auto_enabled():
            _release_reservation(quote)
        return {"error": f"Settlement failed: {exc}"}

    logger.info("pay_mcp: settled %s to %s via %s (payTo=%s)",
                price, server_name, rail.name, quote.pay_to)
    header = x402.encode_header(payload)
    return {
        "voucher_meta": {x402.MCP_PAYMENT_META_KEY: payload},
        "voucher_headers": {name: header for name in _PAYMENT_HEADERS},
    }


def _approve(quote, price: str, server_name: str, rail) -> str:
    """Return "Pay" to settle, anything else to decline.

    Unattended policy first: when a COMPLETE autonomous policy is configured
    (:func:`_auto_enabled`), decide without a human — this is what lets an agent
    turn consume a signed authorization with no one watching. If unattended was
    requested via a cap but the policy is incomplete, refuse autonomy and fall
    back to the interactive confirm (which fail-closes when no human is present).
    """
    if _AUTO_MAX_ATOMIC > 0:
        if _auto_enabled():
            return _auto_approve(quote, price, server_name, rail)
        logger.error(
            "pay_mcp[auto]: unattended settle requested (auto_approve_max_atomic=%s) but "
            "policy is INCOMPLETE — also set auto_approve_session_atomic>0, "
            "auto_approve_assets, and auto_approve_recipients. Refusing autonomous settle.",
            _AUTO_MAX_ATOMIC)
    return confirm_price(
        price, server_name, recipient=quote.pay_to, rail=rail.name,
        asset=quote.asset, atomic=quote.amount,
    )


def _auto_approve(quote, price: str, server_name: str, rail) -> str:
    """Unattended settle within the pre-authorized policy (no human prompt).

    Approves only a charge that (a) pays an allowlisted asset, (b) pays an
    allowlisted recipient, (c) parses to ``0 < amount <= per-call cap``, and (d)
    keeps cumulative process spend within the session cap. Everything else is
    declined — never silently paid, never left blocking on an unanswerable
    prompt. The session-budget check-and-reserve is atomic under ``_auto_lock``
    so parallel tool calls cannot race past the cap; the reservation is rolled
    back by :func:`_release_reservation` if the settle then fails. Every decision
    logs at WARNING for an auditable spend trail — grep ``pay_mcp[auto]``.
    """
    global _auto_spent_atomic
    asset = (quote.asset or "").lower()
    if not asset or not ({f"{quote.chain_id}:{asset}", asset} & _AUTO_ASSETS):
        logger.warning("pay_mcp[auto]: DECLINED %s to %s — asset %s on chain %s not in "
                       "allowlist", price, server_name, quote.asset, quote.chain_id)
        return ""
    if (quote.pay_to or "").lower() not in _AUTO_RECIPIENTS:
        logger.warning("pay_mcp[auto]: DECLINED %s to %s — recipient %s not in allowlist",
                       price, server_name, quote.pay_to)
        return ""
    try:
        amt = int(str(quote.amount))
    except (TypeError, ValueError):
        logger.warning("pay_mcp[auto]: DECLINED %s to %s — unparseable amount %r",
                       price, server_name, quote.amount)
        return ""
    if amt <= 0 or amt > _AUTO_MAX_ATOMIC:
        logger.warning("pay_mcp[auto]: DECLINED %s (%s atomic) to %s — over per-call "
                       "cap %s atomic", price, amt, server_name, _AUTO_MAX_ATOMIC)
        return ""
    with _auto_lock:
        if _AUTO_SESSION_ATOMIC > 0 and _auto_spent_atomic + amt > _AUTO_SESSION_ATOMIC:
            logger.warning("pay_mcp[auto]: DECLINED %s to %s — session budget exhausted "
                           "(%s + %s > %s atomic)", price, server_name,
                           _auto_spent_atomic, amt, _AUTO_SESSION_ATOMIC)
            return ""
        _auto_spent_atomic += amt          # reserve under lock
        spent_now = _auto_spent_atomic
    logger.warning("pay_mcp[auto]: APPROVED (unattended) %s = %s atomic to %s via %s "
                   "(payTo=%s asset=%s); session spend %s/%s atomic",
                   price, amt, server_name, rail.name, quote.pay_to, quote.asset,
                   spent_now, _AUTO_SESSION_ATOMIC)
    return "Pay"


def _release_reservation(quote) -> None:
    """Roll back a reserved unattended amount when the settle FAILED (no charge)."""
    global _auto_spent_atomic
    try:
        amt = int(str(quote.amount))
    except (TypeError, ValueError):
        return
    with _auto_lock:
        _auto_spent_atomic = max(0, _auto_spent_atomic - amt)
    logger.warning("pay_mcp[auto]: released %s atomic reservation to %s after settle failure",
                   amt, quote.pay_to)


def confirm_price(price: str, server: str, recipient: str = "", rail: str = "",
                  asset: str = "", atomic: str = "") -> str:
    """In-chat price confirmation — blocks for Pay/Cancel on CLI + gateways.

    Surfaces the token contract and exact atomic amount (not just the
    6-decimal-assumed float) so the user approves what is actually signed.
    Returns "Pay" only on explicit approval; any other outcome (Cancel,
    timeout, undeliverable prompt) returns a non-"Pay" string.
    """
    detail = price
    if atomic:
        detail += f" (~{atomic} atomic units, 6-decimal assumed)"
    if asset:
        detail += f"\nToken contract: {asset}"
    question = (
        f"{server} is requesting payment of {detail}"
        + (f"\nRecipient: {recipient}" if recipient else "")
        + (f"\nRail: {rail}" if rail else "")
        + "\nApprove this payment?"
    )

    # session_key is a contextvar set by the gateway before agent.run — not a
    # handler kwarg. A registered clarify notify callback means we're on a chat
    # surface; otherwise fall back to the CLI prompt.
    from tools.approval import get_current_session_key
    from tools import clarify_gateway as clarify

    session_key = get_current_session_key()
    if session_key and clarify.get_notify(session_key) is not None:
        clarify_id = uuid.uuid4().hex[:10]
        entry = clarify.register(
            clarify_id=clarify_id, session_key=session_key,
            question=question, choices=["Pay", "Cancel"],
        )
        try:
            clarify.get_notify(session_key)(entry)
        except Exception as exc:
            logger.warning("pay_mcp: confirm notify failed: %s", exc)
            # Drop only THIS entry — clear_session would resolve every pending
            # clarify in the session with an empty answer.
            clarify.resolve_gateway_clarify(clarify_id, "")
            return ""
        response = clarify.wait_for_response(
            clarify_id, timeout=float(clarify.get_clarify_timeout()),
        )
        return response or ""

    # CLI / no-gateway fallback. Forward the thread-local prompt_toolkit
    # approval callback (hermes convention) so an interactive CLI shows the
    # modal instead of fail-closing to deny. Never permanently allowlist.
    from tools.approval import prompt_dangerous_approval
    try:
        from tools.terminal_tool import _get_approval_callback
        approval_cb = _get_approval_callback()
    except Exception:
        approval_cb = None
    decision = prompt_dangerous_approval(
        command=f"pay {price} to {server}",
        description=f"x402 payment confirmation ({detail})",
        allow_permanent=False,
        approval_callback=approval_cb,
    )
    return "Pay" if decision in {"once", "session", "always"} else "Cancel"
