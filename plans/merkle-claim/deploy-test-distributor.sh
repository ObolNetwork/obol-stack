#!/usr/bin/env bash
# Deploy + fund a TEST OBOL MerkleDistributorWithDeadline on mainnet, signed by
# the operator's Hermes wallet keystore. No private key ever touches stdout or
# the environment — cast reads the keystore file directly.
#
# Prereqs: foundry (cast), jq, a running obol stack (for the eRPC upstream), and
# the wallet backup JSON (gitignored). Hermes wallet needs mainnet ETH for gas
# (~0.0002 ETH) and >= TOTAL_OBOL of OBOL to fund the distributor.
#
# Usage:
#   OBOL_STACK_DIR=/path/to/obol-stack \
#   MERKLE_DISTRIBUTOR_DIR=/path/to/merkle-distributor \
#   WALLET_BACKUP=/path/to/obol-wallet-backup-...json \
#   bash deploy-test-distributor.sh
#
# After it prints the distributor address, set it in the skill:
#   jq '.distributor="0x<addr>"' obol-claim-test/config.json  (or OBOL_CLAIM_DISTRIBUTOR env on the agent)
set -euo pipefail

OBOL_STACK_DIR=${OBOL_STACK_DIR:-$(pwd)}
MERKLE_DISTRIBUTOR_DIR=${MERKLE_DISTRIBUTOR_DIR:-"$OBOL_STACK_DIR/../merkle-distributor"}
WALLET_BACKUP=${WALLET_BACKUP:-"$OBOL_STACK_DIR/obol-wallet-backup-hermes-mainet-eth-usdc.json"}
OBOL_TOKEN=${OBOL_TOKEN:-0x0B010000b7624eb9B3DfBC279673C76E9D29D5F7}
DEADLINE_DAYS=${DEADLINE_DAYS:-7}
MERKLE_JSON=${MERKLE_JSON:-"$MERKLE_DISTRIBUTOR_DIR/testclaim/merkle.json"}
export FOUNDRY_DISABLE_NIGHTLY_WARNING=1
export OBOL_DEVELOPMENT=true OBOL_CONFIG_DIR="$OBOL_STACK_DIR/.workspace/config"
OBOL=${OBOL_BIN:-"$OBOL_STACK_DIR/.workspace/bin/obol"}

ROOT=$(jq -r '.merkleRoot' "$MERKLE_JSON")
TOTAL_WEI=$(jq -r '.totalAmount' "$MERKLE_JSON")
DEADLINE=$(( $(date +%s) + DEADLINE_DAYS*24*3600 ))

# Pull the Obol mainnet upstream (credentialed) from the eRPC configmap — masked.
RPC=$("$OBOL" kubectl get cm -n erpc erpc-config -o jsonpath='{.data}' \
  | python3 -c "import sys,json,re;c=list(json.load(sys.stdin).values())[0];m=re.search(r'endpoint:\s*(\S*erpc\.gcp\.obol\.tech/rpc/mainnet)',c);print(m.group(1) if m else '')")
[ -n "$RPC" ] || { echo "could not resolve Obol mainnet upstream from eRPC configmap" >&2; exit 1; }
echo "RPC: $(echo "$RPC" | sed -E 's#//[^@]*@#//[REDACTED]@#')"
echo "Merkle root: $ROOT  total: $TOTAL_WEI wei  deadline: $DEADLINE ($(date -r $DEADLINE -u '+%F %H:%M UTC'))"

# Extract keystore + password to root-only temp files (never printed), shred on exit.
KS=$(mktemp); PW=$(mktemp); trap 'shred -u "$KS" "$PW" 2>/dev/null || rm -f "$KS" "$PW"' EXIT
jq -c '.wallets[0].keystore' "$WALLET_BACKUP" > "$KS"
jq -rj '.wallets[0].keystorePassword' "$WALLET_BACKUP" > "$PW"
DEPLOYER=$(cast wallet address --keystore "$KS" --password-file "$PW")
echo "Deployer / owner: $DEPLOYER  (ETH $(cast balance --rpc-url "$RPC" "$DEPLOYER"))"

BYTECODE=$(jq -r '.bytecode.object' "$MERKLE_DISTRIBUTOR_DIR/out/MerkleDistributorWithDeadline.sol/MerkleDistributorWithDeadline.json")
echo "==> Deploying MerkleDistributorWithDeadline(token, root, deadline)..."
DEPLOY=$(cast send --rpc-url "$RPC" --keystore "$KS" --password-file "$PW" \
  --create "$BYTECODE" "constructor(address,bytes32,uint256)" "$OBOL_TOKEN" "$ROOT" "$DEADLINE" --json)
DIST=$(echo "$DEPLOY" | jq -r '.contractAddress')
echo "$DEPLOY" | jq '{status, transactionHash, contractAddress, gasUsed}'
[ "$(echo "$DEPLOY" | jq -r '.status')" = "0x1" ] || { echo "deploy failed" >&2; exit 1; }

echo "==> Verifying deployed state..."
echo "  merkleRoot: $(cast call --rpc-url "$RPC" "$DIST" 'merkleRoot()(bytes32)')  (want $ROOT)"
echo "  token:      $(cast call --rpc-url "$RPC" "$DIST" 'token()(address)')"
echo "  owner:      $(cast call --rpc-url "$RPC" "$DIST" 'owner()(address)')  (want $DEPLOYER)"

echo "==> Funding distributor with $TOTAL_WEI wei OBOL..."
cast send --rpc-url "$RPC" --keystore "$KS" --password-file "$PW" \
  "$OBOL_TOKEN" 'transfer(address,uint256)(bool)' "$DIST" "$TOTAL_WEI" --json | jq '{status, transactionHash}'
echo "  distributor OBOL balance: $(cast call --rpc-url "$RPC" "$OBOL_TOKEN" 'balanceOf(address)(uint256)' "$DIST")"

echo
echo "================================================================"
echo "TEST DISTRIBUTOR DEPLOYED: $DIST"
echo "Next: pin it in the skill, then create + sell the agent:"
echo "  jq --arg d $DIST '.distributor=\$d' internal/embed/skills/obol-claim-test/config.json > /tmp/c && mv /tmp/c internal/embed/skills/obol-claim-test/config.json ; go build -o .workspace/bin/obol ./cmd/obol"
echo "  (see plans/merkle-claim/HANDOFF.md step 4+)"
echo "================================================================"
