# ERC-8004 Method Reference

Quick reference for all three ERC-8004 registry contracts. Same addresses on 20+ chains (CREATE2).

## IdentityRegistry — `0x8004A169FB4a3325136EB29fA0ceB6D2e539a432`

### Write Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `register()` | `register()(uint256)` | Register with no URI or metadata. Returns agentId |
| `register(string)` | `register(string)(uint256)` | Register with agentURI. Returns agentId |
| `register(string,(string,bytes)[])` | `register(string,(string,bytes)[])(uint256)` | Register with agentURI + metadata entries. Returns agentId |
| `setAgentURI` | `setAgentURI(uint256,string)` | Update agent's URI. Owner only |
| `setMetadata` | `setMetadata(uint256,string,bytes)` | Set arbitrary metadata key-value. Owner only |
| `setAgentWallet` | `setAgentWallet(uint256,address,uint256,bytes)` | Set agent's wallet address. Requires EIP-712 signature from new wallet |
| `unsetAgentWallet` | `unsetAgentWallet(uint256)` | Remove agent's wallet association. Owner only |
| `transferFrom` | `transferFrom(address,address,uint256)` | Transfer agent identity (ERC-721) |
| `safeTransferFrom` | `safeTransferFrom(address,address,uint256)` | Safe transfer with receiver check |

### Read Functions

| Function | Signature | Returns | Description |
|----------|-----------|---------|-------------|
| `tokenURI` | `tokenURI(uint256)(string)` | string | Agent's registration URI |
| `ownerOf` | `ownerOf(uint256)(address)` | address | Owner of agent identity |
| `getAgentWallet` | `getAgentWallet(uint256)(address)` | address | Agent's associated wallet |
| `getMetadata` | `getMetadata(uint256,string)(bytes)` | bytes | Metadata value for key |
| `balanceOf` | `balanceOf(address)(uint256)` | uint256 | Number of agents owned by address |
| `name` | `name()(string)` | string | Registry name |
| `symbol` | `symbol()(string)` | string | Registry symbol |
| `getVersion` | `getVersion()(string)` | string | Contract version |

### Events

| Event | Signature | Indexed Fields |
|-------|-----------|----------------|
| `Registered` | `Registered(uint256,string,address)` | agentId, owner |
| `URIUpdated` | `URIUpdated(uint256,string,address)` | agentId, updatedBy |
| `MetadataSet` | `MetadataSet(uint256,string,string,bytes)` | agentId, indexedMetadataKey |
| `Transfer` | `Transfer(address,address,uint256)` | from, to, tokenId |

---

## ReputationRegistry — `0x8004BAa17C55a88189AE136b182e5fdA19dE9b63`

### Write Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `giveFeedback` | `giveFeedback(uint256,int128,uint8,string,string,string,string,bytes32)` | Post feedback for an agent. Args: agentId, value, valueDecimals, tag1, tag2, endpoint, feedbackURI, feedbackHash |
| `revokeFeedback` | `revokeFeedback(uint256,uint64)` | Revoke previously posted feedback. Caller must be original poster. Args: agentId, feedbackIndex |
| `appendResponse` | `appendResponse(uint256,address,uint64,string,bytes32)` | Agent responds to feedback. Args: agentId, clientAddress, feedbackIndex, responseURI, responseHash |

### Read Functions

| Function | Signature | Returns | Description |
|----------|-----------|---------|-------------|
| `getSummary` | `getSummary(uint256,address[],string,string)(uint64,int128,uint8)` | count, value, decimals | Aggregated reputation. Filter by clients, tag1, tag2 |
| `readFeedback` | `readFeedback(uint256,address,uint64)(int128,uint8,string,string,bool)` | value, decimals, tag1, tag2, isRevoked | Single feedback entry |
| `readAllFeedback` | `readAllFeedback(uint256,address[],string,string,bool)` | arrays | All matching feedback. Filter by clients, tags, includeRevoked |
| `getClients` | `getClients(uint256)(address[])` | address[] | All clients who gave feedback |
| `getLastIndex` | `getLastIndex(uint256,address)(uint64)` | uint64 | Last feedback index for client |
| `getResponseCount` | `getResponseCount(uint256,address,uint64,address[])(uint64)` | uint64 | Number of responses to a feedback entry |
| `getIdentityRegistry` | `getIdentityRegistry()(address)` | address | Linked IdentityRegistry address |

### Events

| Event | Signature | Indexed Fields |
|-------|-----------|----------------|
| `NewFeedback` | `NewFeedback(uint256,address,uint64,int128,uint8,string,string,string,string,string,bytes32)` | agentId, clientAddress, indexedTag1 |
| `FeedbackRevoked` | `FeedbackRevoked(uint256,address,uint64)` | agentId, clientAddress, feedbackIndex |
| `ResponseAppended` | `ResponseAppended(uint256,address,uint64,address,string,bytes32)` | agentId, clientAddress, responder |

### Feedback Value Conventions

| Metric | Value | Decimals | Meaning |
|--------|-------|----------|---------|
| Quality score | 87 | 0 | 87/100 quality |
| Uptime percentage | 9977 | 2 | 99.77% uptime |
| Negative rating | -50 | 0 | Negative feedback |
| Precise score | 85500 | 3 | 85.500 |

---

## ValidationRegistry — (Same CREATE2 pattern, check deployment status)

### Write Functions

| Function | Signature | Description |
|----------|-----------|-------------|
| `validationRequest` | `validationRequest(address,uint256,string,bytes32)` | Request validation. Args: validatorAddress, agentId, requestURI, requestHash |
| `validationResponse` | `validationResponse(bytes32,uint8,string,bytes32,string)` | Respond to validation request. Args: requestHash, response (0-100), responseURI, responseHash, tag |

### Read Functions

| Function | Signature | Returns | Description |
|----------|-----------|---------|-------------|
| `getValidationStatus` | `getValidationStatus(bytes32)(address,uint256,uint8,bytes32,string,uint256)` | validator, agentId, response, responseHash, tag, lastUpdate | Status of a validation request |
| `getAgentValidations` | `getAgentValidations(uint256)(bytes32[])` | bytes32[] | All validation request hashes for an agent |
| `getValidatorRequests` | `getValidatorRequests(address)(bytes32[])` | bytes32[] | All requests assigned to a validator |
| `getSummary` | `getSummary(uint256,address[],string)(uint64,uint8)` | count, avgResponse | Aggregated validation score |
| `getIdentityRegistry` | `getIdentityRegistry()(address)` | address | Linked IdentityRegistry address |

### Events

| Event | Signature | Indexed Fields |
|-------|-----------|----------------|
| `ValidationRequest` | `ValidationRequest(address,uint256,string,bytes32)` | validatorAddress, agentId, requestHash |
| `ValidationResponse` | `ValidationResponse(address,uint256,bytes32,uint8,string,bytes32,string)` | validatorAddress, agentId, requestHash |
