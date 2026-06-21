"""Settlement rails — the pluggable 'generic credit layer'.

A rail turns a normalized :class:`~plugins.pay_mcp.x402.Quote` into the base64
voucher to attach to the retried MCP call. The default rail signs an x402
'exact' EIP-3009 voucher with the obol-agent's wallet via the remote-signer —
the same path obol's ``buy.py`` uses, so no private key lives in-process and no
new dependency is required. Add a rail (MPP, a local key, a prepaid pool) by
subclassing :class:`SettlementRail` and wiring it into :func:`build_rails`.
"""

from __future__ import annotations

import logging
import secrets
import time
from abc import ABC, abstractmethod
from typing import Optional

import httpx

from . import x402
from .x402 import Quote

logger = logging.getLogger(__name__)

# Voucher validity window (seconds). It only needs to cover sign+settle
# latency since the voucher is settled on the immediate retry; obol's buy.py
# defaults to 30 days, we keep it short.
_DEFAULT_AUTH_TTL = 600

# EVM chains we can correctly sign an EIP-3009 ('exact') voucher for today.
# Each must have a known-correct chainId-based USDC EIP-712 domain in x402.py;
# chains with a salt-based domain (e.g. Polygon native USDC) are excluded so we
# refuse rather than mint an invalid voucher.
_SUPPORTED_CHAIN_IDS = {8453, 84532, 1}


class X402Error(RuntimeError):
    """A settlement-rail failure (signing, network, or configuration)."""


class SettlementRail(ABC):
    """A pluggable settlement source behind one interface."""

    name: str = "rail"

    @abstractmethod
    def can_settle(self, quote: Quote) -> bool:
        """Return True if this rail can satisfy ``quote``."""

    @abstractmethod
    def settle(self, quote: Quote) -> dict:
        """Settle ``quote``; return the raw x402 ``PaymentPayload`` dict.

        The plugin adapts it per delivery channel: the MCP ``_meta`` value, or a
        base64 HTTP header (see :func:`plugins.pay_mcp.x402.encode_header`).
        """


class ObolSignerRail(SettlementRail):
    """obol-agent rail: sign an x402 'exact' EIP-3009 voucher via the agent's
    wallet, held by the obol remote-signer (mirrors ``buy.py``/``signer.py``).
    """

    name = "obol"

    def __init__(self, signer_url: str, auth_ttl: int = _DEFAULT_AUTH_TTL,
                 timeout: float = 30.0) -> None:
        self.signer_url = signer_url.rstrip("/")
        self.auth_ttl = auth_ttl
        self.timeout = timeout

    def can_settle(self, quote: Quote) -> bool:
        # EVM 'exact' (EIP-3009) on a chain we can sign a valid domain for.
        return (
            quote.scheme == "exact"
            and quote.chain_id in _SUPPORTED_CHAIN_IDS
            and bool(quote.pay_to)
            and bool(self.signer_url)
        )

    def settle(self, quote: Quote) -> dict:
        signer_address = self._signer_address()
        now = int(time.time())
        authorization = {
            "from": signer_address,
            "to": quote.pay_to,
            "value": str(quote.amount),
            "validAfter": "0",
            "validBefore": str(now + self.auth_ttl),
            "nonce": "0x" + secrets.token_hex(32),
        }
        typed_data = x402.eip3009_typed_data(quote, authorization)
        signature = self._sign(signer_address, typed_data)
        return x402.build_payment_payload(quote, authorization, signature)

    # -- remote-signer HTTP (sync httpx; mirrors obol signer.py) --

    def _signer_address(self) -> str:
        try:
            resp = httpx.get(f"{self.signer_url}/api/v1/keys", timeout=self.timeout)
            resp.raise_for_status()
            keys = (resp.json() or {}).get("keys") or []
        except Exception as exc:
            raise X402Error(
                f"remote-signer unreachable at {self.signer_url}: {exc}"
            ) from exc
        if not keys:
            raise X402Error("remote-signer has no keys (wallet not provisioned)")
        return keys[0]

    def _sign(self, address: str, typed_data: dict) -> str:
        try:
            resp = httpx.post(
                f"{self.signer_url}/api/v1/sign/{address}/typed-data",
                json=typed_data, timeout=self.timeout,
            )
            resp.raise_for_status()
            signature = (resp.json() or {}).get("signature", "")
        except Exception as exc:
            raise X402Error(f"remote-signer typed-data signing failed: {exc}") from exc
        if not signature:
            raise X402Error("remote-signer returned an empty signature")
        return signature


def build_rails(config: dict) -> list[SettlementRail]:
    """Construct enabled rails from plugin config.

    Returns an empty list when nothing is configured, leaving the plugin inert
    (the payment handler is never registered).
    """
    rails: list[SettlementRail] = []
    signer_url = config.get("signer_url")
    if signer_url:
        rails.append(ObolSignerRail(
            signer_url=signer_url,
            auth_ttl=int(config.get("auth_ttl", _DEFAULT_AUTH_TTL)),
        ))
    return rails


def select_rail(quote: Quote, rails: list[SettlementRail]) -> Optional[SettlementRail]:
    """Return the first rail (by priority order) that can settle ``quote``."""
    for rail in rails:
        try:
            if rail.can_settle(quote):
                return rail
        except Exception as exc:  # a probe must never crash selection
            logger.debug("pay_mcp: rail %s can_settle raised: %s", rail.name, exc)
    return None
