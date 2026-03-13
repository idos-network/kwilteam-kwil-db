# Admin JSON-RPC Service

This directory contains the Kwil admin JSON-RPC service. The admin API is used for node operations (validators, peers, config, health) and is typically protected by TLS and/or a password.

**Default listen address**: `127.0.0.1:8584` (configurable via `admin.listen`).  
**RPC path**: `POST /rpc/v1` with JSON-RPC 2.0 (`method`, `params` object, `id`).

---

## Other admin methods

For blacklist-related testing and examples, see [app/blacklist/TESTING.md](../../../app/blacklist/TESTING.md).  
For the full set of admin methods, see the OpenRPC spec served at `GET /spec/v1` on the admin server.

**Listener sync status** (last processed block per EVM listener) is exposed on the **user** RPC as `user.listener_sync_status` so it can be used in production where admin is not exposed. See [../usersvc/README.md](../usersvc/README.md) for details.
