"""pay_mcp — settle paid MCP tools (HTTP 402) via pluggable x402 rails.

When a paid MCP server returns a 402, the plugin settles through a configured
rail (the obol-agent wallet today) and retries with the x402 voucher. It wires
into the MCP call path one of two ways, picked automatically at register():

  * **core seam** — when ``tools/payment_hook.py`` + the ``tools/mcp_tool.py``
    interceptor are present (a hermes build that carries the upstream-mergeable
    seam), the core calls this plugin's handler. Preferred.
  * **self-wired** — otherwise (a stock upstream image with the plugin merely
    dropped into the user-plugins dir), the plugin wraps the MCP SDK's
    ``ClientSession.call_tool`` itself (see ``recovery.install``). This is what
    lets obol inject the plugin by default without rebuilding the image.

Settlement requires an in-chat confirmation by default; an opt-in unattended
policy (caps + asset/recipient allowlists) lets autonomous turns settle.
"""

from __future__ import annotations

import logging

from . import payment, rails, x402

logger = logging.getLogger(__name__)


def register(ctx) -> None:
    """Activate the pay-for-MCP settlement handler.

    Builds the configured settlement rails and registers the 402 payment
    handler. Stays inert (handler not registered) when no rail is configured,
    so it is harmless to bundle and only acts where a signer is available.
    """
    config = payment.load_config()
    built = rails.build_rails(config)
    if not built:
        logger.info("pay_mcp: no settlement rails configured — inactive")
        return

    payment.configure(
        built, config["payment_headers"],
        auto_max_atomic=config.get("auto_approve_max_atomic", 0),
        auto_session_atomic=config.get("auto_approve_session_atomic", 0),
        auto_assets=config.get("auto_approve_assets", []),
        auto_recipients=config.get("auto_approve_recipients", []),
    )
    # Prefer the core seam; fall back to self-wiring on a stock image.
    try:
        from tools.payment_hook import register_payment_recovery
        register_payment_recovery(payment.handle_payment, detector=x402.detect_challenge)
        wiring = "core-seam"
    except ImportError:
        from . import recovery
        wiring = "self-wired" if recovery.install() else "INERT (self-wire failed)"
    unattended = (
        f"<= {payment._AUTO_MAX_ATOMIC} atomic/call, session <= "
        f"{payment._AUTO_SESSION_ATOMIC}, {len(payment._AUTO_ASSETS)} asset(s)/"
        f"{len(payment._AUTO_RECIPIENTS)} payee(s) allowlisted"
        if payment._auto_enabled()
        else ("off — confirm required"
              + (" (incomplete unattended policy ignored)"
                 if config.get("auto_approve_max_atomic", 0) else ""))
    )
    logger.info("pay_mcp: active [%s] with rails: %s (headers: %s; unattended: %s)",
                wiring, ", ".join(r.name for r in built),
                ", ".join(config["payment_headers"]), unattended)
