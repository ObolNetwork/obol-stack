# Merkle leaf & proof recipe (must match the deployed contract)

The distributor is `MerkleDistributorWithDeadline` (Solidity 0.8.17, OpenZeppelin
`MerkleProof`). Its on-chain verification is:

```solidity
bytes32 node = keccak256(bytes.concat(keccak256(abi.encode(index, account, amount))));
if (!MerkleProof.verify(merkleProof, merkleRoot, node)) revert InvalidProof();
```

So the leaf and proof MUST be built the OpenZeppelin `StandardMerkleTree` way —
**not** the Uniswap `parse-balance-map` way. The two differ on three points; get
any one wrong and the recomputed root won't match the chain:

| Aspect | OpenZeppelin (this contract) | Uniswap (wrong here) |
|--------|------------------------------|----------------------|
| Hash depth | **double**: `keccak(keccak(...))` | single: `keccak(...)` |
| Encoding | **`abi.encode`** — address left-padded to 32 bytes (96 bytes total) | `abi.encodePacked` — address is 20 bytes (84 bytes total) |
| Leaf set | generated with `sort_leaves=False`; proofs come from `merkle.json` | leaves sorted ascending |

Leaf, in this skill (`claim.py:leaf_hash`):

```
enc  = uint256(index)            # 32 bytes, big-endian
     + 0x00 * 12 + address(20)   # abi.encode pads address to 32 bytes
     + uint256(amount)           # 32 bytes, big-endian
leaf = keccak256(keccak256(enc))
```

Proof walk (`claim.py:proof_root`) is OpenZeppelin's commutative `_hashPair`:

```
combine(a, b) = keccak256(a + b)  if a <= b
                keccak256(b + a)  otherwise
```

Apply `combine` left-to-right across the proof; the result must equal the
on-chain `merkleRoot()`.

## Where the tree comes from

`merkle.json` is produced by `../merkle-distributor/scripts/merkle_cli.py`:

```python
entries.sort(key=lambda e: e[0].lower())          # sort by lowercased address
values = [[i, addr, amt] for i, (addr, amt) in enumerate(entries)]
tree = StandardMerkleTree.of(values, ["uint256", "address", "uint256"], sort_leaves=False)
```

Output shape (keys are lowercase addresses):

```json
{
  "merkleRoot": "0x6b3b…",
  "totalAmount": "500000000000000000000000",
  "claims": { "0x…": { "index": 0, "amount": "126…", "proof": ["0x…", …] } }
}
```

This skill ships that file verbatim and re-verifies every proof against the live
on-chain root before spending gas, so the bundled data is a convenience, not a
trust anchor. If the distributor is redeployed with a new tree, replace
`merkle.json` and rebuild — the on-chain-root match (`assert_root_matches`) will
otherwise fail closed.
