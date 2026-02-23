#!/bin/sh
# identity.sh — ERC-8004 agent identity lifecycle management via Foundry's cast.
# Handles registration, metadata, reputation, validation, and IPFS pinning.
#
# Read operations use cast call directly via eRPC.
# Write operations encode calldata with cast, then delegate to signer.py for signing/submission.
#
# Usage: sh scripts/identity.sh [--network <name>] [--from <address>] <command> [args...]
#
# Environment:
#   ERPC_URL        Base URL for eRPC gateway (default: http://erpc.erpc.svc.cluster.local:4000/rpc)
#   ERPC_NETWORK    Default network (default: mainnet)
#   SIGNER_SCRIPT   Path to signer.py (default: ../local-ethereum-wallet/scripts/signer.py)
#   IPFS_API        IPFS API endpoint (default: http://ipfs.ipfs.svc.cluster.local:5001/api/v0)
set -eu

ERPC_BASE="${ERPC_URL:-http://erpc.erpc.svc.cluster.local:4000/rpc}"
NETWORK="${ERPC_NETWORK:-mainnet}"
FROM_ADDR=""
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SIGNER="${SIGNER_SCRIPT:-$SCRIPT_DIR/../../local-ethereum-wallet/scripts/signer.py}"
IPFS_API="${IPFS_API:-http://ipfs.ipfs.svc.cluster.local:5001/api/v0}"

# Contract addresses (same on all chains via CREATE2)
IDENTITY_REGISTRY="0x8004A169FB4a3325136EB29fA0ceB6D2e539a432"
REPUTATION_REGISTRY="0x8004BAa17C55a88189AE136b182e5fdA19dE9b63"
# ValidationRegistry uses same CREATE2 pattern — update when deployed
VALIDATION_REGISTRY="${ERC8004_VALIDATION_REGISTRY:-}"

# Parse global flags
while [ $# -gt 0 ]; do
    case "$1" in
        --network) NETWORK="$2"; shift 2 ;;
        --network=*) NETWORK="${1#--network=}"; shift ;;
        --from) FROM_ADDR="$2"; shift 2 ;;
        --from=*) FROM_ADDR="${1#--from=}"; shift ;;
        *) break ;;
    esac
done

RPC_URL="${ERPC_BASE}/${NETWORK}"

# --- Helpers ---

die() { echo "ERROR: $*" >&2; exit 1; }

require_from() {
    [ -n "$FROM_ADDR" ] || die "--from <address> is required for write operations"
}

require_validation_registry() {
    [ -n "$VALIDATION_REGISTRY" ] || die "ValidationRegistry address not set. Export ERC8004_VALIDATION_REGISTRY=0x..."
}

# Encode calldata, show confirmation, send via signer.py
send_tx() {
    TARGET="$1"; shift
    CALLDATA="$1"; shift
    DESCRIPTION="${1:-Contract call}"

    echo ""
    echo "=== Transaction Preview ==="
    echo "  Target:   $TARGET"
    echo "  From:     $FROM_ADDR"
    echo "  Network:  $NETWORK"
    echo "  Action:   $DESCRIPTION"
    echo "  Calldata: ${CALLDATA}"
    echo ""

    # Gas estimate
    GAS=$(cast estimate "$TARGET" "$CALLDATA" --from "$FROM_ADDR" --rpc-url "$RPC_URL" 2>/dev/null || echo "unknown")
    echo "  Gas estimate: $GAS"
    echo ""
    echo "Confirm? [y/N] "
    read -r CONFIRM < /dev/tty 2>/dev/null || CONFIRM="y"
    case "$CONFIRM" in
        y|Y|yes|YES) ;;
        *) echo "Aborted."; exit 0 ;;
    esac

    python3 "$SIGNER" send-tx --from "$FROM_ADDR" --to "$TARGET" --data "$CALLDATA" --network "$NETWORK"
}

# --- Usage ---

if [ $# -eq 0 ]; then
    echo "Usage: sh scripts/identity.sh [--network <name>] [--from <address>] <command> [args...]"
    echo ""
    echo "Identity Registry (register, update, query agents):"
    echo "  register [--uri <ipfs://...>]             Register a new agent identity"
    echo "  set-uri <agentId> <newURI>                Update agent's registration URI"
    echo "  set-metadata <agentId> <key> <hexValue>   Set metadata key-value pair"
    echo "  unset-wallet <agentId>                    Remove agent wallet association"
    echo "  agent-uri <agentId>                       Read agent's registration URI"
    echo "  owner <agentId>                           Read agent's owner address"
    echo "  agent-wallet <agentId>                    Read agent's wallet address"
    echo "  metadata <agentId> <key>                  Read metadata value"
    echo "  balance <address>                         Count of agents owned by address"
    echo ""
    echo "Reputation Registry (feedback lifecycle):"
    echo "  feedback <agentId> <value> <decimals> <tag1> <tag2> [opts]"
    echo "                                            Give feedback to an agent"
    echo "  revoke-feedback <agentId> <feedbackIndex> Revoke your feedback"
    echo "  respond <agentId> <client> <index> <uri> <hash>"
    echo "                                            Respond to feedback as agent owner"
    echo "  reputation <agentId> [--tag1 <t>] [--tag2 <t>]"
    echo "                                            Query aggregated reputation"
    echo "  read-feedback <agentId> <client> <index>  Read single feedback entry"
    echo "  clients <agentId>                         List all feedback clients"
    echo ""
    echo "Validation Registry (third-party verification):"
    echo "  request-validation <validator> <agentId> <uri> <hash>"
    echo "  validation-response <requestHash> <response> <uri> <hash> <tag>"
    echo "  validation-status <requestHash>           Query validation status"
    echo "  agent-validations <agentId>               List validation hashes"
    echo "  validation-summary <agentId> [--tag <t>]  Aggregated validation score"
    echo ""
    echo "Events:"
    echo "  events registered [--from-block N]        Registration events"
    echo "  events feedback <agentId> [--from-block N]"
    echo "  events uri-updated <agentId> [--from-block N]"
    echo ""
    echo "IPFS:"
    echo "  prepare-registration --name <n> --description <d> [opts]"
    echo "  pin <file>                                Pin file to IPFS, return CID"
    echo "  pin-registration --name <n> --description <d> [opts]"
    exit 0
fi

CMD="$1"; shift

case "$CMD" in

# ============================================================
# IDENTITY REGISTRY — WRITE
# ============================================================

register)
    require_from
    URI=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --uri) URI="$2"; shift 2 ;;
            --uri=*) URI="${1#--uri=}"; shift ;;
            *) die "Unknown flag: $1" ;;
        esac
    done

    if [ -n "$URI" ]; then
        CALLDATA=$(cast calldata "register(string)" "$URI")
    else
        CALLDATA=$(cast calldata "register()")
    fi
    send_tx "$IDENTITY_REGISTRY" "$CALLDATA" "Register agent${URI:+ with URI: $URI}"
    ;;

set-uri)
    require_from
    [ $# -lt 2 ] && die "Usage: set-uri <agentId> <newURI>"
    AGENT_ID="$1"; NEW_URI="$2"
    CALLDATA=$(cast calldata "setAgentURI(uint256,string)" "$AGENT_ID" "$NEW_URI")
    send_tx "$IDENTITY_REGISTRY" "$CALLDATA" "Update URI for agent $AGENT_ID to $NEW_URI"
    ;;

set-metadata)
    require_from
    [ $# -lt 3 ] && die "Usage: set-metadata <agentId> <key> <hexValue>"
    AGENT_ID="$1"; KEY="$2"; VALUE="$3"
    CALLDATA=$(cast calldata "setMetadata(uint256,string,bytes)" "$AGENT_ID" "$KEY" "$VALUE")
    send_tx "$IDENTITY_REGISTRY" "$CALLDATA" "Set metadata '$KEY' on agent $AGENT_ID"
    ;;

unset-wallet)
    require_from
    [ $# -lt 1 ] && die "Usage: unset-wallet <agentId>"
    AGENT_ID="$1"
    CALLDATA=$(cast calldata "unsetAgentWallet(uint256)" "$AGENT_ID")
    send_tx "$IDENTITY_REGISTRY" "$CALLDATA" "Unset wallet for agent $AGENT_ID"
    ;;

# ============================================================
# IDENTITY REGISTRY — READ
# ============================================================

agent-uri)
    [ $# -lt 1 ] && die "Usage: agent-uri <agentId>"
    cast call "$IDENTITY_REGISTRY" "tokenURI(uint256)(string)" "$1" --rpc-url "$RPC_URL"
    ;;

owner)
    [ $# -lt 1 ] && die "Usage: owner <agentId>"
    cast call "$IDENTITY_REGISTRY" "ownerOf(uint256)(address)" "$1" --rpc-url "$RPC_URL"
    ;;

agent-wallet)
    [ $# -lt 1 ] && die "Usage: agent-wallet <agentId>"
    cast call "$IDENTITY_REGISTRY" "getAgentWallet(uint256)(address)" "$1" --rpc-url "$RPC_URL"
    ;;

metadata)
    [ $# -lt 2 ] && die "Usage: metadata <agentId> <key>"
    cast call "$IDENTITY_REGISTRY" "getMetadata(uint256,string)(bytes)" "$1" "$2" --rpc-url "$RPC_URL"
    ;;

balance)
    [ $# -lt 1 ] && die "Usage: balance <address>"
    cast call "$IDENTITY_REGISTRY" "balanceOf(address)(uint256)" "$1" --rpc-url "$RPC_URL"
    ;;

# ============================================================
# REPUTATION REGISTRY — WRITE
# ============================================================

feedback)
    require_from
    [ $# -lt 5 ] && die "Usage: feedback <agentId> <value> <decimals> <tag1> <tag2> [--endpoint <url>] [--uri <uri>] [--hash <bytes32>]"
    AGENT_ID="$1"; VALUE="$2"; DECIMALS="$3"; TAG1="$4"; TAG2="$5"; shift 5
    ENDPOINT=""
    FB_URI=""
    FB_HASH="0x0000000000000000000000000000000000000000000000000000000000000000"
    while [ $# -gt 0 ]; do
        case "$1" in
            --endpoint) ENDPOINT="$2"; shift 2 ;;
            --endpoint=*) ENDPOINT="${1#--endpoint=}"; shift ;;
            --uri) FB_URI="$2"; shift 2 ;;
            --uri=*) FB_URI="${1#--uri=}"; shift ;;
            --hash) FB_HASH="$2"; shift 2 ;;
            --hash=*) FB_HASH="${1#--hash=}"; shift ;;
            *) die "Unknown flag: $1" ;;
        esac
    done
    CALLDATA=$(cast calldata "giveFeedback(uint256,int128,uint8,string,string,string,string,bytes32)" \
        "$AGENT_ID" "$VALUE" "$DECIMALS" "$TAG1" "$TAG2" "$ENDPOINT" "$FB_URI" "$FB_HASH")
    send_tx "$REPUTATION_REGISTRY" "$CALLDATA" "Give feedback to agent $AGENT_ID: value=$VALUE tag1=$TAG1 tag2=$TAG2"
    ;;

revoke-feedback)
    require_from
    [ $# -lt 2 ] && die "Usage: revoke-feedback <agentId> <feedbackIndex>"
    CALLDATA=$(cast calldata "revokeFeedback(uint256,uint64)" "$1" "$2")
    send_tx "$REPUTATION_REGISTRY" "$CALLDATA" "Revoke feedback index $2 for agent $1"
    ;;

respond)
    require_from
    [ $# -lt 5 ] && die "Usage: respond <agentId> <clientAddress> <feedbackIndex> <responseURI> <responseHash>"
    CALLDATA=$(cast calldata "appendResponse(uint256,address,uint64,string,bytes32)" "$1" "$2" "$3" "$4" "$5")
    send_tx "$REPUTATION_REGISTRY" "$CALLDATA" "Respond to feedback from $2 on agent $1"
    ;;

# ============================================================
# REPUTATION REGISTRY — READ
# ============================================================

reputation)
    [ $# -lt 1 ] && die "Usage: reputation <agentId> [--clients <addr,...>] [--tag1 <t>] [--tag2 <t>]"
    AGENT_ID="$1"; shift
    CLIENTS="[]"
    TAG1=""
    TAG2=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --clients) CLIENTS="$2"; shift 2 ;;
            --clients=*) CLIENTS="${1#--clients=}"; shift ;;
            --tag1) TAG1="$2"; shift 2 ;;
            --tag1=*) TAG1="${1#--tag1=}"; shift ;;
            --tag2) TAG2="$2"; shift 2 ;;
            --tag2=*) TAG2="${1#--tag2=}"; shift ;;
            *) die "Unknown flag: $1" ;;
        esac
    done
    RESULT=$(cast call "$REPUTATION_REGISTRY" \
        "getSummary(uint256,address[],string,string)(uint64,int128,uint8)" \
        "$AGENT_ID" "$CLIENTS" "$TAG1" "$TAG2" --rpc-url "$RPC_URL")
    echo "$RESULT"
    ;;

read-feedback)
    [ $# -lt 3 ] && die "Usage: read-feedback <agentId> <clientAddress> <feedbackIndex>"
    cast call "$REPUTATION_REGISTRY" \
        "readFeedback(uint256,address,uint64)(int128,uint8,string,string,bool)" \
        "$1" "$2" "$3" --rpc-url "$RPC_URL"
    ;;

clients)
    [ $# -lt 1 ] && die "Usage: clients <agentId>"
    cast call "$REPUTATION_REGISTRY" "getClients(uint256)(address[])" "$1" --rpc-url "$RPC_URL"
    ;;

# ============================================================
# VALIDATION REGISTRY — WRITE
# ============================================================

request-validation)
    require_from
    require_validation_registry
    [ $# -lt 4 ] && die "Usage: request-validation <validatorAddress> <agentId> <requestURI> <requestHash>"
    CALLDATA=$(cast calldata "validationRequest(address,uint256,string,bytes32)" "$1" "$2" "$3" "$4")
    send_tx "$VALIDATION_REGISTRY" "$CALLDATA" "Request validation from $1 for agent $2"
    ;;

validation-response)
    require_from
    require_validation_registry
    [ $# -lt 5 ] && die "Usage: validation-response <requestHash> <response> <responseURI> <responseHash> <tag>"
    CALLDATA=$(cast calldata "validationResponse(bytes32,uint8,string,bytes32,string)" "$1" "$2" "$3" "$4" "$5")
    send_tx "$VALIDATION_REGISTRY" "$CALLDATA" "Respond to validation request $1 with score $2"
    ;;

# ============================================================
# VALIDATION REGISTRY — READ
# ============================================================

validation-status)
    require_validation_registry
    [ $# -lt 1 ] && die "Usage: validation-status <requestHash>"
    cast call "$VALIDATION_REGISTRY" \
        "getValidationStatus(bytes32)(address,uint256,uint8,bytes32,string,uint256)" \
        "$1" --rpc-url "$RPC_URL"
    ;;

agent-validations)
    require_validation_registry
    [ $# -lt 1 ] && die "Usage: agent-validations <agentId>"
    cast call "$VALIDATION_REGISTRY" "getAgentValidations(uint256)(bytes32[])" "$1" --rpc-url "$RPC_URL"
    ;;

validation-summary)
    require_validation_registry
    [ $# -lt 1 ] && die "Usage: validation-summary <agentId> [--validators <addr,...>] [--tag <t>]"
    AGENT_ID="$1"; shift
    VALIDATORS="[]"
    TAG=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --validators) VALIDATORS="$2"; shift 2 ;;
            --validators=*) VALIDATORS="${1#--validators=}"; shift ;;
            --tag) TAG="$2"; shift 2 ;;
            --tag=*) TAG="${1#--tag=}"; shift ;;
            *) die "Unknown flag: $1" ;;
        esac
    done
    cast call "$VALIDATION_REGISTRY" \
        "getSummary(uint256,address[],string)(uint64,uint8)" \
        "$AGENT_ID" "$VALIDATORS" "$TAG" --rpc-url "$RPC_URL"
    ;;

# ============================================================
# EVENTS
# ============================================================

events)
    [ $# -lt 1 ] && die "Usage: events <registered|feedback|uri-updated> [agentId] [--from-block N]"
    EVENT_TYPE="$1"; shift
    FROM_BLOCK="0"
    AGENT_ID=""

    # Parse event-specific args
    case "$EVENT_TYPE" in
        registered)
            while [ $# -gt 0 ]; do
                case "$1" in
                    --from-block) FROM_BLOCK="$2"; shift 2 ;;
                    --from-block=*) FROM_BLOCK="${1#--from-block=}"; shift ;;
                    *) die "Unknown flag: $1" ;;
                esac
            done
            TOPIC0=$(cast sig-event "Registered(uint256,string,address)")
            cast logs --from-block "$FROM_BLOCK" --address "$IDENTITY_REGISTRY" "$TOPIC0" --rpc-url "$RPC_URL"
            ;;

        feedback)
            [ $# -lt 1 ] && die "Usage: events feedback <agentId> [--from-block N]"
            AGENT_ID="$1"; shift
            while [ $# -gt 0 ]; do
                case "$1" in
                    --from-block) FROM_BLOCK="$2"; shift 2 ;;
                    --from-block=*) FROM_BLOCK="${1#--from-block=}"; shift ;;
                    *) die "Unknown flag: $1" ;;
                esac
            done
            TOPIC0=$(cast sig-event "NewFeedback(uint256,address,uint64,int128,uint8,string,string,string,string,string,bytes32)")
            # agentId is indexed as topic1
            TOPIC1=$(cast to-hex "$AGENT_ID" | sed 's/^/0x000000000000000000000000000000000000000000000000000000000000000/' | tail -c 67)
            cast logs --from-block "$FROM_BLOCK" --address "$REPUTATION_REGISTRY" "$TOPIC0" --rpc-url "$RPC_URL"
            ;;

        uri-updated)
            [ $# -lt 1 ] && die "Usage: events uri-updated <agentId> [--from-block N]"
            AGENT_ID="$1"; shift
            while [ $# -gt 0 ]; do
                case "$1" in
                    --from-block) FROM_BLOCK="$2"; shift 2 ;;
                    --from-block=*) FROM_BLOCK="${1#--from-block=}"; shift ;;
                    *) die "Unknown flag: $1" ;;
                esac
            done
            TOPIC0=$(cast sig-event "URIUpdated(uint256,string,address)")
            cast logs --from-block "$FROM_BLOCK" --address "$IDENTITY_REGISTRY" "$TOPIC0" --rpc-url "$RPC_URL"
            ;;

        *)
            die "Unknown event type: $EVENT_TYPE (use: registered, feedback, uri-updated)"
            ;;
    esac
    ;;

# ============================================================
# IPFS
# ============================================================

prepare-registration)
    NAME=""
    DESCRIPTION=""
    SERVICES="[]"
    X402="false"
    TRUST="[\"reputation\"]"
    IMAGE=""
    while [ $# -gt 0 ]; do
        case "$1" in
            --name) NAME="$2"; shift 2 ;;
            --name=*) NAME="${1#--name=}"; shift ;;
            --description) DESCRIPTION="$2"; shift 2 ;;
            --description=*) DESCRIPTION="${1#--description=}"; shift ;;
            --services) SERVICES="$2"; shift 2 ;;
            --services=*) SERVICES="${1#--services=}"; shift ;;
            --x402) X402="true"; shift ;;
            --trust) TRUST="$2"; shift 2 ;;
            --trust=*) TRUST="${1#--trust=}"; shift ;;
            --image) IMAGE="$2"; shift 2 ;;
            --image=*) IMAGE="${1#--image=}"; shift ;;
            *) die "Unknown flag: $1" ;;
        esac
    done
    [ -z "$NAME" ] && die "--name is required"
    [ -z "$DESCRIPTION" ] && die "--description is required"

    # Build JSON
    IMAGE_LINE=""
    if [ -n "$IMAGE" ]; then
        IMAGE_LINE="\"image\": \"$IMAGE\","
    fi

    cat <<JSONEOF
{
  "type": "https://eips.ethereum.org/EIPS/eip-8004#registration-v1",
  "name": "$NAME",
  "description": "$DESCRIPTION",
  ${IMAGE_LINE}
  "services": $SERVICES,
  "x402Support": $X402,
  "active": true,
  "supportedTrust": $TRUST
}
JSONEOF
    ;;

pin)
    [ $# -lt 1 ] && die "Usage: pin <file>"
    FILE="$1"
    [ -f "$FILE" ] || die "File not found: $FILE"
    RESPONSE=$(curl -s -X POST "$IPFS_API/add" -F "file=@$FILE")
    CID=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['Hash'])" 2>/dev/null) \
        || die "Failed to pin to IPFS. Response: $RESPONSE"
    echo "ipfs://$CID"
    ;;

pin-registration)
    # Generate JSON, write to temp file, pin it
    TMPFILE=$(mktemp /tmp/agent-registration-XXXXXX.json)
    # Pass all args through to prepare-registration
    sh "$0" --network "$NETWORK" prepare-registration "$@" > "$TMPFILE"
    echo "Generated registration JSON:"
    cat "$TMPFILE"
    echo ""
    echo "Pinning to IPFS..."
    sh "$0" --network "$NETWORK" pin "$TMPFILE"
    rm -f "$TMPFILE"
    ;;

# ============================================================

*)
    echo "Unknown command: $CMD"
    echo "Run without arguments to see usage."
    exit 1
    ;;
esac
