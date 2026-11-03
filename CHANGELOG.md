# Changelog

Changes in the idOS fork of kwil-db (`idos-network/kwilteam-kwil-db`). The fork
branched from upstream `trufnetwork/kwil-db` at `01b86388` (upstream #1674,
version 0.10.1). Upstream history before that point isn't listed here.

## Versioning

- One version covers the repository. It's set in `version/version.go`
  (`kwilVersion`) and tagged on the merge commit in `main`:
  - `vX.Y.Z` for the root module (`github.com/trufnetwork/kwil-db`)
  - `core/vX.Y.Z` for the core module (`github.com/trufnetwork/kwil-db/core`)
- Tags exist only so Go can resolve them. They aren't GitHub releases. Publishing
  a GitHub release would trigger `release.yaml`, so don't publish one.
- `idos-kgw/idos-kwild/go.mod` consumes the modules through `replace`:

  ```
  replace github.com/trufnetwork/kwil-db => github.com/idos-network/kwilteam-kwil-db vX.Y.Z
  replace github.com/trufnetwork/kwil-db/core => github.com/idos-network/kwilteam-kwil-db/core vX.Y.Z
  ```

- Before 1.0, bump the minor version for changes to consensus, block execution,
  stored state or the Go API, and the patch version for other fixes.
- Add each merged PR to `Unreleased`. When you cut a version, rename that section,
  bump `kwilVersion`, merge, and then tag.

## Unreleased

## 0.11.0 - 2026-11-03

Validators must upgrade together. Several of these changes affect how proposals
are voted on and how blocks are executed.

### Breaking

- `user.authenticated_query` is limited to the database owner. Other signers get
  the new error `ErrorAuthenticatedQueryOwnerOnly` (`-1009`). `kwil-cli` falls
  back to `user.query` when it gets this error. (#42)
- The mempool rejects transactions that can never do anything useful. Unless the
  sender is the database owner, it rejects `raw_statement`, `execute` on an action
  that is missing, `VIEW`, `PRIVATE`, `SYSTEM` or `OWNER`, and validator or
  resolution payloads from senders without validator power. Rejections wrap
  `types.ErrTxNotAdmitted` (`CodeInvalidTxType`). (#38)
- Consensus transactions must use the authenticator type exactly as registered,
  so `MM_TOKEN` or `ED25519` is rejected. (#32)
- `PrepareProposal` takes a `blockTime` argument. If a context-bound auth check
  such as `mm_token` expiry fails, that transaction fails with
  `CodeInvalidSignature` and the rest of the block still runs. Before, the whole
  block aborted. The leader filters these transactions out at proposal time, and
  recheck re-verifies them. (#41)
- Followers skip proposals timestamped more than 1 minute ahead of local time.
  They NACK proposals whose timestamp isn't after the previous block's. The leader
  steps its timestamp to at least 1 ms after the previous block. Validators need
  NTP. (#34)
- Upgrade v3 of the vote store drops the retired `xrpl_access_grant` resolution
  type, and upgrade v2 of the event store deletes leftover events of that type.
  (#28)

### Security

- Consensus:
  - Each commit signer counts once toward quorum. (#16)
  - Resolutions that have already expired are no longer confirmed by votes in the
    expiry block. (#29)
  - Unverified Gnosis-style `vote_epoch` signatures no longer block epoch
    confirmation. (#12)
  - Out-of-sync NACKs that don't prove the leader is behind are ignored. (#26)
  - Block-proposal reads are bounded, so a stalled peer can't hold the proposal
    slot. (#23)
  - A large block hashes the same on every node because pgoutput streaming is
    disabled in the replication monitor. This ports part of upstream #1748.
    (MYT-06, #40)
- Mempool and transactions:
  - Transactions without a body or signature are rejected before they reach the
    mempool, and a panic during admission no longer leaves the transaction
    queued. (#14, #19)
  - Empty encoded values return an error instead of panicking. The payload
    encoding is validated at admission and during execution. (#18)
- Engine and parser:
  - `CREATE OR REPLACE ACTION` on an existing action requires `DROP`. (#13)
  - Namespace denials stay in force after a global `GRANT`. (#15)
  - SELECT privileges are checked on table-function arguments and on the sources
    of `INSERT`, `UPDATE` and `DELETE`. (#31)
  - Array index assignment is capped at 1,048,576 to prevent OOM. (#22)
  - Parse tree depth is limited to prevent fatal stack overflows. (MYT-14, #39)
- Decoders and P2P:
  - Ordered-sync and evm-sync decoders reject length prefixes larger than the
    remaining buffer. An EVM event that can't be decoded is skipped instead of
    halting the chain. (#24)
  - `ConsensusReset` messages whose TxID count exceeds the payload are
    rejected. (#27)
- State sync and restore:
  - Chunk downloads are capped at the protocol chunk size. (#25)
  - Snapshot chunk metadata is validated before use. (#35)
  - The anchor block is verified against trusted providers. (#36)
  - A restore that wasn't accepted (killed, or not verified) is wiped on
    restart. (#33)
  - Genesis and migration restores run `psql` in restricted mode. (#30)

### Fixed

- Changeset replay skips no-op updates and unchanged TOAST columns. (#17)

### Internal

- `task test:unit` runs packages that don't use Postgres in parallel. (#21)

## 0.10.4 - 2026-09-25

### Security

- Ad-hoc SELECTs require SELECT on every namespace they read, not just the
  statement namespace. (#6)
- Negative or missing transaction fees are rejected, so fees can't mint
  balance. (#9)
- State-sync snapshots are restored through restricted `psql` after their hash
  is checked, and a failed restore is rolled back. (#10, #11)

### Changed

- PostgreSQL 16.15, and improvements to the Docker setup.

## 0.10.3 - 2026-09-08

### Added

- `mm_token` authentication for idOS wallet authorization. Token expiry is
  enforced over RPC, in the mempool and during block execution. (#7)

### Security

- `user.authenticated_query` always checks the challenge and signature. First it
  was rejected unless RPC private mode was on (#4), then it was allowed in open
  mode with mandatory authentication (#5).

## 0.10.2 - 2026-03-18

### Added

- Arbitrum and Hardhat chains for the ERC-20 bridge. (#2)
- ERC-20 bridge and oracle can sign withdrawals with a custodial Gnosis Safe or
  with the validator key, and Gnosis Safe-style signatures are verified. (#1)
- Listener sync status on the user RPC, for EVM listener health checks. (#3)

### Changed

- The resolution expiry period went from 1 hour to about 1 month. There are more
  initialization retries, and nonce sync recovers from invalid-nonce errors. (#1)
