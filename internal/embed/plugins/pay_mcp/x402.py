"""x402 v2 'exact' wire helpers — parse a 402 challenge and build a voucher.

Pure functions, no I/O. Mirrors the obol ``buy.py`` envelope (which declares
``x402Version: 2`` with the ``amount``/``accepted`` v2 field shape) and the
official x402 v2 ``PaymentPayload``. NOTE: the obol/x402-rs ecosystem reads the
voucher from the ``X-PAYMENT`` header carrying this v2 body, while a strictly
standards-conformant v2 server reads ``PAYMENT-SIGNATURE``; the header name is
caller-configurable (see payment.py). v1-shaped bodies are out of scope.
"""

from __future__ import annotations

import base64
import json
from dataclasses import dataclass, field
from typing import Optional

# MCP transport (specs/transports-v2/mcp.md): the voucher rides the MCP request
# _meta under this key; the settle receipt comes back under the response key.
MCP_PAYMENT_META_KEY = "x402/payment"
MCP_PAYMENT_RESPONSE_META_KEY = "x402/payment-response"

# CAIP-2 id / chain name -> numeric EVM chain id (resolution only; settle
# support is gated separately in rails.ObolSignerRail.can_settle).
_CHAIN_IDS = {
    "base": 8453, "base-mainnet": 8453, "base-sepolia": 84532,
    "ethereum": 1, "mainnet": 1,
}

# Authoritative per-chain USDC EIP-712 signing domain (name, version). Mirrors
# obol buy.py's ``USDC_EIP712_DOMAIN`` table. Only chains with a known-correct
# EIP-3009 (chainId-based) domain belong here. For these chains the table is
# AUTHORITATIVE over the seller-advertised ``extra.name``/``extra.version`` —
# those mirror the contract's ``name()`` getter, a human-readable display string
# that is NOT always equal to the EIP-712 signing domain (e.g. base-sepolia USDC
# advertises "USD Coin" via name() but its EIP-712 domain is "USDC"; see
# ObolNetwork/obol-stack#612). Signing with the wrong name yields a valid-looking
# signature the facilitator silently rejects.
_USDC_DOMAIN = {
    8453: ("USD Coin", "2"),
    84532: ("USDC", "2"),
    1: ("USD Coin", "2"),
}


@dataclass
class Quote:
    """A normalized, settle-ready x402 'exact' payment requirement."""

    scheme: str
    network: str               # CAIP-2, e.g. "eip155:84532"
    amount: str                # atomic units, decimal string
    asset: str                 # token contract address
    pay_to: str                # recipient address
    max_timeout: int = 60
    extra: dict = field(default_factory=dict)
    accepted: dict = field(default_factory=dict)  # raw requirement, echoed back

    @property
    def chain_id(self) -> int:
        return chain_id_of(self.network)

    def human_amount(self) -> str:
        return human_amount(self.amount, self.extra)


def chain_id_of(network: str) -> int:
    """Resolve an EVM chain id from a CAIP-2 id or a chain name (0 if unknown)."""
    net = (network or "").strip().lower()
    if net.startswith("eip155:"):
        try:
            return int(net.split(":", 1)[1])
        except ValueError:
            return 0
    return _CHAIN_IDS.get(net, 0)


def human_amount(atomic: str, extra: Optional[dict] = None) -> str:
    """Best-effort human price. Assumes 6-decimal stablecoins (USDC/EURC).

    The token contract and exact atomic amount are surfaced separately in the
    confirmation, so this is a hint, not the authoritative value.
    """
    symbol = "USDC"
    if extra and isinstance(extra.get("name"), str) and extra["name"]:
        symbol = extra["name"]
    try:
        value = int(str(atomic)) / 1_000_000
    except (TypeError, ValueError):
        return f"{atomic} {symbol} (atomic units)"
    text = f"{value:.6f}".rstrip("0").rstrip(".")
    return f"{text} {symbol}"


def _b64_json(blob: str) -> Optional[dict]:
    try:
        return json.loads(base64.b64decode(blob).decode("utf-8"))
    except Exception:
        return None


def parse_challenge(headers: dict, body: str) -> Optional[Quote]:
    """Parse an x402 (v2, 'exact') 402 into a :class:`Quote`.

    Reads requirements from the ``PAYMENT-REQUIRED`` header (standard v2,
    base64 JSON) first — header-only, no response body needed — then falls back
    to an ``accepts[]`` array in the JSON body (obol/x402-rs and v1 style).
    Returns None if no usable 'exact' requirement is present.
    """
    accepts: list = []

    header_blob = None
    for key in ("payment-required", "x-payment-required"):
        for name, value in (headers or {}).items():
            if name.lower() == key and value:
                header_blob = value
                break
        if header_blob:
            break
    if header_blob:
        decoded = _b64_json(header_blob)
        if isinstance(decoded, dict):
            accepts = decoded.get("accepts") or []

    if not accepts and body:
        try:
            doc = json.loads(body)
        except Exception:
            doc = None
        if isinstance(doc, dict):
            accepts = doc.get("accepts") or []

    for req in accepts:
        if not isinstance(req, dict) or req.get("scheme", "exact") != "exact":
            continue
        amount = req.get("amount", req.get("maxAmountRequired"))
        if amount is None:
            continue
        return Quote(
            scheme="exact",
            network=str(req.get("network", "")),
            amount=str(amount),
            asset=str(req.get("asset", "")),
            pay_to=str(req.get("payTo", "")),
            max_timeout=int(req.get("maxTimeoutSeconds", 60) or 60),
            extra=req.get("extra") or {},
            accepted=req,
        )
    return None


def detect_challenge(structured, text: str) -> Optional[dict]:
    """Return the x402 challenge doc from an MCP isError result, or None.

    The in-band detector core calls (via ``tools.payment_hook``) to decide
    whether an isError ``CallToolResult`` is an x402 payment challenge —
    cheap and side-effect-free. The canonical paid-MCP path (x402's MCP
    wrapper) carries ``{"x402Version": ..., "accepts": [...]}`` in
    structuredContent or the text block over HTTP 200 (not an HTTP 402).
    """
    if isinstance(structured, dict) and (
        "x402Version" in structured or "accepts" in structured
    ):
        return structured
    if text:
        try:
            doc = json.loads(text)
        except (json.JSONDecodeError, TypeError):
            doc = None
        if isinstance(doc, dict) and ("x402Version" in doc or "accepts" in doc):
            return doc
    return None


def eip3009_typed_data(quote: Quote, authorization: dict) -> dict:
    """Build the EIP-712 ``TransferWithAuthorization`` payload to sign.

    Domain resolution mirrors obol ``buy.py::_resolve_eip3009_domain`` — the
    seller-advertised ``extra.name``/``extra.version`` are display-only for known
    chains and are NOT trusted as the signing domain:

      1. an explicit full domain object (``extra.eip712Domain`` with both
         ``name`` and ``version``) — Obol convention, authoritative;
      2. else, for a chain in the known-good :data:`_USDC_DOMAIN` table, the
         TABLE value (authoritative; the human-readable ``extra.name`` is
         ignored so a seller advertising a wrong name — e.g. base-sepolia USDC
         reporting "USD Coin", ObolNetwork/obol-stack#612 — cannot make us sign
         the wrong domain);
      3. else (unknown chain) the seller-advertised ``extra.name``/``version``
         if present;
      4. else refuse (raise): no trusted domain and nothing advertised.
    """
    advertised_domain = (quote.extra or {}).get("eip712Domain") or {}
    adv_name = advertised_domain.get("name") if isinstance(advertised_domain, dict) else None
    adv_version = advertised_domain.get("version") if isinstance(advertised_domain, dict) else None
    if adv_name and adv_version:
        # (1) explicit full domain object — honor it first.
        name, version = adv_name, adv_version
    elif quote.chain_id in _USDC_DOMAIN:
        # (2) known chain — the table wins over display-only extra.name/version.
        name, version = _USDC_DOMAIN[quote.chain_id]
    else:
        # (3) unknown chain — fall back to advertised extra.name/version.
        name = (quote.extra or {}).get("name")
        version = (quote.extra or {}).get("version")
        if not name or not version:
            # (4) nothing trustworthy and nothing advertised — refuse to sign.
            raise ValueError(
                f"refusing to sign: no trusted EIP-712 domain for chainId "
                f"{quote.chain_id} and the seller omitted extra.eip712Domain "
                f"and extra.name/version"
            )
    return {
        "types": {
            "EIP712Domain": [
                {"name": "name", "type": "string"},
                {"name": "version", "type": "string"},
                {"name": "chainId", "type": "uint256"},
                {"name": "verifyingContract", "type": "address"},
            ],
            "TransferWithAuthorization": [
                {"name": "from", "type": "address"},
                {"name": "to", "type": "address"},
                {"name": "value", "type": "uint256"},
                {"name": "validAfter", "type": "uint256"},
                {"name": "validBefore", "type": "uint256"},
                {"name": "nonce", "type": "bytes32"},
            ],
        },
        "primaryType": "TransferWithAuthorization",
        "domain": {
            "name": name,
            "version": version,
            "chainId": quote.chain_id,
            "verifyingContract": quote.asset,
        },
        "message": dict(authorization),
    }


def build_payment_payload(quote: Quote, authorization: dict, signature: str) -> dict:
    """The raw x402 v2 ``PaymentPayload`` dict.

    Used verbatim as the MCP ``_meta["x402/payment"]`` value (canonical paid-MCP),
    or base64-encoded via :func:`encode_header` for the HTTP ``X-PAYMENT`` header.
    """
    return {
        "x402Version": 2,
        "accepted": quote.accepted,  # the seller's exact requirement, echoed back
        "payload": {
            "signature": signature,
            "authorization": authorization,
        },
    }


def encode_header(payload: dict) -> str:
    """Base64 a ``PaymentPayload`` for an HTTP ``X-PAYMENT`` / ``PAYMENT-SIGNATURE`` header."""
    return base64.b64encode(
        json.dumps(payload, separators=(",", ":")).encode("utf-8")
    ).decode("ascii")


def build_payment_header(quote: Quote, authorization: dict, signature: str) -> str:
    """Convenience: the base64 HTTP-header form of the voucher."""
    return encode_header(build_payment_payload(quote, authorization, signature))
