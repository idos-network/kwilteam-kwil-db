# JSON-RPC API Reference

This document describes the JSON-RPC services exposed by Kwil nodes: method names, parameters, and example requests/responses. It is intended for developers integrating with the node (dApps, operators, monitoring).

**Machine-readable spec:** The same information (plus full schemas) is available as OpenRPC JSON:

- **GET** `/spec/v1` — returns the OpenRPC spec (use GET, not POST).
- **POST** `/rpc/v1` with `{"jsonrpc":"2.0","method":"rpc.discover","params":{},"id":1}` — returns the spec in the `result` field.

---

## Servers and endpoints

| Server   | Default address        | Config key    | Services exposed                          |
|----------|------------------------|---------------|-------------------------------------------|
| User RPC | `0.0.0.0:8484`         | `rpc.listen`  | user, function, chain (each can be disabled) |
| Admin RPC | `/tmp/kwild.socket` (default); or `127.0.0.1:8584` if TCP | `admin.listen` | admin, user, function, chain |

- **Path:** `POST /rpc/v1` for all JSON-RPC calls. Params must be a **JSON object** (named params), not an array.
- **Auth:** User RPC may have challenge/auth for some methods; Admin RPC may require TLS and/or Basic auth (see [node/services/jsonrpc/adminsvc/README.md](node/services/jsonrpc/adminsvc/README.md)).

**Generic request shape:**

```json
{
  "jsonrpc": "2.0",
  "method": "service.method_name",
  "params": { ... },
  "id": 1
}
```

**Generic response shape:**

```json
{
  "jsonrpc": "2.0",
  "result": { ... },
  "id": 1
}
```

On error, the response contains `"error": {"code": ..., "message": "..."}` instead of `"result"`.

---

## 1. User service

**Purpose:** Main API for applications: chain info, accounts, broadcast transactions, call actions, query databases, migrations, withdrawal proofs.

**Namespace:** `user.*`

### Method list and parameters

| Method | Params | Description |
|--------|--------|-------------|
| `user.version` | `{}` | API and kwild version |
| `user.health` | `{}` | Node health (syncing, block age, peers) |
| `user.ping` | `{"message": string}` | Liveness check |
| `user.chain_info` | `{}` | Chain ID, best block height/hash, app hash |
| `user.account` | `{"id": AccountID, "status": "confirmed"\|"unconfirmed"}` | Account balance and nonce |
| `user.num_accounts` | `{}` | Total number of accounts |
| `user.broadcast` | `{"tx": Transaction, "sync": 0\|1}` | Submit transaction; sync 0=accept, 1=commit |
| `user.call` | CallMessage (action name, args, etc.) | Call a view/action |
| `user.databases` | `{"owner": hex?}` | List databases (namespaces) |
| `user.query` | `{"query": string, "params": object?}` | Ad-hoc read-only SQL |
| `user.authenticated_query` | AuthenticatedQuery | Ad-hoc SQL with auth |
| `user.estimate_price` | `{"tx": Transaction}` | Estimate tx fee |
| `user.tx_query` | `{"tx_hash": hash}` | Transaction status/result |
| `user.schema` | `{"namespace": string}` | Schema metadata for a namespace |
| `user.get_withdrawal_proof` | `{"epoch_id": string, "recipient": string}` | Withdrawal proof + validator signatures |
| `user.listener_sync_status` | `{}` | Last processed block per EVM listener (health monitoring) |
| `user.challenge` | `{}` | Get challenge for signed call |
| `user.list_update_proposals` | `{}` | List consensus parameter update proposals |
| `user.update_proposal_status` | `{}` | Same as list_update_proposals |
| `user.list_migrations` | `{}` | List pending migration resolutions |
| `user.migration_status` | `{}` | Migration status |
| `user.changeset_metadata` | `{"height": int64}` | Changeset metadata for a block height |
| `user.changeset` | `{"height": int64, "index": int64}` | Load a changeset chunk by height and index |
| `user.migration_metadata` | `{}` | Migration metadata |
| `user.migration_genesis_chunk` | `{"height": uint64, "chunk_index": uint32}` | Genesis snapshot chunk |

### Example: user.chain_info

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"user.chain_info","params":{},"id":1}'
```

**Response (result only):**

```json
{
  "chain_id": "your-chain-id",
  "block_height": 12345,
  "block_hash": "0x...",
  "app_hash": "0x..."
}
```

### Example: user.ping

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"user.ping","params":{"message":"hello"},"id":1}'
```

**Response (result):** `{"message": "pong"}` (or echo of your message).

### Example: user.get_withdrawal_proof

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "user.get_withdrawal_proof",
    "params": {
      "epoch_id": "<hex-uuid-of-epoch>",
      "recipient": "0x<hex-address>"
    },
    "id": 1
  }'
```

**Response (result):** Includes `status` (e.g. `"ready"`, `"claimed"`), `eth_tx_hash` (if claimed), merkle proof, validator signatures. See [node/services/jsonrpc/usersvc/README.md](node/services/jsonrpc/usersvc/README.md) for details.

### Example: user.listener_sync_status

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"user.listener_sync_status","params":{},"id":1}'
```

**Response (result):**

```json
{
  "listeners": [
    {
      "topic": "erc20_transfer_listener_<uuid>",
      "chain": "arbitrum_one",
      "last_processed_block": 123456789
    }
  ]
}
```

Use this on the **user** RPC (port 8484) so it works in production where admin is not exposed. See [node/services/jsonrpc/usersvc/README.md](node/services/jsonrpc/usersvc/README.md) for details.

---

## 2. Chain service

**Purpose:** Low-level chain/consensus data: blocks, transactions, validators, genesis. Read-only.

**Namespace:** `chain.*`

### Method list and parameters

| Method | Params | Description |
|--------|--------|-------------|
| `chain.version` | `{}` | Service version |
| `chain.health` | `{}` | Chain service health |
| `chain.block` | `{"height": int64?, "hash": hash?, "raw": bool?}` | Block by height (0=latest) or hash |
| `chain.block_header` | `{"height": int64?, "hash": hash?}` | Block header only |
| `chain.block_result` | `{"height": int64?, "hash": hash?}` | Tx results for a block |
| `chain.tx` | `{"hash": hash}` | Transaction by hash |
| `chain.genesis` | `{}` | Genesis config (chain id, validators, allocs, etc.) |
| `chain.consensus_params` | `{}` | Consensus/network parameters |
| `chain.validators` | `{}` | Current validator set |
| `chain.unconfirmed_txs` | `{"limit": int?}` | Mempool (unconfirmed transactions) |

### Example: chain.validators

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"chain.validators","params":{},"id":1}'
```

**Response (result):** Array of validator objects (identifier, power, etc.).

### Example: chain.block

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"chain.block","params":{"height":1,"raw":false},"id":1}'
```

**Response (result):** Block (and commit info) at the given height; `height: 0` returns latest block.

---

## 3. Function service

**Purpose:** Utility: version and signature verification (Kwil signing scheme).

**Namespace:** `function.*`

### Method list and parameters

| Method | Params | Description |
|--------|--------|-------------|
| `function.version` | `{}` | API and kwild version |
| `function.verify_sig` | `{"signature": {"sig": bytes, "type": string}, "sender": hex, "msg": bytes}` | Verify a message signature |

### Example: function.verify_sig

**Request:**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "method": "function.verify_sig",
    "params": {
      "signature": {"sig": "<base64>", "type": "secp256k1"},
      "sender": "0x...",
      "msg": "<base64-encoded-message>"
    },
    "id": 1
  }'
```

**Response (result):** `{"valid": true}` or `{"valid": false, "reason": "..."}`.

---

## 4. Admin service

**Purpose:** Node and network operations: status, config, validators, peers, blacklist, resolutions, block execution. Only on the **admin** server; typically protected.

**Namespace:** `admin.*`

### Method list and parameters

| Method | Params | Description |
|--------|--------|-------------|
| `admin.version` | `{}` | Admin API version |
| `admin.health` | `{}` | Admin service health |
| `admin.status` | `{}` | Node status (sync, validator identity, etc.) |
| `admin.config` | `{}` | Effective node config (TOML bytes) |
| `admin.peers` | `{}` | Connected peers |
| `admin.add_peer` | `{"peerid": string}` | Add peer to whitelist |
| `admin.remove_peer` | `{"peerid": string}` | Remove peer from whitelist |
| `admin.list_peers` | `{}` | Whitelisted peers |
| `admin.blacklist_peer` | `{"peerid": string, "reason": string?, "duration": string?}` | Blacklist peer; duration e.g. "1h", empty = permanent |
| `admin.remove_blacklisted_peer` | `{"peerid": string}` | Remove from blacklist |
| `admin.list_blacklisted_peers` | `{}` | List blacklisted peers |
| `admin.val_list` | `{}` | Current validators |
| `admin.val_approve` | `{"pubkey": bytes, "pubkey_type": string}` | Approve validator join |
| `admin.val_join` | `{}` | Request to join as validator |
| `admin.val_leave` | `{}` | Leave validator set |
| `admin.val_remove` | `{"pubkey": bytes, "pubkey_type": string}` | Vote to remove validator |
| `admin.val_join_status` | `{"pubkey": bytes, "pubkey_type": string}` | Join request status |
| `admin.val_list_joins` | `{}` | List pending join requests |
| `admin.val_promote` | `{"pubkey": bytes, "pubkey_type": string, "height": int64}` | Promote validator to leader at height |
| `admin.create_resolution` | `{"resolution": bytes, "resolution_type": string}` | Create a resolution |
| `admin.approve_resolution` | `{"resolution_id": uuid}` | Approve a resolution |
| `admin.resolution_status` | `{"resolution_id": uuid}` | Resolution status |
| `admin.block_exec_status` | `{}` | Ongoing block execution status |
| `admin.abort_block_execution` | `{"height": int64, "txs": [tx_hash strings]}` | Abort block execution |

### Example: admin.status

**Request (admin server, optionally with auth):**

```bash
curl -s -X POST http://127.0.0.1:8584/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"admin.status","params":{},"id":1}'
```

**Response (result):** Node info, sync info (best block, syncing), validator identity, migration state.

---

## Meta methods

| Method | Params | Description |
|--------|--------|-------------|
| `rpc.discover` | `{}` | Returns the full OpenRPC spec (methods, params, schemas) in the `result` field. |
| `rpc.health` | `{}` | Aggregate health across services. |

**Example: rpc.discover**

```bash
curl -s -X POST http://localhost:8484/rpc/v1 \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"rpc.discover","params":{},"id":1}'
```

**Example: GET OpenRPC spec (no POST)**

```bash
curl -s "http://localhost:8484/spec/v1"
```

---

## See also

- [node/services/jsonrpc/usersvc/README.md](node/services/jsonrpc/usersvc/README.md) — User service tests and withdrawal proof usage.
- [node/services/jsonrpc/adminsvc/README.md](node/services/jsonrpc/adminsvc/README.md) — Admin service.
- [app/blacklist/TESTING.md](../app/blacklist/TESTING.md) — Blacklist and admin curl examples.
