#!/bin/sh
# tx-helper.sh — Pre-signing transaction utilities via Foundry's cast.
# Handles gas estimation, ABI encoding, calldata construction, and unit conversion.
# Signing is still done via Web3Signer (signer.py) — this script prepares tx data.
#
# Usage: sh scripts/tx-helper.sh [--network <name>] <command> [args...]
#
# Environment:
#   ERPC_URL      Base URL for eRPC gateway (default: http://erpc.erpc.svc.cluster.local:4000/rpc)
#   ERPC_NETWORK  Default network (default: mainnet)
set -eu

ERPC_BASE="${ERPC_URL:-http://erpc.erpc.svc.cluster.local:4000/rpc}"
NETWORK="${ERPC_NETWORK:-mainnet}"

# Parse --network flag
while [ $# -gt 0 ]; do
    case "$1" in
        --network) NETWORK="$2"; shift 2 ;;
        --network=*) NETWORK="${1#--network=}"; shift ;;
        *) break ;;
    esac
done

RPC_URL="${ERPC_BASE}/${NETWORK}"

if [ $# -eq 0 ]; then
    echo "Usage: sh scripts/tx-helper.sh [--network <name>] <command> [args...]"
    echo ""
    echo "Commands:"
    echo "  estimate <to> <sig> [args...]      Gas estimate for a contract call"
    echo "  estimate-simple <to> [value]        Gas estimate for a simple ETH transfer"
    echo "  calldata <sig> [args...]            Encode function calldata"
    echo "  decode-tx <raw-tx>                  Decode a raw signed transaction"
    echo "  interface <address>                 Fetch contract ABI (from Etherscan)"
    echo "  to-wei <amount> [unit]              Convert to wei"
    echo "  from-wei <amount> [unit]            Convert from wei"
    echo "  to-hex <decimal>                    Decimal to hex"
    echo "  from-hex <hex>                      Hex to decimal"
    echo "  checksum <address>                  EIP-55 checksum an address"
    echo "  keccak <data>                       Keccak256 hash"
    echo "  sig <function-signature>            Get 4-byte function selector"
    exit 0
fi

CMD="$1"; shift

case "$CMD" in
    estimate)
        [ $# -lt 2 ] && { echo "Usage: estimate <to> <sig> [args...]"; exit 1; }
        TO="$1"; SIG="$2"; shift 2
        cast estimate "$TO" "$SIG" "$@" --rpc-url "$RPC_URL"
        ;;

    estimate-simple)
        [ $# -lt 1 ] && { echo "Usage: estimate-simple <to> [value]"; exit 1; }
        TO="$1"
        VALUE="${2:-0}"
        cast estimate "$TO" --value "$VALUE" --rpc-url "$RPC_URL"
        ;;

    calldata)
        [ $# -lt 1 ] && { echo "Usage: calldata <sig> [args...]"; exit 1; }
        cast calldata "$@"
        ;;

    decode-tx)
        [ $# -lt 1 ] && { echo "Usage: decode-tx <raw-tx>"; exit 1; }
        cast decode-transaction "$1"
        ;;

    interface)
        [ $# -lt 1 ] && { echo "Usage: interface <address>"; exit 1; }
        cast interface "$1" --rpc-url "$RPC_URL"
        ;;

    to-wei)
        [ $# -lt 1 ] && { echo "Usage: to-wei <amount> [unit]"; exit 1; }
        UNIT="${2:-ether}"
        cast to-wei "$1" "$UNIT"
        ;;

    from-wei)
        [ $# -lt 1 ] && { echo "Usage: from-wei <amount> [unit]"; exit 1; }
        UNIT="${2:-ether}"
        cast from-wei "$1" "$UNIT"
        ;;

    to-hex)
        [ $# -lt 1 ] && { echo "Usage: to-hex <decimal>"; exit 1; }
        cast to-hex "$1"
        ;;

    from-hex)
        [ $# -lt 1 ] && { echo "Usage: from-hex <hex>"; exit 1; }
        cast to-dec "$1"
        ;;

    checksum)
        [ $# -lt 1 ] && { echo "Usage: checksum <address>"; exit 1; }
        cast to-check-sum-address "$1"
        ;;

    keccak)
        [ $# -lt 1 ] && { echo "Usage: keccak <data>"; exit 1; }
        cast keccak "$1"
        ;;

    sig)
        [ $# -lt 1 ] && { echo "Usage: sig <function-signature>"; exit 1; }
        cast sig "$1"
        ;;

    *)
        echo "Unknown command: $CMD"
        echo "Run without arguments to see usage."
        exit 1
        ;;
esac
