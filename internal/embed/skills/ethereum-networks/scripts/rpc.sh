#!/bin/sh
# rpc.sh — Query Ethereum networks via Foundry's cast CLI.
# Uses the in-cluster eRPC gateway. Prefer this over rpc.py when cast is available.
#
# Usage: sh scripts/rpc.sh [--network <name>] <command> [args...]
#
# Environment:
#   ERPC_URL      Base URL for eRPC gateway (default: http://rpc.erpc.svc.cluster.local)
#   ERPC_NETWORK  Default network (default: mainnet)
set -eu

ERPC_BASE="${ERPC_URL:-http://rpc.erpc.svc.cluster.local}"
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
    echo "Usage: sh scripts/rpc.sh [--network <name>] <command> [args...]"
    echo ""
    echo "Commands:"
    echo "  balance <address>                 ETH balance in ether"
    echo "  block [number|latest]             Block details"
    echo "  tx <hash>                         Transaction details"
    echo "  receipt <hash>                     Transaction receipt"
    echo "  call <to> <sig> [args...]          Contract read (with ABI decoding)"
    echo "  estimate <to> <sig> [args...]      Gas estimate for a call"
    echo "  chain-id                           Chain ID"
    echo "  gas-price                          Current gas price"
    echo "  base-fee                           Current base fee"
    echo "  nonce <address>                    Transaction count"
    echo "  code <address>                     Contract bytecode"
    echo "  ens <name>                         Resolve ENS name"
    echo "  from-wei <value> [unit]            Convert from wei"
    echo "  to-wei <value> [unit]              Convert to wei"
    echo "  4byte <selector>                   Decode function selector"
    echo "  abi-decode <sig> <data>            Decode ABI-encoded data"
    echo "  logs <address> [topic0] [--from-block N] [--to-block N]"
    echo "  raw <method> [params...]           Raw JSON-RPC method"
    exit 0
fi

CMD="$1"; shift

case "$CMD" in
    balance)
        [ $# -lt 1 ] && { echo "Usage: balance <address>"; exit 1; }
        cast balance "$1" --ether --rpc-url "$RPC_URL"
        ;;

    block)
        BLOCK="${1:-latest}"
        cast block "$BLOCK" --rpc-url "$RPC_URL"
        ;;

    tx)
        [ $# -lt 1 ] && { echo "Usage: tx <hash>"; exit 1; }
        cast tx "$1" --rpc-url "$RPC_URL"
        ;;

    receipt)
        [ $# -lt 1 ] && { echo "Usage: receipt <hash>"; exit 1; }
        cast receipt "$1" --rpc-url "$RPC_URL"
        ;;

    call)
        [ $# -lt 2 ] && { echo "Usage: call <to> <sig> [args...]"; exit 1; }
        TO="$1"; SIG="$2"; shift 2
        cast call "$TO" "$SIG" "$@" --rpc-url "$RPC_URL"
        ;;

    estimate)
        [ $# -lt 2 ] && { echo "Usage: estimate <to> <sig> [args...]"; exit 1; }
        TO="$1"; SIG="$2"; shift 2
        cast estimate "$TO" "$SIG" "$@" --rpc-url "$RPC_URL"
        ;;

    chain-id)
        cast chain-id --rpc-url "$RPC_URL"
        ;;

    gas-price)
        cast gas-price --rpc-url "$RPC_URL"
        ;;

    base-fee)
        cast base-fee --rpc-url "$RPC_URL"
        ;;

    nonce)
        [ $# -lt 1 ] && { echo "Usage: nonce <address>"; exit 1; }
        cast nonce "$1" --rpc-url "$RPC_URL"
        ;;

    code)
        [ $# -lt 1 ] && { echo "Usage: code <address>"; exit 1; }
        cast code "$1" --rpc-url "$RPC_URL"
        ;;

    ens)
        [ $# -lt 1 ] && { echo "Usage: ens <name>"; exit 1; }
        cast resolve-name "$1" --rpc-url "$RPC_URL"
        ;;

    from-wei)
        [ $# -lt 1 ] && { echo "Usage: from-wei <value> [unit]"; exit 1; }
        UNIT="${2:-ether}"
        cast from-wei "$1" "$UNIT"
        ;;

    to-wei)
        [ $# -lt 1 ] && { echo "Usage: to-wei <value> [unit]"; exit 1; }
        UNIT="${2:-ether}"
        cast to-wei "$1" "$UNIT"
        ;;

    4byte)
        [ $# -lt 1 ] && { echo "Usage: 4byte <selector>"; exit 1; }
        cast 4byte "$1"
        ;;

    abi-decode)
        [ $# -lt 2 ] && { echo "Usage: abi-decode <sig> <data>"; exit 1; }
        cast abi-decode "$1" "$2"
        ;;

    logs)
        [ $# -lt 1 ] && { echo "Usage: logs <address> [topic0] [--from-block N] [--to-block N]"; exit 1; }
        cast logs "$@" --rpc-url "$RPC_URL"
        ;;

    raw)
        [ $# -lt 1 ] && { echo "Usage: raw <method> [params...]"; exit 1; }
        cast rpc "$@" --rpc-url "$RPC_URL"
        ;;

    *)
        echo "Unknown command: $CMD"
        echo "Run without arguments to see usage."
        exit 1
        ;;
esac
